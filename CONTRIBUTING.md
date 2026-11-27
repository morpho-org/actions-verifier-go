# Contributing

## Run the Go checks

Use Go 1.25.1 or newer. From the root of the checkout:

```sh
go test -race ./...
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
```

Run `gofmt` on changed Go files. The Go tests replay the committed parity
fixtures and examples without requiring an API key, RPC endpoint, or JavaScript
runtime. When updating the embedded registry, refresh the README chain support
matrix. `go test -run '^TestDocumentedChainSupport$'` prints the expected rows
if the matrix is out of date.

## Replay the published TypeScript verifier

Use Node.js 26.13.1, Python 3.11+, curl, and GNU tar. From a clean checkout run:

```sh
python3 testdata/npm-parity/install.py
node --permission --allow-fs-read="$PWD/testdata" testdata/npm-parity/replay.mjs
```

Installation requires network access to fetch the locked tarballs. The
installer checks their SHA-512 digests without running package install scripts.
The replay then checks both ESM and CommonJS with no network permission.

## Refresh fixtures and the registry

Byte-for-byte regeneration requires a clean SDK source checkout at the pin in
`generate-snapshot.ts`, with its lockfile dependencies installed. The published
npm tarball alone is not enough. See
[fixture regeneration](testdata/parity-generation.md) for prerequisites and
which pins to update.

```sh
bun generate-snapshot.ts /path/to/pinned-sdk-checkout
python3 generate-parity.py /path/to/pinned-sdk-checkout --check
```

## Optional live Actions API test

`TestLiveActions` sends prepare requests for Vault actions but never signs or
submits a transaction. Provide a local JSON array with a `path` and `body` for
each request and an `intent` retained independently of the response. Use
accounts that can prepare the requested operations, and keep the file local.

- `ACTIONS_API_URL`: the base URL from the
  [Actions API reference](https://docs.morpho.org/api/actions/) or a local
  loopback URL. Non-loopback URLs must use HTTPS.
- `ACTIONS_API_CASES`: path to the local cases file.
- `ACTIONS_API_KEY`: API key for a non-loopback endpoint. Set it in your
  environment without printing or committing it; loopback does not need a key.

Set these variables and run `go test -run '^TestLiveActions$' -v`. Leave the
URL and cases unset for the default offline tests.
