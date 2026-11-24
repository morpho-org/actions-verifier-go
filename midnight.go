package verifier

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

//go:embed abi_midnight.json
var midnightABI []byte

//go:embed chains_midnight.json
var midnightRegistry []byte

const (
	buyAssets     = "midnightBundlesV2BuyWithAssetsTargetAndWithdrawCollateral"
	buyUnits      = "midnightBundlesV2BuyWithUnitsTargetAndWithdrawCollateral"
	sellAssets    = "midnightBundlesV2SupplyCollateralAndSellWithAssetsTarget"
	sellUnits     = "midnightBundlesV2SupplyCollateralAndSellWithUnitsTarget"
	cancelAndMake = "midnightBundlesV2CancelAndMake"
)

type midnightDeployment struct {
	ID                                               uint64
	Blue, Midnight, MidnightBundlesV2, WrappedNative common.Address
	PriceRatifierV1, RateRatifierV1                  common.Address
	AdapterPriceRatifiersV1, AdapterRateRatifiersV1  []common.Address
	MidnightBlueBuyCallbackFactory                   common.Address
}

type collateralChange struct {
	Collateral struct {
		Address string `json:"address"`
	} `json:"collateral"`
	Amount amount `json:"amount"`
}

var midnightMethods = loadABI(midnightABI)
var midnightChains = func() map[uint64]midnightDeployment {
	var rows []midnightDeployment
	if err := json.Unmarshal(midnightRegistry, &rows); err != nil {
		panic(err)
	}
	result := make(map[uint64]midnightDeployment, len(rows))
	for _, row := range rows {
		result[row.ID] = row
	}
	return result
}()
var maxUint128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

func field(v any, name string) any {
	return reflect.ValueOf(v).FieldByName(name).Interface()
}

func midnightMarketID(m abi.Method, market any) common.Hash {
	for _, arg := range m.Inputs {
		if arg.Name != "market" {
			continue
		}
		encoded, err := (abi.Arguments{arg}).Pack(market)
		if err != nil {
			return common.Hash{}
		}
		prefix, _ := hex.DecodeString("600b380380600b5f395ff3")
		creation := crypto.Keccak256(prefix, encoded)
		dispatch := address(field(market, "Midnight"))
		return crypto.Keccak256Hash([]byte{255}, dispatch[:], make([]byte, 32), creation)
	}
	return common.Hash{}
}

func midnightMarket(m abi.Method, market any, chain midnightDeployment, id string) bool {
	return uintEq(field(market, "ChainId"), chain.ID) &&
		address(field(market, "Midnight")) == chain.Midnight &&
		address(field(market, "LoanToken")) != zeroAddress &&
		(id == "" || strings.EqualFold(midnightMarketID(m, market).Hex(), id))
}

func verifyMidnight(r route, body request, calls []Call) bool {
	chain, ok := midnightChains[r.chain]
	if !ok {
		return false
	}
	final := calls[len(calls)-1]
	prereqs := calls[:len(calls)-1]
	switch r.family {
	case "midnight/offers":
		return midnightOffer(r.action, chain, body, final, prereqs)
	case "midnight/offer-groups":
		if r.action != "take" {
			return false
		}
	case "midnight/markets":
		switch r.action {
		case "supply-collateral":
			return midnightDirect(r, chain, body, final, prereqs)
		case "withdraw":
			if final.Kind == "redeem" {
				return midnightDirect(r, chain, body, final, prereqs)
			}
		case "borrow", "lend", "repay", "withdraw-collateral":
		default:
			return false
		}
	default:
		return false
	}
	return midnightBundle(r, chain, body, final, prereqs)
}

func midnightDirect(r route, chain midnightDeployment, body request, final Call, prereqs []Call) bool {
	if final.Value != "0" || !callTo(final, chain.Midnight) {
		return false
	}
	method := "midnight.supplyCollateral"
	if r.action == "withdraw" {
		method = "midnight.withdraw"
		if len(prereqs) != 0 || body.Fee.BPS != 0 || body.Fee.Recipient != "" {
			return false
		}
	} else if final.Kind != "supply_collateral" {
		return false
	}
	m := midnightMethods[method]
	a, ok := decode(m, final, body.Origin, true)
	account := common.HexToAddress(body.Account)
	if !ok || !midnightMarket(m, a["market"], chain, r.selector) || address(a["onBehalf"]) != account {
		return false
	}
	if r.action == "withdraw" {
		if address(a["receiver"]) != account || !positive(a["units"]) {
			return false
		}
		if body.Amount.Unit == "all" {
			return body.Amount.Value == ""
		}
		n, valid := decimal(body.Amount.Value)
		return valid && (body.Amount.Unit == "assets" || body.Amount.Unit == "units") && eq(a["units"], n)
	}
	token := midnightCollateral(a["market"], a["collateralIndex"])
	if token == zeroAddress || !nonzeroAddress(body.CollateralToken.Address) ||
		token != common.HexToAddress(body.CollateralToken.Address) || !amounts(body.Amount, a, "assets", "", false) {
		return false
	}
	return midnightPrereqs(chain, body, prereqs, chain.Midnight, map[common.Address]*big.Int{token: number(a["assets"])})
}

