package calldataverifier

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// MidnightGuard specifies the caller's slippage or explicit execution bound.
type MidnightGuard struct {
	Type  string `json:"type"`
	BPS   int    `json:"bps,omitempty"`
	Value string `json:"value,omitempty"`
}

// MidnightCollateral identifies the collateral token in a market.
type MidnightCollateral struct {
	Address string `json:"address"`
}

// MidnightCollateralTransfer describes one requested supply or withdrawal.
type MidnightCollateralTransfer struct {
	Collateral MidnightCollateral `json:"collateral"`
	Amount     Amount             `json:"amount"`
}

// MidnightMarketParams describes the optional market parameters in a prepared intent.
type MidnightMarketParams struct {
	LoanToken      string                     `json:"loan_token"`
	Maturity       uint64                     `json:"maturity"`
	RCFThreshold   string                     `json:"rcf_threshold"`
	EnterGate      string                     `json:"enter_gate"`
	LiquidatorGate string                     `json:"liquidator_gate"`
	Collaterals    []MidnightCollateralParams `json:"collaterals"`
}

// MidnightCollateralParams describes the market's parameters for one collateral token.
type MidnightCollateralParams struct {
	Token             string `json:"token"`
	LLTV              string `json:"lltv"`
	LiquidationCursor string `json:"liquidation_cursor"`
	Oracle            string `json:"oracle"`
}

// MidnightMarketIntent describes a Midnight borrow, lend, repay, withdrawal,
// redemption, or collateral action.
type MidnightMarketIntent struct {
	CommonIntent
	Market       string                `json:"market"`
	ExpiresAt    *uint64               `json:"expires_at,omitempty"`
	Type         string                `json:"type,omitempty"`
	Amount       Amount                `json:"amount,omitempty"`
	Guard        *MidnightGuard        `json:"guard,omitempty"`
	MarketParams *MidnightMarketParams `json:"market_params,omitempty"`
	// Funding permits native value on supported Midnight bundles, including standalone collateral supply.
	Funding                                  *Funding                     `json:"funding,omitempty"`
	ReferralFee                              *ReferralFee                 `json:"referral_fee,omitempty"`
	AllowPartial                             *bool                        `json:"allow_partial,omitempty"`
	Collateral                               MidnightCollateral           `json:"collateral,omitempty"`
	CollateralSupplies                       []MidnightCollateralTransfer `json:"collateral_supplies,omitempty"`
	CollateralWithdrawals                    []MidnightCollateralTransfer `json:"collateral_withdrawals,omitempty"`
	nullGuard, nullSupplies, nullWithdrawals bool
}

func (i MidnightMarketIntent) calldataIntent() CommonIntent { return i.CommonIntent }

// UnmarshalJSON preserves the distinction between omitted and null guards and collateral lists.
func (i *MidnightMarketIntent) UnmarshalJSON(data []byte) error {
	type fields MidnightMarketIntent
	decoded := fields{CommonIntent: CommonIntent{MaxFeeBPS: i.MaxFeeBPS}}
	raw, err := decodeIntent(data, &decoded, &decoded.CommonIntent)
	if err != nil {
		return err
	}
	decoded.nullGuard = string(raw["guard"]) == "null"
	decoded.nullSupplies = string(raw["collateral_supplies"]) == "null"
	decoded.nullWithdrawals = string(raw["collateral_withdrawals"]) == "null"
	*i = MidnightMarketIntent(decoded)
	return nil
}

type midnightMarket struct {
	ChainId                   *big.Int
	Midnight, LoanToken       common.Address
	CollateralParams          []midnightCollateral
	Maturity, RcfThreshold    *big.Int
	EnterGate, LiquidatorGate common.Address
}

type midnightCollateral struct {
	Token                   common.Address
	Lltv, LiquidationCursor *big.Int
	Oracle                  common.Address
}

type midnightTransfer struct {
	CollateralIndex, Assets *big.Int
}

type midnightPull struct {
	token  common.Address
	assets *big.Int
}

type midnightContext struct {
	intent MidnightMarketIntent
	chain  Deployment
	call   CalldataCall
	take   *MidnightTakeIntent
}

