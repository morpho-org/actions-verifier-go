# Regenerate TypeScript parity fixtures

Use a clean source checkout of `@morpho-org/calldata-verifier` at the revision
in `generate-snapshot.ts`, with its lockfile dependencies installed. From this
repository's root, run:

```sh
bun generate-snapshot.ts /path/to/pinned-sdk-checkout
```

The snapshot generator also runs `generate-parity.py`, which uses Python
3.9+ and Node.js 22+ to run the pinned Vitest suite with an in-memory capture
plugin. It updates the parity fixtures for
every action family on the current branch, including Action Proof where
present. Proofs created by the TypeScript tests use reproducible test-only
keys and signatures during capture. The plugin does not edit the SDK
checkout or require new SDK exports.

To compare regenerated parity fixtures without rewriting them, run:

```sh
python3 generate-parity.py /path/to/pinned-sdk-checkout --check
```

Use `python` instead of `python3` on Windows. Git attributes preserve the
committed bytes of the generated JSON and copied proof fixtures even when
`core.autocrlf` is enabled.

When bumping the pin, update `vectorRevision` in `verifier_test.go`, review
the line-based exclusions and JSON input adaptations in `generate-parity.py`
and `capture-parity.mjs`, and refresh the declaration inventory if present.
Then run `go test -race ./...`. CI checks that every parity fixture's revision
matches the generator and embedded snapshot and replays the fixtures offline.

To also replay the committed expectations against both module formats of the
published npm verifier, use Node.js 26.13.1, Python 3.11+, curl, and GNU tar.
From a clean checkout, run:

```sh
python3 testdata/npm-parity/install.py
node --permission --allow-fs-read="$PWD/testdata" testdata/npm-parity/replay.mjs
```

The npm check covers every captured case and shared vector. For malformed
inputs that Go rejects during JSON decoding, it checks the separately recorded
TypeScript error. CI downloads a checksum-pinned Node.js archive and removes
npm and Corepack. The installer verifies the SHA-512 integrity of every npm
tarball in the lockfile and extracts it without running package code. The
replay runs with no Node.js network permission.
Byte-for-byte regeneration requires access to the pinned source checkout, so
run the companion command with `--check` before publishing a fixture update.

## Pending make-offer parity

The pinned verifier rejects every `midnight.make_*` action, so its suite has
no make-offer cases. Until the make-offer rule from sdks-internal#1288 and
#1290 is released and the pin above moves past it, `generate-maker-parity.py`
captures `midnightMakers.test.ts` at the sdks-internal main revision in the
script into
`testdata/pending-parity-makers.json`, with the same capture plugin. Run it on
a clean checkout of that revision with its lockfile dependencies installed:

```sh
python3 generate-maker-parity.py /path/to/sdk-checkout-at-maker-revision --check
```

`TestParityMidnightMakers` replays the fixture, and `TestMakerParityPin` keeps
the script and `makerParityRevision` in step. The npm replay does not cover
it, because no published version accepts make-offer actions. When the pin
moves past the released rule, add `midnightMakers` to `generate-parity.py`,
drop the pending fixture and script, and remove the
`midnightOffers.test.ts:502` override in `TestParityMidnightOffers`.