func midnightBundle(r route, chain midnightDeployment, body request, final Call, prereqs []Call) bool {
	var buy, reduce, direct, collateralOnly bool
	kind, marketID := "", r.selector
	if r.family == "midnight/offer-groups" {
		marketID = body.MidnightID
	}
	switch r.action {
	case "lend":
		buy, kind = true, "lend"
	case "borrow":
		kind = "borrow"
		if len(body.Supplies) > 0 {
			kind = "supply_collateral_and_borrow"
		}
	case "take":
		buy, kind = final.Kind == "lend", "borrow"
		if buy {
			kind = "lend"
		} else if len(body.Supplies) > 0 {
			kind = "supply_collateral_and_borrow"
		}
	case "repay":
		buy, reduce = true, true
		switch body.Type {
		case "direct":
			direct, kind = true, "repay_and_withdraw_collateral"
		case "market":
			kind = "market_repay_and_withdraw_collateral"
		default:
			return false
		}
	case "withdraw-collateral":
		buy, reduce, direct, collateralOnly, kind = true, true, true, true, "repay_and_withdraw_collateral"
		if len(body.Withdrawals) == 0 {
			return false
		}
	case "withdraw":
		reduce, kind = true, "withdraw"
	default:
		return false
	}
	if kind != final.Kind || !callTo(final, chain.MidnightBundlesV2) {
		return false
	}
	units := body.Amount.Unit == "units" || body.Amount.Unit == "all"
	method := sellAssets
	if buy && units {
		method = buyUnits
	} else if buy {
		method = buyAssets
	} else if units {
		method = sellUnits
	}
	m := midnightMethods[method]
	a, ok := decode(m, final, body.Origin, true)
	if !ok || !midnightMarket(m, a["market"], chain, marketID) || a["reduceOnly"] != reduce ||
		(buy && a["repayEnabled"] != direct) || !midnightLimits(body, a, direct) ||
		!midnightGuard(body, a) {
		return false
	}
	target := "targetSellerAssets"
	recv := "receiver"
	if buy {
		target, recv = "targetBuyerAssets", "collateralReceiver"
	}
	if units {
		target = "targetUnits"
	}
	if !midnightTarget(body.Amount, a[target], direct, collateralOnly) ||
		(address(a[recv]) != common.HexToAddress(body.Account) &&
			(!buy || len(body.Withdrawals) != 0 || address(a[recv]) != zeroAddress)) {
		return false
	}
	if direct && !units && !eq(a["minUnits"], number(a[target])) {
		return false
	}
	market := a["market"]
	fills := reflect.ValueOf(a["offerFills"])
	if (fills.Len() == 0) != direct {
		return false
	}
	group, maker := "", ""
	if r.action == "take" {
		group, maker = r.selector, body.Maker
	}
	actualID := midnightMarketID(m, market).Hex()
	for i := 0; i < fills.Len(); i++ {
		offer := field(fills.Index(i).Interface(), "Offer")
		if !midnightMarket(m, field(offer, "Market"), chain, actualID) ||
			field(offer, "Buy") != !buy ||
			(group != "" && !matchesWord(field(offer, "Group"), group)) ||
			(maker != "" && (!nonzeroAddress(maker) || address(field(offer, "Maker")) != common.HexToAddress(maker))) {
			return false
		}
	}
	supplies, ok := midnightChanges(market, a["collateralSupplies"], body.Supplies, false)
	if !ok {
		return false
	}
	if _, ok := midnightChanges(market, a["collateralWithdrawals"], body.Withdrawals, true); !ok {
		return false
	}
	if buy && !collateralOnly {
		cap := number(a["targetBuyerAssets"])
		if units {
			cap = number(a["maxBuyerAssets"])
		}
		if cap == nil || cap.Sign() <= 0 {
			return false
		}
		supplies[address(field(market, "LoanToken"))] = cap
	}
	return midnightFunding(chain, body, final, a, supplies) &&
		midnightPrereqs(chain, body, prereqs, chain.MidnightBundlesV2, supplies)
}

func midnightLimits(body request, a map[string]any, direct bool) bool {
	if body.Expires != 0 && !direct {
		deadline := number(a["deadline"])
		if deadline == nil || deadline.Cmp(new(big.Int).SetUint64(body.Expires)) > 0 {
			return false
		}
		body.Expires = 0
	}
	return limits(body, a)
}

func midnightGuard(body request, a map[string]any) bool {
	var arg string
	switch body.Guard.Type {
	case "max_assets":
		arg = "maxBuyerAssets"
	case "max_units":
		arg = "maxUnits"
	case "min_assets":
		arg = "minSellerAssets"
	case "min_units":
		arg = "minUnits"
	case "", "slippage", "min_rate", "max_rate":
		return true
	default:
		return false
	}
	actual := number(a[arg])
	want, ok := decimal(body.Guard.Value)
	if !ok || actual == nil {
		return false
	}
	if strings.HasPrefix(arg, "max") {
		return actual.Cmp(want) <= 0
	}
	return actual.Cmp(want) >= 0
}