func (c midnightContext) market(raw any) (midnightMarket, error) {
	market := converted[midnightMarket](raw)
	err := func(cause error) error { return &CalldataTargetError{detail("market", &c.call, cause)} }
	if !market.ChainId.IsUint64() || market.ChainId.Uint64() != c.intent.ChainID || !sameAddress(market.Midnight.Hex(), c.chain.Midnight) {
		return market, err(nil)
	}
	for i, collateral := range market.CollateralParams {
		if i > 0 && bytes.Compare(market.CollateralParams[i-1].Token[:], collateral.Token[:]) >= 0 {
			return market, err(nil)
		}
	}
	if count := len(market.CollateralParams); count < 1 || count > 128 {
		return market, err(fmt.Errorf("invalid collateral count: %d", count))
	}
	for _, collateral := range market.CollateralParams {
		if !validMidnightCollateral(collateral) {
			return market, err(fmt.Errorf("invalid parameters for collateral %s", collateral.Token.Hex()))
		}
	}
	method := defaultSnapshot.abis["midnight"].Methods["withdraw"]
	encoded, cause := abi.Arguments{method.Inputs[0]}.Pack(raw)
	if cause != nil {
		return market, err(cause)
	}
	prefix := common.FromHex("0x600b380380600b5f395ff3")
	creationHash := crypto.Keccak256(prefix, encoded)
	id := crypto.Keccak256Hash([]byte{255}, market.Midnight[:], make([]byte, 32), creationHash)
	selector := c.intent.Market
	if c.take != nil {
		selector = *c.take.MarketID
	}
	if !strings.EqualFold(id.Hex(), selector) {
		return market, err(nil)
	}
	return market, nil
}

func validMidnightCollateral(collateral midnightCollateral) bool {
	if collateral.Lltv.Cmp(wad) > 0 || collateral.LiquidationCursor.Cmp(wad) >= 0 {
		return false
	}
	gap := new(big.Int).Sub(wad, collateral.Lltv)
	gap.Mul(gap, collateral.LiquidationCursor).Quo(gap, wad)
	denominator := new(big.Int).Sub(wad, gap)
	limit := new(big.Int).Mul(wad, wad)
	lif := new(big.Int).Quo(limit, denominator)
	if lif.Cmp(new(big.Int).Lsh(wad, 1)) > 0 {
		return false
	}
	product := new(big.Int).Mul(collateral.Lltv, lif)
	bound := new(big.Int).Mul(wad, big.NewInt(999_000_000_000_000_000))
	return collateral.Lltv.Cmp(wad) == 0 || product.Cmp(bound) <= 0
}

func verifyMidnight(intent MidnightMarketIntent, calls []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	switch intent.Action {
	case "midnight.borrow", "midnight.lend", "midnight.repay", "midnight.market_repay", "midnight.withdraw", "midnight.redeem", "midnight.supply_collateral", "midnight.withdraw_collateral":
	default:
		return nil, unknownMidnightAction(calls)
	}
	return verifyMidnightCalls(intent, nil, calls, chain)
}

func verifyMidnightCalls(intent MidnightMarketIntent, take *MidnightTakeIntent, calls []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	call, err := selectMain(calls, math.MaxInt)
	if err != nil {
		return nil, err
	}
	if (intent.Action == "midnight.repay" && intent.Type != "direct") || (intent.Action == "midnight.market_repay" && intent.Type != "market") {
		return nil, callError("type", &call)
	}
	if intent.Action == "midnight.supply_collateral_take_borrow" && len(intent.CollateralSupplies) == 0 {
		return nil, callError("collateral_supplies", &call)
	}
	c := midnightContext{intent, chain, call, take}
	if err := c.checkGuardType(); err != nil {
		return nil, err
	}
	if take != nil {
		if take.MarketID == nil {
			return nil, &CalldataUnsupportedError{detail("market_id", &call, nil)}
		}
		if !validGroup(take.Group) {
			return nil, &CalldataTargetError{detail("group", &call, nil)}
		}
	}
	prefix := calls[:len(calls)-1]
	if sameAddress(call.To, chain.Midnight) {
		return c.core(prefix)
	}
	if err := target(call, chain.MidnightBundlesV2); err != nil {
		return nil, err
	}
	decoded, err := decodeCall(call, defaultSnapshot.abis["midnightBundle"], false)
	if err != nil {
		return nil, err
	}
	var pulls []midnightPull
	switch decoded.FunctionName {
	case "midnightBundlesV2BuyWithAssetsTargetAndWithdrawCollateral":
		pulls, err = c.buy(decoded.Args, "assets")
	case "midnightBundlesV2BuyWithUnitsTargetAndWithdrawCollateral":
		pulls, err = c.buy(decoded.Args, "units")
	case "midnightBundlesV2SupplyCollateralAndSellWithAssetsTarget":
		pulls, err = c.sell(decoded.Args, "assets")
	case "midnightBundlesV2SupplyCollateralAndSellWithUnitsTarget":
		pulls, err = c.sell(decoded.Args, "units")
	case "midnightBundlesV2CancelAndMake":
		pulls, err = c.supplyCollateralWithNative(decoded.Args)
	default:
		err = callError("function", &call)
	}
	if err != nil {
		return nil, err
	}
	checked, err := c.prerequisites(prefix, pulls, true)
	if err != nil {
		return nil, err
	}
	return append(checked, decoded), nil
}

