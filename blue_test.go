package calldataverifier

import (
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

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
