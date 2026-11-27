package calldataverifier

import (
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// BlueCalldataIntent describes a Morpho Blue supply, borrow, withdrawal,
// collateral, repay, or refinance action.
type BlueCalldataIntent struct {
	CommonIntent
	Market            string       `json:"market"`
	DestinationMarket string       `json:"destination_market,omitempty"`
	Amount            Amount       `json:"amount,omitempty"`
	CollateralAmount  *Amount      `json:"collateral_amount,omitempty"`
	Funding           *Funding     `json:"funding,omitempty"`
	ReferralFee       *ReferralFee `json:"referral_fee,omitempty"`
	ExpiresAt         *uint64      `json:"expires_at,omitempty"`
	SignatureMode     string       `json:"signature_mode,omitempty"`
}

func (i BlueCalldataIntent) calldataIntent() CommonIntent { return i.CommonIntent }

var blueFunctions = map[string]string{
	"blue.supply":              "blueBundlesV1Supply",
	"blue.withdraw":            "blueBundlesV1Withdraw",
	"blue.supply_collateral":   "blueBundlesV1SupplyCollateralAndBorrow",
	"blue.withdraw_collateral": "blueBundlesV1RepayAndWithdrawCollateral",
	"blue.borrow":              "blueBundlesV1SupplyCollateralAndBorrow",
	"blue.repay":               "blueBundlesV1RepayAndWithdrawCollateral",
	"blue.refinance":           "blueBundlesV1MigrateBorrowPosition",
}

type blueMarketParams struct {
	LoanToken, CollateralToken, Oracle, Irm common.Address
	Lltv                                    *big.Int
}

type tokenPermit struct {
	Kind uint8
	Data []byte
}

type signature struct {
	V    uint8
	R, S [32]byte
}

func (s signature) valid() bool {
	return (s.V == 27 || s.V == 28) && s.R != [32]byte{} && s.S != [32]byte{}
}

func converted[T any](value any) T {
	return *abi.ConvertType(value, new(T)).(*T)
}

type blueFunding struct {
	token  common.Address
	amount *big.Int
	kind   uint8
}

type blueRequirements struct {
	funding       *blueFunding
	authorization bool
}

func verifyBlue(intent BlueCalldataIntent, calls []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	expected, ok := blueFunctions[intent.Action]
	if !ok {
		if len(calls) != 0 {
			return nil, callError("action", &calls[0])
		}
		return nil, callError("action", nil)
	}
	call, err := selectMain(calls, 4)
	if err != nil {
		return nil, err
	}
	if err := target(call, chain.BlueBundlesV1); err != nil {
		return nil, err
	}
	decoded, err := decodeCall(call, defaultSnapshot.abis["blueBundle"], false)
	if err != nil {
		return nil, err
	}
	if decoded.FunctionName != expected {
		return nil, callError("function", &call)
	}
	value, err := readUint(call.Value, "value", call)
	if err != nil {
		return nil, err
	}
	req, err := blueBundleRequirements(intent, decoded)
	if err != nil {
		return nil, err
	}
	if value.Sign() != 0 {
		if err := checkNativeBudget(value, intent.Funding, call); err != nil {
			return nil, err
		}
		if req.funding == nil || req.funding.kind != 0 || !sameAddress(req.funding.token.Hex(), chain.WNative) || value.Cmp(req.funding.amount) != 0 {
			return nil, amountError("value", call)
		}
		req.funding = nil
	}
	if req.funding != nil && req.funding.kind != 0 {
		req.funding = nil
	}
	prefix, err := bluePrerequisites(calls[:len(calls)-1], call.To, chain, req)
	if err != nil {
		return nil, err
	}
	return append(prefix, decoded), nil
}

func bluePrerequisites(calls []CalldataCall, spender string, chain Deployment, req blueRequirements) ([]DecodedCalldataCall, error) {
	checked := make([]DecodedCalldataCall, 0, len(calls)+1)
	var approvals int
	var firstPositive bool
	for i, call := range calls {
		if sameAddress(call.To, chain.Blue) {
			if !req.authorization {
				return nil, callError("authorization", &call)
			}
			if err := expectZeroValue(call); err != nil {
				return nil, err
			}
			decoded, err := decodeCall(call, defaultSnapshot.abis["blue"], false)
			if err != nil {
				return nil, err
			}
			if decoded.FunctionName != "setAuthorization" || !decoded.Args[1].(bool) {
				return nil, callError("authorization", &call)
			}
			if !sameAddress(decoded.Args[0].(common.Address).Hex(), chain.BlueBundlesV1) {
				return nil, &CalldataTargetError{detail("authorization.spender", &call, nil)}
			}
			req.authorization = false
			checked = append(checked, decoded)
			continue
		}
		if req.funding == nil || approvals == 2 {
			return nil, callError("approval", &call)
		}
		token := req.funding.token.Hex()
		allowReset := i+1 < len(calls) && sameAddress(calls[i+1].To, token)
		decoded, err := checkApproval(call, spender, token, req.funding.amount, allowReset)
		if err != nil {
			return nil, err
		}
		positive := decoded.Args[1].(*big.Int).Sign() != 0
		if approvals == 1 && (firstPositive || !positive) {
			return nil, callError("approval", &call)
		}
		firstPositive = positive
		approvals++
		checked = append(checked, decoded)
	}
	return checked, nil
}

func blueMarketID(params blueMarketParams) string {
	data, err := defaultSnapshot.market.Pack(params)
	if err != nil {
		panic(err)
	}
	return crypto.Keccak256Hash(data).Hex()
}

func checkBlueFee(intent BlueCalldataIntent, fee *big.Int, to common.Address, call CalldataCall) error {
	if err := checkFee(fee, to, intent.MaxFeeBPS, intent.ReferralFee, call); err != nil {
		return err
	}
	if intent.Action != "blue.refinance" && intent.Amount.Unit == "all" && fee.Sign() > 0 {
		return recipient(to, intent.Account, "referral_fee.recipient", call)
	}
	return nil
}

func checkBlueLTV(actual, lltv *big.Int, call CalldataCall) error {
	want := maxUint256
	if lltv != nil {
		want = new(big.Int).Sub(lltv, big.NewInt(5_000_000_000_000_000))
		if want.Sign() < 0 {
			want.SetInt64(0)
		}
	}
	if actual.Cmp(want) != 0 {
		return amountError("max_ltv", call)
	}
	return nil
}

func checkBluePermit(permit tokenPermit, funded bool, call CalldataCall) error {
	if permit.Kind == 0 && len(permit.Data) == 0 {
		return nil
	}
	if permit.Kind != 1 || !funded {
		return callError("permit.kind", &call)
	}
	args, err := unpackCanonical(defaultSnapshot.permits[1], permit.Data, true)
	if err != nil || !(signature{args[1].(uint8), args[2].([32]byte), args[3].([32]byte)}).valid() {
		if err == errNonCanonical {
			err = nil
		}
		return &CalldataDecodeError{detail("permit.data", &call, err)}
	}
	return nil
}

func blueAuthorization(raw any, call CalldataCall) (bool, error) {
	sig := converted[signature](reflect.ValueOf(raw).Field(0).Interface())
	if sig == (signature{}) {
		return false, nil
	}
	if !sig.valid() {
		return false, &CalldataDecodeError{detail("authorization.signature", &call, nil)}
	}
	return true, nil
}

func checkBluePosition(amount Amount, assets, shares *big.Int, call CalldataCall) error {
	if amount.Unit == "all" {
		if (assets.Sign() == 0) == (shares.Sign() == 0) {
			return amountError("amount", call)
		}
		return nil
	}
	switch amount.Unit {
	case "assets":
	case "shares":
		assets, shares = shares, assets
	default:
		return amountError("amount.unit", call)
	}
	n, err := readUint(amount.Value, "amount", call)
	if err != nil {
		return err
	}
	if n.Sign() == 0 || n.Cmp(assets) != 0 || shares.Sign() != 0 {
		return amountError("amount", call)
	}
	return nil
}

type blueReallocation struct {
	Vault, Adapter     common.Address
	MarketParams       blueMarketParams
	FromIdle           bool
	SourceAdapter      common.Address
	SourceMarketParams blueMarketParams
	Assets             *big.Int
	Penalty            uint64
}

func checkBlueReallocations(raw any, params blueMarketParams, call CalldataCall) error {
	expected := blueMarketID(params)
	entries := reflect.ValueOf(raw)
	used := make(map[string]bool, entries.Len())
	var zero common.Address
	for i := 0; i < entries.Len(); i++ {
		entry := converted[blueReallocation](entries.Index(i).Interface())
		if entry.Penalty != 0 {
			return &CalldataFeeError{detail("reallocations.penalty", &call, nil)}
		}
		source := blueMarketID(entry.SourceMarketParams)
		key := entry.Vault.Hex() + ":" + entry.Adapter.Hex()
		if !entry.FromIdle {
			key += ":" + entry.SourceAdapter.Hex() + ":" + source
		}
		if entry.Assets.Sign() == 0 || used[key] || entry.Vault == zero || entry.Adapter == zero ||
			blueMarketID(entry.MarketParams) != expected || entry.SourceMarketParams.LoanToken != params.LoanToken ||
			(entry.FromIdle && entry.SourceAdapter != zero) || (!entry.FromIdle && (entry.SourceAdapter == zero || source == expected)) {
			return callError("reallocations", &call)
		}
		used[key] = true
	}
	return nil
}

func blueBundleRequirements(intent BlueCalldataIntent, decoded DecodedCalldataCall) (blueRequirements, error) {
	call, args := decoded.Call, decoded.Args
	params := converted[blueMarketParams](args[0])
	var req blueRequirements
	if blueMarketID(params) != strings.ToLower(intent.Market) {
		return req, &CalldataTargetError{detail("market", &call, nil)}
	}
	switch decoded.FunctionName {
	case "blueBundlesV1Supply":
		assets, permit := args[1].(*big.Int), converted[tokenPermit](args[2])
		if err := checkAssets(assets, intent.Amount, call); err != nil {
			return req, err
		}
		if err := checkBluePermit(permit, true, call); err != nil {
			return req, err
		}
		req.funding = &blueFunding{params.LoanToken, assets, permit.Kind}
		return req, checkBlueFee(intent, args[3].(*big.Int), args[4].(common.Address), call)
	case "blueBundlesV1SupplyCollateralAndBorrow":
		return blueSupplyBorrow(intent, params, decoded)
	case "blueBundlesV1RepayAndWithdrawCollateral":
		return blueRepayWithdraw(intent, params, decoded)
	case "blueBundlesV1Withdraw":
		if err := checkBluePosition(intent.Amount, args[1].(*big.Int), args[2].(*big.Int), call); err != nil {
			return req, err
		}
		if err := checkBlueReallocations(args[4], params, call); err != nil {
			return req, err
		}
		if err := checkBlueFee(intent, args[5].(*big.Int), args[6].(common.Address), call); err != nil {
			return req, err
		}
	case "blueBundlesV1MigrateBorrowPosition":
		destination := converted[blueMarketParams](args[1])
		if strconv.FormatUint(intent.ChainID, 10)+":"+blueMarketID(destination) != strings.ToLower(intent.DestinationMarket) {
			return req, &CalldataTargetError{detail("destination_market", &call, nil)}
		}
		if err := checkBlueReallocations(args[4], destination, call); err != nil {
			return req, err
		}
		if err := checkBlueLTV(args[2].(*big.Int), destination.Lltv, call); err != nil {
			return req, err
		}
		if err := checkBlueFee(intent, args[5].(*big.Int), args[6].(common.Address), call); err != nil {
			return req, err
		}
	}
	signed, err := blueAuthorization(args[3], call)
	req.authorization = !signed
	return req, err
}

func blueSupplyBorrow(intent BlueCalldataIntent, params blueMarketParams, decoded DecodedCalldataCall) (blueRequirements, error) {
	call, args := decoded.Call, decoded.Args
	collateral, borrow, maxLTV := args[1].(*big.Int), args[2].(*big.Int), args[3].(*big.Int)
	permit := converted[tokenPermit](args[4])
	var req blueRequirements
	var lltv *big.Int
	if intent.Action == "blue.supply_collateral" {
		if err := checkAssets(collateral, intent.Amount, call); err != nil {
			return req, err
		}
		if borrow.Sign() != 0 {
			return req, amountError("amount", call)
		}
		if reflect.ValueOf(args[6]).Len() != 0 {
			return req, callError("borrow", &call)
		}
		signed, err := blueAuthorization(args[5], call)
		if err != nil {
			return req, err
		}
		if signed {
			return req, callError("borrow", &call)
		}
	} else {
		if err := checkAssets(borrow, intent.Amount, call); err != nil {
			return req, err
		}
		if err := checkOptionalCollateral(collateral, intent.CollateralAmount, call); err != nil {
			return req, err
		}
		if intent.CollateralAmount == nil && permit.Kind != 0 {
			return req, callError("permit", &call)
		}
		if err := checkBlueReallocations(args[6], params, call); err != nil {
			return req, err
		}
		signed, err := blueAuthorization(args[5], call)
		if err != nil {
			return req, err
		}
		req.authorization = !signed
		lltv = params.Lltv
	}
	if err := checkBlueFee(intent, args[7].(*big.Int), args[8].(common.Address), call); err != nil {
		return req, err
	}
	if err := checkBlueLTV(maxLTV, lltv, call); err != nil {
		return req, err
	}
	if err := checkBluePermit(permit, collateral.Sign() > 0, call); err != nil {
		return req, err
	}
	if collateral.Sign() > 0 {
		req.funding = &blueFunding{params.CollateralToken, collateral, permit.Kind}
	}
	return req, nil
}

func checkOptionalCollateral(collateral *big.Int, amount *Amount, call CalldataCall) error {
	if amount == nil {
		if collateral.Sign() == 0 {
			return nil
		}
		return amountError("collateral_amount", call)
	}
	return checkNamedAssets(collateral, *amount, "collateral_amount", call)
}

func blueRepayWithdraw(intent BlueCalldataIntent, params blueMarketParams, decoded DecodedCalldataCall) (blueRequirements, error) {
	call, args := decoded.Call, decoded.Args
	assets, shares, maximum := args[1].(*big.Int), args[2].(*big.Int), args[3].(*big.Int)
	collateral, maxLTV := args[4].(*big.Int), args[5].(*big.Int)
	permit, fee := converted[tokenPermit](args[6]), args[8].(*big.Int)
	var req blueRequirements
	if err := checkBlueFee(intent, fee, args[9].(common.Address), call); err != nil {
		return req, err
	}
	if intent.Action == "blue.withdraw_collateral" {
		if err := checkAssets(collateral, intent.Amount, call); err != nil {
			return req, err
		}
		if assets.Sign() != 0 || shares.Sign() != 0 || maximum.Sign() != 0 || permit.Kind != 0 {
			return req, callError("repay", &call)
		}
	} else {
		if err := checkBluePosition(intent.Amount, assets, shares, call); err != nil {
			return req, err
		}
		if (intent.Amount.Unit == "all") != (shares.Cmp(maxUint256) == 0) || maximum.Sign() == 0 {
			return req, amountError("amount", call)
		}
		if intent.Amount.Unit == "assets" {
			want := new(big.Int).Mul(assets, fee)
			want.Quo(want, new(big.Int).Sub(wad, fee)).Add(want, assets)
			if maximum.Cmp(want) != 0 {
				return req, amountError("max_repay_assets", call)
			}
		}
		if err := checkOptionalCollateral(collateral, intent.CollateralAmount, call); err != nil {
			return req, err
		}
		req.funding = &blueFunding{params.LoanToken, maximum, permit.Kind}
	}
	signed, err := blueAuthorization(args[7], call)
	if err != nil {
		return req, err
	}
	if intent.Action == "blue.repay" && intent.CollateralAmount == nil && signed {
		return req, callError("authorization", &call)
	}
	req.authorization = (intent.Action == "blue.withdraw_collateral" || intent.CollateralAmount != nil) && !signed
	var lltv *big.Int
	if collateral.Sign() > 0 {
		lltv = params.Lltv
	}
	if err := checkBlueLTV(maxLTV, lltv, call); err != nil {
		return req, err
	}
	return req, checkBluePermit(permit, req.funding != nil, call)
}
