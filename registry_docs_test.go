package calldataverifier

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestDocumentedChainSupport(t *testing.T) {
	deployments := defaultSnapshot.Deployments
	type roles [7]bool
	groups := make(map[roles][]string)
	var order []roles
	for _, id := range slices.Sorted(maps.Keys(deployments)) {
		d := deployments[id]
		row := roles{
			d.VaultBundlesV1 != "",
			d.VaultBundlesV1 != "" && d.WNative != "",
			d.VaultExitBundlesV1 != "",
			d.BlueBundlesV1 != "",
			d.Midnight != "" && d.MidnightBundlesV2 != "",
			d.Midnight != "",
			d.Blue != "",
		}
		if _, ok := groups[row]; !ok {
			order = append(order, row)
		}
		groups[row] = append(groups[row], strconv.FormatUint(id, 10))
	}
	var expected strings.Builder
	expected.WriteString("| Chain IDs | Vault deposits and withdrawals | Vault native deposits | Vault bundled exits | Blue bundle actions | Midnight bundles | Midnight core calls and cancellation | Midnight funding withdrawal |\n")
	expected.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, row := range order {
		fmt.Fprintf(&expected, "| %s |", strings.Join(groups[row], ", "))
		for _, supported := range row {
			if supported {
				expected.WriteString(" Yes |")
			} else {
				expected.WriteString(" — |")
			}
		}
		expected.WriteByte('\n')
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, foundStart := strings.Cut(strings.ReplaceAll(string(readme), "\r\n", "\n"), "<!-- chain-support:start -->\n")
	table, _, foundEnd := strings.Cut(section, "<!-- chain-support:end -->")
	if !foundStart || !foundEnd || table != expected.String() {
		t.Fatalf("update the README chain support table to match the embedded registry:\n%s", expected.String())
	}
}
