package calldataverifier

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

const vectorRevision = "9268b554ef77ce6e8dc18269fe8a3d8b767b9051"
const otherAccount = "0x3000000000000000000000000000000000000003"
const zeroAddress = "0x0000000000000000000000000000000000000000"

type staticVector[T CalldataIntent] struct {
	Intent T
	Calls  []CalldataCall
}

func loadVectors[T CalldataIntent](t testing.TB, filename string) map[string]staticVector[T] {
	t.Helper()
	data, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Revision string
		Cases    map[string]staticVector[T]
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Revision != vectorRevision || len(file.Cases) == 0 {
		t.Fatalf("invalid vector provenance or empty cases in %s", filename)
	}
	return file.Cases
}

func vaultVectors(t testing.TB) map[string]staticVector[VaultCalldataIntent] {
	t.Helper()
	return loadVectors[VaultCalldataIntent](t, "vault-vectors.json")
}

type checkError interface {
	error
	location() errorDetail
}

func requireError[E checkError](t *testing.T, err error, field string, callID *string) {
	t.Helper()
	var check E
	if !errors.As(err, &check) {
		t.Fatalf("got %T: %v; want %T", err, err, check)
	}
	location := check.location()
	if location.Field != field || (location.CallID == nil) != (callID == nil) ||
		(callID != nil && *location.CallID != *callID) {
		t.Fatalf("got %v; want field %q call ID %v", err, field, callID)
	}
}

// verifyCalldata.test.ts: "verifies %s and rejects another vault".
func TestVaultVectors(t *testing.T) {
	for name, vector := range vaultVectors(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decoded, err := VerifyCalldata(vector.Intent, vector.Calls)
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded) != len(vector.Calls) {
				t.Fatalf("got %d decoded calls; want %d", len(decoded), len(vector.Calls))
			}
			for i, call := range decoded {
				if call.Call != vector.Calls[i] || call.FunctionName == "" || len(call.Args) == 0 {
					t.Fatalf("unexpected decoded call: %+v", call)
				}
			}
			intent := vector.Intent
			intent.Vault = otherAccount
			_, err = VerifyCalldata(intent, vector.Calls)
			var target *CalldataTargetError
			if !errors.As(err, &target) {
				t.Fatalf("wrong vault: got %v; want CalldataTargetError", err)
			}
		})
	}
}

// verifyCalldata.test.ts: chain resolution, invalid addresses and caps, and call kind independence.
func TestVerifyCallValidation(t *testing.T) {
	vector := vaultVectors(t)["v1Deposit"]
	intent, call := vector.Intent, vector.Calls[0]
	for _, tc := range []struct {
		name  string
		chain uint64
	}{
		{"unknown", 42},
		{"zero", 0},
		{"unsafe integer", 1 << 53},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent := intent
			intent.ChainID = tc.chain
			_, err := VerifyCalldata(intent, []CalldataCall{call})
			requireError[*CalldataChainError](t, err, "chain_id", &call.ID)
		})
	}
	for _, bad := range []string{"0x1", zeroAddress} {
		invalid := intent
		invalid.Account = bad
		_, err := VerifyCalldata(invalid, []CalldataCall{call})
		requireError[*CalldataRecipientError](t, err, "account", &call.ID)
		invalid = intent
		invalid.Vault = bad
		_, err = VerifyCalldata(invalid, nil)
		requireError[*CalldataTargetError](t, err, "vault", nil)
		badCall := call
		badCall.To = bad
		_, err = VerifyCalldata(intent, []CalldataCall{badCall})
		requireError[*CalldataTargetError](t, err, "to", &call.ID)
	}
	for _, invalidCap := range []int{-1, 10_001} {
		intent := intent
		intent.MaxFeeBPS = invalidCap
		_, err := VerifyCalldata(intent, []CalldataCall{call})
		requireError[*CalldataFeeError](t, err, "max_fee_bps", &call.ID)
	}
	badAction := intent
	badAction.Action = "toString"
	_, err := VerifyCalldata(badAction, []CalldataCall{call})
	requireError[*CalldataCallError](t, err, "action", &call.ID)
	_, err = VerifyCalldata(intent, nil)
	requireError[*CalldataCallError](t, err, "calls", nil)
	call.Kind = "approval"
	if _, err := VerifyCalldata(intent, []CalldataCall{call}); err != nil {
		t.Fatal(err)
	}
}

