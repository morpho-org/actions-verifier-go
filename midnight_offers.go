package calldataverifier

import (
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// MidnightTakeIntent describes taking a Midnight borrow or lend offer.
type MidnightTakeIntent struct {
	CommonIntent
	ExpiresAt          *uint64                      `json:"expires_at,omitempty"`
	P2P                *bool                        `json:"p2p,omitempty"`
	Group              string                       `json:"group"`
	MarketID           *string                      `json:"market_id,omitempty"`
	Maker              *string                      `json:"maker,omitempty"`
	Amount             Amount                       `json:"amount"`
	Guard              *MidnightGuard               `json:"guard,omitempty"`
	ReferralFee        *ReferralFee                 `json:"referral_fee,omitempty"`
	Funding            *Funding                     `json:"funding,omitempty"`
	CollateralSupplies []MidnightCollateralTransfer `json:"collateral_supplies,omitempty"`
	nullGuard          bool
}

func (i MidnightTakeIntent) calldataIntent() CommonIntent { return i.CommonIntent }

// UnmarshalJSON preserves the distinction between omitted and null guards.
func (i *MidnightTakeIntent) UnmarshalJSON(data []byte) error {
	type fields MidnightTakeIntent
	decoded := fields{CommonIntent: CommonIntent{MaxFeeBPS: i.MaxFeeBPS}}
	raw, err := decodeIntent(data, &decoded, &decoded.CommonIntent)
	if err != nil {
		return err
	}
	decoded.nullGuard = string(raw["guard"]) == "null"
	*i = MidnightTakeIntent(decoded)
	return nil
}

// MidnightOfferManagementIntent describes cancellations, funding withdrawals,
// and make-offer actions.
type MidnightOfferManagementIntent struct {
	CommonIntent
	ExpiresAt          *uint64                      `json:"expires_at,omitempty"`
	P2P                *bool                        `json:"p2p,omitempty"`
	Groups             []string                     `json:"groups,omitempty"`
	FundingAddress     string                       `json:"funding_address,omitempty"`
	FundingMarket      string                       `json:"funding_market,omitempty"`
	Amount             Amount                       `json:"amount,omitempty"`
	Offers             *MidnightMakerOffers         `json:"offers,omitempty"`
	MarketParams       []MidnightMarketParams       `json:"market_params,omitempty"`
	CollateralSupplies []MidnightCollateralTransfer `json:"collateral_supplies,omitempty"`
	Funding            *Funding                     `json:"funding,omitempty"`
	FundingSource      *MidnightFundingSource       `json:"funding_source,omitempty"`
}

func (i MidnightOfferManagementIntent) calldataIntent() CommonIntent { return i.CommonIntent }

// UnmarshalJSON reads the exact Actions API field names.
func (i *MidnightOfferManagementIntent) UnmarshalJSON(data []byte) error {
	type fields MidnightOfferManagementIntent
	_, err := decodeIntent(data, (*fields)(i), &i.CommonIntent)
	return err
}

// MidnightMakerOffers describes the price or rate offers of a make-offer action, in published order.
type MidnightMakerOffers struct {
	Type    string                    `json:"type"`
	Entries []MidnightMakerOfferEntry `json:"entries"`
}

// MidnightMakerOfferEntry specifies the market, validity period, and tick or rate.
type MidnightMakerOfferEntry struct {
	MarketID  string `json:"market_id"`
	StartsAt  uint64 `json:"starts_at"`
	ExpiresAt uint64 `json:"expires_at"`
	Tick      string `json:"tick,omitempty"`
	RateWAD   string `json:"rate_wad,omitempty"`
}

// MidnightFundingSource identifies wallet funding or a variable rate market.
type MidnightFundingSource struct {
	Type     string `json:"type"`
	MarketID string `json:"market_id,omitempty"`
}

func verifyMidnightTake(intent MidnightTakeIntent, calls []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	switch intent.Action {
	case "midnight.take_borrow", "midnight.supply_collateral_take_borrow", "midnight.take_lend":
	default:
		return nil, unknownMidnightAction(calls)
	}
	return verifyMidnightCalls(MidnightMarketIntent{
		CommonIntent: intent.CommonIntent, Amount: intent.Amount, Guard: intent.Guard, nullGuard: intent.nullGuard,
		ReferralFee: intent.ReferralFee, Funding: intent.Funding, CollateralSupplies: intent.CollateralSupplies,
	}, &intent, calls, chain)
}

func validGroup(group string) bool {
	if len(group) != 66 || !strings.EqualFold(group[:2], "0x") {
		return false
	}
	_, err := hex.DecodeString(group[2:])
	return err == nil
}

func verifyMidnightOffer(intent MidnightOfferManagementIntent, calls []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	switch intent.Action {
	case "midnight.make_borrow", "midnight.make_lend", "midnight.make_repay", "midnight.make_withdraw":
		return verifyMidnightMake(intent, calls, chain)
	case "midnight.cancel_offers", "midnight.withdraw_funding":
	default:
		return nil, unknownMidnightAction(calls)
	}
	call, err := selectMain(calls, 1)
	if err != nil {
		return nil, err
	}
	if err := expectZeroValue(call); err != nil {
		return nil, err
	}
	var decoded DecodedCalldataCall
	if intent.Action == "midnight.cancel_offers" {
		decoded, err = cancelMidnightOffers(intent, call, chain)
	} else {
		decoded, err = withdrawMidnightFunding(intent, call, chain)
	}
	if err != nil {
		return nil, err
	}
	return []DecodedCalldataCall{decoded}, nil
}

func cancelMidnightOffers(intent MidnightOfferManagementIntent, call CalldataCall, chain Deployment) (DecodedCalldataCall, error) {
	if err := target(call, chain.Midnight); err != nil {
		return DecodedCalldataCall{}, err
	}
	if intent.Groups == nil {
		return DecodedCalldataCall{}, &CalldataTargetError{detail("groups", &call, nil)}
	}
	groups := make(map[string]bool, len(intent.Groups))
	for _, group := range intent.Groups {
		if !validGroup(group) {
			return DecodedCalldataCall{}, &CalldataTargetError{detail("groups", &call, nil)}
		}
		groups[strings.ToLower(group)] = true
	}
	if len(groups) == 0 || len(groups) != len(intent.Groups) {
		return DecodedCalldataCall{}, callError("groups", &call)
	}
	contract := defaultSnapshot.abis["midnight"]
	decoded, err := decodeCall(call, contract, false)
	if err != nil {
		return DecodedCalldataCall{}, err
	}
	maxConsumed := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	check := func(entry DecodedCalldataCall) error {
		leg := entry.Call
		if entry.FunctionName != "setConsumed" {
			return callError("function", &leg)
		}
		args := entry.Args
		group := common.Hash(args[0].([32]byte)).Hex()
		if !groups[group] {
			return &CalldataTargetError{detail("groups", &leg, nil)}
		}
		delete(groups, group)
		if args[1].(*big.Int).Cmp(maxConsumed) != 0 {
			return amountError("consumed", leg)
		}
		return recipient(args[2].(common.Address), intent.Account, "on_behalf", leg)
	}
	if decoded.FunctionName != "multicall" {
		if len(groups) != 1 {
			return DecodedCalldataCall{}, callError("groups", &call)
		}
		return decoded, check(decoded)
	}
	children := decoded.Args[0].([][]byte)
	if len(children) != len(groups) {
		return DecodedCalldataCall{}, callError("groups", &call)
	}
	decoded.Calls = make([]DecodedCalldataCall, len(children))
	for i, data := range children {
		leg := innerCalldata(call, data, i)
		entry, err := decodeCall(leg, contract, true)
		if err != nil {
			return DecodedCalldataCall{}, err
		}
		if err := check(entry); err != nil {
			return DecodedCalldataCall{}, err
		}
		decoded.Calls[i] = entry
	}
	return decoded, nil
}

func withdrawMidnightFunding(intent MidnightOfferManagementIntent, call CalldataCall, chain Deployment) (DecodedCalldataCall, error) {
	if err := target(call, chain.Blue); err != nil {
		return DecodedCalldataCall{}, err
	}
	decoded, err := decodeCall(call, defaultSnapshot.abis["blue"], false)
	if err != nil {
		return DecodedCalldataCall{}, err
	}
	if decoded.FunctionName != "withdraw" {
		return DecodedCalldataCall{}, callError("function", &call)
	}
	args := decoded.Args
	if !strings.EqualFold(blueMarketID(converted[blueMarketParams](args[0])), intent.FundingMarket) {
		return DecodedCalldataCall{}, &CalldataTargetError{detail("funding_market", &call, nil)}
	}
	if !nonzeroAddress(intent.FundingAddress) {
		return DecodedCalldataCall{}, &CalldataTargetError{detail("funding_address", &call, nil)}
	}
	if err := recipient(args[3].(common.Address), intent.FundingAddress, "funding_address", call); err != nil {
		return DecodedCalldataCall{}, err
	}
	if err := recipient(args[4].(common.Address), intent.Account, "receiver", call); err != nil {
		return DecodedCalldataCall{}, err
	}
	if args[1].(*big.Int).Sign() != 0 || args[2].(*big.Int).Sign() == 0 {
		return DecodedCalldataCall{}, amountError("shares", call)
	}
	return decoded, nil
}