func unknownMidnightAction(calls []CalldataCall) error {
	if len(calls) > 0 {
		return callError("action", &calls[0])
	}
	return callError("action", nil)
}

func midnightTransfers(raw any) []midnightTransfer {
	return converted[[]midnightTransfer](raw)
}

func (c midnightContext) collateral(transfers []midnightTransfer, requested []MidnightCollateralTransfer, market midnightMarket, withdrawal string, invalidList bool) ([]midnightPull, error) {
	if invalidList || len(transfers) != len(requested) {
		return nil, callError("collateral", &c.call)
	}
	seen := make(map[common.Address]bool, len(transfers))
	pulls := make([]midnightPull, 0, len(transfers))
	for _, transfer := range transfers {
		if !transfer.CollateralIndex.IsUint64() || transfer.CollateralIndex.Uint64() >= uint64(len(market.CollateralParams)) {
			return nil, &CalldataTargetError{detail("collateral", &c.call, nil)}
		}
		token := market.CollateralParams[transfer.CollateralIndex.Uint64()].Token
		var requestedAmount *Amount
		for _, item := range requested {
			if sameAddress(item.Collateral.Address, token.Hex()) {
				requestedAmount = &item.Amount
				break
			}
		}
		if requestedAmount == nil || seen[token] {
			return nil, &CalldataTargetError{detail("collateral", &c.call, nil)}
		}
		seen[token] = true
		all := requestedAmount.Unit == "all" && withdrawal != ""
		if requestedAmount.Unit != "assets" && !all {
			return nil, amountError("collateral.amount.unit", c.call)
		}
		if transfer.Assets.Sign() == 0 || (withdrawal == "bundle" && transfer.Assets.Cmp(maxUint256) == 0 && !all) {
			return nil, amountError("collateral.amount", c.call)
		}
		if !all {
			if err := checkNamedAssets(transfer.Assets, *requestedAmount, "collateral.amount", c.call); err != nil {
				return nil, err
			}
		}
		pulls = append(pulls, midnightPull{token, transfer.Assets})
	}
	return pulls, nil
}

func (c midnightContext) prerequisites(calls []CalldataCall, pulls []midnightPull, allowAuthorization bool) ([]DecodedCalldataCall, error) {
	var authorized bool
	approved := make(map[common.Address]*big.Int, len(pulls))
	result := make([]DecodedCalldataCall, 0, len(calls)+1)
	for i, call := range calls {
		if sameAddress(call.To, c.chain.Midnight) {
			if err := expectZeroValue(call); err != nil {
				return nil, err
			}
			decoded, err := decodeCall(call, defaultSnapshot.abis["midnight"], false)
			if err != nil {
				return nil, err
			}
			if !allowAuthorization || authorized || decoded.FunctionName != "setIsAuthorized" || !decoded.Args[1].(bool) {
				return nil, callError("authorization", &call)
			}
			if !sameAddress(decoded.Args[0].(common.Address).Hex(), c.chain.MidnightBundlesV2) {
				return nil, &CalldataTargetError{detail("authorization.spender", &call, nil)}
			}
			if err := recipient(decoded.Args[2].(common.Address), c.intent.Account, "on_behalf", call); err != nil {
				return nil, err
			}
			authorized = true
			result = append(result, decoded)
			continue
		}
		var pull *midnightPull
		for j := range pulls {
			if sameAddress(call.To, pulls[j].token.Hex()) {
				pull = &pulls[j]
				break
			}
		}
		if pull == nil {
			return nil, &CalldataTargetError{detail("approval.token", &call, nil)}
		}
		token := pull.token.Hex()
		reset := i+1 < len(calls) && sameAddress(calls[i+1].To, token)
		decoded, err := checkApproval(call, c.call.To, token, pull.assets, reset)
		if err != nil {
			return nil, err
		}
		amount := decoded.Args[1].(*big.Int)
		if previous := approved[pull.token]; previous != nil && (previous.Sign() != 0 || amount.Sign() == 0) {
			return nil, callError("approval", &call)
		}
		approved[pull.token] = amount
		result = append(result, decoded)
	}
	return result, nil
}