func midnightTarget(amount amount, actual any, direct, collateralOnly bool) bool {
	if collateralOnly {
		return amount.Unit == "" && uintEq(actual, 0)
	}
	if direct && amount.Unit == "all" {
		return amount.Value == "" && eq(actual, maxUint256)
	}
	n, ok := decimal(amount.Value)
	return ok && n.Sign() > 0 && (amount.Unit == "assets" || amount.Unit == "units" && !direct) && eq(actual, n)
}

func midnightCollateral(market any, index any) common.Address {
	n := number(index)
	collateral := reflect.ValueOf(field(market, "CollateralParams"))
	if n == nil || !n.IsUint64() || n.Uint64() >= uint64(collateral.Len()) {
		return zeroAddress
	}
	return address(field(collateral.Index(int(n.Uint64())).Interface(), "Token"))
}

func midnightChanges(market, entries any, requested []collateralChange, withdraw bool) (map[common.Address]*big.Int, bool) {
	caps := make(map[common.Address]*big.Int, len(requested))
	if entries == nil {
		return caps, len(requested) == 0
	}
	rows := reflect.ValueOf(entries)
	if rows.Len() != len(requested) {
		return nil, false
	}
	for _, item := range requested {
		token := common.HexToAddress(item.Collateral.Address)
		if !nonzeroAddress(item.Collateral.Address) || caps[token] != nil {
			return nil, false
		}
		n, ok := decimal(item.Amount.Value)
		if item.Amount.Unit == "all" && item.Amount.Value == "" && withdraw {
			n = maxUint256
		} else if item.Amount.Unit != "assets" || !ok || n.Sign() <= 0 {
			return nil, false
		}
		caps[token] = n
	}
	seen := make(map[common.Address]bool, len(caps))
	for i := 0; i < rows.Len(); i++ {
		row := rows.Index(i).Interface()
		token := midnightCollateral(market, field(row, "CollateralIndex"))
		if seen[token] || !eq(field(row, "Assets"), caps[token]) {
			return nil, false
		}
		seen[token] = true
	}
	return caps, true
}

func midnightFunding(chain midnightDeployment, body request, call Call, a map[string]any, caps map[common.Address]*big.Int) bool {
	actual, ok := decimal(call.Value)
	if !ok {
		return false
	}
	token := chain.WrappedNative
	cap := caps[token]
	if actual.Sign() == 0 {
		if address(a["wrappedNative"]) != zeroAddress {
			return false
		}
		switch body.Funding.Type {
		case "", "erc20":
			return body.Funding.MaxNative == ""
		case "auto":
			_, ok := decimal(body.Funding.MaxNative)
			return ok && token != zeroAddress && cap != nil
		default:
			return false
		}
	}
	nativeCap, ok := decimal(body.Funding.MaxNative)
	if !ok || nativeCap.Cmp(actual) < 0 || token == zeroAddress || address(a["wrappedNative"]) != token ||
		cap == nil || cap.Cmp(actual) < 0 {
		return false
	}
	if body.Funding.Type != "auto" && (body.Funding.Type != "native" || cap.Cmp(actual) != 0) {
		return false
	}
	caps[token] = new(big.Int).Sub(cap, actual)
	return true
}

func midnightPrereqs(chain midnightDeployment, body request, calls []Call, spender common.Address, caps map[common.Address]*big.Int) bool {
	seen := make(map[common.Address]bool, len(caps))
	var authorized bool
	for i, call := range calls {
		if call.Value != "0" || authorized {
			return false
		}
		switch call.Kind {
		case "authorization", "authorize":
			a, ok := decode(midnightMethods["midnight.setIsAuthorized"], call, "", false)
			if !ok || spender != chain.MidnightBundlesV2 || !callTo(call, chain.Midnight) || address(a["authorized"]) != spender ||
				address(a["onBehalf"]) != common.HexToAddress(body.Account) || a["newIsAuthorized"] != true {
				return false
			}
			authorized = true
		case "approval", "erc20_approval":
			token := common.HexToAddress(call.To)
			cap := caps[token]
			a, ok := decode(methods["approve"], call, "", false)
			n := number(a["amount"])
			if !ok || seen[token] || cap == nil || cap.Sign() <= 0 || n == nil || address(a["spender"]) != spender {
				return false
			}
			if n.Sign() == 0 {
				if i+1 >= len(calls) || !callTo(calls[i+1], token) || (i > 0 && callTo(calls[i-1], token)) {
					return false
				}
			} else {
				if !eq(n, cap) && (n.Cmp(cap) < 0 || n.Cmp(maxUint256) != 0 && n.Cmp(maxUint96) != 0) {
					return false
				}
				seen[token] = true
			}
		default:
			return false
		}
	}
	return true
}

func matchesWord(value any, hexWord string) bool {
	word, ok := value.([32]byte)
	return ok && strings.EqualFold(common.Hash(word).Hex(), hexWord)
}
