import argparse
import base64
import collections
import json
import os
import re
import subprocess
import tempfile
from pathlib import Path

root = Path(__file__).resolve().parent
fixture_dir = root / 'testdata'
owner = {
    "actionProof": "proof",
    "blue": "blue",
    "blueSdk": "blue",
    "midnight": "midnight",
    "midnightSdk": "midnight",
    "midnightOffers": "offers",
    "midnightOffersSdk": "offers",
    "vaultSdk": "vault",
    "verifyCalldata": "vault",
}
js_only = {
    ("actionProof", 395): "The Go proof fields are byte slices; JS arrays are a distinct runtime type.",
    ("actionProof", 410): "JavaScript realm identity does not apply to Go byte slices.",
    ("actionProof", 428): "JS byte-view branding and Symbol.toStringTag are not Go input types.",
    ("actionProof", 452): "Array-like objects cannot be passed as a Go slice of calls. The signed-response variant is replayed separately.",
    ("actionProof", 626): "The Go proof body is a field on a value struct, not a JavaScript getter.",
    ("actionProof", 643): "The JS test uses a mocked SHA call to mutate the caller's buffer mid-verification. Go uses synchronous stdlib SHA; mutating the caller's bytes concurrently would be a data race. Go clones the body before hashing and parsing.",
    ("verifyCalldata", 53): "Go requires a string action in a typed intent; JavaScript coercion does not apply.",
    ("verifyCalldata", 948): "TypeScript compile-time union and required-property assertions do not apply to Go's struct API.",
    ("verifyCalldata", 958): "The JavaScript ABI subpath import restriction does not apply to the Go module.",
}
mapped = {
    **{("checks", i): "TestCheckNumericBounds" for i in [26, 39]},
    **{("checks", i): "TestApprovalAndCanonicalEncoding" for i in [62, 71, 85, 100, 116]},
    **{("actionProof", i): "TestActionProofBinding" for i in [347, 358, 484, 545, 558, 566, 577, 668, 684, 697]},
    **{("actionProof", i): "TestActionProofVectors" for i in [494, 528]},
    ("verifyCalldata", 40): "TestVaultVectors",
    ("verifyCalldata", 86): "TestCalldataCallJSON",
    ("blue", 32): "TestBlueVectors",
    ("midnight", 53): "TestMidnightMarketVectors",
}
json_type_errors = {
    ("verifyCalldata", 96, "calls"): "",
    ("verifyCalldata", 142, "chain_id"): "chain_id",
    ("verifyCalldata", 228, "max_fee_bps"): "max_fee_bps",
    ("verifyCalldata", 914, "market"): "market",
    ("blue", 143, "market"): "market",
    ("midnightOffers", 351, "groups"): "groups",
    ("actionProof", 291, "proof.exchange"): "RequestID",
}

def requires_type_rejection(source, line, intent, calls, proof, error):
    key = (source, line, error["field"])
    if key not in json_type_errors:
        return None
    field = json_type_errors[key]
    mismatch = (
        (field == "" and calls is not None and not isinstance(calls, list)) or
        (field in ("chain_id", "max_fee_bps") and isinstance(intent[field], float)) or
        (field == "market" and isinstance(intent["market"], int)) or
        (field == "groups" and any(isinstance(g, int) for g in (intent.get("groups") or []))) or
        (field == "RequestID" and isinstance(proof["response"]["requestId"], int))
    )
    return field if mismatch else None

def arg(value):
    if isinstance(value, dict):
        if "$bigint" in value:
            return value["$bigint"]
        return {k.lower(): arg(v) for k, v in value.items()}
    if isinstance(value, list):
        return [arg(v) for v in value]
    if isinstance(value, int) and not isinstance(value, bool):
        return str(value)
    if isinstance(value, str) and value.startswith("0x"):
        return value.lower()
    return value

def summary(call):
    return dict(call=call["call"], functionName=call["functionName"],
                args=arg(call.get("args", [])),
                calls=[summary(c) for c in call.get("calls", [])])

def as_bytes(x):
    if isinstance(x, dict):
        x = x.get("$bytes", x.get("data"))
    return base64.b64encode(bytes(x)).decode() if isinstance(x, list) else x

