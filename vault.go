package verifier

import (
	"encoding/hex"
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
)

func verifyVault(r route, chain deployment, body request, calls []Call) bool {
	final := calls[len(calls)-1]
	vault := common.HexToAddress(r.selector)
	bundle := chain.VaultV1Bundle
	kindPrefix := "vault_v1_"
	if r.family == "vaults-v2" {
		bundle, kindPrefix = chain.VaultV2Bundle, "vault_v2_"
	}
	var method, kind string
	var cap *big.Int
	token := vault
	switch r.action {
	case "deposit":
		method, kind, token = "vaultBundlesV1Deposit", kindPrefix+"deposit", zeroAddress
	case "withdraw":
		method, kind = "vaultBundlesV1Withdraw", kindPrefix+"withdraw"
		if body.Amount.Unit != "assets" {
			kind = kindPrefix + "redeem"
		}
	case "force-withdraw":
		if r.family != "vaults-v2" || chain.VaultExit == zeroAddress {
			return false
		}
		if body.Amount.Unit == "all" {
			return len(calls) == 1 && verifyVaultMulticall(final, vault, body)
		}
		method, kind, bundle = "vaultExitBundlesV1ForceWithdrawVaultV2", "vault_v2_force_withdraw", chain.VaultExit
	case "redeem-in-kind":
		if r.family != "vaults-v2" {
			return false
		}
		method, kind, bundle = "vaultExitBundlesV1InKindRedemptionVaultV2", "vault_v2_in_kind_redeem", chain.VaultExit
	default:
		return false
	}
	if !callTo(final, bundle) || final.Kind != kind {
		return false
	}
	a, ok := decode(methods[method], final, body.Origin, true)
	if !ok || address(a["vault"]) != vault || !limits(body, a) {
		return false
	}
	switch r.action {
	case "deposit":
		if !amounts(body.Amount, a, "assets", "", false) || !positive(a["maxSharePriceE27"]) {
			return false
		}
		cap = number(a["assets"])
		if !funding(body, final.Value, token, chain, cap) {
			return false
		}
		if final.Value != "0" {
			cap = nil
		}
		for _, call := range calls[:len(calls)-1] {
			if callTo(call, vault) {
				return false
			}
		}
	case "withdraw":
		if !amounts(body.Amount, a, "assets", "shares", true) {
			return false
		}
		cap = maxUint256
	case "force-withdraw":
		if !amounts(body.Amount, a, "exitAssets", "", false) ||
			!positive(a["minSharePriceE27"]) || address(a["adapter"]) == zeroAddress {
			return false
		}
		cap = maxUint256
	case "redeem-in-kind":
		if !amounts(body.Amount, a, "exitAssets", "", false) || address(a["adapter"]) == zeroAddress {
			return false
		}
		markets := reflect.ValueOf(a["marketParamsList"])
		id, market := splitSelector(body.Market)
		if id != r.chain || markets.Len() != 1 ||
			!marketMatches(methods["blueBundlesV1Supply"], "marketParams", markets.Index(0).Interface(), market) {
			return false
		}
		cap = maxUint256
	}
	return (r.action == "deposit" || final.Value == "0") &&
		prerequisites(calls[:len(calls)-1], chain, bundle, token, cap, false)
}

func verifyVaultMulticall(call Call, vault common.Address, body request) bool {
	if body.Amount.Value != "" || call.Kind != "vault_v2_force_redeem" || call.Value != "0" || !callTo(call, vault) {
		return false
	}
	a, ok := decode(methods["multicall"], call, body.Origin, true)
	inner, valid := a["data"].([][]byte)
	if !ok || !valid || len(inner) < 2 {
		return false
	}
	account := common.HexToAddress(body.Account)
	for _, data := range inner[:len(inner)-1] {
		d, ok := decode(methods["forceDeallocate"], Call{Data: "0x" + hex.EncodeToString(data)}, "", false)
		marketData, valid := d["data"].([]byte)
		if !ok || !valid || len(marketData) != 160 || address(d["adapter"]) == zeroAddress ||
			address(d["onBehalf"]) != account || !positive(d["assets"]) {
			return false
		}
		for i := 0; i < 128; i += 32 {
			for _, padding := range marketData[i : i+12] {
				if padding != 0 {
					return false
				}
			}
		}
	}
	redeem, ok := decode(methods["redeem"], Call{Data: "0x" + hex.EncodeToString(inner[len(inner)-1])}, "", false)
	return ok && positive(redeem["shares"]) && address(redeem["receiver"]) == account && address(redeem["owner"]) == account
}