// verifyCalldata.test.ts: "rejects a non-asset unit on $intent.action even when the value matches"
// and "reports a missing withdrawal amount as an amount.unit error".
func TestVaultAmountUnits(t *testing.T) {
	vectors := vaultVectors(t)
	for _, name := range []string{"v1Deposit", "v2Deposit", "v2Force", "v2InKind", "v1Withdraw"} {
		t.Run(name, func(t *testing.T) {
			vector := vectors[name]
			if name == "v1Withdraw" {
				vector.Intent.Amount = Amount{}
			} else {
				vector.Intent.Amount.Unit = "shares"
			}
			_, err := VerifyCalldata(vector.Intent, vector.Calls)
			requireError[*CalldataAmountError](t, err, "amount.unit", &vector.Calls[len(vector.Calls)-1].ID)
		})
	}
}

// verifyCalldata.test.ts: "rejects invalid call metadata %o" and malformed calls.
func TestCalldataCallJSON(t *testing.T) {
	id := "call_0"
	for _, tc := range []struct {
		raw, field string
		id         *string
	}{
		{`null`, "calls", nil},
		{`0`, "calls", nil},
		{`"not a call"`, "calls", nil},
		{`{"id":123,"kind":""}`, "id", nil},
		{`{"kind":""}`, "id", nil},
		{`{"id":"call_0","kind":123}`, "kind", &id},
		{`{"id":"call_0"}`, "kind", &id},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var call CalldataCall
			err := json.Unmarshal([]byte(tc.raw), &call)
			requireError[*CalldataCallError](t, err, tc.field, tc.id)
		})
	}
	var call CalldataCall
	if err := json.Unmarshal([]byte(`{"id":"","kind":""}`), &call); err != nil {
		t.Fatal(err)
	}
}

func changeArgs(t *testing.T, contract string, call CalldataCall, edit func([]any)) CalldataCall {
	t.Helper()
	bin, err := hex.DecodeString(call.Data[2:])
	if err != nil {
		t.Fatal(err)
	}
	contractABI := defaultSnapshot.abis[contract]
	definition, err := contractABI.MethodById(bin)
	if err != nil {
		t.Fatal(err)
	}
	args, err := definition.Inputs.Unpack(bin[4:])
	if err != nil {
		t.Fatal(err)
	}
	edit(args)
	bin, err = definition.Inputs.Pack(args...)
	if err != nil {
		t.Fatal(err)
	}
	call.Data = "0x" + hex.EncodeToString(definition.ID) + hex.EncodeToString(bin)
	return call
}

// verifyCalldata.test.ts: native deposit budget, exact value, and approval reset checks.
func TestVaultDepositFunding(t *testing.T) {
	vector := vaultVectors(t)["v1DepositApproval"]
	intent, approval, deposit := vector.Intent, vector.Calls[0], vector.Calls[1]
	native := intent
	native.Asset = defaultSnapshot.Deployments[1].WNative
	nativeCall := deposit
	nativeCall.Value = intent.Amount.Value
	_, err := VerifyCalldata(native, []CalldataCall{nativeCall})
	requireError[*CalldataAmountError](t, err, "value", &nativeCall.ID)
	native.Funding = &Funding{Type: "native", MaxNativeAssets: intent.Amount.Value}
	if _, err := VerifyCalldata(native, []CalldataCall{nativeCall}); err != nil {
		t.Fatal(err)
	}
	nativeCall.Value = "1"
	_, err = VerifyCalldata(native, []CalldataCall{nativeCall})
	requireError[*CalldataAmountError](t, err, "value", &nativeCall.ID)

	reset := changeArgs(t, "erc20", approval, func(args []any) { args[1] = big.NewInt(0) })
	reset.ID = "reset"
	if _, err := VerifyCalldata(intent, []CalldataCall{reset, approval, deposit}); err != nil {
		t.Fatal(err)
	}
	short := changeArgs(t, "erc20", approval, func(args []any) { args[1] = big.NewInt(1) })
	_, err = VerifyCalldata(intent, []CalldataCall{reset, short, deposit})
	requireError[*CalldataAmountError](t, err, "approval.amount", &approval.ID)
	badReset := reset
	badReset.To = otherAccount
	_, err = VerifyCalldata(intent, []CalldataCall{badReset, approval, deposit})
	requireError[*CalldataTargetError](t, err, "approval.token", &reset.ID)
	wrongSpender := changeArgs(t, "erc20", approval, func(args []any) { args[0] = common.HexToAddress(otherAccount) })
	_, err = VerifyCalldata(intent, []CalldataCall{reset, wrongSpender, deposit})
	requireError[*CalldataTargetError](t, err, "approval.spender", &approval.ID)
	badApproval := approval
	badApproval.Value = "1"
	_, err = VerifyCalldata(intent, []CalldataCall{badApproval, deposit})
	requireError[*CalldataAmountError](t, err, "value", &approval.ID)
	badApproval = changeArgs(t, "erc20", approval, func([]any) {})
	transfer := defaultSnapshot.abis["erc20"].Methods["transfer"]
	badApproval.Data = "0x" + hex.EncodeToString(transfer.ID) + badApproval.Data[10:]
	_, err = VerifyCalldata(intent, []CalldataCall{badApproval, deposit})
	requireError[*CalldataCallError](t, err, "function", &approval.ID)
}

