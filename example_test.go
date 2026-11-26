package calldataverifier_test

import (
	"errors"
	"fmt"

	"github.com/morpho-org/calldata-verifier-go"
)

func ExampleVerifyCalldata() {
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
			fmt.Println(target.Field, *target.CallID)
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
