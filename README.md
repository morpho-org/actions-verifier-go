# Morpho Calldata Verifier for Go

Check ordered Morpho Actions API wallet calls against the original intent
before signing. `VerifyCalldata` returns decoded calls or a typed error. It is
synchronous and offline: it does not make HTTP or RPC requests, simulate, or
read chain state.

Requires Go 1.25.1 or newer.

```sh
go get github.com/morpho-org/calldata-verifier-go
```

```go
import "github.com/morpho-org/calldata-verifier-go"

// Keep this intent from your request and trusted vault data, not the response.
intent := calldataverifier.VaultCalldataIntent{
    CommonIntent: calldataverifier.CommonIntent{
        Action: "vault_v2.deposit",
        ChainID: 1,
        Account: account,
        MaxFeeBPS: 0,
    },
    Vault: vault,
    Asset: trustedUnderlyingAsset,
    Amount: calldataverifier.Amount{Unit: "assets", Value: "1000000"},
    Funding: &calldataverifier.Funding{Type: "erc20"},
}
verified, err := calldataverifier.VerifyCalldata(intent, responseCalls)
if err != nil {
    return err // Do not sign.
}
// Use verified[i].Call in order, on intent.ChainID from intent.Account.
```

See the runnable [verification examples](example_test.go) for a prepared
response, typed errors, and a signed response.

### Supported chains

The table shows which contract roles are present in the
[shipped registry](testdata/sdk-snapshot.json). It describes local verification
coverage, not whether the API can prepare a particular action or whether it
will succeed on-chain.

<!-- chain-support:start -->
| Chain IDs | Vault deposits and withdrawals | Vault bundled exits | Blue bundle actions | Midnight bundles | Midnight core calls and cancellation | Midnight funding withdrawal |
| --- | --- | --- | --- | --- | --- | --- |
| 1, 4217, 4663, 5042, 8453, 42161 | Yes | Yes | Yes | Yes | Yes | Yes |
| 10, 130, 137, 143, 196, 480, 988, 999, 29032, 747474 | Yes | Yes | Yes | — | — | Yes |
| 14, 25, 50, 56, 146, 239, 252, 714, 1135, 1329, 1672, 1868, 2741, 2818, 4326, 8217, 16661, 34443, 42220, 42793, 43111, 57073, 59144, 98866, 200901, 534352, 685689, 21000000, 123420001114 | — | — | — | — | — | Yes |
<!-- chain-support:end -->

Vault bundled exits are exact-asset force withdrawals and in-kind redemptions.
An `all` force withdrawal goes directly to the trusted Vault V2 address and
does not require a bundle deployment. Midnight funding withdrawals go directly
to Blue and do not require a Midnight deployment. A dash means that the
required role is not in the default registry.

### Supported Vault actions

| Intent | Amount | Static checks |
| --- | --- | --- |
| `vault_v1.deposit`, `vault_v2.deposit` | `assets` | Vault, underlying asset approval or permit encoding, amount, fee, and share-price ceiling shape. Native value must equal the deposit amount and fit the caller's original funding budget. |
| `vault_v1.withdraw`, `vault_v2.withdraw` | `assets`, `shares`, `all` | Vault, amount mode, share approval or permit, and fee. In `all` mode a nonzero fee must go to the intended account. |
| `vault_v2.force_withdraw` | `assets`, `all` | Exact-asset exits check the vault, amount, share-price floor shape, fee, and share approval or permit. An `all` exit checks each deallocation and the final redemption in order. |
| `vault_v2.redeem_in_kind` | `assets` | Vault, amount, share approval or permit, and one intended Blue market (`chain_id:market_hash`) with the trusted loan asset. |

Use the original requested fee and recipient in `ReferralFee` when applicable;
`MaxFeeBPS` separately caps any fee. Pass a vault's independently known
underlying token as `Asset`. For native funding, set `Funding.Type` to
`"native"` or `"auto"` and supply the original `MaxNativeAssets` in base units.
Call labels (`Kind`) do not select the rule.

### Supported Morpho Blue actions