func (c midnightContext) core(prefix []CalldataCall) ([]DecodedCalldataCall, error) {
	if err := expectZeroValue(c.call); err != nil {
		return nil, err
	}
	decoded, err := decodeCall(c.call, defaultSnapshot.abis["midnight"], false)
	if err != nil {
		return nil, err
	}
	args := decoded.Args
	if c.intent.Action == "midnight.supply_collateral" && decoded.FunctionName == "supplyCollateral" {
		market, err := c.market(args[0])
		if err != nil {
			return nil, err
		}
		if err := recipient(args[3].(common.Address), c.intent.Account, "on_behalf", c.call); err != nil {
			return nil, err
		}
		pulls, err := c.collateral([]midnightTransfer{{args[1].(*big.Int), args[2].(*big.Int)}}, []MidnightCollateralTransfer{{c.intent.Collateral, c.intent.Amount}}, market, "", false)
		if err != nil {
			return nil, err
		}
		result, err := c.prerequisites(prefix, pulls, false)
		if err != nil {
			return nil, err
		}
		return append(result, decoded), nil
	}
	if len(prefix) != 0 {
		return nil, callError("calls", &c.call)
	}
	if c.intent.Action == "midnight.withdraw_collateral" {
		checked, err := c.coreWithdrawals(decoded)
		if err != nil {
			return nil, err
		}
		return []DecodedCalldataCall{checked}, nil
	}
	if (c.intent.Action != "midnight.withdraw" && c.intent.Action != "midnight.redeem") || decoded.FunctionName != "withdraw" {
		return nil, callError("function", &c.call)
	}
	if _, err := c.market(args[0]); err != nil {
		return nil, err
	}
	if err := c.ownerAndReceiver(args[2].(common.Address), args[3].(common.Address)); err != nil {
		return nil, err
	}
	amount, units := c.intent.Amount, args[1].(*big.Int)
	if amount.Unit != "all" && amount.Unit != "units" && amount.Unit != "assets" {
		return nil, amountError("amount.unit", c.call)
	}
	if units.Sign() == 0 {
		return nil, amountError("amount", c.call)
	}
	if amount.Unit != "all" {
		if err := expectMidnightAmount(units, amount.Value, "amount", c.call); err != nil {
			return nil, err
		}
	}
	if err := c.amountGuard("units", units, units); err != nil {
		return nil, err
	}
	return []DecodedCalldataCall{decoded}, nil
}

func (c midnightContext) ownerAndReceiver(owner, receiver common.Address) error {
	if err := recipient(owner, c.intent.Account, "on_behalf", c.call); err != nil {
		return err
	}
	return recipient(receiver, c.intent.Account, "receiver", c.call)
}

