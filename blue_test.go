package calldataverifier

import (
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestParityBlue(t *testing.T) {
	runParity(t, "blue", parityIntent[BlueCalldataIntent], nil)
}

// blue.test.ts: "requires authorization for native Blue supply" and
// "rejects native Blue funding with an embedded ERC-2612 permit".
func TestBlueNativeSupply(t *testing.T) {
	vector := loadVectors[BlueCalldataIntent](t, "blue-vectors.json")["supply_allowanceSufficient"]
	call := vector.Calls[0]
	intent := vector.Intent
	value := intent.Amount.Value
	var params blueMarketParams
	call = changeArgs(t, "blueBundle", call, func(args []any) {
		params = converted[blueMarketParams](args[0])
		params.LoanToken = common.HexToAddress(defaultSnapshot.Deployments[intent.ChainID].WNative)
		args[0] = params
	})
	intent.Market = blueMarketID(params)
	call.Value = value
	intent.Funding = &Funding{Type: "native", MaxNativeAssets: value}
	if _, err := VerifyCalldata(intent, []CalldataCall{call}); err != nil {
		t.Fatal(err)
	}
	for _, funding := range []*Funding{nil, {Type: "unknown", MaxNativeAssets: value}, {Type: "auto", MaxNativeAssets: "1"}} {
		changed := intent
		changed.Funding = funding
		_, err := VerifyCalldata(changed, []CalldataCall{call})
		requireError[*CalldataAmountError](t, err, "value", &call.ID)
	}
	wrongValue := call
	wrongValue.Value = "1"
	_, err := VerifyCalldata(intent, []CalldataCall{wrongValue})
	requireError[*CalldataAmountError](t, err, "value", &call.ID)
	wrongToken := vector.Calls[0]
	wrongToken.Value = value
	original := vector.Intent
	original.Funding = intent.Funding
	_, err = VerifyCalldata(original, []CalldataCall{wrongToken})
	requireError[*CalldataAmountError](t, err, "value", &call.ID)
	sig := signature{V: 27, R: [32]byte{1}, S: [32]byte{1}}
	permitted := changeArgs(t, "blueBundle", call, func(args []any) {
		args[2] = tokenPermit{Kind: 1, Data: bluePermitData(t, sig)}
	})
	_, err = VerifyCalldata(intent, []CalldataCall{permitted})
	requireError[*CalldataAmountError](t, err, "value", &call.ID)
}

func bluePermitData(t *testing.T, sig signature) []byte {
	t.Helper()
	data, err := defaultSnapshot.permits[1].Pack(big.NewInt(1820000000), sig.V, sig.R, sig.S)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// blue.test.ts: checks embedded funding permit kinds and ABI payloads,
// malformed Morpho authorization signatures, and duplicate authorization.
func TestBlueEmbeddedSignatures(t *testing.T) {
	vectors := loadVectors[BlueCalldataIntent](t, "blue-vectors.json")
	supply := vectors["supply_allowanceSufficient"]
	withdraw := vectors["withdraw_assets"]
	call := supply.Calls[0]
	sig := signature{V: 27, R: [32]byte{1}, S: [32]byte{1}}
	permit := func(kind uint8, data []byte) CalldataCall {
		return changeArgs(t, "blueBundle", call, func(args []any) { args[2] = tokenPermit{Kind: kind, Data: data} })
	}
	valid := bluePermitData(t, sig)
	if _, err := VerifyCalldata(supply.Intent, []CalldataCall{permit(1, valid)}); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyCalldata(supply.Intent, []CalldataCall{permit(2, valid)})
	requireError[*CalldataCallError](t, err, "permit.kind", &call.ID)
	badSigs := []signature{
		{V: 29, R: sig.R, S: sig.S}, {V: 27, S: sig.S}, {V: 27, R: sig.R},
	}
	malformed := [][]byte{nil, append(append([]byte{}, valid...), 0)}
	for _, bad := range badSigs {
		malformed = append(malformed, bluePermitData(t, bad))
	}
	for _, bad := range malformed {
		_, err := VerifyCalldata(supply.Intent, []CalldataCall{permit(1, bad)})
		requireError[*CalldataDecodeError](t, err, "permit.data", &call.ID)
	}
	grant, exit := withdraw.Calls[0], withdraw.Calls[1]
	auth := func(sig signature) CalldataCall {
		return changeArgs(t, "blueBundle", exit, func(args []any) {
			args[3] = struct {
				Signature       signature
				Nonce, Deadline *big.Int
			}{sig, big.NewInt(0), big.NewInt(1820000000)}
		})
	}
	signed := auth(sig)
	if _, err := VerifyCalldata(withdraw.Intent, []CalldataCall{signed}); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyCalldata(withdraw.Intent, []CalldataCall{grant, signed})
	requireError[*CalldataCallError](t, err, "authorization", &grant.ID)
	badSigs[0].V = 0
	for _, bad := range badSigs {
		_, err := VerifyCalldata(withdraw.Intent, []CalldataCall{auth(bad)})
		requireError[*CalldataDecodeError](t, err, "authorization.signature", &exit.ID)
	}
}

// blue.test.ts: "accepts distinct idle and market sources, and rejects invalid reallocations".
func TestBlueReallocations(t *testing.T) {
	vector := loadVectors[BlueCalldataIntent](t, "blue-vectors.json")["borrow_exactAssets"]
	grant, call := vector.Calls[0], vector.Calls[1]
	decoded, err := decodeCall(call, defaultSnapshot.abis["blueBundle"], false)
	if err != nil {
		t.Fatal(err)
	}
	params := converted[blueMarketParams](decoded.Args[0])
	idle := blueReallocation{
		Vault: common.HexToAddress(otherAccount), Adapter: common.HexToAddress("0x1000000000000000000000000000000000000003"),
		MarketParams: params, SourceMarketParams: params, FromIdle: true, Assets: big.NewInt(123),
	}
	market := idle
	market.FromIdle, market.SourceAdapter, market.SourceMarketParams.Irm = false, idle.Adapter, idle.Vault
	verify := func(entries ...blueReallocation) error {
		changed := changeArgs(t, "blueBundle", call, func(args []any) {
			args[6] = entries
		})
		_, err := VerifyCalldata(vector.Intent, []CalldataCall{grant, changed})
		return err
	}
	if err := verify(idle, market); err != nil {
		t.Fatal(err)
	}
	requireError[*CalldataCallError](t, verify(idle, idle), "reallocations", &call.ID)
	t.Run("market source without adapter", func(t *testing.T) {
		bad := market
		bad.SourceAdapter = common.Address{}
		requireError[*CalldataCallError](t, verify(bad), "reallocations", &call.ID)
	})
	t.Run("market source equals destination", func(t *testing.T) {
		bad := market
		bad.SourceMarketParams = bad.MarketParams
		requireError[*CalldataCallError](t, verify(bad), "reallocations", &call.ID)
	})
	for name, modify := range map[string]func(*blueReallocation){
		"destination":    func(e *blueReallocation) { e.MarketParams = market.SourceMarketParams },
		"amount":         func(e *blueReallocation) { e.Assets = new(big.Int) },
		"vault":          func(e *blueReallocation) { e.Vault = common.Address{} },
		"adapter":        func(e *blueReallocation) { e.Adapter = common.Address{} },
		"loan token":     func(e *blueReallocation) { e.SourceMarketParams.LoanToken = common.Address{} },
		"source adapter": func(e *blueReallocation) { e.SourceAdapter = e.Adapter },
	} {
		t.Run(name, func(t *testing.T) {
			bad := idle
			modify(&bad)
			requireError[*CalldataCallError](t, verify(bad), "reallocations", &call.ID)
		})
	}
	idle.Penalty = 1
	requireError[*CalldataFeeError](t, verify(idle), "reallocations.penalty", &call.ID)
}

// blue.test.ts: verifies each prepared case and rejects another market.
func TestBlueVectors(t *testing.T) {
	for name, vector := range loadVectors[BlueCalldataIntent](t, "blue-vectors.json") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			verified, err := VerifyCalldata(vector.Intent, vector.Calls)
			if err != nil {
				t.Fatal(err)
			}
			if len(verified) != len(vector.Calls) {
				t.Fatalf("got %d calls, want %d", len(verified), len(vector.Calls))
			}
			for i, decoded := range verified {
				if decoded.Call != vector.Calls[i] || decoded.FunctionName == "" {
					t.Fatalf("unexpected decoded call %d: %+v", i, decoded)
				}
			}
			intent := vector.Intent
			intent.Market = "0x" + strings.Repeat("00", 32)
			_, err = VerifyCalldata(intent, vector.Calls)
			requireError[*CalldataTargetError](t, err, "market", &vector.Calls[len(vector.Calls)-1].ID)
		})
	}
}