Use `BlueCalldataIntent` with the original market ID and the original amount,
funding policy, and referral terms where applicable.

| Intent | Amount and other inputs |
| --- | --- |
| `blue.supply`, `blue.supply_collateral`, `blue.withdraw_collateral` | Exact asset amount. |
| `blue.borrow` | Exact assets to borrow; optional `CollateralAmount` to supply. |
| `blue.repay` | Assets, shares, or all; optional `CollateralAmount` to withdraw. |
| `blue.withdraw` | Assets, shares, or all. |
| `blue.refinance` | Original market and `DestinationMarket` (`chain_id:market_hash`). |

The verifier binds the expected market, amount and authorized legs to the
bundle. It checks the encoded LTV guard, authorization and approval shape,
fee caps, and reallocation structure. It does not check actual positions,
liquidity, authorization state, or state-derived share and all-mode amounts.
Blue reallocation legs with nonzero penalties are rejected.

### Supported Midnight market actions

Use `MidnightMarketIntent` with the original market ID and requested amounts.

| Intent | Inputs |
| --- | --- |
| `midnight.lend`, `midnight.borrow` | Assets or units, with an optional amount guard. Borrow can include collateral supplies. |
| `midnight.repay` | `Type: "direct"`; assets or all, with optional collateral withdrawals. |
| `midnight.market_repay` | `Type: "market"`; assets or units, with optional collateral withdrawals and an amount guard. |
| `midnight.withdraw`, `midnight.redeem` | Assets, units, or all as supported by the call form. |
| `midnight.supply_collateral`, `midnight.withdraw_collateral` | The collateral token and amount; withdrawals can use multiple core calls inside a multicall. |

The verifier binds encoded market parameters and collateral indices to the
intent, and checks owners, receivers, fees, offer fill shape, and native funding
limits. Explicit amount guards are checked; rate guards are unsupported.
Slippage against a quote, live liquidity, and actual execution amounts need
external checks or simulation.

### Supported Midnight offer actions

- Use `MidnightTakeIntent` for `midnight.take_borrow`,
  `midnight.supply_collateral_take_borrow`, and `midnight.take_lend`.
  Supply the requested market ID, group, amount, and guard. If a maker was
  requested, the verifier checks every fill against it; otherwise all fills
  must use the same maker. Reduce-only offers are unsupported for takes.
- Use `MidnightOfferManagementIntent` for `midnight.cancel_offers` and
  `midnight.withdraw_funding`. A cancellation must consume the requested groups
  in full. Funding withdrawals must be share-based Blue withdrawals from the
  requested owner and market to the intended account.
- `midnight.make_borrow`, `midnight.make_lend`, `midnight.make_repay`, and
  `midnight.make_withdraw` return `CalldataUnsupportedError`. The verifier
  does not interpret their compressed offer payloads.

The default registry and ABIs are embedded from the pinned
`@morpho-org/calldata-verifier` reference. For additional trusted deployments,
construct a `Registry` with `NewRegistry` and supply it in `VerifyOptions`.
Registry construction copies the input, does not affect other verifications,
and rejects any conflicting default address. Never populate the registry from
the response being verified.

### Intent field formats

- Pass amounts as decimal integer strings in the action's unit, not decimal
  token amounts (for example, `"1000000"` for one token with six decimals).
  Use raw asset, share, or Midnight position units as specified above. For
  `all`, omit `Value`. Fee caps in BPS use 100 basis points per 1%.
- Use the bare `0x`-prefixed market ID for a Blue source market, a Midnight
  market or take, and a Midnight funding withdrawal. Use
  `<chain_id>:<market_hash>` for a Blue refinance destination or a Vault V2
  in-kind redemption. Cancellation groups are `0x`-prefixed 32-byte values.

### What a result means

`DecodedCalldataCall` contains a copy of the original call, the ABI function
name and ordered arguments, and decoded children when applicable. ABI arguments
use Go ABI types, including `*big.Int` for uint256 and `common.Address` for
addresses. Use `errors.As` with the typed errors (for example,
`*CalldataTargetError`) and inspect `Field` and the optional `CallID`.
`Field` identifies the check that failed; `CallID` is nil for an error not tied
to a call. Do not sign when any error is returned.