func (c midnightContext) coreWithdrawals(decoded DecodedCalldataCall) (DecodedCalldataCall, error) {
	entries := []DecodedCalldataCall{decoded}
	if decoded.FunctionName == "multicall" {
		data := decoded.Args[0].([][]byte)
		entries = make([]DecodedCalldataCall, len(data))
		for i, item := range data {
			child := innerCalldata(c.call, item, i)
			var err error
			if entries[i], err = decodeCall(child, defaultSnapshot.abis["midnight"], true); err != nil {
				return DecodedCalldataCall{}, err
			}
		}
	}
	if len(entries) == 0 {
		return DecodedCalldataCall{}, callError("collateral", &c.call)
	}
	var market midnightMarket
	transfers := make([]midnightTransfer, len(entries))
	for i, entry := range entries {
		args, child := entry.Args, entry.Call
		if entry.FunctionName != "withdrawCollateral" {
			return DecodedCalldataCall{}, callError("function", &child)
		}
		childContext := c
		childContext.call = child
		params, err := childContext.market(args[0])
		if err != nil {
			return DecodedCalldataCall{}, err
		}
		if err := childContext.ownerAndReceiver(args[3].(common.Address), args[4].(common.Address)); err != nil {
			return DecodedCalldataCall{}, err
		}
		if i == 0 {
			market = params
		}
		transfers[i] = midnightTransfer{args[1].(*big.Int), args[2].(*big.Int)}
	}
	if _, err := c.collateral(transfers, c.intent.CollateralWithdrawals, market, "core", c.intent.nullWithdrawals || c.intent.CollateralWithdrawals == nil); err != nil {
		return DecodedCalldataCall{}, err
	}
	if decoded.FunctionName == "multicall" {
		decoded.Calls = entries
	}
	return decoded, nil
}

func expectMidnightAmount(value *big.Int, expected, field string, call CalldataCall) error {
	want, err := readUint(expected, field, call)
	if err != nil {
		return err
	}
	if value.Cmp(want) != 0 {
		return amountError(field, call)
	}
	return nil
}

func (c midnightContext) targetAmount(unit string, target *big.Int, allowZero bool) error {
	amount := c.intent.Amount
	if amount.Unit == "all" {
		if unit == "units" && target.Cmp(maxUint256) == 0 {
			return nil
		}
		return amountError("amount", c.call)
	}
	if amount.Unit != unit || target.Cmp(maxUint256) == 0 || (target.Sign() == 0 && !allowZero) {
		return amountError("amount", c.call)
	}
	return expectMidnightAmount(target, amount.Value, "amount", c.call)
}

func (c midnightContext) checkGuardType() error {
	if c.intent.nullGuard {
		return &CalldataUnsupportedError{detail("guard.type", &c.call, nil)}
	}
	guard := c.intent.Guard
	if guard == nil || guard.Type == "slippage" {
		return nil
	}
	switch c.intent.Action {
	case "midnight.lend", "midnight.market_repay", "midnight.take_lend":
		if guard.Type == "min_units" || guard.Type == "max_assets" {
			return nil
		}
	case "midnight.borrow", "midnight.withdraw", "midnight.redeem", "midnight.take_borrow", "midnight.supply_collateral_take_borrow":
		if guard.Type == "max_units" || guard.Type == "min_assets" {
			return nil
		}
	}
	return &CalldataUnsupportedError{detail("guard.type", &c.call, nil)}
}

func (c midnightContext) amountGuard(unit string, target, bound *big.Int) error {
	if c.intent.Guard == nil {
		return nil
	}
	guard := *c.intent.Guard
	if guard.Type == "slippage" {
		return nil
	}
	if strings.HasSuffix(guard.Type, unit) {
		bound = target
	}
	want, err := readUint(guard.Value, "guard.value", c.call)
	if err != nil {
		return err
	}
	cmp := bound.Cmp(want)
	if (strings.HasPrefix(guard.Type, "min_") && cmp < 0) || (strings.HasPrefix(guard.Type, "max_") && cmp > 0) {
		return amountError("guard.value", c.call)
	}
	return nil
}

