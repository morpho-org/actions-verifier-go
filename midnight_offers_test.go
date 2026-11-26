package calldataverifier

import (
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestMidnightTakeJSONGuard(t *testing.T) {
	vector := loadVectors[MidnightTakeIntent](t, "midnight-offers-vectors.json")["exactAssets"]
	bin, err := json.Marshal(vector.Intent)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(bin, &fields); err != nil {
		t.Fatal(err)
	}
	last := vector.Calls[len(vector.Calls)-1]
	var intent MidnightTakeIntent
	fields["guard"] = json.RawMessage("null")
	bin, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bin, &intent); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataUnsupportedError](t, err, "guard.type", &last.ID)

	delete(fields, "guard")
	bin, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bin, &intent); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCalldata(intent, vector.Calls); err != nil {
		t.Fatal(err)
	}
}

func TestMidnightTakeLendContinuousFeeParity(t *testing.T) {
	vector := loadVectors[MidnightTakeIntent](t, "midnight-offers-vectors.json")["lend"]
	for _, cap := range []*big.Int{new(big.Int), new(big.Int).Set(maxUint256)} {
		calls := append([]CalldataCall(nil), vector.Calls...)
		last := len(calls) - 1
		calls[last] = changeArgs(t, "midnightBundle", calls[last], func(args []any) { args[10] = cap })
		if _, err := VerifyCalldata(vector.Intent, calls); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMidnightWithdrawFundingShareParity(t *testing.T) {
	vector := loadVectors[MidnightOfferManagementIntent](t, "midnight-offers-vectors.json")["withdrawFunding"]
	for _, shares := range []int64{1, 200} {
		call := changeArgs(t, "blue", vector.Calls[0], func(args []any) { args[2] = big.NewInt(shares) })
		if _, err := VerifyCalldata(vector.Intent, []CalldataCall{call}); err != nil {
			t.Fatal(err)
		}
	}
}

// midnightOffers.test.ts: verifies each prepared offer case.
func TestMidnightOfferVectors(t *testing.T) {
	takes := loadVectors[MidnightTakeIntent](t, "midnight-offers-vectors.json")
	management := loadVectors[MidnightOfferManagementIntent](t, "midnight-offers-vectors.json")
	for name, vector := range takes {
		t.Run(name, func(t *testing.T) {
			var intent CalldataIntent = vector.Intent
			if vector.Intent.Action == "midnight.cancel_offers" || vector.Intent.Action == "midnight.withdraw_funding" {
				intent = management[name].Intent
			}
			got, err := VerifyCalldata(intent, vector.Calls)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(vector.Calls) {
				t.Fatalf("got %d calls, want %d", len(got), len(vector.Calls))
			}
			if name == "batchCancellation" && len(got[0].Calls) != len(management[name].Intent.Groups) {
				t.Fatal("missing cancellation children")
			}
		})
	}
}

// midnightOffers.test.ts: checks the group, market, amount, optional maker, and every fill.
func TestMidnightTakeBindings(t *testing.T) {
	vector := loadVectors[MidnightTakeIntent](t, "midnight-offers-vectors.json")["exactAssets"]
	last := vector.Calls[len(vector.Calls)-1]
	zero := (common.Hash{}).Hex()
	intent := vector.Intent
	intent.Group = zero
	_, err := VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataTargetError](t, err, "group", &last.ID)
	intent = vector.Intent
	intent.MarketID = &zero
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataTargetError](t, err, "market", &last.ID)
	intent.MarketID = nil
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataUnsupportedError](t, err, "market_id", &last.ID)
	intent = vector.Intent
	intent.Maker = &intent.Account
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataCallError](t, err, "offer_fills", &last.ID)
	intent = vector.Intent
	intent.Guard = &MidnightGuard{Type: "max_rate", Value: "1"}
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataUnsupportedError](t, err, "guard.type", &last.ID)

	bad := changeArgs(t, "midnightBundle", last, func(args []any) {
		fills := reflect.ValueOf(args[6])
		second := reflect.New(fills.Type().Elem()).Elem()
		second.Set(fills.Index(0))
		second.FieldByName("Offer").FieldByName("Group").Set(reflect.ValueOf([32]byte{}))
		args[6] = reflect.Append(fills, second).Interface()
	})
	calls := append(append([]CalldataCall{}, vector.Calls[:len(vector.Calls)-1]...), bad)
	_, err = VerifyCalldata(vector.Intent, calls)
	requireError[*CalldataTargetError](t, err, "group", &last.ID)
}

// midnightOffers.test.ts: checks cancellation groups, consumed amount, and funding withdrawal.
func TestMidnightOfferManagement(t *testing.T) {
	vectors := loadVectors[MidnightOfferManagementIntent](t, "midnight-offers-vectors.json")
	vector := vectors["batchCancellation"]
	call := vector.Calls[0]
	intent := vector.Intent
	intent.Groups = []string{intent.Groups[0], intent.Groups[0]}
	_, err := VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataCallError](t, err, "groups", &call.ID)
	intent.Groups = []string{}
	_, err = VerifyCalldata(intent, vector.Calls)
	requireError[*CalldataCallError](t, err, "groups", &call.ID)
	bad := changeArgs(t, "midnight", call, func(args []any) {
		legs := args[0].([][]byte)
		leg := innerCalldata(call, legs[0], 0)
		tampered := changeArgs(t, "midnight", leg, func(args []any) { args[1] = big.NewInt(1) })
		legs[0] = common.FromHex(tampered.Data)
	})
	id := call.ID + ".0"
	_, err = VerifyCalldata(vector.Intent, []CalldataCall{bad})
	requireError[*CalldataAmountError](t, err, "consumed", &id)

	vector = vectors["withdrawFunding"]
	call = vector.Calls[0]
	bad = changeArgs(t, "blue", call, func(args []any) { args[2] = new(big.Int) })
	_, err = VerifyCalldata(vector.Intent, []CalldataCall{bad})
	requireError[*CalldataAmountError](t, err, "shares", &call.ID)

	for _, action := range []string{"midnight.make_borrow", "midnight.make_lend", "midnight.make_repay", "midnight.make_withdraw"} {
		intent = vector.Intent
		intent.Action = action
		_, err := VerifyCalldata(intent, vector.Calls)
		requireError[*CalldataUnsupportedError](t, err, action, &call.ID)
	}
}
