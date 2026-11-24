package verifier

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestMidnightRoutes(t *testing.T) {
	for _, f := range loadFixtures(t, "midnight.json") {
		t.Run(f.Calls[len(f.Calls)-1].Kind, func(t *testing.T) {
			if !Verify(f.Intent, f.Calls) {
				t.Fatalf("valid response rejected: %s", f.Intent.Path)
			}
			for name, mutate := range map[string]func(*Intent, *Call){
				"target":        func(_ *Intent, c *Call) { c.To = "0x" + strings.Repeat("ee", 20) },
				"selector":      func(_ *Intent, c *Call) { c.Data = "0xffffffff" + c.Data[10:] },
				"kind":          func(_ *Intent, c *Call) { c.Kind = "unknown" },
				"native-value":  func(_ *Intent, c *Call) { c.Value = "1" },
				"unknown-route": func(intent *Intent, _ *Call) { intent.Path += "-unknown" },
			} {
				t.Run(name, func(t *testing.T) {
					intent := f.Intent
					calls := slices.Clone(f.Calls)
					mutate(&intent, &calls[len(calls)-1])
					if Verify(intent, calls) {
						t.Fatal("mutated response accepted")
					}
				})
			}
		})
	}
}

func TestMidnightWalletOfferApproval(t *testing.T) {
	chain := midnightChains[1]
	for _, f := range loadFixtures(t, "midnight.json") {
		if !strings.HasSuffix(f.Intent.Path, "/offers/repay") {
			continue
		}
		token := "0x" + strings.Repeat("12", 20)
		for _, c := range []struct {
			name, token string
			spender     common.Address
			amount      *big.Int
			valid       bool
		}{
			{"bounded", token, chain.Midnight, big.NewInt(5), true},
			{"over intent", token, chain.Midnight, big.NewInt(6), false},
			{"unlimited", token, chain.Midnight, maxUint256, true},
			{"unlimited wrong spender", token, chain.MidnightBundlesV2, maxUint256, false},
			{"unlimited pinned token", chain.Midnight.Hex(), chain.Midnight, maxUint256, false},
			{"pinned", chain.Midnight.Hex(), chain.Midnight, big.NewInt(5), false},
		} {
			t.Run(c.name, func(t *testing.T) {
				method := methods["approve"]
				args, err := method.Inputs.Pack(c.spender, c.amount)
				if err != nil {
					t.Fatal(err)
				}
				calldata := "0x" + hex.EncodeToString(slices.Concat(method.ID, args))
				leading := Call{ID: "call_21", Kind: "approval", To: c.token, Data: calldata, Value: "0"}
				if result := Verify(f.Intent, append([]Call{leading}, f.Calls...)); result != c.valid {
					t.Errorf("Verify = %t; want %t", result, c.valid)
				}
			})
		}
		return
	}
	t.Fatal("wallet repay fixture missing")
}

func TestMidnightNativeFundingApproval(t *testing.T) {
	chain := midnightChains[1]
	var body request
	for _, mode := range []struct {
		name, value string
	}{
		{"auto", "2"},
		{"native", "5"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			body.Funding.Type = mode.name
			body.Funding.MaxNative = "5"
			caps := map[common.Address]*big.Int{chain.WrappedNative: big.NewInt(5)}
			wrapped := map[string]any{"wrappedNative": chain.WrappedNative}
			if !midnightFunding(chain, body, Call{Value: mode.value}, wrapped, caps) {
				t.Fatal("valid native funding rejected")
			}
			for _, amount := range []*big.Int{big.NewInt(5), maxUint256} {
				m := methods["approve"]
				data, err := m.Inputs.Pack(chain.MidnightBundlesV2, amount)
				if err != nil {
					t.Fatal(err)
				}
				call := Call{Kind: "approval", To: chain.WrappedNative.Hex(), Data: "0x" + hex.EncodeToString(slices.Concat(m.ID, data)), Value: "0"}
				if !midnightPrereqs(chain, body, []Call{call}, chain.MidnightBundlesV2, caps) {
					t.Fatalf("approval %s rejected for full wrapped-native pull", amount)
				}
			}
		})
	}
}

func TestMidnightCancelRejectsUnknownInnerSelector(t *testing.T) {
	for _, f := range loadFixtures(t, "midnight.json") {
		if !strings.HasSuffix(f.Intent.Path, "/offers/cancel") {
			continue
		}
		selector := hex.EncodeToString(midnightMethods["midnight.setConsumed"].ID)
		f.Calls[0].Data = strings.Replace(f.Calls[0].Data, selector, "ffffffff", 1)
		if Verify(f.Intent, f.Calls) {
			t.Fatal("multicall accepted unknown nested selector")
		}
		return
	}
	t.Fatal("cancellation fixture missing")
}

func TestWalletLoanTokenBinding(t *testing.T) {
	raw := json.RawMessage(`[{"loan_token":"0x1111111111111111111111111111111111111111"}]`)
	if !matchesWalletToken(raw, common.HexToAddress("0x1111111111111111111111111111111111111111")) ||
		matchesWalletToken(raw, common.HexToAddress("0x2222222222222222222222222222222222222222")) {
		t.Fatal("wallet loan token binding did not match request")
	}
}

func TestMidnightIntentBindings(t *testing.T) {
	for _, f := range loadFixtures(t, "midnight.json") {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(f.Intent.Body, &fields); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(f.Intent.Path, "/markets/"):
			path := strings.Split(f.Intent.Path, "/")
			path[5] = "1:0x" + strings.Repeat("ff", 32)
			f.Intent.Path = strings.Join(path, "/")
		case strings.HasSuffix(f.Intent.Path, "/take"):
			fields["maker"] = json.RawMessage(`"0x1111111111111111111111111111111111111111"`)
		case strings.HasSuffix(f.Intent.Path, "/cancel"):
			fields["groups"] = json.RawMessage(`["0x` + strings.Repeat("ff", 32) + `","0x` + strings.Repeat("45", 32) + `"]`)
		case strings.HasSuffix(f.Intent.Path, "/funding/withdraw"):
			fields["funding_address"] = json.RawMessage(`"0x1111111111111111111111111111111111111111"`)
		default:
			continue
		}
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		f.Intent.Body = body
		if Verify(f.Intent, f.Calls) {
			t.Errorf("mutated intent accepted: %s", f.Intent.Path)
		}
	}
}
