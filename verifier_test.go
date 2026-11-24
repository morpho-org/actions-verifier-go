package verifier

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type fixture struct {
	Intent Intent `json:"intent"`
	Calls  []Call `json:"calls"`
}

func loadFixtures(t *testing.T, file string) []fixture {
	t.Helper()
	data, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixture
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty fixture file")
	}
	return cases
}

func TestBlueAndVaultRoutes(t *testing.T) {
	for _, f := range loadFixtures(t, "blue_vault.json") {
		t.Run(f.Calls[len(f.Calls)-1].Kind, func(t *testing.T) {
			if !Verify(f.Intent, f.Calls) {
				t.Fatal("valid response rejected")
			}
			for _, mutate := range []struct {
				name string
				edit func(*Call)
			}{
				{"target", func(c *Call) { c.To = "0x0000000000000000000000000000000000000100" }},
				{"selector", func(c *Call) { c.Data = "0xdeadbeef" + c.Data[10:] }},
				{"value", func(c *Call) { c.Value = "1" }},
				{"kind", func(c *Call) { c.Kind = "approval" }},
				{"trailing-word", func(c *Call) { c.Data += strings.Repeat("00", 32) }},
			} {
				t.Run(mutate.name, func(t *testing.T) {
					calls := append([]Call(nil), f.Calls...)
					mutate.edit(&calls[len(calls)-1])
					if Verify(f.Intent, calls) {
						t.Fatal("mutated response accepted")
					}
				})
			}
		})
	}
}

func TestVaultDepositApproval(t *testing.T) {
	for _, f := range loadFixtures(t, "blue_vault.json") {
		if !strings.HasSuffix(f.Intent.Path, "/deposit") {
			continue
		}
		approval, main := f.Calls[0], f.Calls[1]
		unlimited := approval
		unlimited.Data = approval.Data[:74] + strings.Repeat("ff", 32)
		t.Run(main.Kind+"/unlimited", func(t *testing.T) {
			if !Verify(f.Intent, []Call{unlimited, main}) {
				t.Fatal("SDK max approval rejected")
			}
		})
		wrongSpender := unlimited
		wrongSpender.Data = unlimited.Data[:10] + strings.Repeat("0", 64) + unlimited.Data[74:]
		pinnedToken := unlimited
		pinnedToken.To = main.To
		overIntent := unlimited
		overIntent.Data = unlimited.Data[:136] + "fe"
		for _, tt := range []struct {
			name  string
			calls []Call
		}{
			{"reversed", []Call{main, approval}},
			{"duplicate", []Call{approval, {ID: "call_2", Kind: approval.Kind, To: approval.To, Data: approval.Data, Value: approval.Value}, main}},
			{"pinned-token", []Call{{ID: approval.ID, Kind: approval.Kind, To: main.To, Data: approval.Data, Value: approval.Value}, main}},
			{"unlimited-wrong-spender", []Call{wrongSpender, main}},
			{"unlimited-pinned-token", []Call{pinnedToken, main}},
			{"over-intent", []Call{overIntent, main}},
		} {
			t.Run(main.Kind+"/"+tt.name, func(t *testing.T) {
				if Verify(f.Intent, tt.calls) {
					t.Fatal("invalid approvals accepted")
				}
			})
		}
	}
}

func TestVaultForceRedeemReceiver(t *testing.T) {
	for _, f := range loadFixtures(t, "blue_vault.json") {
		if f.Calls[0].Kind != "vault_v2_force_redeem" {
			continue
		}
		f.Intent.Body = json.RawMessage(strings.Replace(string(f.Intent.Body), "0000000000000000000000000000000000000001", "0000000000000000000000000000000000000009", 1))
		if Verify(f.Intent, f.Calls) {
			t.Fatal("wrong account accepted")
		}
	}
}

func TestCanonicalMulticallOffsets(t *testing.T) {
	method := methods["multicall"]
	args, err := method.Inputs.Pack([][]byte{{1}, {2}})
	if err != nil {
		t.Fatal(err)
	}
	if !canonicalBytesArray(args) {
		t.Fatal("canonical offsets rejected")
	}
	args[95]++
	call := Call{Data: "0x" + hex.EncodeToString(method.ID) + hex.EncodeToString(args)}
	if _, ok := decode(method, call, "", false); ok {
		t.Fatal("noncanonical offset accepted")
	}
}

func TestBlueRepayReferenceCalls(t *testing.T) {
	for _, f := range loadFixtures(t, "blue_repay.json") {
		if !Verify(f.Intent, f.Calls) {
			t.Fatalf("reference repayment rejected: %s", f.Intent.Body)
		}
		f.Calls[0].To = "0x0000000000000000000000000000000000000100"
		if Verify(f.Intent, f.Calls) {
			t.Fatal("wrong approval token accepted")
		}
	}
}
