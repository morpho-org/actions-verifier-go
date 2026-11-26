package calldataverifier

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// checks.test.ts: rejects nondecimal and out-of-range uints, and invalid referral bps.
func TestCheckNumericBounds(t *testing.T) {
	call := CalldataCall{ID: "approval"}
	for _, value := range []string{"0x1", new(big.Int).Add(maxUint256, big.NewInt(1)).String()} {
		_, err := readUint(value, "value", call)
		requireError[*CalldataAmountError](t, err, "value", &call.ID)
	}
	for _, bps := range []int{-1, 10_001} {
		err := checkFee(new(big.Int), common.Address{}, 0, &ReferralFee{BPS: bps}, call)
		requireError[*CalldataFeeError](t, err, "referral_fee.bps", &call.ID)
	}
	for _, input := range []struct {
		dst any
		raw string
	}{
		{&call, `{"id":"approval","kind":"approve","value":1}`},
		{new(ReferralFee), `{"bps":"0"}`},
		{new(ReferralFee), `{"bps":0.5}`},
	} {
		var wrongType *json.UnmarshalTypeError
		if err := json.Unmarshal([]byte(input.raw), input.dst); !errors.As(err, &wrongType) {
			t.Fatalf("input %s: got %v, want JSON type error", input.raw, err)
		}
	}
}

// checks.test.ts: verifies approval function, zero value and canonical encoding.
func TestApprovalAndCanonicalEncoding(t *testing.T) {
	contract := defaultSnapshot.abis["erc20"]
	token := common.HexToAddress("0x1000000000000000000000000000000000000001")
	spender := common.HexToAddress("0x2000000000000000000000000000000000000002")
	makeCall := func(name string, recipient common.Address, amount int64) CalldataCall {
		t.Helper()
		data, err := contract.Pack(name, recipient, big.NewInt(amount))
		if err != nil {
			t.Fatal(err)
		}
		return CalldataCall{ID: "approval", Kind: "approve", To: token.Hex(), Value: "0", Data: "0x" + common.Bytes2Hex(data)}
	}

	call := makeCall("approve", spender, 1)
	payable := call
	payable.Value = "1"
	_, err := checkApproval(payable, spender.Hex(), token.Hex(), nil, false)
	requireError[*CalldataAmountError](t, err, "value", &call.ID)
	_, err = checkApproval(makeCall("transfer", spender, 1), spender.Hex(), token.Hex(), nil, false)
	requireError[*CalldataCallError](t, err, "function", &call.ID)

	withSuffix := call
	withSuffix.Data += "00"
	got, err := decodeCall(withSuffix, contract, false)
	if err != nil || got.FunctionName != "approve" {
		t.Fatalf("outer calldata: got %v, %v", got, err)
	}
	_, err = decodeCall(withSuffix, contract, true)
	requireError[*CalldataDecodeError](t, err, "data", &call.ID)
	var inner *CalldataDecodeError
	if errors.As(errors.Unwrap(err), &inner) {
		t.Fatalf("nested decode error wrapped another decode error: %v", err)
	}
	for _, data := range []string{call.Data + "0", call.Data + "zz", call.Data[:10] + "01" + call.Data[12:]} {
		bad := call
		bad.Data = data
		_, err := decodeCall(bad, contract, false)
		requireError[*CalldataDecodeError](t, err, "data", &call.ID)
	}

	zero := makeCall("approve", common.Address{}, 0)
	got, err = decodeCall(zero, contract, false)
	if err != nil || got.Args[0] != (common.Address{}) || got.Args[1].(*big.Int).Sign() != 0 {
		t.Fatalf("zero approval arguments: got %v, %v", got.Args, err)
	}
	zero.Data += "0"
	_, err = decodeCall(zero, contract, false)
	requireError[*CalldataDecodeError](t, err, "data", &call.ID)
}

func TestCanonicalEncodingRejectsNarrowIntegerOverflow(t *testing.T) {
	contract, err := abi.JSON(strings.NewReader(`[
		{"type":"function","name":"unsigned","inputs":[{"name":"value","type":"uint128"}]},
		{"type":"function","name":"signed","inputs":[{"name":"value","type":"int128"}]},
		{"type":"function","name":"nested","inputs":[{"name":"values","type":"tuple[]","components":[{"name":"values","type":"uint128[1]"}]}]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	one := big.NewInt(1)
	uintLimit := new(big.Int).Lsh(one, 128)
	intLimit := new(big.Int).Lsh(one, 127)
	maxUnsigned := new(big.Int).Sub(uintLimit, one)
	minSigned := new(big.Int).Neg(intLimit)
	type tuple struct{ Values [1]*big.Int }
	for _, tc := range []struct {
		name, method string
		arg          any
		ok           bool
	}{
		{"unsigned_max", "unsigned", maxUnsigned, true},
		{"unsigned_overflow", "unsigned", uintLimit, false},
		{"signed_max", "signed", new(big.Int).Sub(intLimit, one), true},
		{"signed_overflow", "signed", intLimit, false},
		{"signed_min", "signed", minSigned, true},
		{"signed_underflow", "signed", new(big.Int).Sub(minSigned, one), false},
		{"nested_max", "nested", []tuple{{Values: [1]*big.Int{maxUnsigned}}}, true},
		{"nested_overflow", "nested", []tuple{{Values: [1]*big.Int{uintLimit}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := contract.Pack(tc.method, tc.arg)
			if err != nil {
				t.Fatal(err)
			}
			call := CalldataCall{ID: tc.name, Data: "0x" + common.Bytes2Hex(data)}
			decoded, err := decodeCall(call, contract, true)
			if !tc.ok {
				requireError[*CalldataDecodeError](t, err, "data", &call.ID)
				return
			}
			if err != nil || decoded.FunctionName != tc.method {
				t.Fatalf("got %v, %v; want decoded %s", decoded, err, tc.method)
			}
		})
	}
}
