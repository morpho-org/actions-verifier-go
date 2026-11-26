# Morpho Calldata Verifier for Go

Check ordered Morpho Actions API wallet calls against the original intent
before signing. `VerifyCalldata` returns decoded calls or a typed error. It is
synchronous and offline: it does not make HTTP or RPC requests, simulate, or
read chain state.

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

### What a result means

`DecodedCalldataCall` contains a copy of the original call, the ABI function
name and ordered arguments, and decoded children when applicable. ABI arguments
use Go ABI types, including `*big.Int` for uint256 and `common.Address` for
addresses. Use `errors.As` with the typed errors (for example,
`*CalldataTargetError`) and inspect `Field` and the optional `CallID`.

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

Keep the original POST request target, content type, and raw body, and the
response status, content type, `X-Request-Id`, and body after HTTP transport
decompression. Do not parse and reserialize either body before passing it to
the verifier. Fetch and refresh the trusted key set outside the verifier; it
does not read the clock or make network requests.

Only a signed prepared response with successful simulation can proceed. The
proof must bind the intended chain and all five fields of each call in order;
the static verifier still rejects calls that do not match the original intent.
An authentication error is a `CalldataProofError`; do not fall back to the
proof-free path if your application requires proof.

### Development

```sh
go test -race ./...
```

The shared Vault, Blue, Midnight, and Action Proof vectors and the ABI and
deployment snapshot under `testdata/` are pinned to the source of
`@morpho-org/calldata-verifier@0.0.0`. Regenerate with
`bun generate-snapshot.ts /path/to/pinned-sdk-checkout` after installing the
pinned checkout's lockfile dependencies. Go builds and tests do not use Bun.

The snapshot command also runs `generate-parity.py` to regenerate the
test parity fixtures. See [fixture regeneration](testdata/parity-generation.md)
for the prerequisites and standalone freshness check.

To opt into `TestLiveActions`, provide a JSON array with a `path` and `body`
for each sent request and an `intent` for the independently retained verifier
input. Use Vault actions with accounts that can prepare the requested operations.
The harness never submits transactions. See the public
[Actions API reference](https://docs.morpho.org/api/actions/) for the base URL.

```sh
ACTIONS_API_URL='<base URL from the public Actions API reference>' \
ACTIONS_API_KEY="$ACTIONS_API_KEY" \
ACTIONS_API_CASES=/path/to/local-cases.json go test -run '^TestLiveActions$' -v
```

An API key is required for the hosted API, but not for loopback URLs.
