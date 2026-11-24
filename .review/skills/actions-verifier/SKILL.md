---
name: actions-verifier
description: Review changes to the Morpho Actions Go verifier, its deployment and ABI pins, and its fixtures.
---

# Actions verifier trust boundary

- Trace `Verify` from the original endpoint path and request body through the
  ordered `data.calls` entries. Keep verification synchronous and in memory:
  reject runtime RPC, HTTP, filesystem I/O, asynchronous work, simulation,
  and proof or signature verification in this path. These limits do not
  prohibit network calls in the opt-in live test.
- Check that unrecognized routes, chains, call kinds, targets, selectors and
  malformed ABI data fail closed. Validate known nested call structures
  rather than trusting the outer selector alone. Review changes to pinned
  per-chain deployments and ABI fragments against their protocol source.
- Bind fields the inputs establish, including token identity when the
  verified market parameters hash to the requested market ID. Where token
  identity cannot be derived, such as a vault deposit, allow at most one
  approval per required token, only to the pinned route spender, only up to
  the intent amount, and never to a pinned Morpho contract (or the intended
  vault). Never accept unlimited approvals. Do not require an approval when
  the inputs cannot show one is needed. Document what the verifier cannot
  bind rather than treating unknown selectors or targets as a supported limit.
- Check that documentation does not claim that a passing result proves
  execution, proves sender or chain, validates state-derived amounts, or
  verifies opaque payloads or signatures. Require targeted rejection tests
  for changes to a verification boundary.
- Reject committed credentials, private response payloads, real signatures,
  or personal addresses in fixtures and examples. Use sanitized or synthetic
  data; live test output must not expose secrets.
