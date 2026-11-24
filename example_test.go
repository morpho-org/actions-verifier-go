package verifier_test

import (
	"encoding/json"
	"fmt"

	"github.com/morpho-org/actions-verifier-go"
)

func ExampleVerify() {
	intent := verifier.Intent{
		Path: "/v0/actions/vaults-v1/1:0x0000000000000000000000000000000000000010/deposit",
		Body: json.RawMessage(`{"account":"0x0000000000000000000000000000000000000001","amount":{"unit":"assets","value":"1000"},"funding":{"type":"erc20"}}`),
	}
	var response struct {
		Data struct {
			Calls []verifier.Call `json:"calls"`
		} `json:"data"`
	}
	// A response without prepared calls must not be sent.
	if err := json.Unmarshal([]byte(`{"data":{"calls":[]}}`), &response); err != nil {
		panic(err)
	}
	fmt.Println(verifier.Verify(intent, response.Data.Calls))
	// Output: false
}