func (c midnightContext) buy(args []any, unit string) ([]midnightPull, error) {
	direct := c.intent.Action == "midnight.repay"
	lend := c.intent.Action == "midnight.lend" || c.intent.Action == "midnight.take_lend"
	repay := c.intent.Action == "midnight.market_repay"
	if !direct && !lend && !repay {
		return nil, callError("function", &c.call)
	}
	market, err := c.market(args[0])
	if err != nil {
		return nil, err
	}
	target, bound := args[1].(*big.Int), args[2].(*big.Int)
	reduceOnly, repayEnabled := args[3].(bool), args[4].(bool)
	fills := reflect.ValueOf(args[5])
	withdrawals := midnightTransfers(args[6])
	if reduceOnly == lend || repayEnabled != direct || (fills.Len() == 0) != direct {
		return nil, callError("repay", &c.call)
	}
	amount := c.intent.Amount
	if direct && amount.Unit != "all" && amount.Unit != "assets" || !direct && amount.Unit != "assets" && amount.Unit != "units" {
		return nil, amountError("amount.unit", c.call)
	}
	if err := c.targetAmount(unit, target, direct && len(withdrawals) > 0); err != nil {
		return nil, err
	}
	if direct && unit == "assets" && bound.Cmp(target) != 0 {
		return nil, amountError("min_units", c.call)
	}
	if direct && amount.Unit == "all" && (bound.Sign() == 0 || bound.Cmp(maxUint256) == 0) {
		return nil, amountError("max_assets", c.call)
	}
	if err := c.amountGuard(unit, target, bound); err != nil {
		return nil, err
	}
	var requested []MidnightCollateralTransfer
	var invalidList bool
	if direct || repay {
		requested, invalidList = c.intent.CollateralWithdrawals, c.intent.nullWithdrawals
	}
	if _, err := c.collateral(withdrawals, requested, market, "bundle", invalidList); err != nil {
		return nil, err
	}
	receiver := args[7].(common.Address)
	if len(withdrawals) != 0 || receiver != (common.Address{}) {
		if err := recipient(receiver, c.intent.Account, "collateral_receiver", c.call); err != nil {
			return nil, err
		}
	}
	if err := c.fills(fills, false); err != nil {
		return nil, err
	}
	if err := c.fee(args[8].(*big.Int), args[9].(common.Address), direct || repay); err != nil {
		return nil, err
	}
	maxAssets := target
	if unit == "units" {
		maxAssets = bound
	}
	wrapped := args[12].(common.Address)
	cap := new(big.Int)
	if wrapped == market.LoanToken {
		cap = maxAssets
	}
	if err := c.native(wrapped, cap, false); err != nil {
		return nil, err
	}
	if maxAssets.Sign() == 0 {
		return nil, nil
	}
	return []midnightPull{{market.LoanToken, maxAssets}}, nil
}

func (c midnightContext) sell(args []any, unit string) ([]midnightPull, error) {
	borrow := c.intent.Action == "midnight.borrow" || c.intent.Action == "midnight.take_borrow" || c.intent.Action == "midnight.supply_collateral_take_borrow"
	withdraw := c.intent.Action == "midnight.withdraw"
	if !borrow && !withdraw {
		return nil, callError("function", &c.call)
	}
	market, err := c.market(args[0])
	if err != nil {
		return nil, err
	}
	target, bound := args[1].(*big.Int), args[2].(*big.Int)
	fills := reflect.ValueOf(args[6])
	if args[3].(bool) != withdraw || fills.Len() == 0 || c.intent.Amount.Unit == "all" {
		return nil, callError("offers", &c.call)
	}
	if err := c.targetAmount(unit, target, false); err != nil {
		return nil, err
	}
	if err := c.amountGuard(unit, target, bound); err != nil {
		return nil, err
	}
	if err := recipient(args[4].(common.Address), c.intent.Account, "receiver", c.call); err != nil {
		return nil, err
	}
	if err := c.fills(fills, true); err != nil {
		return nil, err
	}
	if err := c.fee(args[7].(*big.Int), args[8].(common.Address), false); err != nil {
		return nil, err
	}
	var requested []MidnightCollateralTransfer
	var invalidList bool
	if c.intent.Action == "midnight.borrow" || c.intent.Action == "midnight.supply_collateral_take_borrow" {
		requested, invalidList = c.intent.CollateralSupplies, c.intent.nullSupplies
	}
	pulls, err := c.collateral(midnightTransfers(args[5]), requested, market, "", invalidList)
	if err != nil {
		return nil, err
	}
	wrapped := args[10].(common.Address)
	cap := new(big.Int)
	for _, pull := range pulls {
		if pull.token == wrapped {
			cap = pull.assets
			break
		}
	}
	return pulls, c.native(wrapped, cap, false)
}

