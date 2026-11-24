# Morpho Actions Verifier for Go

Check whether the wallet calls in a prepared Morpho Actions API response match
the original request. Verification runs synchronously in memory. It does not
call the API, read chain state, or verify proofs or signatures.

## Use it

Once the module is published:

```sh
go get github.com/morpho-org/actions-verifier-go
```

```go
import (
    "encoding/json"

    "github.com/morpho-org/actions-verifier-go"
)

func matches(path string, requestBody, responseBody []byte) bool {
    var response struct {
        Data struct {
            Status string          `json:"status"`
            Calls  []verifier.Call `json:"calls"`
        } `json:"data"`
    }
    if json.Unmarshal(responseBody, &response) != nil || response.Data.Status != "prepared" {
        return false
    }
    return verifier.Verify(verifier.Intent{Path: path, Body: requestBody}, response.Data.Calls)
}
```

Keep the trusted request path and body from before you sent the request.
Use the concrete path (for example, `/v0/actions/blue/markets/1:0x…/supply`),
the original JSON body, and all of `data.calls` in the returned order. Sign
and send from the requested account on the requested chain; a transaction's
sender and chain are not encoded in these call entries.

If the API asks for signatures before preparing the calls, verify the final
calls against the initial action request, not the continuation body containing
the continuation token and signatures.

Use a Go module version tag for reproducible builds. The root import path
does not change for v0 or v1; an incompatible Go v2 would use a `/v2`
module suffix, independent of the HTTP endpoint version.

## What a result means

`true` means the calls passed the implemented structural checks: permitted
call kinds, order, targets and selectors; canonical ABI arguments; matching
market or vault and exact amounts where encoded; supported prerequisites;
and allowed native value. Known multicalls are checked one level down.
Unrecognized routes, selectors, and chains fail closed.

It does **not** establish that the transaction will succeed, that the
account has the needed balance or allowance, that price or position state
is current, that an expiry has not passed, or that state-derived share and
asset quantities or price bounds are right. An `all` request checks the
structural exit path, not the actual position size. Vault V2 exit penalty
limits in the request are not enforced by this verifier; the exit calldata
does not encode the maximum penalty. Embedded permit and
authorization signatures are not verified. API attribution with no origin
specified in the request is not authenticated.

For vault deposits, the underlying ERC-20 is not identified by the request
or the bundle arguments. If an approval is present, it must be the only
approval, grant the pinned deposit bundle either at most the requested assets
or the SDK's unlimited allowance (`maxUint256`), and not target a pinned
Morpho contract or the intended vault. An unlimited approval can outlast the
transaction, and the response supplies its token address. A passing check
cannot identify the correct underlying token or
tell whether an approval was required, so a deposit can also have no
approval. Applications that need token identity must obtain it separately.
Where the loan or collateral token is in market parameters that hash to
the requested market ID, the approval target must match that token. Blue
funding paths accept the SDK's persistent approvals to the pinned spender,
as well as the approval amount derived from the verified call.

For Midnight offer publication, the compressed payload is opaque. A passing
check does not establish the published offer terms, tree root, validity
period, or cap represented by the payload. Wallet-funded lend and repay
publications can omit the token identity from the main call. An optional
single approval must target an unpinned token and match the loan token if
the intent supplies it. It must approve Midnight as spender, either up to
the requested amount or for the SDK's unlimited allowance (`maxUint256`).
If the intent omits the loan token, the response supplies the token address;
a passing check does not establish that it is the intended loan token.
For Midnight fills, callback and ratifier data, counterparty validity, and
relative quote guards are not verified. An optional market or maker on a
group take is checked when supplied; otherwise the group is checked but the
market and maker cannot independently be identified from the request. Without
an absolute guard that binds the take direction, the verifier checks the
response's direction for internal consistency but cannot authenticate it.
Cancellation and funding withdrawal have no onchain deadline. The verifier
cannot establish that a funding address belongs to the account, or that the
funding shares in the call are the full position.

## Supported route families

| Family | Actions |
| --- | --- |
| Blue | borrow, refinance, repay, supply, supply-collateral, withdraw, withdraw-collateral |
| Vault V1 | deposit, withdraw |
| Vault V2 | deposit, force-withdraw, redeem-in-kind, withdraw |
| Midnight markets | borrow, lend, repay, supply-collateral, withdraw, withdraw-collateral |
| Midnight offer groups | take |
| Midnight offers | borrow, cancel, funding/withdraw, lend, repay, withdraw |

The per-chain pins are in [chains_blue_vault.json](chains_blue_vault.json).
A family needs its corresponding bundle address on that chain. Vault V2
force-withdraw and redeem-in-kind require a Vault Exit deployment. The
deployment addresses and ABI fragments are derived from the Morpho Actions
registries; the verifier checks the pinned selector against each ABI
signature at initialization.

Midnight deployment pins are in [chains_midnight.json](chains_midnight.json)
(chains 1, 4663, 5042, and 8453). Midnight market IDs are derived from the
ABI-encoded market and its pinned deployment, not from a Blue market hash.

```sh
go vet ./...
go test -race ./...
```

## Test against a live Actions API

The live test is off by default. Create a JSON array of intents (each with
`path` and `body`, as above) using markets, accounts and positions that can
exercise each route on your chosen chains. Set `ACTIONS_API_KEY` in your
environment from your secret store; do not put it in the intents file.

```sh
ACTIONS_API_URL=https://private.api.morpho.org \
  ACTIONS_API_CASES=/path/to/cases.json \
  go test -run '^TestLiveActions$' -count=1 -v
```

For a local Actions API use its loopback URL and omit the key if it is not
required. The test sends only preparation requests. It does not sign or
broadcast transactions, save responses, or print headers. A non-prepared
response is reported separately from a prepared response rejected by
`Verify`; an authentication failure stops further requests to that host.