### Known limitations

- As in the matching TypeScript verifier, a Vault deposit with
  `Funding.Type: "native"` does not require a nonzero transaction `Value`.
  Nonzero value is checked against the funding budget and the deposit; if your
  policy requires payment in native currency, check the value separately.
- Midnight `make_*` actions are unsupported; see
  [Midnight offer actions](#supported-midnight-offer-actions).

A successful check does not establish execution safety, prove current position
sizes or allowances, or read a vault's underlying token. Permit and
authorization signatures embedded in calldata are not cryptographically
verified. Without a proof input, the verifier does not authenticate the API
response. The verifier does not enforce
`slippage_bps`, `max_penalty_bps`, or request deadlines. Independently verify
state-derived price limits, deadlines, deallocation and penalty bounds, and
simulate before signing. Approvals can persist beyond the action.

- The verifier binds `Asset` where it appears in deposit approvals, native
  funding, or an encoded Blue market. The deposit bundle reads
  `vault.asset()` at execution; its deposit and permit arguments do not carry
  an asset address. For a direct check, read `vault.asset()` at a pinned block.
- For deposits, compare decoded argument 2 (the RAY-scaled ceiling) with an
  independently derived limit using fee-net assets and the lower of current
  and deadline-accrued share previews, rounded down. Reject argument 6 if it
  exceeds the deadline from your request or local policy.
- For an exact-asset force withdrawal, compare decoded argument 3 with an
  independent RAY-scaled floor from the original slippage tolerance, exit
  amount, signer, and deadline using pinned vault data. Account for
  deallocation penalties and fee shares minted to the signer, which reduce net
  shares burned. The bundle skips the onchain floor check at zero net burn.
  Reject argument 7 if it exceeds your deadline.
- For in-kind redemption and `all` force withdrawals, compare each relevant
  adapter's onchain `forceDeallocatePenalty(adapter)` at a pinned block with
  `max_penalty_bps * 10^14` when a cap is supplied. A zero cap requires a zero
  rate. The rate can change before execution and is not bounded by calldata.
  Separately bound deallocation or proceeds through simulation. In-kind
  redemption should bound idle assets or Blue supply shares received, not
  rely on a simulated penalty quote.

### Authenticate an Actions API response

Pass `VerifyOptions{Proof: &proof}` to verify the Actions API's ES256 proof
signature and authenticate the response before the same static checks.
Populate `ActionProofInput` with the `Morpho-Action-Proof`
header, independently trusted issuer JWKS JSON, the current Unix time in
seconds, and the exact HTTP exchange. The issuer defaults to
`https://api.morpho.org/v0/actions`.

Keep the POST method, exact request target (path and query as sent),
`Content-Type`, and raw request body. From the response keep the status code,
`Content-Type`, `X-Request-Id`, `Morpho-Action-Proof`, and the response body
after HTTP transport decompression. Parse a copy or keep the original bytes
alongside the parsed values; do not reserialize either body. In Go, pass the
exact target as `ActionProofRequest.Path` (for an outbound request,
`req.URL.RequestURI()` gives the request target).

Fetch or provision the JWKS from an independently trusted issuer source,
outside the verifier. Pass the raw JWKS JSON in `ActionProofInput.JWKS` and
the current Unix time in seconds in `Now`. Do not use a key set supplied by
the response. See [the runnable proof example](example_test.go).

Only a signed prepared response with successful simulation can proceed. The
proof must bind the intended chain and all five fields of each call in order;
the static verifier still rejects calls that do not match the original intent.
An authentication error is a `CalldataProofError`; do not fall back to the
proof-free path if your application requires proof.

### Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for tests, parity replay, fixture
regeneration, and the opt-in Actions API test. See [CHANGELOG.md](CHANGELOG.md)
for releases and their matching TypeScript versions. Report vulnerabilities
privately as described in [SECURITY.md](SECURITY.md).
