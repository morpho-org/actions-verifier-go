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
structural exit path, not the actual position size. Embedded permit and
authorization signatures are not verified. API attribution with no origin
specified in the request is not authenticated.

For vault deposits, the underlying ERC-20 is not identified by the request
or the bundle arguments. If an approval is present, it must be the only
approval, grant the pinned deposit bundle either at most the requested assets
or the SDK's unlimited allowance (`maxUint256`), and not target a pinned
Morpho contract or the intended vault. An unlimited approval can outlast the
transaction. A passing check cannot identify the correct underlying token or
tell whether an approval was required, so a deposit can also have no
approval. Applications that need token identity must obtain it separately.
Where the loan or collateral token is in market parameters that hash to
the requested market ID, the approval target must match that token.

## Supported route families

| Family | Actions |
| --- | --- |
| Blue | borrow, refinance, repay, supply, supply-collateral, withdraw, withdraw-collateral |
| Vault V1 | deposit, withdraw |
| Vault V2 | deposit, force-withdraw, redeem-in-kind, withdraw |

The per-chain pins are in [chains_blue_vault.json](chains_blue_vault.json).
A family needs its corresponding bundle address on that chain. Vault V2
force-withdraw and redeem-in-kind require a Vault Exit deployment. The
deployment addresses and ABI fragments are derived from the Morpho Actions
registries; the verifier checks the pinned selector against each ABI
signature at initialization.

```sh
go vet ./...
go test -race ./...
```
