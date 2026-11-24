package verifier

import (
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
)

func verifyBlue(r route, chain deployment, body request, calls []Call) bool {
	final := calls[len(calls)-1]
	if !callTo(final, chain.BlueBundle) {
		return false
	}
	var method, kind string
	var allowAuth bool
	marketArg := "marketParams"
	switch r.action {
	case "supply":
		method, kind = "blueBundlesV1Supply", "blueSupply"
	case "withdraw":
		method, kind, allowAuth = "blueBundlesV1Withdraw", "blueWithdraw", true
	case "borrow", "supply-collateral":
		method, kind = "blueBundlesV1SupplyCollateralAndBorrow", "blueBorrow"
		allowAuth = r.action == "borrow"
		if r.action == "supply-collateral" {
			kind = "blueSupplyCollateral"
		} else if body.Collateral.Unit != "" {
			kind = "blueSupplyCollateralBorrow"
		}
	case "repay", "withdraw-collateral":
		method, kind = "blueBundlesV1RepayAndWithdrawCollateral", "blueRepay"
		allowAuth = r.action == "withdraw-collateral" || body.Collateral.Unit != ""
		if r.action == "withdraw-collateral" {
			kind = "blueWithdrawCollateral"
		} else if body.Collateral.Unit != "" {
			kind = "blueRepayWithdrawCollateral"
		}
	case "refinance":
		method, kind, marketArg, allowAuth = "blueBundlesV1MigrateBorrowPosition", "blueRefinance", "sourceMarketParams", true
	default:
		return false
	}
	m := methods[method]
	a, ok := decode(m, final, body.Origin, true)
	if final.Kind != kind || !ok || !limits(body, a) || !marketMatches(m, marketArg, a[marketArg], r.selector) {
		return false
	}
	params := reflect.ValueOf(a[marketArg])
	loan := address(params.FieldByName("LoanToken").Interface())
	collateral := address(params.FieldByName("CollateralToken").Interface())
	reallocationMarket := r.selector
	if r.action == "refinance" {
		_, reallocationMarket = splitSelector(body.Destination)
	}
	if rows := a["reallocations"]; rows != nil && !validReallocations(rows, reallocationMarket) {
		return false
	}
	var token common.Address
	var cap *big.Int
	switch r.action {
	case "supply":
		if !amounts(body.Amount, a, "assets", "", false) {
			return false
		}
		token, cap = loan, number(a["assets"])
	case "withdraw":
		allAsAssets := body.Amount.Unit == "all" && body.Amount.Value == "" &&
			positive(a["withdrawAssets"]) && uintEq(a["withdrawShares"], 0)
		if !allAsAssets && !amounts(body.Amount, a, "withdrawAssets", "withdrawShares", true) {
			return false
		}
	case "borrow":
		if !amounts(body.Amount, a, "borrowAssets", "", false) {
			return false
		}
		if body.Collateral.Unit == "" {
			if !uintEq(a["collateralAssets"], 0) {
				return false
			}
		} else {
			if !amounts(body.Collateral, a, "collateralAssets", "", false) {
				return false
			}
			token, cap = collateral, number(a["collateralAssets"])
		}
	case "supply-collateral":
		if !uintEq(a["borrowAssets"], 0) || !amounts(body.Amount, a, "collateralAssets", "", false) {
			return false
		}
		token, cap = collateral, number(a["collateralAssets"])
	case "repay":
		if !amounts(body.Amount, a, "repayAssets", "repayShares", false) {
			return false
		}
		if body.Collateral.Unit == "" {
			if !uintEq(a["collateralAssets"], 0) {
				return false
			}
		} else if !amounts(body.Collateral, a, "collateralAssets", "", false) {
			return false
		}
		token, cap = loan, number(a["maxRepayAssets"])
		if cap.Sign() <= 0 {
			return false
		}
		if body.Amount.Unit == "assets" {
			assetAmount := number(a["repayAssets"])
			bps := int64(body.Fee.BPS)
			if bps >= 10000 {
				return false
			}
			fee := new(big.Int).Mul(assetAmount, big.NewInt(bps))
			fee.Div(fee, big.NewInt(10000-bps))
			if cap.Cmp(fee.Add(fee, assetAmount)) != 0 {
				return false
			}
		}
	case "withdraw-collateral":
		if !uintEq(a["repayAssets"], 0) || !uintEq(a["repayShares"], 0) ||
			!amounts(body.Amount, a, "collateralAssets", "", false) {
			return false
		}
	case "refinance":
		id, market := splitSelector(body.Destination)
		if id != r.chain || !marketMatches(m, "destMarketParams", a["destMarketParams"], market) {
			return false
		}
	}
	if lt, ok := a["maxLtv"]; ok {
		want := maxUint256
		switch r.action {
		case "borrow", "withdraw-collateral":
			want = blueLTV(params)
		case "repay":
			if body.Collateral.Unit != "" {
				want = blueLTV(params)
			}
		case "refinance":
			want = blueLTV(reflect.ValueOf(a["destMarketParams"]))
		}
		if !eq(lt, want) {
			return false
		}
	}
	if cap == nil && final.Value != "0" || cap != nil && (token == zeroAddress || !funding(body, final.Value, token, chain, cap)) {
		return false
	}
	if final.Value != "0" {
		cap = nil
	}
	return prerequisites(calls[:len(calls)-1], chain, chain.BlueBundle, token, cap, allowAuth)
}

func blueLTV(params reflect.Value) *big.Int {
	lltv := number(params.FieldByName("Lltv").Interface())
	buffer := big.NewInt(5_000_000_000_000_000)
	if lltv.Cmp(buffer) <= 0 {
		return new(big.Int)
	}
	return buffer.Sub(lltv, buffer)
}

func validReallocations(rows any, market string) bool {
	m := methods["blueBundlesV1Supply"]
	entries := reflect.ValueOf(rows)
	for i := 0; i < entries.Len(); i++ {
		entry := entries.Index(i)
		get := func(name string) any { return entry.FieldByName(name).Interface() }
		target := get("MarketParams")
		source := get("SourceMarketParams")
		sourceAdapter := address(get("SourceAdapter"))
		targetLoan := address(reflect.ValueOf(target).FieldByName("LoanToken").Interface())
		sourceParams := reflect.ValueOf(source)
		if !marketMatches(m, "marketParams", target, market) ||
			address(get("Vault")) == zeroAddress || address(get("Adapter")) == zeroAddress ||
			address(sourceParams.FieldByName("LoanToken").Interface()) != targetLoan ||
			!positive(get("Assets")) || !uintEq(get("Penalty"), 0) {
			return false
		}
		if get("FromIdle") == true {
			if sourceAdapter != zeroAddress ||
				address(sourceParams.FieldByName("CollateralToken").Interface()) != zeroAddress ||
				address(sourceParams.FieldByName("Oracle").Interface()) != zeroAddress ||
				address(sourceParams.FieldByName("Irm").Interface()) != zeroAddress ||
				!uintEq(sourceParams.FieldByName("Lltv").Interface(), 0) {
				return false
			}
		} else if sourceAdapter == zeroAddress || marketMatches(m, "marketParams", source, market) {
			return false
		}
	}
	return true
}
