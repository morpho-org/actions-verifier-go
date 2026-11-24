// Package verifier checks ordered Morpho Actions API wallet calls against the
// original endpoint intent without network access or execution.
//
// A true result is structural acceptance, not a simulation or a check of
// current chain state, signatures, or opaque Midnight offer terms. The caller
// must use the intended sender and chain. State-derived quantities, Vault V2
// penalty caps, and relative Midnight quote guards are not independently
// verified.
//
// Approvals can persist beyond the action, including SDK unlimited allowances.
// When an intent and its checked arguments do not identify an approval token
// (for example, a vault's underlying token), a true result does not establish
// that the response chose the intended token. See the [README] for route-specific
// guarantees and limitations.
//
// [README]: https://github.com/morpho-org/actions-verifier-go#what-a-result-means
package verifier
