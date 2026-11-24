package verifier

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Intent wraps the original JSON request body and the concrete Actions API
// path, including the chain-qualified selector where the route uses one.
type Intent struct {
	Path string          `json:"path"`
	Body json.RawMessage `json:"body"`
}

// Call is an entry in the API's ordered data.calls list.
type Call struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	To    string `json:"to"`
	Data  string `json:"data"`
	Value string `json:"value"`
}

// Verify reports whether calls pass the supported offline intent checks.
// The caller must send from the account and chain in the original intent.
func Verify(intent Intent, calls []Call) bool {
	return check(intent, calls)
}

//go:embed abi_blue_vault.json
var baseABI []byte

//go:embed chains_blue_vault.json
var baseRegistry []byte

type deployment struct {
	ID            uint64         `json:"id"`
	Blue          common.Address `json:"blue"`
	BlueBundle    common.Address `json:"blue_bundle"`
	VaultV1Bundle common.Address `json:"vault_v1_bundle"`
	VaultV2Bundle common.Address `json:"vault_v2_bundle"`
	VaultExit     common.Address `json:"vault_exit"`
	Wrapped       common.Address `json:"wrapped"`
}

var methods = loadABI(baseABI)
var chains = loadChains(baseRegistry)
var zeroAddress common.Address
var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
var maxUint96 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 96), big.NewInt(1))

func loadABI(source []byte) map[string]abi.Method {
	var entries []struct {
		Key      string
		Selector string
		Method   json.RawMessage
	}
	if err := json.Unmarshal(source, &entries); err != nil {
		panic(err)
	}
	methods := make(map[string]abi.Method)
	for _, entry := range entries {
		parsed, err := abi.JSON(strings.NewReader("[" + string(entry.Method) + "]"))
		if err != nil {
			panic(err)
		}
		for name, method := range parsed.Methods {
			if "0x"+hex.EncodeToString(method.ID) != entry.Selector {
				panic("selector mismatch: " + name)
			}
			if entry.Key != "" {
				name = entry.Key
			}
			methods[name] = method
		}
	}
	return methods
}

func loadChains(source []byte) map[uint64]deployment {
	var entries []deployment
	if err := json.Unmarshal(source, &entries); err != nil {
		panic(err)
	}
	chains := make(map[uint64]deployment, len(entries))
	for _, entry := range entries {
		chains[entry.ID] = entry
	}
	return chains
}

type amount struct {
	Unit  string `json:"unit"`
	Value string `json:"value"`
}

