package calldataverifier

import (
	"encoding/hex"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func TestFunding(t *testing.T) {
	wrapped := common.Address{19: 1}
	other := common.Address{19: 2}
	for _, tc := range []struct {
		name, mode, cap, value string
		token, wrapped         common.Address
		want                   bool
	}{
		{"erc20", "erc20", "", "0", other, wrapped, true},
		{"erc20-value", "erc20", "", "1", wrapped, wrapped, false},
		{"erc20-with-cap", "erc20", "10", "0", other, wrapped, false},
		{"auto-erc20", "auto", "10", "0", other, wrapped, true},
		{"auto-native", "auto", "10", "10", wrapped, wrapped, true},
		{"native-bound-token", "native", "10", "10", wrapped, wrapped, true},
		{"native-unbound-token", "native", "10", "10", zeroAddress, wrapped, true},
		{"native-zero", "native", "10", "0", wrapped, wrapped, false},
		{"native-above-cap", "native", "9", "10", wrapped, wrapped, false},
		{"native-above-amount", "native", "11", "11", wrapped, wrapped, false},
		{"native-wrong-token", "native", "10", "10", other, wrapped, false},
		{"native-no-deployment", "native", "10", "10", zeroAddress, zeroAddress, false},
		{"native-malformed-cap", "native", "010", "10", wrapped, wrapped, false},
		{"unknown", "wallet", "", "0", wrapped, wrapped, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body request
			body.Funding.Type, body.Funding.MaxNative = tc.mode, tc.cap
			if got := funding(body, tc.value, tc.token, deployment{Wrapped: tc.wrapped}, big.NewInt(10)); got != tc.want {
				t.Fatalf("funding() = %t, want %t", got, tc.want)
			}
		})
	}
}

func repackCall(t *testing.T, method abi.Method, call Call, args map[string]any) Call {
	t.Helper()
	values := make([]any, len(method.Inputs))
	for i, input := range method.Inputs {
		values[i] = args[input.Name]
	}
	packed, err := method.Inputs.Pack(values...)
	if err != nil {
		t.Fatal(err)
	}
	call.Data = "0x" + hex.EncodeToString(method.ID) + hex.EncodeToString(packed)
	if _, ok := decode(method, call, "", true); !ok {
		t.Fatal("repacked call did not pass canonical decoding")
	}
	return call
}