// checks.test.ts: "rejects trailing bytes only in nested calldata"
// and "rejects an odd hex suffix even when the decoded arguments are unchanged".
func TestVaultCanonicalCalldata(t *testing.T) {
	vectors := vaultVectors(t)
	vector := vectors["v1Deposit"]
	call := vector.Calls[0]
	call.Data += "abcd"
	if _, err := VerifyCalldata(vector.Intent, []CalldataCall{call}); err != nil {
		t.Fatal(err)
	}
	call.Data += "1"
	_, err := VerifyCalldata(vector.Intent, []CalldataCall{call})
	requireError[*CalldataDecodeError](t, err, "data", &call.ID)
	call = vector.Calls[0]
	call.Data = call.Data[:2] + strings.ToUpper(call.Data[2:10]) + call.Data[10:]
	_, err = VerifyCalldata(vector.Intent, []CalldataCall{call})
	requireError[*CalldataDecodeError](t, err, "data", &call.ID)
	call = vector.Calls[0]
	offset := 10 + 3*64
	call.Data = call.Data[:offset] + strings.Repeat("0", 62) + "20" + call.Data[offset+64:]
	_, err = VerifyCalldata(vector.Intent, []CalldataCall{call})
	requireError[*CalldataDecodeError](t, err, "data", &call.ID)

	force := vectors["v2ForceAll"]
	for _, i := range []int{0, 1} {
		call := changeArgs(t, "vault", force.Calls[0], func(args []any) {
			items := args[0].([][]byte)
			items[i] = append(slices.Clone(items[i]), 0xab, 0xcd)
		})
		id := call.ID + "." + string(rune('0'+i))
		_, err := VerifyCalldata(force.Intent, []CalldataCall{call})
		requireError[*CalldataDecodeError](t, err, "data", &id)
	}
}

// verifyCalldata.test.ts: "resolves the contract on the intended chain".
// Custom registry tests cover the Go-specific immutable registry API.
func TestRegistry(t *testing.T) {
	vector := vaultVectors(t)["v1Deposit"]
	intent := vector.Intent
	intent.ChainID = 137
	_, err := VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataTargetError](t, err, "to", &vector.Calls[0].ID)

	deployment := defaultSnapshot.Deployments[1]
	intent.ChainID = 31_337
	input := map[uint64]Deployment{31_337: deployment, 1: {VaultBundlesV1: strings.ToLower(deployment.VaultBundlesV1)}}
	registry, err := NewRegistry(input)
	if err != nil {
		t.Fatal(err)
	}
	delete(input, 31_337)
	if _, err := VerifyCalldata(intent, vector.Calls, VerifyOptions{Registry: registry}); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataChainError](t, err, "chain_id", &vector.Calls[0].ID)
	if _, err := NewRegistry(map[uint64]Deployment{1: {VaultBundlesV1: otherAccount}}); err == nil {
		t.Fatal("conflicting default accepted")
	}
	if _, err := NewRegistry(map[uint64]Deployment{31_337: {VaultBundlesV1: otherAccount}}); err == nil {
		t.Fatal("incomplete custom chain accepted")
	}
}