type request struct {
	Account         string `json:"account"`
	ChainID         uint64 `json:"chain_id"`
	Amount          amount `json:"amount"`
	Collateral      amount `json:"collateral_amount"`
	Destination     string `json:"destination_market"`
	Market          string `json:"market"`
	MidnightID      string `json:"market_id"`
	CollateralToken struct {
		Address string `json:"address"`
	} `json:"collateral"`
	Supplies    []collateralChange `json:"collateral_supplies"`
	Withdrawals []collateralChange `json:"collateral_withdrawals"`
	Maker       string             `json:"maker"`
	Groups      []string           `json:"groups"`
	Type        string             `json:"type"`
	Guard       struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"guard"`
	FundingAddress string          `json:"funding_address"`
	FundingMarket  string          `json:"funding_market"`
	MarketParams   json.RawMessage `json:"market_params"`
	FundingSource  struct {
		Type   string `json:"type"`
		Market string `json:"market_id"`
	} `json:"funding_source"`
	Offers struct {
		Type    string `json:"type"`
		Entries []struct {
			Market string `json:"market_id"`
		} `json:"entries"`
	} `json:"offers"`
	Expires uint64 `json:"expires_at"`
	Origin  string `json:"calldata_origin"`
	Funding struct {
		Type      string `json:"type"`
		MaxNative string `json:"max_native_assets"`
	} `json:"funding"`
	Fee struct {
		BPS       uint64 `json:"bps"`
		Recipient string `json:"recipient"`
	} `json:"referral_fee"`
}

type route struct {
	family, action, selector string
	chain                    uint64
}

func check(intent Intent, calls []Call) bool {
	if len(calls) == 0 || len(calls) > 128 || len(intent.Body) > 1<<20 {
		return false
	}
	var body request
	if json.Unmarshal(intent.Body, &body) != nil || !nonzeroAddress(body.Account) {
		return false
	}
	r, ok := parseRoute(intent.Path, body.ChainID)
	if !ok {
		return false
	}
	ids := make(map[string]bool, len(calls))
	for _, call := range calls {
		if !validCall(call) || ids[call.ID] {
			return false
		}
		ids[call.ID] = true
	}
	if strings.HasPrefix(r.family, "midnight/") {
		return verifyMidnight(r, body, calls)
	}
	chain, ok := chains[r.chain]
	if !ok {
		return false
	}
	switch r.family {
	case "blue":
		return verifyBlue(r, chain, body, calls)
	case "vaults-v1", "vaults-v2":
		return verifyVault(r, chain, body, calls)
	}
	return false
}

func parseRoute(path string, bodyChain uint64) (route, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/v0/actions/"), "/")
	if !strings.HasPrefix(path, "/v0/actions/") || len(parts) < 3 {
		return route{}, false
	}
	r := route{family: parts[0]}
	switch r.family {
	case "midnight":
		r.family += "/" + parts[1]
		switch parts[1] {
		case "markets", "offer-groups":
			if len(parts) != 4 {
				return route{}, false
			}
			r.chain, r.selector = splitSelector(parts[2])
			if _, ok := hexBytes(r.selector, 32); !ok {
				return route{}, false
			}
			r.action = parts[3]
		case "offers":
			r.chain = bodyChain
			if len(parts) == 3 {
				r.action = parts[2]
			} else if len(parts) == 4 && parts[2] == "funding" && parts[3] == "withdraw" {
				r.action = "funding/withdraw"
			} else {
				return route{}, false
			}
		default:
			return route{}, false
		}
	case "blue":
		if len(parts) != 4 || parts[1] != "markets" {
			return route{}, false
		}
		r.action = parts[3]
		r.chain, r.selector = splitSelector(parts[2])
		if _, ok := hexBytes(r.selector, 32); !ok {
			return route{}, false
		}
	case "vaults-v1", "vaults-v2":
		if len(parts) != 3 {
			return route{}, false
		}
		r.action = parts[2]
		r.chain, r.selector = splitSelector(parts[1])
		if !nonzeroAddress(r.selector) {
			return route{}, false
		}
	default:
		return route{}, false
	}
	return r, r.chain != 0 && (bodyChain == 0 || r.chain == bodyChain)
}

func splitSelector(selector string) (uint64, string) {
	raw, value, ok := strings.Cut(strings.NewReplacer("%3A", ":", "%3a", ":").Replace(selector), ":")
	chain, err := strconv.ParseUint(raw, 10, 53)
	if !ok || err != nil || strconv.FormatUint(chain, 10) != raw {
		return 0, ""
	}
	return chain, value
}

func validCall(call Call) bool {
	id, ok := strings.CutPrefix(call.ID, "call_")
	validID := id != "" && strings.Trim(id, "0123456789") == ""
	_, validValue := decimal(call.Value)
	return ok && validID && validValue && nonzeroAddress(call.To) && len(call.Data) <= 2<<20
}

func decimal(s string) (*big.Int, bool) {
	if len(s) == 0 || len(s) > 78 || (len(s) > 1 && s[0] == '0') {
		return nil, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	return n, ok && n.BitLen() <= 256
}

func nonzeroAddress(s string) bool {
	return len(s) == 42 && strings.HasPrefix(s, "0x") && common.IsHexAddress(s) && common.HexToAddress(s) != zeroAddress
}

func hexBytes(s string, size int) ([]byte, bool) {
	if !strings.HasPrefix(s, "0x") || (size >= 0 && len(s) != 2+2*size) {
		return nil, false
	}
	b, err := hex.DecodeString(s[2:])
	return b, err == nil
}

func decode(method abi.Method, call Call, origin string, final bool) (map[string]any, bool) {
	data, ok := hexBytes(call.Data, -1)
	if !ok || len(data) < 4 || !bytes.Equal(data[:4], method.ID) {
		return nil, false
	}
	args := data[4:]
	if final && origin != "" {
		expected, valid := hexBytes(origin, 4)
		suffix := len(args) % 32
		if !valid || (suffix != 8 && suffix != 12) || !bytes.Equal(args[len(args)-suffix+4:len(args)-suffix+8], expected) {
			return nil, false
		}
	}
	if suffix := len(args) % 32; suffix != 0 {
		if !final || (suffix != 8 && suffix != 12) {
			return nil, false
		}
		args = args[:len(args)-suffix]
	}
	if !canonicalABILayout(method.Inputs, args) {
		return nil, false
	}
	values, err := method.Inputs.Unpack(args)
	if err != nil {
		return nil, false
	}
	canonical, err := method.Inputs.Pack(values...)
	if err != nil || !bytes.Equal(args, canonical) {
		return nil, false
	}
	result := make(map[string]any, len(values))
	for i, arg := range method.Inputs {
		result[arg.Name] = values[i]
	}
	return result, true
}

func number(v any) *big.Int {
	switch n := v.(type) {
	case *big.Int:
		return n
	case uint8:
		return new(big.Int).SetUint64(uint64(n))
	case uint16:
		return new(big.Int).SetUint64(uint64(n))
	case uint32:
		return new(big.Int).SetUint64(uint64(n))
	case uint64:
		return new(big.Int).SetUint64(n)
	}
	return nil
}

func eq(v any, want *big.Int) bool {
	n := number(v)
	return n != nil && want != nil && n.Cmp(want) == 0
}

func uintEq(v any, want uint64) bool {
	return eq(v, new(big.Int).SetUint64(want))
}

func positive(v any) bool {
	n := number(v)
	return n != nil && n.Sign() > 0
}

func marketMatches(method abi.Method, name string, value any, selector string) bool {
	for _, input := range method.Inputs {
		if input.Name == name {
			packed, err := (abi.Arguments{input}).Pack(value)
			return err == nil && strings.EqualFold(crypto.Keccak256Hash(packed).Hex(), selector)
		}
	}
	return false
}

func address(v any) common.Address {
	if a, ok := v.(common.Address); ok {
		return a
	}
	return zeroAddress
}

func (d deployment) pinned(a common.Address) bool {
	return a == d.Blue || a == d.BlueBundle || a == d.VaultV1Bundle ||
		a == d.VaultV2Bundle || a == d.VaultExit
}

func callTo(call Call, target common.Address) bool {
	return target != zeroAddress && common.HexToAddress(call.To) == target
}

func amounts(selected amount, args map[string]any, assets, shares string, dynamicAll bool) bool {
	n, ok := decimal(selected.Value)
	switch selected.Unit {
	case "assets":
		return ok && n.Sign() > 0 && eq(args[assets], n) && (shares == "" || uintEq(args[shares], 0))
	case "shares":
		return shares != "" && ok && n.Sign() > 0 && n.Cmp(maxUint256) < 0 && uintEq(args[assets], 0) && eq(args[shares], n)
	case "all":
		return selected.Value == "" && shares != "" && uintEq(args[assets], 0) &&
			(eq(args[shares], maxUint256) || (dynamicAll && positive(args[shares])))
	}
	return false
}

func limits(body request, args map[string]any) bool {
	if d, exists := args["deadline"]; exists {
		if !positive(d) || (body.Expires != 0 && !uintEq(d, body.Expires)) {
			return false
		}
	}
	if fee, exists := args["referralFeePct"]; exists {
		pct := new(big.Int).Mul(new(big.Int).SetUint64(body.Fee.BPS), big.NewInt(100000000000000))
		if body.Fee.BPS > 10000 || !eq(fee, pct) || address(args["referralFeeRecipient"]) != common.HexToAddress(body.Fee.Recipient) {
			return false
		}
		if body.Fee.Recipient != "" && !nonzeroAddress(body.Fee.Recipient) {
			return false
		}
	}
	return true
}

func funding(body request, value string, token common.Address, chain deployment, maximum *big.Int) bool {
	switch body.Funding.Type {
	case "erc20":
		return value == "0" && body.Funding.MaxNative == ""
	case "auto", "native":
	default:
		return false
	}
	cap, ok := decimal(body.Funding.MaxNative)
	actual, valid := decimal(value)
	if !ok || !valid || maximum == nil || cap.Cmp(actual) < 0 {
		return false
	}
	if actual.Sign() == 0 {
		return body.Funding.Type == "auto"
	}
	return actual.Cmp(maximum) == 0 && (token == zeroAddress || token == chain.Wrapped) && chain.Wrapped != zeroAddress
}

func prerequisites(calls []Call, chain deployment, bundle, token common.Address, maximum *big.Int, authorization bool) bool {
	var approvals int
	var authorized bool
	for i, call := range calls {
		if call.Value != "0" {
			return false
		}
		switch call.Kind {
		case "approval":
			if authorized || maximum == nil {
				return false
			}
			args, ok := decode(methods["approve"], call, "", false)
			if !ok || address(args["spender"]) != bundle {
				return false
			}
			approvals++
			n := number(args["amount"])
			if n == nil {
				return false
			}
			if token == zeroAddress {
				target := common.HexToAddress(call.To)
				if len(calls) > 2 || target == bundle || chain.pinned(target) || midnightChains[chain.ID].pinned(target) {
					return false
				}
				if n.Sign() == 0 {
					if i != 0 || len(calls) != 2 || calls[1].Kind != "approval" || !callTo(calls[1], target) {
						return false
					}
				} else if i != len(calls)-1 || (n.Cmp(maxUint256) != 0 && (n.Cmp(maximum) > 0 || n.Cmp(maxUint96) == 0)) {
					return false
				}
				continue
			}
			if !callTo(call, token) || approvals > 2 ||
				(n.Sign() == 0 && (i != 0 || len(calls) <= 1 || calls[i+1].Kind != "approval")) ||
				(n.Sign() > 0 && i+1 < len(calls) && calls[i+1].Kind == "approval") ||
				(n.Sign() > 0 && n.Cmp(maximum) != 0 && n.Cmp(maxUint256) != 0 && n.Cmp(maxUint96) != 0) {
				return false
			}
		case "authorization":
			if authorized || !authorization || !callTo(call, chain.Blue) {
				return false
			}
			a, ok := decode(methods["setAuthorization"], call, "", false)
			if !ok || address(a["authorized"]) != bundle || a["newIsAuthorized"] != true {
				return false
			}
			authorized = true
		default:
			return false
		}
	}
	return true
}
