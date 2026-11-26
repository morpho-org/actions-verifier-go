// Package calldataverifier checks ordered Morpho Actions API wallet calls against
// the original intent without network access or execution.
//
// VerifyCalldata decodes and checks Vault and Morpho Blue calls, returning typed
// errors for failed checks. Supply the original market or vault, amounts,
// funding, and fee limits independently of the response.
// Invoke the verified calls only from the intended account and chain.
//
// A successful check is not a simulation, state check, or signature verification.
// Verify state-dependent price, deadline and penalty limits and simulate
// independently before signing. See the [README] for the verification boundary.
//
// [README]: https://github.com/morpho-org/calldata-verifier-go#what-a-result-means
package calldataverifier
