package calldataverifier

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// makerParityRevision is the sdks-internal#1288 commit whose make-offer suite
// generate-maker-parity.py captured. Fold the fixture into the ts-parity files
// once the pin in generate-snapshot.ts includes the released make-offer rule.
const makerParityRevision = "48d5157959f2c3127dd4d5822bc8f4d2e3fe6f07"

func TestParityMidnightMakers(t *testing.T) {
	runParityFixture(t, "testdata/pending-parity-makers.json", makerParityRevision, nil, midnightOfferParityIntent, nil)
}

func TestMakerParityPin(t *testing.T) {
	generator, err := os.ReadFile("generate-maker-parity.py")
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`(?m)^revision = "([a-f0-9]{40})"
?$`).FindSubmatch(generator)
	if len(pin) != 2 || string(pin[1]) != makerParityRevision {
		t.Fatal("make-offer parity generator and Go pins differ")
	}
}

func makerCase(t *testing.T, name string) (MidnightOfferManagementIntent, []CalldataCall) {
	t.Helper()
	data, err := os.ReadFile("testdata/pending-parity-makers.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Strings []json.RawMessage
		Cases   []json.RawMessage
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, raw := range fixture.Cases {
		expanded := parityReference.ReplaceAllFunc(raw, func(ref []byte) []byte {
			i, _ := strconv.Atoi(string(ref[3 : len(ref)-1]))
			return fixture.Strings[i]
		})
		var vector struct {
			Name   string
			Intent MidnightOfferManagementIntent
			Calls  []CalldataCall
		}
		if err := json.Unmarshal(expanded, &vector); err != nil {
			t.Fatal(err)
		}
		if vector.Name == name {
			return vector.Intent, vector.Calls
		}
	}
	t.Fatalf("missing make-offer case %s", name)
	return MidnightOfferManagementIntent{}, nil
}

// Readable counterparts of the midnightMakers.test.ts cases on the attestor's
// golden multi-market price lend.
func TestMidnightMakeLend(t *testing.T) {
	intent, calls := makerCase(t, "midnightMakers.test.ts:300/3")
	if intent.Action != "midnight.make_lend" || len(calls) != 3 {
		t.Fatalf("unexpected golden vector %s with %d calls", intent.Action, len(calls))
	}
	decoded, err := VerifyCalldata(intent, calls)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 3 || decoded[2].FunctionName != "midnightBundlesV2CancelAndMake" {
		t.Fatalf("unexpected decoded calls: %+v", decoded)
	}
	publication := calls[2]
	id := publication.ID
	withMake := func(edit func([]any)) []CalldataCall {
		changed := append([]CalldataCall(nil), calls...)
		changed[2] = changeArgs(t, "midnightBundle", publication, edit)
		return changed
	}
	payload := func(edit func([]byte) []byte) []CalldataCall {
		return withMake(func(args []any) { args[14] = edit(bytes.Clone(args[14].([]byte))) })
	}

	t.Run("tampered root", func(t *testing.T) {
		bad := withMake(func(args []any) {
			root := args[6].([32]byte)
			root[0] ^= 1
			args[6] = root
		})
		_, err := VerifyCalldata(intent, bad)
		requireError[*CalldataCallError](t, err, "root", &id)
	})
	t.Run("mismatched amount", func(t *testing.T) {
		changed := intent
		changed.Amount.Value = "999999"
		_, err := VerifyCalldata(changed, calls)
		requireError[*CalldataAmountError](t, err, "amount", &id)
	})
	entry := func(edit func(*MidnightMakerOfferEntry)) MidnightOfferManagementIntent {
		changed := intent
		offers := *intent.Offers
		offers.Entries = append([]MidnightMakerOfferEntry(nil), offers.Entries...)
		edit(&offers.Entries[1])
		changed.Offers = &offers
		return changed
	}
	for _, tc := range []struct {
		name, field string
		edit        func(*MidnightMakerOfferEntry)
		check       func(*testing.T, error, string, *string)
	}{
		{"mismatched tick", "offers.tick", func(e *MidnightMakerOfferEntry) { e.Tick = "5002" }, requireError[*CalldataAmountError]},
		{"mismatched expiry", "offers.expires_at", func(e *MidnightMakerOfferEntry) { e.ExpiresAt-- }, requireError[*CalldataAmountError]},
		{"mismatched start", "offers.expires_at", func(e *MidnightMakerOfferEntry) { e.StartsAt++ }, requireError[*CalldataAmountError]},
		{"another market", "offers.market_id", func(e *MidnightMakerOfferEntry) { e.MarketID = intent.Offers.Entries[0].MarketID }, requireError[*CalldataTargetError]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyCalldata(entry(tc.edit), calls)
			tc.check(t, err, tc.field, &id)
		})
	}
	t.Run("another maker", func(t *testing.T) {
		changed := intent
		changed.Account = "0x2000000000000000000000000000000000000002"
		_, err := VerifyCalldata(changed, calls)
		requireError[*CalldataRecipientError](t, err, "offers.maker", &id)
	})
	t.Run("rate request on price offers", func(t *testing.T) {
		changed := intent
		offers := *intent.Offers
		offers.Type = "rate"
		changed.Offers = &offers
		_, err := VerifyCalldata(changed, calls)
		requireError[*CalldataTargetError](t, err, "ratifier", &id)
	})
	t.Run("unknown deployment ratifier", func(t *testing.T) {
		registry, err := NewRegistry(map[uint64]Deployment{1: {}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyCalldata(intent, calls, VerifyOptions{Registry: registry}); err != nil {
			t.Fatal(err)
		}
		chain := defaultSnapshot.Deployments[1]
		chain.PriceRatifierV1 = ""
		_, err = verifyMidnightMake(intent, calls, chain)
		requireError[*CalldataTargetError](t, err, "ratifier", &id)
	})
	t.Run("zero deadline", func(t *testing.T) {
		_, err := VerifyCalldata(intent, withMake(func(args []any) { args[15] = new(big.Int) }))
		requireError[*CalldataAmountError](t, err, "deadline", &id)
	})
	t.Run("attribution suffix", func(t *testing.T) {
		_, err := VerifyCalldata(intent, payload(func(p []byte) []byte { return append(p, 0) }))
		requireError[*CalldataDecodeError](t, err, "payload", &id)
	})
	t.Run("gzip checksum", func(t *testing.T) {
		_, err := VerifyCalldata(intent, payload(func(p []byte) []byte {
			p[len(p)-8] ^= 1
			return p
		}))
		requireError[*CalldataDecodeError](t, err, "payload", &id)
	})
	t.Run("approval to the bundle", func(t *testing.T) {
		bad := append([]CalldataCall(nil), calls...)
		bad[0].Data = calls[0].Data[:34] + strings.ToLower(publication.To[2:]) + calls[0].Data[74:]
		approval := bad[0].ID
		_, err := VerifyCalldata(intent, bad)
		requireError[*CalldataTargetError](t, err, "approval.spender", &approval)
	})
}
