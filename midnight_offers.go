package verifier

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"reflect"
	"slices"

	"github.com/ethereum/go-ethereum/common"
)

func midnightOffer(action string, chain midnightDeployment, body request, call Call, prereqs []Call) bool {
	account := common.HexToAddress(body.Account)
	switch action {
	case "cancel":
		return len(prereqs) == 0 && midnightCancel(chain, body, call)
	case "funding/withdraw":
		if len(prereqs) != 0 || call.Kind != "withdraw_funding" || !callTo(call, chain.Blue) || call.Value != "0" ||
			!nonzeroAddress(body.FundingAddress) {
			return false
		}
		m := midnightMethods["blue.withdraw"]
		a, ok := decode(m, call, body.Origin, true)
		return ok && marketMatches(m, "marketParams", a["marketParams"], body.FundingMarket) &&
			uintEq(a["assets"], 0) && positive(a["shares"]) &&
			address(a["onBehalf"]) == common.HexToAddress(body.FundingAddress) && address(a["receiver"]) == account
	case "borrow", "lend", "repay", "withdraw":
		return midnightPublish(action, chain, body, call, prereqs)
	}
	return false
}

func midnightCancel(chain midnightDeployment, body request, call Call) bool {
	if call.Kind != "cancel_offers" || call.Value != "0" || !callTo(call, chain.Midnight) || len(body.Groups) == 0 {
		return false
	}
	account := common.HexToAddress(body.Account)
	check := func(c Call, group string, top bool) bool {
		a, ok := decode(midnightMethods["midnight.setConsumed"], c, body.Origin, top)
		return ok && matchesWord(a["group"], group) && address(a["onBehalf"]) == account && eq(a["amount"], maxUint128)
	}
	if len(body.Groups) == 1 {
		return check(call, body.Groups[0], true)
	}
	a, ok := decode(midnightMethods["midnight.multicall"], call, body.Origin, true)
	inner, valid := a["calls"].([][]byte)
	if !ok || !valid || len(inner) != len(body.Groups) {
		return false
	}
	for i, raw := range inner {
		if !check(Call{Data: "0x" + hex.EncodeToString(raw)}, body.Groups[i], false) {
			return false
		}
	}
	return true
}

func midnightPublish(action string, chain midnightDeployment, body request, call Call, prereqs []Call) bool {
	amount, ok := decimal(body.Amount.Value)
	unit := "assets"
	if action == "repay" || action == "withdraw" {
		unit = "units"
	}
	if !ok || amount.Sign() <= 0 || body.Amount.Unit != unit || call.Kind != "cancel_and_publish" ||
		!callTo(call, chain.MidnightBundlesV2) || len(body.Offers.Entries) == 0 {
		return false
	}
	m := midnightMethods[cancelAndMake]
	a, ok := decode(m, call, body.Origin, true)
	if !ok || !limits(body, a) || !midnightRatifier(chain, body.Offers.Type, address(a["ratifier"])) ||
		reflect.ValueOf(a["groupsToCancel"]).Len() != 0 || matchesWord(a["newRoot"], (common.Hash{}).Hex()) ||
		len(a["payload"].([]byte)) == 0 || !uintEq(a["signatureHeight"], 0) ||
		!uintEq(a["signatureNonce"], 0) || !uintEq(a["signatureDeadline"], 0) ||
		!uintEq(a["v"], 0) || !matchesWord(a["r"], (common.Hash{}).Hex()) || !matchesWord(a["s"], (common.Hash{}).Hex()) {
		return false
	}
	caps := make(map[common.Address]*big.Int)
	market := a["market"]
	if len(body.Supplies) != 0 {
		if action != "borrow" || !midnightMarket(m, market, chain, "") {
			return false
		}
		marketID := midnightMarketID(m, market)
		for _, entry := range body.Offers.Entries {
			if !stringsEqualHash(entry.Market, marketID) {
				return false
			}
		}
		caps, ok = midnightChanges(market, a["collateralSupplies"], body.Supplies, false)
		if !ok {
			return false
		}
	} else if reflect.ValueOf(a["collateralSupplies"]).Len() != 0 || !uintEq(field(market, "ChainId"), 0) ||
		address(field(market, "Midnight")) != zeroAddress {
		return false
	}
	blue := a["blueMarket"]
	if action == "lend" && body.FundingSource.Type == "variable_rate_market" {
		if !eq(a["assetsToPark"], amount) || !marketMatches(m, "blueMarket", blue, body.FundingSource.Market) {
			return false
		}
		token := address(field(blue, "LoanToken"))
		if token == zeroAddress {
			return false
		}
		caps[token] = amount
	} else if !uintEq(a["assetsToPark"], 0) || address(field(blue, "LoanToken")) != zeroAddress ||
		!matchesWord(a["callbackSalt"], (common.Hash{}).Hex()) {
		return false
	}
	if !midnightFunding(chain, body, call, a, caps) {
		return false
	}
	if (action == "lend" && body.FundingSource.Type != "variable_rate_market") || action == "repay" {
		return midnightWalletOffer(chain, body, prereqs, amount)
	}
	return midnightPrereqs(chain, body, prereqs, chain.MidnightBundlesV2, caps)
}

func midnightWalletOffer(chain midnightDeployment, body request, prereqs []Call, cap *big.Int) bool {
	if body.FundingSource.Type != "" && body.FundingSource.Type != "wallet" {
		return false
	}
	if len(prereqs) > 0 && (prereqs[0].Kind == "approval" || prereqs[0].Kind == "erc20_approval") {
		call := prereqs[0]
		token := common.HexToAddress(call.To)
		a, ok := decode(methods["approve"], call, "", false)
		n := number(a["amount"])
		if !ok || call.Value != "0" || address(a["spender"]) != chain.Midnight ||
			n == nil || n.Sign() <= 0 || n.Cmp(cap) > 0 || n.Cmp(maxUint256) == 0 ||
			n.Cmp(maxUint96) == 0 || chains[chain.ID].pinned(token) ||
			chain.pinned(token) || !matchesWalletToken(body.MarketParams, token) {
			return false
		}
		prereqs = prereqs[1:]
	}
	return midnightPrereqs(chain, body, prereqs, chain.MidnightBundlesV2, nil)
}

func matchesWalletToken(raw json.RawMessage, token common.Address) bool {
	if len(raw) == 0 {
		return true
	}
	var markets []struct {
		LoanToken string `json:"loan_token"`
	}
	if json.Unmarshal(raw, &markets) != nil || len(markets) == 0 {
		return false
	}
	for _, market := range markets {
		if !nonzeroAddress(market.LoanToken) || common.HexToAddress(market.LoanToken) != token {
			return false
		}
	}
	return true
}

func (chain midnightDeployment) pinned(token common.Address) bool {
	return token == chain.Blue || token == chain.Midnight || token == chain.MidnightBundlesV2 ||
		token == chain.MidnightBlueBuyCallbackFactory || midnightRatifier(chain, "price", token) ||
		midnightRatifier(chain, "rate", token)
}

func midnightRatifier(chain midnightDeployment, typ string, address common.Address) bool {
	switch typ {
	case "price":
		return address == chain.PriceRatifierV1 || slices.Contains(chain.AdapterPriceRatifiersV1, address)
	case "rate":
		return address == chain.RateRatifierV1 || slices.Contains(chain.AdapterRateRatifiersV1, address)
	}
	return false
}

func stringsEqualHash(s string, expected common.Hash) bool {
	b, ok := hexBytes(s, 32)
	return ok && expected == common.Hash(b)
}
