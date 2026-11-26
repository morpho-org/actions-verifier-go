package calldataverifier

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// CalldataCall is an Actions API call in transaction order.
type CalldataCall struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	To    string `json:"to"`
	Data  string `json:"data"`
	Value string `json:"value"`
}

// UnmarshalJSON rejects absent or non-string call IDs and kinds.
func (c *CalldataCall) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return callError("calls", nil)
	}
	var id, kind *string
	if err := json.Unmarshal(fields["id"], &id); err != nil || id == nil {
		return callError("id", nil)
	}
	call := CalldataCall{ID: *id}
	if err := json.Unmarshal(fields["kind"], &kind); err != nil || kind == nil {
		return callError("kind", &call)
	}
	call.Kind = *kind
	for _, field := range []struct {
		name  string
		value *string
	}{{"to", &call.To}, {"data", &call.Data}, {"value", &call.Value}} {
		if raw := fields[field.name]; raw != nil {
			if err := json.Unmarshal(raw, field.value); err != nil {
				return err
			}
		}
	}
	*c = call
	return nil
}

// DecodedCalldataCall contains the checked call, ABI arguments, and checked children.
type DecodedCalldataCall struct {
	Call         CalldataCall          `json:"call"`
	FunctionName string                `json:"functionName"`
	Args         []any                 `json:"args"`
	Calls        []DecodedCalldataCall `json:"calls,omitempty"`
}

// CommonIntent is the caller's chain, account, and fee policy.
type CommonIntent struct {
	Action         string `json:"action"`
	ChainID        uint64 `json:"chain_id"`
	Account        string `json:"account"`
	MaxFeeBPS      int    `json:"max_fee_bps"`
	CalldataOrigin string `json:"calldata_origin,omitempty"`
}

// Amount specifies a unit and, except for "all", a decimal integer.
type Amount struct {
	Unit  string `json:"unit"`
	Value string `json:"value,omitempty"`
}

// Funding describes permitted funding and the caller's native value limit.
type Funding struct {
	Type            string `json:"type"`
	MaxNativeAssets string `json:"max_native_assets,omitempty"`
}

// ReferralFee specifies the maximum referral fee and its intended recipient.
type ReferralFee struct {
	BPS       int    `json:"bps"`
	Recipient string `json:"recipient"`
}

// VaultCalldataIntent describes a Vault V1 or V2 action. Asset must come from
// a trusted source independent of the calldata response.
type VaultCalldataIntent struct {
	CommonIntent
	Vault         string       `json:"vault"`
	Asset         string       `json:"asset"`
	Amount        Amount       `json:"amount"`
	Funding       *Funding     `json:"funding,omitempty"`
	ReferralFee   *ReferralFee `json:"referral_fee,omitempty"`
	ExpiresAt     *uint64      `json:"expires_at,omitempty"`
	SignatureMode string       `json:"signature_mode,omitempty"`
	SlippageBPS   *int         `json:"slippage_bps,omitempty"`
	MaxPenaltyBPS *int         `json:"max_penalty_bps,omitempty"`
	Market        string       `json:"market,omitempty"`
}

// CalldataIntent is the original request augmented with a chain and fee policy.
type CalldataIntent interface {
	calldataIntent() CommonIntent
}

func (i VaultCalldataIntent) calldataIntent() CommonIntent { return i.CommonIntent }

// VerifyOptions provides caller-owned verification configuration.
type VerifyOptions struct {
	Registry *Registry
}