def entries(capture):
    for raw in capture.read_text().splitlines():
        data = json.loads(raw)
        source = data["file"].split(".")[0]
        if (source, data["line"]) in js_only or (source, data["line"]) in mapped:
            continue
        intent, calls, *options = data["input"]
        v = dict(intent=intent, calls=calls)
        if data.get("error"):
            v["error"] = data["error"]
        else:
            v["decoded"] = [summary(call) for call in data["out"]]
        if options and options[0].get("proof") is not None:
            proof = options[0]["proof"]
            for side in ("response", "request"):
                proof[side]["body"] = as_bytes(proof[side].get("body"))
            if isinstance(proof.get("now"), dict):
                proof["now"] = proof["now"]["$number"]
            v["proof"] = proof
        if data.get("error"):
            field = requires_type_rejection(source, data["line"], intent, calls, v.get("proof"), data["error"])
            if field is not None:
                v["jsonTypeError"] = field
            if (source, data["line"]) == ("verifyCalldata", 142) and isinstance(intent["chain_id"], int):
                v["hasCause"] = True
        yield owner[source], f'{data["file"]}:{data["line"]}', v

def main():
    cli = argparse.ArgumentParser()
    cli.add_argument("checkout", type=Path)
    cli.add_argument("--check", action="store_true")
    args = cli.parse_args()
    checkout = args.checkout.resolve()
    git = lambda *a: subprocess.check_output(["git", "-C", checkout, *a], text=True).strip()
    revision = re.search(r'^const revision = "([a-f0-9]{40})";$', (root / "generate-snapshot.ts").read_text(), re.M)[1]
    if git("rev-parse", "HEAD") != revision or git("status", "--porcelain", "--untracked-files=no"):
        cli.error(f"expected a clean SDK checkout at {revision}")
    with tempfile.TemporaryDirectory(prefix="calldata-parity-") as work:
        capture = Path(work) / "capture.ndjson"
        subprocess.run(
            ["node", checkout / "node_modules/vitest/vitest.mjs", "run",
             "--config", root / "capture-parity.mjs", "--configLoader", "runner"],
            cwd=checkout, check=True,
            env={**os.environ, "PARITY_SDK_CHECKOUT": str(checkout), "PARITY_CAPTURE": str(capture)},
        )
        vectors = list(entries(capture))
    grouped = collections.defaultdict(lambda: collections.defaultdict(dict))
    for family, name, vector in vectors:
        compact = json.dumps(vector, separators=(",", ":"), sort_keys=True)
        grouped[family][name][compact] = vector
    stale = []
    for family, test_file in {
        "blue": "blue_test.go", "midnight": "midnight_test.go",
        "offers": "midnight_offers_test.go", "proof": "action_proof_test.go",
        "vault": "parity_test.go",
    }.items():
        if not (root / test_file).is_file():
            continue
        path = fixture_dir / f"ts-parity-{family}.json"
        declarations = grouped[family]
        if not declarations:
            raise SystemExit(f"No verifier cases captured for {path.name}")
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
                for name, cases in sorted(declarations.items(), key=lambda row: (row[0].split(":")[0], int(row[0].split(":")[1])))
                for n, vector in enumerate(cases.values(), 1)]
        output = (
            '{\n  "revision": "' + revision + '",\n  "strings": [\n'
            + ",\n".join("    " + json.dumps(s) for s in strings)
            + '\n  ],\n  "cases": [\n'
            + ",\n".join("    " + json.dumps(row, separators=(",", ":"), sort_keys=True) for row in rows)
            + '\n  ]\n}\n'
        )
        # Keep private hostnames out of the published fixtures.
        data = re.sub(r"(?i)(?<![\w.-])(?:[a-z0-9-]+\.)+morpho\.dev(?![\w.-])",
                      "api.example.com", output).encode("utf-8")
        if args.check:
            if not path.is_file() or path.read_bytes() != data:
                stale.append(path.name)
        else:
            path.write_bytes(data)
        print(f"{path.name}: {len(declarations)} declarations, {len(rows)} cases")
    if stale:
        raise SystemExit("Stale parity fixtures: " + ", ".join(stale))

if __name__ == "__main__":
    main()
