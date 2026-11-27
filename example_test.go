package calldataverifier_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/morpho-org/calldata-verifier-go"
)

func ExampleVerifyCalldata() {
	// Keep this from the original prepare request, before receiving any calls.
	body := []byte(`{"account":"0x1000000000000000000000000000000000000001","amount":{"unit":"assets","value":"1000000"},"funding":{"type":"erc20"}}`)
	request := exampleJSON[depositRequest](body)
	intent := calldataverifier.VaultCalldataIntent{
		CommonIntent: calldataverifier.CommonIntent{
			Action: "vault_v2.deposit", ChainID: 1, Account: request.Account, MaxFeeBPS: 0,
		},
		Vault:  "0x2000000000000000000000000000000000000002",
		Asset:  "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", // Independently known underlying.
		Amount: request.Amount, Funding: request.Funding,
	}
	// In an integration these are data.calls from the prepared response, in order.
	fixture := exampleJSON[struct {
		Cases map[string]struct {
			Calls []calldataverifier.CalldataCall
		}
	}](exampleFile("vault-vectors.json"))
	verified, err := calldataverifier.VerifyCalldata(intent, fixture.Cases["v2Deposit"].Calls)
	if err != nil {
		fmt.Println(err)
		return // Do not sign.
	}
	for _, call := range verified {
		fmt.Println(call.Call.ID, call.FunctionName)
	}
	// Output: call_0 vaultBundlesV1Deposit
}

func ExampleVerifyCalldata_typedError() {
	intent := calldataverifier.VaultCalldataIntent{
		CommonIntent: calldataverifier.CommonIntent{
			Action: "vault_v1.deposit", ChainID: 1,
			Account: "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf", MaxFeeBPS: 0,
		},
		Vault:   "0x2000000000000000000000000000000000000002",
		Asset:   "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		Amount:  calldataverifier.Amount{Unit: "assets", Value: "1000000"},
		Funding: &calldataverifier.Funding{Type: "erc20"},
	}
	calls := []calldataverifier.CalldataCall{{
		ID: "call_0", Kind: "vaultV1Deposit", Value: "0",
		To: "0x3000000000000000000000000000000000000003",
	}}
	if _, err := calldataverifier.VerifyCalldata(intent, calls); err != nil {
		var target *calldataverifier.CalldataTargetError
		if errors.As(err, &target) {
			if target.CallID != nil {
				fmt.Println(target.Field, *target.CallID)
			} else {
				fmt.Println(target.Field)
			}
		}
		return // Do not sign.
	}
	// Output: to call_0
}

func ExampleVerifyCalldata_blue() {
	intent := calldataverifier.BlueCalldataIntent{
		CommonIntent: calldataverifier.CommonIntent{
			Action: "blue.repay", ChainID: 1,
			Account: "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf",
		},
		Market:  "0x0000000000000000000000000000000000000000000000000000000000000001",
		Amount:  calldataverifier.Amount{Unit: "all"},
		Funding: &calldataverifier.Funding{Type: "erc20"},
	}
	// Pass the ordered calls from the prepared response, not an empty slice.
	_, err := calldataverifier.VerifyCalldata(intent, []calldataverifier.CalldataCall{})
	var callError *calldataverifier.CalldataCallError
	if errors.As(err, &callError) {
		fmt.Println(callError.Field)
	}
	// Output: calls
}

func ExampleVerifyCalldata_midnight() {
	intent := calldataverifier.MidnightMarketIntent{
		CommonIntent: calldataverifier.CommonIntent{
			Action: "midnight.lend", ChainID: 8453,
			Account: "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf",
		},
		Market:  "0x0000000000000000000000000000000000000000000000000000000000000001",
		Amount:  calldataverifier.Amount{Unit: "units", Value: "1000000"},
		Guard:   &calldataverifier.MidnightGuard{Type: "max_assets", Value: "1000000"},
		Funding: &calldataverifier.Funding{Type: "erc20"},
	}
	// Use the calls returned for this request rather than an empty slice.
	_, err := calldataverifier.VerifyCalldata(intent, []calldataverifier.CalldataCall{})
	var callError *calldataverifier.CalldataCallError
	if errors.As(err, &callError) {
		fmt.Println(callError.Field)
	}
	// Output: calls
}

