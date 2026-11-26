# Changelog

## v0.1.0 (planned)

- First Go release, matching
  [`@morpho-org/calldata-verifier@0.1.0`](https://www.npmjs.com/package/@morpho-org/calldata-verifier/v/0.1.0).
  It verifies supported Vault, Blue, and Midnight calldata offline, with
  optional ES256 Action Proof verification.
- Known limits: Vault native funding checks a budget but does not require a
  nonzero value. Midnight `make_*` actions are unsupported. Static
  verification is not a simulation or a check of current chain state. See
  [known limitations](README.md#known-limitations).

## Versioning policy

Go follows the matched TypeScript version for coordinated releases when that
version is available as a new, higher Go tag. Go-only fixes bump the Go patch
version without changing the TypeScript baseline. If versions have diverged,
choose the next appropriate Go SemVer rather than reusing or moving a tag.
Tags are immutable, and each Go release records its matched TypeScript version.
