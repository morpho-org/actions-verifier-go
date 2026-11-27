"""Capture the pending make-offer parity fixture from sdks-internal main.

The published verifier pinned in generate-snapshot.ts rejects every
midnight.make_* action, so its suite has no make-offer cases. Until the
make-offer rule is released and the pin moves past it, this script records
midnightMakers.test.ts cases at the revision below (sdks-internal#1290).
"""
import argparse
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

sys.dont_write_bytecode = True

revision = "61a288bc9170062ac356ff01ef7785158a161fcd"
root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("generate_parity", root / "generate-parity.py")
parity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(parity)
parity.owner["midnightMakers"] = "makers"


def main():
    cli = argparse.ArgumentParser()
    cli.add_argument("checkout", type=Path)
    cli.add_argument("--check", action="store_true")
    args = cli.parse_args()
    checkout = args.checkout.resolve()
    git = lambda *a: subprocess.check_output(["git", "-C", checkout, *a], text=True).strip()
    if git("rev-parse", "HEAD") != revision or git("status", "--porcelain", "--untracked-files=no"):
        cli.error(f"expected a clean SDK checkout at {revision}")
    with tempfile.TemporaryDirectory(prefix="calldata-maker-parity-") as work:
        capture = Path(work) / "capture.ndjson"
        subprocess.run(
            ["node", checkout / "node_modules/vitest/vitest.mjs", "run",
             "--config", root / "capture-parity.mjs", "--configLoader", "runner",
             "packages/calldata-verifier/src/midnightMakers.test.ts"],
            cwd=checkout, check=True,
            env={**os.environ, "PARITY_SDK_CHECKOUT": str(checkout), "PARITY_CAPTURE": str(capture)},
        )
        cases = {}
        for _, name, vector in parity.entries(capture):
            compact = json.dumps(vector, separators=(",", ":"), sort_keys=True)
            cases.setdefault(name, {})[compact] = vector
    if not cases:
        raise SystemExit("No make-offer cases captured")
    strings = {}

    def pool(value):
        if isinstance(value, dict):
            return {k: pool(v) for k, v in value.items()}
        if isinstance(value, list):
            return [pool(v) for v in value]
        if isinstance(value, str) and len(value) >= 100:
            return f"$p{strings.setdefault(value, len(strings))}"
        return value

    rows = [dict(name=f"{name}/{n}", **pool(vector))
            for name, vectors in sorted(cases.items(), key=lambda row: int(row[0].split(":")[1]))
            for n, vector in enumerate(vectors.values(), 1)]
    output = (
        '{\n  "revision": "' + revision + '",\n  "strings": [\n'
        + ",\n".join("    " + json.dumps(s) for s in strings)
        + '\n  ],\n  "cases": [\n'
        + ",\n".join("    " + json.dumps(row, separators=(",", ":"), sort_keys=True) for row in rows)
        + '\n  ]\n}\n'
    ).encode("utf-8")
    path = root / "testdata" / "pending-parity-makers.json"
    if args.check:
        if not path.is_file() or path.read_bytes() != output:
            raise SystemExit(f"Stale parity fixture: {path.name}")
    else:
        path.write_bytes(output)
    print(f"{path.name}: {len(cases)} declarations, {len(rows)} cases")


if __name__ == "__main__":
    main()
