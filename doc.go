// Package calldataverifier checks ordered Morpho Actions API wallet calls against
// the original intent without network access or execution.
//
// VerifyCalldata checks Vault, Blue, and supported Midnight market and offer
// calls, returning typed errors for failed checks. Supply the original intent,
// funding, and fee limits independently of the response.
// Invoke the verified calls only from the intended account and chain.
// Midnight make-offer actions are unsupported.
//
// A successful check is not a simulation, state check, or signature verification.
// Verify state-dependent price, deadline and penalty limits and simulate
// independently before signing. See the [README] for the verification boundary.
//
// [README]: https://github.com/morpho-org/calldata-verifier-go#what-a-result-means
package calldataverifier
