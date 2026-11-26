package calldataverifier

import (
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func verifyVault(intent VaultCalldataIntent, calls []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	var (
		contract, method, to string
		maxCalls             int
	)
	switch intent.Action {
	case "vault_v1.deposit", "vault_v2.deposit":
		contract, method, to, maxCalls = "vaultBundle", "vaultBundlesV1Deposit", chain.VaultBundlesV1, 3
	case "vault_v1.withdraw", "vault_v2.withdraw":
		contract, method, to, maxCalls = "vaultBundle", "vaultBundlesV1Withdraw", chain.VaultBundlesV1, 2
	case "vault_v2.force_withdraw":
		if intent.Amount.Unit == "all" {
			contract, method, to, maxCalls = "vault", "multicall", intent.Vault, 1
		} else {
			contract, method, to, maxCalls = "vaultExit", "vaultExitBundlesV1ForceWithdrawVaultV2", chain.VaultExitBundlesV1, 2
		}
	case "vault_v2.redeem_in_kind":
		contract, method, to, maxCalls = "vaultExit", "vaultExitBundlesV1InKindRedemptionVaultV2", chain.VaultExitBundlesV1, 2
	default:
		if len(calls) == 0 {
			return nil, callError("action", nil)
		}
		return nil, callError("action", &calls[0])
	}
	call, err := selectMain(calls, maxCalls)
	if err != nil {
		return nil, err
	}
	if !validAddress(intent.Asset) {
		return nil, &CalldataTargetError{detail("asset", &call, nil)}
	}
	if err := target(call, to); err != nil {
		return nil, err
	}
	if method != "vaultBundlesV1Deposit" {
		if err := expectZeroValue(call); err != nil {
			return nil, err
		}
	}
	decoded, err := decodeCall(call, defaultSnapshot.abis[contract], false)
	if err != nil {
		return nil, err
	}
	if decoded.FunctionName != method {
		return nil, callError("function", &call)
	}
	if method == "multicall" {
		return verifyVaultForceRedeem(intent, decoded)
	}
	if !sameAddress(decoded.Args[0].(common.Address).Hex(), intent.Vault) {
		return nil, &CalldataTargetError{detail("vault", &call, nil)}
	}
	prefix := calls[:len(calls)-1]
	switch method {
	case "vaultBundlesV1Deposit":
		return verifyVaultDeposit(intent, decoded, prefix, chain)
	case "vaultBundlesV1Withdraw":
		return verifyVaultWithdraw(intent, decoded, prefix)
	default:
		return verifyVaultExit(intent, decoded, prefix)
	}
}

func checkAssets(actual *big.Int, requested Amount, call CalldataCall) error {
	if requested.Unit != "assets" {
		return amountError("amount.unit", call)
	}
	n, err := readUint(requested.Value, "amount", call)
	if err != nil {
		return err
	}
	if actual.Sign() == 0 || actual.Cmp(n) != 0 {
		return amountError("amount", call)
	}
	return nil
}

func verifyVaultDeposit(intent VaultCalldataIntent, main DecodedCalldataCall, prefix []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	call, args := main.Call, main.Args
	assets, price := args[1].(*big.Int), args[2].(*big.Int)
	if err := checkAssets(assets, intent.Amount, call); err != nil {
		return nil, err
	}
	if price.Sign() == 0 || price.Cmp(maxUint256) == 0 {
		return nil, amountError("max_share_price", call)
	}
	if err := checkFee(args[4].(*big.Int), args[5].(common.Address), intent.MaxFeeBPS, intent.ReferralFee, call); err != nil {
		return nil, err
	}
	value, err := readUint(call.Value, "value", call)
	if err != nil {
		return nil, err
	}
	if err := checkNativeBudget(value, intent.Funding, call); err != nil {
		return nil, err
	}
	permit := reflect.ValueOf(args[3])
	kind, data := uint8(permit.Field(0).Uint()), permit.Field(1).Bytes()
	if kind == 0 {
		if len(data) != 0 {
			return nil, &CalldataDecodeError{detail("permit.data", &call, nil)}
		}
	} else {
		params, ok := defaultSnapshot.permits[kind]
		if !ok {
			return nil, callError("permit.kind", &call)
		}
		if _, err := unpackCanonical(params, data, true); err != nil {
			return nil, &CalldataDecodeError{detail("permit.data", &call, nil)}
		}
	}
	if value.Sign() != 0 {
		if !sameAddress(intent.Asset, chain.WNative) {
			return nil, &CalldataTargetError{detail("asset", &call, nil)}
		}
		if value.Cmp(assets) != 0 {
			return nil, amountError("value", call)
		}
		if kind != 0 {
			return nil, callError("permit", &call)
		}
	}
	if len(prefix) == 0 {
		return []DecodedCalldataCall{main}, nil
	}
	approval := prefix[len(prefix)-1]
	if value.Sign() != 0 || kind != 0 {
		return nil, callError("approval", &approval)
	}
	verified, err := checkApproval(approval, call.To, intent.Asset, assets, false)
	if err != nil {
		return nil, err
	}
	if len(prefix) == 1 {
		return []DecodedCalldataCall{verified, main}, nil
	}
	reset, err := checkApproval(prefix[0], call.To, intent.Asset, nil, true)
	if err != nil {
		return nil, err
	}
	if reset.Args[1].(*big.Int).Sign() != 0 {
		return nil, amountError("approval.amount", reset.Call)
	}
	return []DecodedCalldataCall{reset, verified, main}, nil
}

func checkVaultSharePermit(raw any, main CalldataCall, prefix []CalldataCall, min *big.Int) error {
	permit := reflect.ValueOf(raw)
	value := permit.Field(0).Interface().(*big.Int)
	signed := !permit.Field(3).IsZero() || !permit.Field(4).IsZero() || !permit.Field(5).IsZero()
	if len(prefix) != 0 && (signed || value.Sign() != 0) {
		return callError("approval", &prefix[0])
	}
	if signed && (value.Sign() == 0 || (min != nil && value.Cmp(min) < 0)) {
		return amountError("permit.value", main)
	}
	return nil
}

func verifyVaultWithdraw(intent VaultCalldataIntent, main DecodedCalldataCall, prefix []CalldataCall) ([]DecodedCalldataCall, error) {
	call, args := main.Call, main.Args
	assets, shares := args[1].(*big.Int), args[2].(*big.Int)
	switch intent.Amount.Unit {
	case "assets", "shares":
		n, err := readUint(intent.Amount.Value, "amount", call)
		if err != nil {
			return nil, err
		}
		if (intent.Amount.Unit == "assets" && (assets.Cmp(n) != 0 || shares.Sign() != 0)) ||
			(intent.Amount.Unit == "shares" && (shares.Cmp(n) != 0 || assets.Sign() != 0)) {
			return nil, amountError("amount", call)
		}
	case "all":
		if assets.Sign() != 0 {
			return nil, amountError("amount", call)
		}
	default:
		return nil, amountError("amount.unit", call)
	}
	if (assets.Sign() == 0) == (shares.Sign() == 0) {
		return nil, amountError("amount", call)
	}
	if err := checkVaultSharePermit(args[3], call, prefix, shares); err != nil {
		return nil, err
	}
	fee, to := args[4].(*big.Int), args[5].(common.Address)
	if err := checkFee(fee, to, intent.MaxFeeBPS, intent.ReferralFee, call); err != nil {
		return nil, err
	}
	if intent.Amount.Unit == "all" && fee.Sign() > 0 {
		if err := recipient(to, intent.Account, "referral_fee.recipient", call); err != nil {
			return nil, err
		}
	}
	return finishVaultExit(intent, main, prefix, shares)
}

func finishVaultExit(intent VaultCalldataIntent, main DecodedCalldataCall, prefix []CalldataCall, min *big.Int) ([]DecodedCalldataCall, error) {
	if len(prefix) == 0 {
		return []DecodedCalldataCall{main}, nil
	}
	approval, err := checkApproval(prefix[0], main.Call.To, intent.Vault, min, false)
	if err != nil {
		return nil, err
	}
	return []DecodedCalldataCall{approval, main}, nil
}

func verifyVaultExit(intent VaultCalldataIntent, main DecodedCalldataCall, prefix []CalldataCall) ([]DecodedCalldataCall, error) {
	call, args := main.Call, main.Args
	if main.FunctionName == "vaultExitBundlesV1ForceWithdrawVaultV2" {
		if err := checkAssets(args[2].(*big.Int), intent.Amount, call); err != nil {
			return nil, err
		}
		if args[3].(*big.Int).Sign() == 0 {
			return nil, amountError("min_share_price", call)
		}
		if err := checkFee(args[5].(*big.Int), args[6].(common.Address), intent.MaxFeeBPS, intent.ReferralFee, call); err != nil {
			return nil, err
		}
	} else {
		if err := checkAssets(args[3].(*big.Int), intent.Amount, call); err != nil {
			return nil, err
		}
		markets := reflect.ValueOf(args[2])
		if markets.Len() != 1 {
			return nil, &CalldataTargetError{detail("market", &call, nil)}
		}
		market := markets.Index(0)
		encoded, err := defaultSnapshot.market.Pack(market.Interface())
		if err != nil || fmt.Sprintf("%d:%s", intent.ChainID, crypto.Keccak256Hash(encoded).Hex()) != strings.ToLower(intent.Market) {
			return nil, &CalldataTargetError{detail("market", &call, nil)}
		}
		if !sameAddress(market.Field(0).Interface().(common.Address).Hex(), intent.Asset) {
			return nil, &CalldataTargetError{detail("asset", &call, nil)}
		}
	}
	if err := checkVaultSharePermit(args[4], call, prefix, nil); err != nil {
		return nil, err
	}
	return finishVaultExit(intent, main, prefix, nil)
}

func verifyVaultForceRedeem(intent VaultCalldataIntent, main DecodedCalldataCall) ([]DecodedCalldataCall, error) {
	subcalls := main.Args[0].([][]byte)
	if len(subcalls) < 2 {
		return nil, callError("function", &main.Call)
	}
	routes := make(map[string]bool)
	main.Calls = make([]DecodedCalldataCall, 0, len(subcalls))
	contract := defaultSnapshot.abis["vault"]
	for i, data := range subcalls {
		call := innerCalldata(main.Call, data, i)
		part, err := decodeCall(call, contract, true)
		if err != nil {
			return nil, err
		}
		if i == len(subcalls)-1 {
			err = checkVaultRedeem(intent, part)
		} else {
			err = checkVaultDeallocate(intent, part, routes, defaultSnapshot.market)
		}
		if err != nil {
			return nil, err
		}
		main.Calls = append(main.Calls, part)
	}
	return []DecodedCalldataCall{main}, nil
}

func checkVaultRedeem(intent VaultCalldataIntent, part DecodedCalldataCall) error {
	call, args := part.Call, part.Args
	if part.FunctionName != "redeem" {
		return callError("function", &call)
	}
	if args[0].(*big.Int).Sign() == 0 {
		return amountError("amount", call)
	}
	if err := recipient(args[1].(common.Address), intent.Account, "receiver", call); err != nil {
		return err
	}
	return recipient(args[2].(common.Address), intent.Account, "owner", call)
}

func checkVaultDeallocate(intent VaultCalldataIntent, part DecodedCalldataCall, routes map[string]bool, market abi.Arguments) error {
	call, args := part.Call, part.Args
	if part.FunctionName != "forceDeallocate" {
		return callError("function", &call)
	}
	if err := recipient(args[3].(common.Address), intent.Account, "onBehalf", call); err != nil {
		return err
	}
	if args[2].(*big.Int).Sign() == 0 {
		return amountError("amount", call)
	}
	data := args[1].([]byte)
	if len(data) != 0 {
		params, err := unpackCanonical(market, data, true)
		if err != nil {
			if err == errNonCanonical {
				err = nil
			}
			return &CalldataDecodeError{detail("adapterData", &call, err)}
		}
		loanToken := reflect.ValueOf(params[0]).Field(0).Interface().(common.Address)
		if !sameAddress(loanToken.Hex(), intent.Asset) {
			return &CalldataTargetError{detail("asset", &call, nil)}
		}
	}
	key := string(args[0].(common.Address).Bytes()) + string(data)
	if routes[key] {
		return callError("forceDeallocate", &call)
	}
	routes[key] = true
	return nil
}