// VerifyCalldata checks ordered Actions API calls against an independently
// retained intent, without simulation, network access, or input mutation.
func VerifyCalldata(intent CalldataIntent, calls []CalldataCall, options ...VerifyOptions) ([]DecodedCalldataCall, error) {
	if len(options) > 1 {
		return nil, callError("options", nil)
	}
	if intent == nil || (reflect.ValueOf(intent).Kind() == reflect.Pointer && reflect.ValueOf(intent).IsNil()) {
		return nil, callError("action", nil)
	}
	commonIntent := intent.calldataIntent()
	var first *CalldataCall
	if len(calls) != 0 {
		first = &calls[0]
	}
	chains := defaultSnapshot.Deployments
	if len(options) != 0 && options[0].Registry != nil && options[0].Registry.chains != nil {
		chains = options[0].Registry.chains
	}
	chain, ok := chains[commonIntent.ChainID]
	if commonIntent.ChainID == 0 || commonIntent.ChainID > 1<<53-1 {
		return nil, &CalldataChainError{detail("chain_id", first, nil)}
	}
	if !ok {
		cause := fmt.Errorf("unsupported chain ID %d", commonIntent.ChainID)
		return nil, &CalldataChainError{detail("chain_id", first, cause)}
	}
	if !nonzeroAddress(commonIntent.Account) {
		return nil, &CalldataRecipientError{detail("account", first, nil)}
	}
	var vault *VaultCalldataIntent
	switch i := intent.(type) {
	case VaultCalldataIntent:
		vault = &i
	case *VaultCalldataIntent:
		vault = i
	}
	if vault != nil && !nonzeroAddress(vault.Vault) {
		return nil, &CalldataTargetError{detail("vault", first, nil)}
	}
	if commonIntent.MaxFeeBPS < 0 || commonIntent.MaxFeeBPS > 10_000 {
		return nil, &CalldataFeeError{detail("max_fee_bps", first, nil)}
	}
	for i := range calls {
		if !nonzeroAddress(calls[i].To) {
			return nil, &CalldataTargetError{detail("to", &calls[i], nil)}
		}
	}
	if vault != nil {
		return verifyVault(*vault, calls, chain)
	}
	switch i := intent.(type) {
	case BlueCalldataIntent:
		return verifyBlue(i, calls, chain)
	case *BlueCalldataIntent:
		return verifyBlue(*i, calls, chain)
	case MidnightMarketIntent:
		return verifyMidnight(i, calls, chain)
	case *MidnightMarketIntent:
		return verifyMidnight(*i, calls, chain)
	case MidnightTakeIntent:
		return verifyMidnightTake(i, calls, chain)
	case *MidnightTakeIntent:
		return verifyMidnightTake(*i, calls, chain)
	case MidnightOfferManagementIntent:
		return verifyMidnightOffer(i, calls, chain)
	case *MidnightOfferManagementIntent:
		return verifyMidnightOffer(*i, calls, chain)
	}
	return nil, callError("action", first)
}

func validAddress(s string) bool {
	return len(s) == 42 && strings.HasPrefix(s, "0x") && common.IsHexAddress(s)
}

func nonzeroAddress(s string) bool {
	return validAddress(s) && common.HexToAddress(s) != (common.Address{})
}

type errorDetail struct {
	Field  string
	CallID *string
	cause  error
}

func detail(field string, call *CalldataCall, cause error) errorDetail {
	e := errorDetail{Field: field, cause: cause}
	if call != nil {
		id := call.ID
		e.CallID = &id
	}
	return e
}

func (e errorDetail) Error() string {
	if e.CallID == nil {
		return fmt.Sprintf("Cannot verify \"%s\". Check the original intent and supplied calls.", e.Field)
	}
	return fmt.Sprintf("Cannot verify \"%s\" in call \"%s\". Do not sign this call.", e.Field, *e.CallID)
}

func (e errorDetail) Unwrap() error { return e.cause }

func (e errorDetail) location() errorDetail { return e }

// CalldataChainError means the intended chain has no usable registry entry.
type CalldataChainError struct{ errorDetail }

// CalldataTargetError means a target or market is not the intended one.
type CalldataTargetError struct{ errorDetail }

// CalldataDecodeError means the bytes are not canonical for the expected ABI.
type CalldataDecodeError struct{ errorDetail }

// CalldataCallError means a call or action is missing, unexpected, or out of order.
type CalldataCallError struct{ errorDetail }

// CalldataAmountError means a transaction amount differs from the intent.
type CalldataAmountError struct{ errorDetail }

// CalldataRecipientError means an owner or recipient differs from the intent.
type CalldataRecipientError struct{ errorDetail }

// CalldataFeeError means the fee or its recipient is not authorized.
type CalldataFeeError struct{ errorDetail }

// CalldataUnsupportedError means an action variant cannot be checked locally.
type CalldataUnsupportedError struct{ errorDetail }

// CalldataProofError means an Actions API proof is invalid or does not authorize the calls.
type CalldataProofError struct{ errorDetail }

func callError(field string, call *CalldataCall) error {
	return &CalldataCallError{detail(field, call, nil)}
}