func TestBlueReallocationBindings(t *testing.T) {
	var f fixture
	for _, candidate := range loadFixtures(t, "blue_vault.json") {
		if strings.HasSuffix(candidate.Intent.Path, "/borrow") {
			f = candidate
			break
		}
	}
	if !Verify(f.Intent, f.Calls) {
		t.Fatal("borrow fixture rejected")
	}
	method := methods["blueBundlesV1SupplyCollateralAndBorrow"]
	final := f.Calls[len(f.Calls)-1]
	args, ok := decode(method, final, "", true)
	if !ok {
		t.Fatal("borrow calldata did not decode")
	}
	target := reflect.ValueOf(args["marketParams"])
	sliceType := reflect.TypeOf(args["reallocations"])
	testAddress := common.Address{19: 1}
	makeRow := func(idle bool) reflect.Value {
		row := reflect.New(sliceType.Elem()).Elem()
		for _, name := range []string{"Vault", "Adapter"} {
			row.FieldByName(name).Set(reflect.ValueOf(testAddress))
		}
		row.FieldByName("Assets").Set(reflect.ValueOf(big.NewInt(1)))
		row.FieldByName("MarketParams").Set(target)
		row.FieldByName("FromIdle").SetBool(idle)
		source := row.FieldByName("SourceMarketParams")
		if !idle {
			row.FieldByName("SourceAdapter").Set(reflect.ValueOf(testAddress))
			source.Set(target)
			source.FieldByName("Oracle").Set(reflect.ValueOf(testAddress))
		}
		source.FieldByName("LoanToken").Set(target.FieldByName("LoanToken"))
		source.FieldByName("Lltv").Set(reflect.ValueOf(new(big.Int)))
		return row
	}
	for _, tc := range []struct {
		name string
		idle bool
		edit func(reflect.Value)
	}{
		{"market-source", false, nil},
		{"idle-source", true, nil},
		{"wrong-destination", false, func(row reflect.Value) {
			row.FieldByName("MarketParams").FieldByName("Oracle").Set(reflect.ValueOf(testAddress))
		}},
		{"same-market-source", false, func(row reflect.Value) { row.FieldByName("SourceMarketParams").Set(target) }},
		{"wrong-loan-token", false, func(row reflect.Value) {
			row.FieldByName("SourceMarketParams").FieldByName("LoanToken").Set(reflect.ValueOf(testAddress))
		}},
		{"missing-adapter", false, func(row reflect.Value) { row.FieldByName("Adapter").SetZero() }},
		{"missing-vault", false, func(row reflect.Value) { row.FieldByName("Vault").SetZero() }},
		{"missing-source-adapter", false, func(row reflect.Value) { row.FieldByName("SourceAdapter").SetZero() }},
		{"idle-with-adapter", true, func(row reflect.Value) { row.FieldByName("SourceAdapter").Set(reflect.ValueOf(testAddress)) }},
		{"idle-with-market", true, func(row reflect.Value) { row.FieldByName("SourceMarketParams").Set(target) }},
		{"zero-assets", false, func(row reflect.Value) { row.FieldByName("Assets").Set(reflect.ValueOf(new(big.Int))) }},
		{"nonzero-penalty", false, func(row reflect.Value) { row.FieldByName("Penalty").SetUint(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := makeRow(tc.idle)
			if tc.edit != nil {
				tc.edit(row)
			}
			args["reallocations"] = reflect.Append(reflect.MakeSlice(sliceType, 0, 1), row).Interface()
			calls := slices.Clone(f.Calls)
			calls[len(calls)-1] = repackCall(t, method, final, args)
			if got, want := Verify(f.Intent, calls), tc.edit == nil; got != want {
				t.Fatalf("Verify() = %t, want %t", got, want)
			}
		})
	}
}

func TestMidnightDeadlinesAndGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		direct bool
		value  any
		want   bool
	}{
		{"earlier-bundle", false, uint64(9), true},
		{"exact-bundle", false, uint64(10), true},
		{"late-bundle", false, uint64(11), false},
		{"missing-bundle", false, nil, false},
		{"zero-bundle", false, uint64(0), false},
		{"earlier-direct", true, uint64(9), false},
		{"exact-direct", true, uint64(10), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := midnightLimits(request{Expires: 10}, map[string]any{"deadline": tc.value}, tc.direct); got != tc.want {
				t.Fatalf("midnightLimits() = %t, want %t", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		guard, arg string
		outside    uint64
	}{
		{"max_assets", "maxBuyerAssets", 11},
		{"max_units", "maxUnits", 11},
		{"min_assets", "minSellerAssets", 9},
		{"min_units", "minUnits", 9},
	} {
		t.Run(tc.guard, func(t *testing.T) {
			var body request
			body.Guard.Type, body.Guard.Value = tc.guard, "10"
			if !midnightGuard(body, map[string]any{tc.arg: uint64(10)}) {
				t.Fatal("exact guard rejected")
			}
			if midnightGuard(body, map[string]any{tc.arg: tc.outside}) || midnightGuard(body, nil) {
				t.Fatal("out-of-bound or missing guard accepted")
			}
		})
	}
}

func TestReferralFeeBinding(t *testing.T) {
	var body request
	body.Fee.BPS, body.Fee.Recipient = 125, common.Address{19: 1}.Hex()
	for _, tc := range []struct {
		name      string
		pct       int64
		recipient common.Address
		want      bool
	}{
		{"exact", 12_500_000_000_000_000, common.Address{19: 1}, true},
		{"different-rate", 12_500_000_000_000_001, common.Address{19: 1}, false},
		{"different-recipient", 12_500_000_000_000_000, common.Address{19: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"referralFeePct": big.NewInt(tc.pct), "referralFeeRecipient": tc.recipient}
			if got := limits(body, args); got != tc.want {
				t.Fatalf("limits() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestVerifyRejectsInvalidEnvelope(t *testing.T) {
	f := loadFixtures(t, "blue_repay.json")[0]
	if !Verify(f.Intent, f.Calls) {
		t.Fatal("repay fixture rejected")
	}
	for _, tc := range []struct {
		name string
		edit func(*Intent, []Call)
	}{
		{"chain-mismatch", func(intent *Intent, _ []Call) {
			intent.Body = append(slices.Clone(intent.Body[:len(intent.Body)-1]), `,"chain_id":8453}`...)
		}},
		{"unsupported-chain", func(intent *Intent, _ []Call) {
			_, selector, _ := strings.Cut(intent.Path, ":")
			intent.Path = "/v0/actions/blue/markets/999999:" + selector
		}},
		{"noncanonical-chain", func(intent *Intent, _ []Call) {
			intent.Path = strings.Replace(intent.Path, "/markets/", "/markets/0", 1)
		}},
		{"unknown-action", func(intent *Intent, _ []Call) { intent.Path += "-unknown" }},
		{"repeated-call-id", func(_ *Intent, calls []Call) { calls[0].ID = calls[1].ID }},
		{"invalid-decimal", func(_ *Intent, calls []Call) { calls[0].Value = "00" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent, calls := f.Intent, slices.Clone(f.Calls)
			tc.edit(&intent, calls)
			if Verify(intent, calls) {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
}

func TestVerifyConcurrent(t *testing.T) {
	for _, file := range []string{"blue_vault.json", "midnight.json", "blue_repay.json"} {
		t.Run(file, func(t *testing.T) {
			fixtures := loadFixtures(t, file)
			for _, f := range fixtures {
				t.Run(f.Calls[len(f.Calls)-1].Kind, func(t *testing.T) {
					body, calls := slices.Clone(f.Intent.Body), slices.Clone(f.Calls)
					t.Run("shared", func(t *testing.T) {
						for range 4 {
							t.Run("verify", func(t *testing.T) {
								t.Parallel()
								if !Verify(f.Intent, f.Calls) {
									t.Fatal("valid response rejected")
								}
							})
						}
					})
					if !slices.Equal(f.Intent.Body, body) || !slices.Equal(f.Calls, calls) {
						t.Fatal("Verify modified shared inputs")
					}
				})
			}
		})
	}
}