// blue.test.ts: amount, approval spender, approval reset, and fee limits.
func TestBlueSupply(t *testing.T) {
	vectors := loadVectors[BlueCalldataIntent](t, "blue-vectors.json")
	vector := vectors["supply_approvalRequired"]
	approval, supply := vector.Calls[0], vector.Calls[1]
	intent := vector.Intent
	bad := intent
	bad.Amount.Value = "1"
	_, err := VerifyCalldata(bad, vector.Calls)
	requireError[*CalldataAmountError](t, err, "amount", &supply.ID)
	bad.Amount.Unit = "unknown"
	_, err = VerifyCalldata(bad, vector.Calls)
	requireError[*CalldataAmountError](t, err, "amount.unit", &supply.ID)
	wrongSpender := changeArgs(t, "erc20", approval, func(args []any) { args[0] = common.HexToAddress(intent.Account) })
	_, err = VerifyCalldata(intent, []CalldataCall{wrongSpender, supply})
	requireError[*CalldataTargetError](t, err, "approval.spender", &approval.ID)
	reset := changeArgs(t, "erc20", approval, func(args []any) { args[1] = big.NewInt(0) })
	reset.ID = "reset"
	if _, err := VerifyCalldata(intent, []CalldataCall{reset, approval, supply}); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyCalldata(intent, []CalldataCall{approval, reset, supply})
	requireError[*CalldataCallError](t, err, "approval", &reset.ID)
	_, err = VerifyCalldata(intent, []CalldataCall{reset, supply})
	requireError[*CalldataCallError](t, err, "approval", &reset.ID)

	vector = vectors["supply_referralFee"]
	intent = vector.Intent
	intent.MaxFeeBPS = 0
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataFeeError](t, err, "max_fee_bps", &vector.Calls[len(vector.Calls)-1].ID)
	intent = vector.Intent
	referral := *intent.ReferralFee
	referral.BPS--
	intent.ReferralFee = &referral
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataFeeError](t, err, "referral_fee.bps", &vector.Calls[len(vector.Calls)-1].ID)
}

