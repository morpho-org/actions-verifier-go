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

	"github.com/ethereum/go-ethereum/common"
)

// makerParityRevision is the sdks-internal main commit (after #1290) whose make-offer suite
// generate-maker-parity.py captured. Fold the fixture into the ts-parity files
// once the pin in generate-snapshot.ts includes the released make-offer rule.
const makerParityRevision = "61a288bc9170062ac356ff01ef7785158a161fcd"

func TestParityMidnightMakers(t *testing.T) {
	runParityFixture(t, "testdata/pending-parity-makers.json", makerParityRevision, nil, midnightOfferParityIntent, nil)
}

var makerParityPin = regexp.MustCompile(`(?m)^revision = "([a-f0-9]{40})"\r?$`)

func TestMakerParityPin(t *testing.T) {
	generator, err := os.ReadFile("generate-maker-parity.py")
	if err != nil {
		t.Fatal(err)
	}
	pin := makerParityPin.FindSubmatch(generator)
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
	intent, calls := makerCase(t, "midnightMakers.test.ts:363/3")
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

// BlueBuyCallback.test.ts: the factory's CREATE2 address on every chain that has one.
// The 4217 and 42161 vectors come from midnight-sdk's getBlueBuyCallbackAddress at the maker parity revision.
func TestBlueBuyCallbackAddress(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	var salt [32]byte
	for i := range salt {
		salt[i] = 0xab
	}
	for chain, want := range map[uint64]string{
		1:      "0x8E2D6Dc011a5D0707d4c1f784f1AbE8e2F22CcBB",
		8453:   "0xA708d48561220ef7b7e7D22aAfAE691bf3B85FC0",
		4663:   "0x99671150E374a96C809c19d96407aFA8332b5A6b",
		5042:   "0x9da312d4fC1bA828E028F8ed457AA9586079dAd0",
		10:     "0x06d72550466898078962de71425Bf37C32Dd20fc",
		137:    "0x072e29040dC2185FF6783487a81777A3a0341b0D",
		480:    "0x8aE4FB714D3a90d0642E78AB749117eC3dE4D3c4",
		130:    "0x3f5Ce75AAF895D3cCC56D3A6F5c70c655d7b5f42",
		747474: "0xAC6C07525355705231944A0bB99a36E5666c2a22",
		143:    "0xe805df25F67EF7592aA7a972B418Fc6660aE78F6",
		988:    "0x8E73911e66f7ED89A2490619050D7638Bc15C057",
		29032:  "0x7A6C0CC163505B8db00AFaA0ec5b1eA401F14A16",
		4217:   "0x61b77C9D386875af7d637151CDe7e88BE94fA24B",
		42161:  "0x0f6eC061fD4f862bcAEE2878672EF357bE0E0391",
	} {
		got, ok := blueBuyCallbackAddress(defaultSnapshot.Deployments[chain], owner, salt)
		if !ok || got.Hex() != want {
			t.Errorf("chain %d: got %s, want %s", chain, got.Hex(), want)
		}
	}
	if _, ok := blueBuyCallbackAddress(defaultSnapshot.Deployments[196], owner, salt); ok {
		t.Fatal("derived a callback without a factory")
	}
}

// Go-side guards around the variable-rate funded make_lend that the backend builds.
func TestMidnightBlueFundedMakeLend(t *testing.T) {
	intent, calls := makerCase(t, "midnightMakers.test.ts:363/5")
	if intent.FundingSource == nil || intent.FundingSource.Type != "variable_rate_market" {
		t.Fatalf("unexpected backend vector %+v", intent.FundingSource)
	}
	if _, err := VerifyCalldata(intent, calls); err != nil {
		t.Fatal(err)
	}
	id := calls[len(calls)-1].ID
	t.Run("registry without a callback factory", func(t *testing.T) {
		chain := defaultSnapshot.Deployments[1]
		chain.MidnightBlueBuyCallbackFactory = ""
		_, err := verifyMidnightMake(intent, calls, chain)
		requireError[*CalldataUnsupportedError](t, err, "funding_source", &id)
	})
	t.Run("caller-registered factory", func(t *testing.T) {
		factory := defaultSnapshot.Deployments[1].MidnightBlueBuyCallbackFactory
		registry, err := NewRegistry(map[uint64]Deployment{1: {MidnightBlueBuyCallbackFactory: strings.ToLower(factory)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyCalldata(intent, calls, VerifyOptions{Registry: registry}); err != nil {
			t.Fatal(err)
		}
		if _, err := NewRegistry(map[uint64]Deployment{1: {MidnightBlueBuyCallbackFactory: otherAccount}}); err == nil {
			t.Fatal("conflicting callback factory accepted")
		}
	})
	for _, tc := range []struct {
		name, source string
		check        func(*testing.T, error, string, *string)
	}{
		{"unknown funding source", "credit_line", requireError[*CalldataUnsupportedError]},
		{"wallet funding with Blue parking", "wallet", requireError[*CalldataCallError]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := intent
			changed.FundingSource = &MidnightFundingSource{Type: tc.source, MarketID: intent.FundingSource.MarketID}
			_, err := VerifyCalldata(changed, calls)
			tc.check(t, err, "funding_source", &id)
		})
	}
	t.Run("native value above the parked amount", func(t *testing.T) {
		native, nativeCalls := makerCase(t, "midnightMakers.test.ts:1158/1")
		if _, err := VerifyCalldata(native, nativeCalls); err != nil {
			t.Fatal(err)
		}
		amount, _ := new(big.Int).SetString(native.Amount.Value, 10)
		funding := *native.Funding
		funding.MaxNativeAssets = maxUint256.String()
		native.Funding = &funding
		nativeCalls = append([]CalldataCall(nil), nativeCalls...)
		publication := &nativeCalls[len(nativeCalls)-1]
		publication.Value = new(big.Int).Add(amount, big.NewInt(1)).String()
		_, err := VerifyCalldata(native, nativeCalls)
		requireError[*CalldataAmountError](t, err, "value", &publication.ID)
	})
	t.Run("lend amount above uint128", func(t *testing.T) {
		changed := intent
		changed.Amount.Value = new(big.Int).Lsh(big.NewInt(1), 128).String()
		_, err := VerifyCalldata(changed, calls)
		requireError[*CalldataAmountError](t, err, "amount", &id)
	})
}

// An empty CollateralSupplies list is sent as an absent one, because the JSON field is omitempty.
func TestMidnightMakeBorrowEmptyCollateral(t *testing.T) {
	intent, calls := makerCase(t, "midnightMakers.test.ts:363/1")
	if intent.Action != "midnight.make_borrow" || intent.CollateralSupplies != nil {
		t.Fatalf("unexpected no-collateral borrow vector %s", intent.Action)
	}
	intent.CollateralSupplies = []MidnightCollateralTransfer{}
	if _, err := VerifyCalldata(intent, calls); err != nil {
		t.Fatal(err)
	}
	collateral, collateralCalls := makerCase(t, "midnightMakers.test.ts:389/1")
	collateral.CollateralSupplies = []MidnightCollateralTransfer{}
	id := collateralCalls[len(collateralCalls)-1].ID
	_, err := VerifyCalldata(collateral, collateralCalls)
	requireError[*CalldataCallError](t, err, "collateral_supplies", &id)
}

func TestMakerParityPinCRLF(t *testing.T) {
	generator, err := os.ReadFile("generate-maker-parity.py")
	if err != nil {
		t.Fatal(err)
	}
	crlf := bytes.ReplaceAll(generator, []byte("\n"), []byte("\r\n"))
	pin := makerParityPin.FindSubmatch(crlf)
	if len(pin) != 2 || string(pin[1]) != makerParityRevision {
		t.Fatal("make-offer parity pin not found in a CRLF checkout")
	}
}
