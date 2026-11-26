package calldataverifier

import (
	"bytes"
	"encoding/json"
	"math/big"
	"slices"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// midnight.test.ts: verify prepared market cases against their original intent.
func TestMidnightMarketVectors(t *testing.T) {
	for name, vector := range loadVectors[MidnightMarketIntent](t, "midnight-vectors.json") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := VerifyCalldata(vector.Intent, vector.Calls)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(vector.Calls) {
				t.Fatalf("decoded %d calls, want %d", len(got), len(vector.Calls))
			}
			for i := range got {
				if got[i].Call != vector.Calls[i] || got[i].FunctionName == "" {
					t.Fatalf("unexpected decoded call: %+v", got[i])
				}
			}
			intent := vector.Intent
			intent.Market = (common.Hash{}).Hex()
			_, err = VerifyCalldata(intent, vector.Calls)
			last := got[len(got)-1]
			id := last.Call.ID
			if len(last.Calls) > 0 {
				id = last.Calls[0].Call.ID
			}
			requireError[*CalldataTargetError](t, err, "market", &id)
		})
	}
}

// midnight.test.ts: lender pull caps, unsupported rate guards, and null collateral lists.
func TestMidnightGuardAndCollateralLists(t *testing.T) {
	vectors := loadVectors[MidnightMarketIntent](t, "midnight-vectors.json")
	lend := vectors["lend_exactAssets"]
	intent := lend.Intent
	intent.Guard = &MidnightGuard{Type: "max_assets", Value: "0"}
	last := lend.Calls[len(lend.Calls)-1]
	_, err := VerifyCalldata(intent, lend.Calls)
	requireError[*CalldataAmountError](t, err, "guard.value", &last.ID)
	intent.Guard = &MidnightGuard{Type: "min_rate", Value: "0"}
	_, err = VerifyCalldata(intent, lend.Calls)
	requireError[*CalldataUnsupportedError](t, err, "guard.type", &last.ID)

	for name, key := range map[string]string{"repay_partialErc20": "collateral_withdrawals", "borrow_exactAssets": "collateral_supplies"} {
		vector := vectors[name]
		data, err := json.Marshal(vector.Intent)
		if err != nil {
			t.Fatal(err)
		}
		data = append(bytes.TrimSuffix(data, []byte("}")), []byte(",\""+key+"\":null}")...)
		var malformed MidnightMarketIntent
		if err := json.Unmarshal(data, &malformed); err != nil {
			t.Fatal(err)
		}
		_, err = VerifyCalldata(malformed, vector.Calls)
		requireError[*CalldataCallError](t, err, "collateral", &vector.Calls[len(vector.Calls)-1].ID)
	}
}

// midnight.test.ts: rejects zero all-mode legs and decodes and checks each multicall leg.
func TestMidnightCoreCollateralWithdrawal(t *testing.T) {
	vector := loadVectors[MidnightMarketIntent](t, "midnight-vectors.json")["repay_collateralOnlyMultiple"]
	last := vector.Calls[len(vector.Calls)-1]
	decoded, err := decodeCall(last, defaultSnapshot.abis["midnight"], false)
	if err != nil {
		t.Fatal(err)
	}
	intent := vector.Intent
	contract := defaultSnapshot.abis["midnight"]
	call := last
	data := decoded.Args[0].([][]byte)
	if len(data) < 2 {
		t.Fatal("fixture must have multiple withdrawals")
	}
	makeMulticall := func(legs [][]byte) CalldataCall {
		packed, err := contract.Pack("multicall", legs)
		if err != nil {
			t.Fatal(err)
		}
		call.Data = "0x" + common.Bytes2Hex(packed)
		return call
	}
	verified, err := VerifyCalldata(intent, []CalldataCall{makeMulticall(data)})
	if err != nil {
		t.Fatal(err)
	}
	if len(verified[0].Calls) != len(data) || verified[0].Calls[0].Call.ID != call.ID+".0" {
		t.Fatalf("unexpected checked children: %+v", verified[0].Calls)
	}
	tampered := slices.Clone(data)
	zero := changeArgs(t, "midnight", verified[0].Calls[0].Call, func(args []any) { args[2] = new(big.Int) })
	tampered[0] = common.FromHex(zero.Data)
	_, err = VerifyCalldata(intent, []CalldataCall{makeMulticall(tampered)})
	requireError[*CalldataAmountError](t, err, "collateral.amount", &call.ID)
	tampered[0] = []byte{1}
	_, err = VerifyCalldata(intent, []CalldataCall{makeMulticall(tampered)})
	childID := call.ID + ".0"
	requireError[*CalldataDecodeError](t, err, "data", &childID)
}