// blue.test.ts: "distinguishes repay-all from an exact share repay",
// "rejects a larger maxRepayAssets on an exact-assets repay",
// and "rejects a zero maximum in an all-mode Blue repay".
func TestBlueRepay(t *testing.T) {
	vectors := loadVectors[BlueCalldataIntent](t, "blue-vectors.json")
	vector := vectors["repay_allShares"]
	calls := slices.Clone(vector.Calls)
	last := len(calls) - 1
	intent := vector.Intent
	intent.Amount = Amount{Unit: "shares", Value: maxUint256.String()}
	_, err := VerifyCalldata(intent, calls)
	requireError[*CalldataAmountError](t, err, "amount", &calls[last].ID)
	calls[last] = changeArgs(t, "blueBundle", calls[last], func(args []any) { args[3] = big.NewInt(0) })
	_, err = VerifyCalldata(vector.Intent, calls)
	requireError[*CalldataAmountError](t, err, "amount", &calls[last].ID)

	vector = vectors["repay_exactAssets"]
	calls = slices.Clone(vector.Calls)
	last = len(calls) - 1
	calls[last] = changeArgs(t, "blueBundle", calls[last], func(args []any) {
		args[3] = new(big.Int).Add(args[3].(*big.Int), big.NewInt(1))
	})
	_, err = VerifyCalldata(vector.Intent, calls)
	requireError[*CalldataAmountError](t, err, "max_repay_assets", &calls[last].ID)
}

// blue.test.ts: altered refinance destination and unauthorized or duplicated delegation.
func TestBlueRefinanceAndDelegation(t *testing.T) {
	vectors := loadVectors[BlueCalldataIntent](t, "blue-vectors.json")
	vector := vectors["refinance_noFee"]
	intent := vector.Intent
	intent.DestinationMarket = "1:0x" + strings.Repeat("00", 32)
	_, err := VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataTargetError](t, err, "destination_market", &vector.Calls[len(vector.Calls)-1].ID)

	vector = vectors["borrow_exactAssets"]
	borrow := vector.Calls[len(vector.Calls)-1]
	chain := defaultSnapshot.Deployments[vector.Intent.ChainID]
	auth := CalldataCall{ID: "authorization", To: chain.Blue, Value: "0"}
	packed, err := defaultSnapshot.abis["blue"].Pack("setAuthorization", common.HexToAddress(chain.BlueBundlesV1), true)
	if err != nil {
		t.Fatal(err)
	}
	auth.Data = "0x" + common.Bytes2Hex(packed)
	if _, err := VerifyCalldata(vector.Intent, []CalldataCall{auth, borrow}); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyCalldata(vector.Intent, []CalldataCall{auth, auth, borrow})
	requireError[*CalldataCallError](t, err, "authorization", &auth.ID)
	wrongSpender := changeArgs(t, "blue", auth, func(args []any) { args[0] = common.HexToAddress(vector.Intent.Account) })
	_, err = VerifyCalldata(vector.Intent, []CalldataCall{wrongSpender, borrow})
	requireError[*CalldataTargetError](t, err, "authorization.spender", &auth.ID)
}
