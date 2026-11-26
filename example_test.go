package calldataverifier_test

import (
	"encoding/json"
	"fmt"

	"github.com/morpho-org/calldata-verifier-go"
)

func ExampleVerify() {
	intent := calldataverifier.Intent{
		Path: "/v0/actions/vaults-v1/1:0x0000000000000000000000000000000000000010/deposit",
		Body: json.RawMessage(`{"account":"0x0000000000000000000000000000000000000001","amount":{"unit":"assets","value":"1000"},"funding":{"type":"erc20"}}`),
	}
	var response struct {
		Data struct {
			Calls []calldataverifier.Call `json:"calls"`
		} `json:"data"`
	}
	// A response without prepared calls must not be sent.
	if err := json.Unmarshal([]byte(`{"data":{"calls":[]}}`), &response); err != nil {
		panic(err)
	}
	fmt.Println(calldataverifier.Verify(intent, response.Data.Calls))
	// Output: false
}

func ExampleVerify_prepared() {
	intent := calldataverifier.Intent{
		Path: "/v0/actions/vaults-v1/1:0x0000000000000000000000000000000000000002/deposit",
		Body: json.RawMessage(`{"account":"0x0000000000000000000000000000000000000001","expires_at":4000000000,"amount":{"unit":"assets","value":"1000"},"funding":{"type":"erc20"}}`),
	}
	status := "prepared"
	calls := []calldataverifier.Call{{
		ID:   "call_0",
		Kind: "vault_v1_deposit",
		To:   "0x02912516d49dE997db75B9D7858faAE59209650B",
		Data: "0x6bbba4e0" +
			"0000000000000000000000000000000000000000000000000000000000000002" +
			"00000000000000000000000000000000000000000000000000000000000003e8" +
			"0000000000000000000000000000000000000000033b2e3c9fd0803ce8000000" +
			"00000000000000000000000000000000000000000000000000000000000000e0" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"00000000000000000000000000000000000000000000000000000000ee6b2800" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000040" +
			"0000000000000000000000000000000000000000000000000000000000000000",
		Value: "0",
	}}
	fmt.Println(status == "prepared" && calldataverifier.Verify(intent, calls))
	// Output: true
}
