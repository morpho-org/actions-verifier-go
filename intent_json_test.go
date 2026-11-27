package calldataverifier

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestIntentJSONParity(t *testing.T) {
	var vectors []struct {
		Name, Family, Base string
		Rename             map[string]string
		Set                map[string]json.RawMessage
		Error              *parityError
	}
	readJSONFixture(t, "testdata/intent-json-vectors.json", &vectors)
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			var fixture struct {
				Cases map[string]struct {
					Intent map[string]json.RawMessage
					Calls  []CalldataCall
				}
			}
			readJSONFixture(t, "testdata/"+v.Family+"-vectors.json", &fixture)
			base, ok := fixture.Cases[v.Base]
			if !ok {
				t.Fatalf("missing base fixture %q", v.Base)
			}
			var intent CalldataIntent
			switch v.Family {
			case "blue":
				intent = new(BlueCalldataIntent)
			case "vault":
				intent = new(VaultCalldataIntent)
			case "midnight":
				intent = new(MidnightMarketIntent)
			case "midnight-offers":
				if v.Base == "batchCancellation" {
					intent = new(MidnightOfferManagementIntent)
				} else {
					intent = new(MidnightTakeIntent)
				}
			default:
				t.Fatalf("unknown fixture family %q", v.Family)
			}
			for name, renamed := range v.Rename {
				base.Intent[renamed] = base.Intent[name]
				delete(base.Intent, name)
			}
			for name, value := range v.Set {
				base.Intent[name] = value
			}
			raw, err := json.Marshal(base.Intent)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, intent); err != nil {
				t.Fatal(err)
			}
			decoded, err := VerifyCalldata(intent, base.Calls)
			if v.Error == nil {
				if err != nil || len(decoded) != len(base.Calls) {
					t.Fatalf("expected verified calls, got %d: %v", len(decoded), err)
				}
				for index, call := range decoded {
					if call.Call != base.Calls[index] {
						t.Fatalf("verified call %d changed", index)
					}
				}
				return
			}
			var typed interface{ location() errorDetail }
			if !errors.As(err, &typed) {
				t.Fatalf("expected %+v, got %T %v", *v.Error, err, err)
			}
			loc := typed.location()
			actual := parityError{reflect.TypeOf(typed).Elem().Name(), loc.Field, loc.CallID}
			if !reflect.DeepEqual(actual, *v.Error) {
				t.Fatalf("expected %s %s (%s), got %s %s (%s)", v.Error.Type, v.Error.Field, showID(v.Error.CallID), actual.Type, actual.Field, showID(actual.CallID))
			}
		})
	}
}

func TestIntentJSONPreservesPrefilledFields(t *testing.T) {
	vault := VaultCalldataIntent{Asset: "trusted asset", Amount: Amount{Unit: "assets", Value: "100"}}
	blue := BlueCalldataIntent{Market: "trusted market", CollateralAmount: &Amount{Unit: "assets", Value: "100"}}
	offer := MidnightOfferManagementIntent{Groups: []string{"trusted group"}}
	for _, intent := range []CalldataIntent{&vault, &blue, &offer} {
		if err := json.Unmarshal([]byte(`{"max_fee_bps":0,"amount":{"value":"200"}}`), intent); err != nil {
			t.Fatal(err)
		}
	}
	if vault.Asset != "trusted asset" || vault.Amount != (Amount{Unit: "assets", Value: "200"}) {
		t.Fatalf("lost prefilled Vault fields: %+v", vault)
	}
	if blue.Market != "trusted market" || blue.CollateralAmount == nil || blue.CollateralAmount.Value != "100" {
		t.Fatalf("lost prefilled Blue fields: %+v", blue)
	}
	if !reflect.DeepEqual(offer.Groups, []string{"trusted group"}) {
		t.Fatalf("lost prefilled Midnight offer groups: %v", offer.Groups)
	}
	if err := json.Unmarshal([]byte(`{"max_fee_bps":0,"collateral_amount":null}`), &blue); err != nil {
		t.Fatal(err)
	}
	if blue.CollateralAmount == nil || *blue.CollateralAmount != (Amount{}) {
		t.Fatalf("expected null collateral to override the prefilled value, got %+v", blue.CollateralAmount)
	}
}

func TestReferralFeeJSONRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		missing             bool
	}{
		{"missing", `{"recipient":"recipient"}`, `{"recipient":"recipient"}`, true},
		{"null", `{"bps":null,"recipient":"recipient"}`, `{"recipient":"recipient"}`, true},
		{"zero", `{"bps":0,"recipient":"recipient"}`, `{"bps":0,"recipient":"recipient"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fee ReferralFee
			if err := json.Unmarshal([]byte(tc.before), &fee); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(fee)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.after {
				t.Fatalf("expected %s, got %s", tc.after, encoded)
			}
			var decoded ReferralFee
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.missingBPS != tc.missing {
				t.Fatalf("missing bps = %t, want %t", decoded.missingBPS, tc.missing)
			}
		})
	}
}

func readJSONFixture(t *testing.T, name string, dst any) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatal(err)
	}
}
