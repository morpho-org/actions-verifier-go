package calldataverifier

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Deployment contains trusted contracts indexed by their protocol role.
// A new chain requires Blue and AdaptiveCurveIRM. Empty fields leave defaults unchanged.
type Deployment struct {
	Blue               string `json:"blue"`
	AdaptiveCurveIRM   string `json:"adaptiveCurveIrm"`
	WNative            string `json:"wNative"`
	VaultBundlesV1     string `json:"vaultBundlesV1"`
	VaultExitBundlesV1 string `json:"vaultExitBundlesV1"`
	BlueBundlesV1      string `json:"blueBundlesV1"`
	Midnight           string `json:"midnight"`
	MidnightBundlesV2  string `json:"midnightBundlesV2"`
	PriceRatifierV1    string `json:"priceRatifierV1"`
	RateRatifierV1     string `json:"rateRatifierV1"`
	// MidnightBlueBuyCallbackFactory derives the BlueBuyCallback that funds variable-rate make_lend offers.
	MidnightBlueBuyCallbackFactory string `json:"midnightBlueBuyCallbackFactory"`
}

// Registry is an immutable snapshot of the default and caller-trusted deployments.
// A zero Registry uses the shipped defaults.
type Registry struct {
	chains map[uint64]Deployment
}

// NewRegistry copies additions into the default registry. Conflicting addresses
// are rejected; adding the same address with different casing is not a conflict.
func NewRegistry(additions map[uint64]Deployment) (*Registry, error) {
	chains := make(map[uint64]Deployment, len(defaultSnapshot.Deployments)+len(additions))
	for id, chain := range defaultSnapshot.Deployments {
		chains[id] = chain
	}
	for id, update := range additions {
		if id == 0 || id > 1<<53-1 {
			return nil, fmt.Errorf("invalid chain ID %d", id)
		}
		base := chains[id]
		for _, field := range []struct {
			name string
			dst  *string
			src  string
		}{
			{"blue", &base.Blue, update.Blue},
			{"adaptiveCurveIrm", &base.AdaptiveCurveIRM, update.AdaptiveCurveIRM},
			{"wNative", &base.WNative, update.WNative},
			{"vaultBundlesV1", &base.VaultBundlesV1, update.VaultBundlesV1},
			{"vaultExitBundlesV1", &base.VaultExitBundlesV1, update.VaultExitBundlesV1},
			{"blueBundlesV1", &base.BlueBundlesV1, update.BlueBundlesV1},
			{"midnight", &base.Midnight, update.Midnight},
			{"midnightBundlesV2", &base.MidnightBundlesV2, update.MidnightBundlesV2},
			{"priceRatifierV1", &base.PriceRatifierV1, update.PriceRatifierV1},
			{"rateRatifierV1", &base.RateRatifierV1, update.RateRatifierV1},
			{"midnightBlueBuyCallbackFactory", &base.MidnightBlueBuyCallbackFactory, update.MidnightBlueBuyCallbackFactory},
		} {
			if field.src == "" {
				continue
			}
			if !validAddress(field.src) || (field.src[2:] != strings.ToLower(field.src[2:]) &&
				field.src[2:] != strings.ToUpper(field.src[2:]) && field.src != common.HexToAddress(field.src).Hex()) {
				return nil, fmt.Errorf("invalid address for chain %d: %s", id, field.name)
			}
			if *field.dst != "" && !strings.EqualFold(*field.dst, field.src) {
				return nil, fmt.Errorf("conflicting default address for chain %d: %s", id, field.name)
			}
			*field.dst = common.HexToAddress(field.src).Hex()
		}
		if base.Blue == "" || base.AdaptiveCurveIRM == "" {
			return nil, fmt.Errorf("chain %d requires blue and adaptiveCurveIrm", id)
		}
		chains[id] = base
	}
	return &Registry{chains: chains}, nil
}

//go:embed testdata/sdk-snapshot.json
var snapshotJSON []byte

type sdkSnapshot struct {
	Deployments map[uint64]Deployment
	abis        map[string]abi.ABI
	permits     map[uint8]abi.Arguments
	market      abi.Arguments
}

var defaultSnapshot = loadSnapshot()

func loadSnapshot() sdkSnapshot {
	var raw struct {
		Deployments      map[uint64]Deployment
		ABIs             map[string]json.RawMessage
		PermitParameters map[uint8]json.RawMessage
		MarketParameters json.RawMessage
	}
	if err := json.Unmarshal(snapshotJSON, &raw); err != nil {
		panic(err)
	}
	parseABI := func(data []byte) abi.ABI {
		parsed, err := abi.JSON(bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		return parsed
	}
	parseArgs := func(data []byte) abi.Arguments {
		return parseABI(fmt.Appendf(nil, `[{"name":"args","type":"function","inputs":%s}]`, data)).Methods["args"].Inputs
	}
	snapshot := sdkSnapshot{
		Deployments: raw.Deployments,
		abis:        make(map[string]abi.ABI, len(raw.ABIs)),
		permits:     make(map[uint8]abi.Arguments, len(raw.PermitParameters)),
		market:      parseArgs(raw.MarketParameters),
	}
	for name, data := range raw.ABIs {
		snapshot.abis[name] = parseABI(data)
	}
	for kind, data := range raw.PermitParameters {
		snapshot.permits[kind] = parseArgs(data)
	}
	return snapshot
}
