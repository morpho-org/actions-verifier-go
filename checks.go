package calldataverifier

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

var wad = big.NewInt(1_000_000_000_000_000_000)
var maxUint128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
var errNonCanonical = errors.New("noncanonical ABI encoding")
var errABIIntegerWidth = errors.New("ABI integer exceeds its declared width")

func sameAddress(a, b string) bool {
	return validAddress(a) && validAddress(b) && strings.EqualFold(a, b)
}

func target(call CalldataCall, want string) error {
	if !sameAddress(call.To, want) {
		return &CalldataTargetError{detail("to", &call, nil)}
	}
	return nil
}

func readUint(value, field string, call CalldataCall) (*big.Int, error) {
	if len(value) > 0 && len(value) <= 78 && strings.Trim(value, "0123456789") == "" {
		n, ok := new(big.Int).SetString(value, 10)
		if ok && n.BitLen() <= 256 {
			return n, nil
		}
	}
	return nil, amountError(field, call)
}

func amountError(field string, call CalldataCall) error {
	return &CalldataAmountError{detail(field, &call, nil)}
}

func expectZeroValue(call CalldataCall) error {
	value, err := readUint(call.Value, "value", call)
	if err == nil && value.Sign() != 0 {
		return amountError("value", call)
	}
	return err
}

func recipient(actual common.Address, expected, field string, call CalldataCall) error {
	if !sameAddress(actual.Hex(), expected) {
		return &CalldataRecipientError{detail(field, &call, nil)}
	}
	return nil
}

func checkFee(fee *big.Int, to common.Address, max int, referral *ReferralFee, call CalldataCall) error {
	capFor := func(bps int) *big.Int { return big.NewInt(int64(bps) * 100_000_000_000_000) }
	if fee.Cmp(wad) >= 0 || fee.Cmp(capFor(max)) > 0 {
		return &CalldataFeeError{detail("max_fee_bps", &call, nil)}
	}
	if referral != nil && (referral.missingBPS || referral.BPS < 0 || referral.BPS > 10_000 || fee.Cmp(capFor(referral.BPS)) > 0) {
		return &CalldataFeeError{detail("referral_fee.bps", &call, nil)}
	}
	if fee.Sign() > 0 && (referral == nil || !sameAddress(to.Hex(), referral.Recipient)) {
		return &CalldataFeeError{detail("referral_fee.recipient", &call, nil)}
	}
	return nil
}

func checkNativeBudget(value *big.Int, funding *Funding, call CalldataCall) error {
	if value.Sign() == 0 {
		return nil
	}
	if funding != nil && (funding.Type == "native" || funding.Type == "auto") {
		budget, err := readUint(funding.MaxNativeAssets, "max_native_assets", call)
		if err != nil {
			return err
		}
		if value.Cmp(budget) <= 0 {
			return nil
		}
	}
	return amountError("value", call)
}

func unpackCanonical(inputs abi.Arguments, data []byte, exact bool) ([]any, error) {
	size, ok := abiSequence(data, len(inputs), func(i int) *abi.Type { return &inputs[i].Type })
	if !ok || (exact && size != len(data)) {
		return nil, errNonCanonical
	}
	args, err := inputs.Unpack(data[:size])
	if err != nil {
		return nil, err
	}
	for i, arg := range args {
		if !fitsABIIntegers(inputs[i].Type, reflect.ValueOf(arg)) {
			return nil, errABIIntegerWidth
		}
	}
	canonical, err := inputs.Pack(args...)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, data[:size]) {
		return nil, errNonCanonical
	}
	if args == nil {
		args = []any{}
	}
	return args, nil
}

func fitsABIIntegers(t abi.Type, value reflect.Value) bool {
	switch t.T {
	case abi.IntTy, abi.UintTy:
		if t.Size == 256 || value.Kind() != reflect.Pointer {
			return true
		}
		n := value.Interface().(*big.Int)
		if t.T == abi.UintTy {
			return n.BitLen() <= t.Size
		}
		if n.Sign() < 0 {
			n = new(big.Int).Not(n)
		}
		return n.BitLen() < t.Size
	case abi.TupleTy:
		for i, elem := range t.TupleElems {
			if !fitsABIIntegers(*elem, value.Field(i)) {
				return false
			}
		}
	case abi.ArrayTy, abi.SliceTy:
		for i := 0; i < value.Len(); i++ {
			if !fitsABIIntegers(*t.Elem, value.Index(i)) {
				return false
			}
		}
	}
	return true
}

func decodeCall(call CalldataCall, contract abi.ABI, nested bool) (DecodedCalldataCall, error) {
	fail := func(cause error) (DecodedCalldataCall, error) {
		return DecodedCalldataCall{}, &CalldataDecodeError{detail("data", &call, cause)}
	}
	if len(call.Data) < 10 || call.Data[:10] != strings.ToLower(call.Data[:10]) || !strings.HasPrefix(call.Data, "0x") {
		return fail(nil)
	}
	data, err := hex.DecodeString(call.Data[2:])
	if err != nil {
		return fail(err)
	}
	method, err := contract.MethodById(data)
	if err != nil {
		return fail(err)
	}
	args, err := unpackCanonical(method.Inputs, data[4:], nested)
	if err != nil {
		if err == errNonCanonical {
			err = nil
		}
		return fail(err)
	}
	return DecodedCalldataCall{Call: call, FunctionName: method.RawName, Args: args}, nil
}

func selectMain(calls []CalldataCall, max int) (CalldataCall, error) {
	if len(calls) == 0 {
		return CalldataCall{}, callError("calls", nil)
	}
	if len(calls) > max {
		return CalldataCall{}, callError("calls", &calls[0])
	}
	return calls[len(calls)-1], nil
}

func innerCalldata(parent CalldataCall, data []byte, index int) CalldataCall {
	parent.ID += "." + strconv.Itoa(index)
	parent.Data = "0x" + hex.EncodeToString(data)
	parent.Value = "0"
	return parent
}

func checkApproval(call CalldataCall, spender, token string, min *big.Int, allowReset bool) (DecodedCalldataCall, error) {
	if token != "" && !sameAddress(call.To, token) {
		return DecodedCalldataCall{}, &CalldataTargetError{detail("approval.token", &call, nil)}
	}
	if err := expectZeroValue(call); err != nil {
		return DecodedCalldataCall{}, err
	}
	decoded, err := decodeCall(call, defaultSnapshot.abis["erc20"], false)
	if err != nil {
		return DecodedCalldataCall{}, err
	}
	if decoded.FunctionName != "approve" {
		return DecodedCalldataCall{}, callError("function", &call)
	}
	if !sameAddress(decoded.Args[0].(common.Address).Hex(), spender) {
		return DecodedCalldataCall{}, &CalldataTargetError{detail("approval.spender", &call, nil)}
	}
	amount := decoded.Args[1].(*big.Int)
	if amount.Sign() == 0 && !allowReset {
		return DecodedCalldataCall{}, callError("approval", &call)
	}
	if amount.Sign() != 0 && min != nil && amount.Cmp(min) < 0 {
		return DecodedCalldataCall{}, amountError("approval.amount", call)
	}
	return decoded, nil
}