func (c midnightContext) supplyCollateralWithNative(args []any) ([]midnightPull, error) {
	if c.intent.Action != "midnight.supply_collateral" {
		return nil, callError("function", &c.call)
	}
	var zeroAddress common.Address
	var zeroHash [32]byte
	blueMarket := converted[blueMarketParams](args[0])
	emptyBlueMarket := blueMarket.LoanToken == zeroAddress &&
		blueMarket.CollateralToken == zeroAddress && blueMarket.Oracle == zeroAddress &&
		blueMarket.Irm == zeroAddress && blueMarket.Lltv.Sign() == 0
	emptyPublication := args[5].(common.Address) == zeroAddress &&
		args[6].([32]byte) == zeroHash && args[7].(*big.Int).Sign() == 0 &&
		args[8].(*big.Int).Sign() == 0 && args[9].(*big.Int).Sign() == 0 &&
		args[10].(uint8) == 0 && args[11].([32]byte) == zeroHash &&
		args[12].([32]byte) == zeroHash && reflect.ValueOf(args[13]).Len() == 0 &&
		len(args[14].([]byte)) == 0
	if !emptyBlueMarket || args[1].(*big.Int).Sign() != 0 ||
		args[2].([32]byte) != zeroHash || !emptyPublication {
		return nil, callError("cancel_and_make", &c.call)
	}
	if args[15].(*big.Int).Sign() == 0 {
		return nil, amountError("deadline", c.call)
	}
	market, err := c.market(args[3])
	if err != nil {
		return nil, err
	}
	pulls, err := c.collateral(midnightTransfers(args[4]), []MidnightCollateralTransfer{
		{Collateral: c.intent.Collateral, Amount: c.intent.Amount},
	}, market, "", false)
	if err != nil {
		return nil, err
	}
	wrapped := args[16].(common.Address)
	cap := new(big.Int)
	for _, pull := range pulls {
		if pull.token == wrapped {
			cap = pull.assets
			break
		}
	}
	return pulls, c.native(wrapped, cap, true)
}

func (c midnightContext) fills(entries reflect.Value, buy bool) error {
	var firstMaker common.Address
	for i := 0; i < entries.Len(); i++ {
		entry := entries.Index(i)
		offer := entry.FieldByName("Offer")
		if _, err := c.market(offer.FieldByName("Market").Interface()); err != nil {
			return err
		}
		maker := offer.FieldByName("Maker").Interface().(common.Address)
		if offer.FieldByName("Buy").Bool() != buy || entry.FieldByName("Units").Interface().(*big.Int).Sign() == 0 || sameAddress(maker.Hex(), c.intent.Account) {
			return callError("offer_fills", &c.call)
		}
		if c.take != nil {
			group := common.Hash(offer.FieldByName("Group").Interface().([32]byte))
			if !strings.EqualFold(group.Hex(), c.take.Group) {
				return &CalldataTargetError{detail("group", &c.call, nil)}
			}
			if offer.FieldByName("ReduceOnly").Bool() {
				return &CalldataUnsupportedError{detail("offer.reduce_only", &c.call, nil)}
			}
			if i == 0 {
				firstMaker = maker
			}
			if c.take.Maker == nil && maker != firstMaker || c.take.Maker != nil && !sameAddress(maker.Hex(), *c.take.Maker) {
				return callError("offer_fills", &c.call)
			}
		}
	}
	return nil
}

func (c midnightContext) native(wrapped common.Address, cap *big.Int, requireValue bool) error {
	value, err := readUint(c.call.Value, "value", c.call)
	if err != nil {
		return err
	}
	if !requireValue && value.Sign() == 0 && wrapped == (common.Address{}) {
		return nil
	}
	if value.Sign() == 0 || !sameAddress(wrapped.Hex(), c.chain.WNative) || value.Cmp(cap) > 0 {
		return amountError("value", c.call)
	}
	return checkNativeBudget(value, c.intent.Funding, c.call)
}

func (c midnightContext) fee(pct *big.Int, to common.Address, repayment bool) error {
	if pct.Cmp(wad) >= 0 {
		return &CalldataFeeError{detail("referral_fee", &c.call, nil)}
	}
	referral := c.intent.ReferralFee
	if repayment {
		referral = nil
	}
	return checkFee(pct, to, c.intent.MaxFeeBPS, referral, c.call)
}
