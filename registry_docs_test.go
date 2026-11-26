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
	type roles [6]bool
	groups := make(map[roles][]string)
	var order []roles
	for _, id := range slices.Sorted(maps.Keys(deployments)) {
		d := deployments[id]
		row := roles{
			d.VaultBundlesV1 != "",
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
	var rows strings.Builder
	for _, row := range order {
		fmt.Fprintf(&rows, "| %s |", strings.Join(groups[row], ", "))
		for _, supported := range row {
			if supported {
				rows.WriteString(" Yes |")
			} else {
				rows.WriteString(" — |")
			}
		}
		rows.WriteByte('\n')
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, foundStart := strings.Cut(strings.ReplaceAll(string(readme), "\r\n", "\n"), "<!-- chain-support:start -->\n")
	table, _, foundEnd := strings.Cut(section, "<!-- chain-support:end -->")
	lines := strings.SplitN(table, "\n", 3)
	if !foundStart || !foundEnd || len(lines) != 3 || lines[2] != rows.String() {
		t.Fatalf("update the README chain support rows to match the embedded registry:\n%s", rows.String())
	}
}