func ExampleVerifyCalldata_unsupportedOffer() {
	intent := calldataverifier.MidnightTakeIntent{
		CommonIntent: calldataverifier.CommonIntent{
			Action: "midnight.take_lend", ChainID: 8453,
			Account: "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf",
		},
		Amount: calldataverifier.Amount{Unit: "assets", Value: "1000000"},
		// Without the request's market ID, a group does not identify the offers taken.
		Group: "0x0000000000000000000000000000000000000000000000000000000000000001",
	}
	calls := []calldataverifier.CalldataCall{{
		ID: "take", Kind: "midnightTake", Value: "0",
		To: "0x78edB268D228529715744211E11A5d50fd2BDbA4",
	}}
	_, err := calldataverifier.VerifyCalldata(intent, calls)
	var unsupported *calldataverifier.CalldataUnsupportedError
	if errors.As(err, &unsupported) {
		fmt.Println(unsupported.Field)
	}
	// Output: market_id
}

func ExampleVerifyCalldata_actionProof() {
	// The fixture provides the exact HTTP exchange and a fixed verification time.
	req, res, header, now := exampleProofExchange()
	request := exampleJSON[depositRequest](req.Body)
	intent := calldataverifier.VaultCalldataIntent{
		CommonIntent: calldataverifier.CommonIntent{
			Action: "vault_v2.deposit", ChainID: 8453, Account: request.Account, MaxFeeBPS: 0,
		},
		Vault:  "0xbeef0e0834849aCC03f0089F01f4F1Eeb06873C9", // Original request target.
		Asset:  "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", // Independently known underlying.
		Amount: request.Amount, Funding: request.Funding, SlippageBPS: request.SlippageBPS,
	}
	response := exampleJSON[struct {
		Data struct {
			Calls []calldataverifier.CalldataCall
		}
	}](res.Body)
	// Use your independently trusted issuer JWKS and current Unix time in production.
	jwks := exampleFile("action-proof-jwks.json")
	verified, err := calldataverifier.VerifyCalldata(intent, response.Data.Calls, calldataverifier.VerifyOptions{
		Proof: &calldataverifier.ActionProofInput{
			Header: header, JWKS: jwks, Now: &now, Request: req, Response: res,
		},
	})
	if err != nil {
		fmt.Println(err)
		return // Do not sign or retry without proof.
	}
	for _, call := range verified {
		fmt.Println(call.Call.ID, call.FunctionName)
	}
	// Output:
	// call_0 approve
	// call_1 vaultBundlesV1Deposit
}

type depositRequest struct {
	Account     string
	Amount      calldataverifier.Amount
	Funding     *calldataverifier.Funding
	SlippageBPS *int `json:"slippage_bps"`
}

func exampleFile(name string) []byte {
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	return data
}

func exampleJSON[T any](data []byte) T {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		panic(err)
	}
	return value
}

func exampleProofExchange() (calldataverifier.ActionProofRequest, calldataverifier.ActionProofResponse, string, float64) {
	document := exampleJSON[struct {
		Vectors []struct {
			Name    string
			Now     float64
			Request struct {
				Method      string
				Path        string
				ContentType string `json:"content_type"`
				Body        []byte `json:"body_base64"`
			}
			Response struct {
				Status  int
				Headers map[string]string
				Body    []byte `json:"body_base64"`
			}
		} `json:"proof_vectors"`
	}](exampleFile("action-proof-vectors.json"))
	for _, v := range document.Vectors {
		if v.Name == "approved_prepared_success" {
			return calldataverifier.ActionProofRequest{
					Method: v.Request.Method, Path: v.Request.Path, ContentType: v.Request.ContentType, Body: v.Request.Body,
				}, calldataverifier.ActionProofResponse{
					Status: v.Response.Status, ContentType: v.Response.Headers["content-type"],
					RequestID: v.Response.Headers["x-request-id"], Body: v.Response.Body,
				}, v.Response.Headers["morpho-action-proof"], v.Now
		}
	}
	panic("missing successful action proof fixture")
}
