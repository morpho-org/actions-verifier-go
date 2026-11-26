package calldataverifier

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type proofVector struct {
	Name    string
	Now     float64
	JWKS    json.RawMessage
	Request struct {
		Method      string
		Path        string
		ContentType string `json:"content_type"`
		Body        string `json:"body_base64"`
	}
	Response struct {
		Status  int
		Headers map[string]string
		Body    string `json:"body_base64"`
	}
	Expected struct {
		Accepted bool
		Decision string
		Execute  bool `json:"execute_calls"`
	}
}

func proofVectors(t *testing.T) ([]proofVector, json.RawMessage) {
	t.Helper()
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	revision := read("action-proof-revision.txt")
	if strings.TrimSpace(string(revision)) != vectorRevision {
		t.Fatalf("unexpected proof fixture revision: %s", revision)
	}
	var document struct {
		Vectors []proofVector `json:"proof_vectors"`
	}
	if err := json.Unmarshal(read("action-proof-vectors.json"), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Vectors) != 45 {
		t.Fatalf("got %d proof vectors, want 45", len(document.Vectors))
	}
	return document.Vectors, read("action-proof-jwks.json")
}

func (v proofVector) proof(t *testing.T, jwks json.RawMessage) ActionProofInput {
	t.Helper()
	header := func(name string) string {
		for key, value := range v.Response.Headers {
			if strings.EqualFold(key, name) {
				return value
			}
		}
		return ""
	}
	decode := func(encoded string) []byte {
		t.Helper()
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if len(v.JWKS) > 0 {
		jwks = v.JWKS
	}
	return ActionProofInput{
		Header: header("morpho-action-proof"), JWKS: jwks, Now: &v.Now,
		Request: ActionProofRequest{
			Method: v.Request.Method, Path: v.Request.Path,
			ContentType: v.Request.ContentType, Body: decode(v.Request.Body),
		},
		Response: ActionProofResponse{
			Status: v.Response.Status, ContentType: header("content-type"),
			RequestID: header("x-request-id"), Body: decode(v.Response.Body),
		},
	}
}

func proofVaultIntent() VaultCalldataIntent {
	slippage := 50
	return VaultCalldataIntent{
		CommonIntent: CommonIntent{Action: "vault_v2.deposit", ChainID: 8453, Account: "0x545b6Cc751F0C47C202694cf835db2E3C071400A"},
		Asset:        "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Vault:        "0xbeef0e0834849aCC03f0089F01f4F1Eeb06873C9",
		Amount:       Amount{Unit: "assets", Value: "1000000"},
		Funding:      &Funding{Type: "erc20"}, SlippageBPS: &slippage,
	}
}

func proofResponseCalls(t *testing.T, proof ActionProofInput) []CalldataCall {
	t.Helper()
	var result struct {
		Data struct{ Calls []CalldataCall }
	}
	if err := json.Unmarshal(proof.Response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Calls == nil {
		return []CalldataCall{}
	}
	return result.Data.Calls
}

// actionProof.test.ts: runs all 45 shared vectors through VerifyCalldata.
func TestActionProofVectors(t *testing.T) {
	vectors, jwks := proofVectors(t)
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			t.Parallel()
			proof := vector.proof(t, jwks)
			_, decision, err := verifyProofExchange(proof)
			if vector.Expected.Accepted {
				if err != nil || decision != vector.Expected.Decision {
					t.Fatalf("exchange decision = %q, err = %v", decision, err)
				}
			} else {
				var typed *CalldataProofError
				if !errors.As(err, &typed) {
					t.Fatalf("expected proof error, got %v", err)
				}
			}
			calls := []CalldataCall{}
			if vector.Expected.Accepted {
				calls = proofResponseCalls(t, proof)
			}
			result, err := VerifyCalldata(proofVaultIntent(), calls, VerifyOptions{Proof: &proof})
			switch {
			case vector.Expected.Execute:
				if err != nil || len(result) != len(calls) {
					t.Fatalf("verified %d of %d calls: %v", len(result), len(calls), err)
				}
				for i := range calls {
					if calls[i] != result[i].Call {
						t.Fatalf("call %d did not match", i)
					}
				}
			case vector.Expected.Accepted:
				requireError[*CalldataProofError](t, err, "proof.decision", nil)
			default:
				var typed *CalldataProofError
				if !errors.As(err, &typed) || typed.Field == "proof.calls" || typed.Field == "proof.decision" {
					t.Fatalf("expected invalid exchange, got %v", err)
				}
			}
		})
	}
}

// actionProof.test.ts: authenticates the ordered calls before running the static rules.
func TestActionProofBinding(t *testing.T) {
	vectors, jwks := proofVectors(t)
	index := slices.IndexFunc(vectors, func(v proofVector) bool { return v.Name == "approved_prepared_success" })
	if index < 0 {
		t.Fatal("missing approved vector")
	}
	proof := vectors[index].proof(t, jwks)
	calls := proofResponseCalls(t, proof)
	intent := proofVaultIntent()
	verify := func(intent VaultCalldataIntent, calls []CalldataCall, proof ActionProofInput) ([]DecodedCalldataCall, error) {
		return VerifyCalldata(intent, calls, VerifyOptions{Proof: &proof})
	}
	static, err := VerifyCalldata(intent, calls)
	if err != nil {
		t.Fatal(err)
	}
	got, err := verify(intent, calls, proof)
	if err != nil || !reflect.DeepEqual(got, static) {
		t.Fatalf("proof and static paths differ: %v", err)
	}
	changed := intent
	changed.Amount.Value = "1"
	_, err = verify(changed, calls, proof)
	requireError[*CalldataAmountError](t, err, "amount", &calls[len(calls)-1].ID)
	changed = intent
	changed.ChainID = 1
	_, err = verify(changed, calls, proof)
	requireError[*CalldataProofError](t, err, "proof.chain_id", nil)

	for name, change := range map[string]func(*CalldataCall){
		"id":    func(c *CalldataCall) { c.ID += "x" },
		"kind":  func(c *CalldataCall) { c.Kind += "x" },
		"to":    func(c *CalldataCall) { c.To = "invalid" },
		"data":  func(c *CalldataCall) { c.Data += "00" },
		"value": func(c *CalldataCall) { c.Value = "1" },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := slices.Clone(calls)
			change(&tampered[0])
			_, err := verify(intent, tampered, proof)
			requireError[*CalldataProofError](t, err, "proof.calls", nil)
		})
	}
	reordered := slices.Clone(calls)
	slices.Reverse(reordered)
	for _, changed := range [][]CalldataCall{reordered, calls[:1], append(slices.Clone(calls), calls[0])} {
		_, err := verify(intent, changed, proof)
		requireError[*CalldataProofError](t, err, "proof.calls", nil)
	}

	infinite := math.Inf(1)
	for _, now := range []*float64{nil, &infinite} {
		changed := proof
		changed.Now = now
		_, err := verify(intent, calls, changed)
		requireError[*CalldataProofError](t, err, "proof.now", nil)
	}
	changedProof := proof
	outOfRange := float64(8_640_000_000_001)
	changedProof.Now = &outOfRange
	_, err = verify(intent, calls, changedProof)
	requireError[*CalldataProofError](t, err, "proof.claims", nil)
	changedProof = proof
	changedProof.Response.Body = append(slices.Clone(proof.Response.Body), '\n')
	_, err = verify(intent, calls, changedProof)
	requireError[*CalldataProofError](t, err, "proof.exchange", nil)
}
