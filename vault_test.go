package calldataverifier

import (
	"encoding/hex"
	"math/big"
	"reflect"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// verifyCalldata.test.ts: "checks the embedded deposit permit encoding without verifying the signature".
func TestVaultDepositPermits(t *testing.T) {
	vector := vaultVectors(t)["v1DepositApproval"]
	intent, call := vector.Intent, vector.Calls[1]
	makePermit := func(kind uint8, data []byte) CalldataCall {
		return changeArgs(t, "vaultBundle", call, func(args []any) {
			permit := reflect.New(reflect.TypeOf(args[3])).Elem()
			permit.Field(0).SetUint(uint64(kind))
			permit.Field(1).SetBytes(data)
			args[3] = permit.Interface()
		})
	}
	erc2612, err := defaultSnapshot.permits[1].Pack(big.NewInt(123), uint8(27), [32]byte{}, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	permit2, err := defaultSnapshot.permits[2].Pack(big.NewInt(1), big.NewInt(123), []byte{0x12, 0x34})
	if err != nil {
		t.Fatal(err)
	}
	for kind, data := range map[uint8][]byte{1: erc2612, 2: permit2} {
		valid := makePermit(kind, data)
		if _, err := VerifyCalldata(intent, []CalldataCall{valid}); err != nil {
			t.Fatal(err)
		}
		_, err := VerifyCalldata(intent, []CalldataCall{vector.Calls[0], valid})
		requireError[*CalldataCallError](t, err, "approval", &vector.Calls[0].ID)
		for _, malformed := range [][]byte{nil, append(append([]byte{}, data...), 0)} {
			_, err = VerifyCalldata(intent, []CalldataCall{makePermit(kind, malformed)})
			requireError[*CalldataDecodeError](t, err, "permit.data", &call.ID)
		}
		native := intent
		native.Asset = defaultSnapshot.Deployments[1].WNative
		native.Funding = &Funding{Type: "native", MaxNativeAssets: intent.Amount.Value}
		valid.Value = intent.Amount.Value
		_, err = VerifyCalldata(native, []CalldataCall{valid})
		requireError[*CalldataCallError](t, err, "permit", &call.ID)
	}
	_, err = VerifyCalldata(intent, []CalldataCall{makePermit(0, []byte{1})})
	requireError[*CalldataDecodeError](t, err, "permit.data", &call.ID)
	_, err = VerifyCalldata(intent, []CalldataCall{makePermit(3, erc2612)})
	requireError[*CalldataCallError](t, err, "permit.kind", &call.ID)
}

// verifyCalldata.test.ts: fee cap, fee destination, exact withdrawal amount, and all-mode fee receiver.
// vaultSdk.test.ts: requested fee cap, signed zero share permit, unsigned nonzero permit with approval.
func TestVaultWithdrawFeesAndPermits(t *testing.T) {
	vectors := vaultVectors(t)
	vector := vectors["v2WithdrawFee"]
	intent, call := vector.Intent, vector.Calls[0]
	intent.MaxFeeBPS--
	_, err := VerifyCalldata(intent, []CalldataCall{call})
	requireError[*CalldataFeeError](t, err, "max_fee_bps", &call.ID)
	intent = vector.Intent
	referral := *intent.ReferralFee
	intent.ReferralFee = &referral
	referral.BPS--
	_, err = VerifyCalldata(intent, []CalldataCall{call})
	requireError[*CalldataFeeError](t, err, "referral_fee.bps", &call.ID)
	referral.BPS++
	referral.Recipient = zeroAddress
	_, err = VerifyCalldata(intent, []CalldataCall{call})
	requireError[*CalldataFeeError](t, err, "referral_fee.recipient", &call.ID)
	intent = vector.Intent
	intent.MaxFeeBPS = 10_000
	fullFee := changeArgs(t, "vaultBundle", call, func(args []any) { args[4] = wad })
	_, err = VerifyCalldata(intent, []CalldataCall{fullFee})
	requireError[*CalldataFeeError](t, err, "max_fee_bps", &call.ID)
	nonzeroShares := changeArgs(t, "vaultBundle", call, func(args []any) { args[2] = big.NewInt(1) })
	_, err = VerifyCalldata(intent, []CalldataCall{nonzeroShares})
	requireError[*CalldataAmountError](t, err, "amount", &call.ID)

	all := vectors["v1All"]
	all.Intent.MaxFeeBPS = 50
	all.Intent.ReferralFee = &ReferralFee{BPS: 50, Recipient: otherAccount}
	allFee := changeArgs(t, "vaultBundle", all.Calls[0], func(args []any) {
		args[4] = big.NewInt(5_000_000_000_000_000)
		args[5] = common.HexToAddress(otherAccount)
	})
	_, err = VerifyCalldata(all.Intent, []CalldataCall{allFee})
	requireError[*CalldataRecipientError](t, err, "referral_fee.recipient", &allFee.ID)
	all.Intent.Account = otherAccount
	if _, err := VerifyCalldata(all.Intent, []CalldataCall{allFee}); err != nil {
		t.Fatal(err)
	}

	shares := vectors["v1Withdraw"]
	approval, main := shares.Calls[0], shares.Calls[1]
	for _, tc := range []struct {
		name  string
		value int64
		v     uint8
	}{{"signed zero", 0, 27}, {"unsigned nonzero", 1, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			modified := changeArgs(t, "vaultBundle", main, func(args []any) {
				permit := reflect.New(reflect.TypeOf(args[3])).Elem()
				permit.Set(reflect.ValueOf(args[3]))
				permit.Field(0).Set(reflect.ValueOf(big.NewInt(tc.value)))
				permit.Field(3).SetUint(uint64(tc.v))
				args[3] = permit.Interface()
			})
			_, err := VerifyCalldata(shares.Intent, []CalldataCall{approval, modified})
			requireError[*CalldataCallError](t, err, "approval", &approval.ID)
			if tc.v != 0 {
				_, err := VerifyCalldata(shares.Intent, []CalldataCall{modified})
				requireError[*CalldataAmountError](t, err, "permit.value", &main.ID)
			}
		})
	}
}

// verifyCalldata.test.ts: "checks the market in an in-kind redemption".
// vaultSdk.test.ts: second in-kind market and zero exact force-withdraw price floor.
func TestVaultExitArguments(t *testing.T) {
	vectors := vaultVectors(t)
	inKind := vectors["v2InKind"]
	intent := inKind.Intent
	intent.Market = "8453:" + intent.Market[2:]
	_, err := VerifyCalldata(intent, inKind.Calls)
	requireError[*CalldataTargetError](t, err, "market", &inKind.Calls[0].ID)
	markets := changeArgs(t, "vaultExit", inKind.Calls[0], func(args []any) {
		values := reflect.ValueOf(args[2])
		args[2] = reflect.Append(values, values.Index(0)).Interface()
	})
	_, err = VerifyCalldata(inKind.Intent, []CalldataCall{markets})
	requireError[*CalldataTargetError](t, err, "market", &markets.ID)
	intent = inKind.Intent
	intent.Asset = otherAccount
	_, err = VerifyCalldata(intent, inKind.Calls)
	requireError[*CalldataTargetError](t, err, "asset", &inKind.Calls[0].ID)

	force := vectors["v2Force"]
	zeroPrice := changeArgs(t, "vaultExit", force.Calls[0], func(args []any) { args[3] = big.NewInt(0) })
	_, err = VerifyCalldata(force.Intent, []CalldataCall{zeroPrice})
	requireError[*CalldataAmountError](t, err, "min_share_price", &zeroPrice.ID)
}

// verifyCalldata.test.ts: nested recipients, repeated empty deallocation routes,
// invalid force-deallocation arguments, and force redemption without deallocation.
func TestVaultForceRedeem(t *testing.T) {
	force := vaultVectors(t)["v2ForceAll"]
	call := force.Calls[0]
	decoded, err := VerifyCalldata(force.Intent, force.Calls)
	if err != nil {
		t.Fatal(err)
	}
	children := decoded[0].Calls
	for i, child := range children {
		want := "forceDeallocate"
		if i == len(children)-1 {
			want = "redeem"
		}
		if child.Call.ID != call.ID+"."+strconv.Itoa(i) || child.FunctionName != want {
			t.Fatalf("unexpected child: %+v", child)
		}
	}
	editChild := func(i int, edit func([]any)) CalldataCall {
		return changeArgs(t, "vault", call, func(args []any) {
			items := args[0].([][]byte)
			child := changeArgs(t, "vault", innerCalldata(call, items[i], i), edit)
			data, err := hex.DecodeString(child.Data[2:])
			if err != nil {
				t.Fatal(err)
			}
			items[i] = data
		})
	}
	firstID := call.ID + ".0"
	last := len(children) - 1
	lastID := call.ID + "." + strconv.Itoa(last)
	deallocate := editChild(0, func(args []any) { args[3] = common.HexToAddress(otherAccount) })
	_, err = VerifyCalldata(force.Intent, []CalldataCall{deallocate})
	requireError[*CalldataRecipientError](t, err, "onBehalf", &firstID)
	deallocate = editChild(0, func(args []any) { args[1] = []byte{0x01} })
	_, err = VerifyCalldata(force.Intent, []CalldataCall{deallocate})
	requireError[*CalldataDecodeError](t, err, "adapterData", &firstID)
	deallocate = editChild(0, func(args []any) { args[2] = big.NewInt(0) })
	_, err = VerifyCalldata(force.Intent, []CalldataCall{deallocate})
	requireError[*CalldataAmountError](t, err, "amount", &firstID)
	redeem := editChild(last, func(args []any) { args[1] = common.HexToAddress(otherAccount) })
	_, err = VerifyCalldata(force.Intent, []CalldataCall{redeem})
	requireError[*CalldataRecipientError](t, err, "receiver", &lastID)
	call.Value = "1"
	_, err = VerifyCalldata(force.Intent, []CalldataCall{call})
	requireError[*CalldataAmountError](t, err, "value", &call.ID)
	call.Value = "0"
	onlyRedeem := changeArgs(t, "vault", call, func(args []any) { args[0] = args[0].([][]byte)[last:] })
	_, err = VerifyCalldata(force.Intent, []CalldataCall{onlyRedeem})
	requireError[*CalldataCallError](t, err, "function", &call.ID)
}

// Go-specific regression test for canonical decode safety on arbitrary bytes.
func FuzzVerifyCalldata(f *testing.F) {
	vector := vaultVectors(f)["v2ForceAll"]
	call := vector.Calls[0]
	data, err := hex.DecodeString(call.Data[2:])
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		candidate := call
		candidate.Data = "0x" + hex.EncodeToString(data)
		_, _ = VerifyCalldata(vector.Intent, []CalldataCall{candidate})
	})
}
