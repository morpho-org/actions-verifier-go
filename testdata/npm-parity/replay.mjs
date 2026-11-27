import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { isDeepStrictEqual } from "node:util";

const require = createRequire(import.meta.url);
const read = (name) => JSON.parse(readFileSync(new URL(`../${name}`, import.meta.url), "utf8"));
const families = ["vault", "blue", "midnight", "offers", "proof"];
const staticFamilies = ["vault", "blue", "midnight", "midnight-offers"];
const intentJSONVectors = read("intent-json-vectors.json");
const defaultJWKS = read("action-proof-jwks.json");
const proofVectors = read("action-proof-vectors.json").proof_vectors;
const vaultIntent = {
  action: "vault_v2.deposit",
  chain_id: 8453,
  account: "0x545b6Cc751F0C47C202694cf835db2E3C071400A",
  asset: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
  vault: "0xbeef0e0834849aCC03f0089F01f4F1Eeb06873C9",
  amount: { unit: "assets", value: "1000000" },
  funding: { type: "erc20" },
  slippage_bps: 50,
  max_fee_bps: 0,
};

function normalize(value) {
  if (typeof value === "bigint" || typeof value === "number") return String(value);
  if (typeof value === "string" && value.startsWith("0x")) return value.toLowerCase();
  if (Array.isArray(value)) return value.map(normalize);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key.toLowerCase(), normalize(item)]));
  }
  return value;
}

function callSummary({ call, functionName, args = [], calls = [] }) {
  return { call, functionName, args: normalize(args), calls: calls.map(callSummary) };
}

function base64(value) {
  return typeof value === "string" ? Uint8Array.from(Buffer.from(value, "base64")) : value;
}

function errorSummary(error) {
  return { type: error.constructor.name, field: error.field, ...(error.callId === undefined ? {} : { callID: error.callId }) };
}

function parityCase(raw, strings) {
  const entry = JSON.parse(JSON.stringify(raw), (_, value) => {
    const match = typeof value === "string" && /^\$p(\d+)$/.exec(value);
    if (!match) return value;
    assert.ok(Number(match[1]) < strings.length, "invalid pooled string index");
    return strings[Number(match[1])];
  });
  if (entry.proof != null) {
    for (const side of ["request", "response"]) {
      entry.proof[side].body = base64(entry.proof[side].body);
    }
    if (typeof entry.proof.now === "string") entry.proof.now = Number(entry.proof.now);
  }
  return entry;
}

function sharedProof({ now, jwks, request, response }) {
  const headers = Object.fromEntries(Object.entries(response.headers).map(([key, value]) => [key.toLowerCase(), value]));
  return {
    header: headers["morpho-action-proof"],
    jwks: jwks ?? defaultJWKS,
    now,
    request: {
      method: request.method,
      path: request.path,
      contentType: request.content_type,
      body: base64(request.body_base64),
    },
    response: {
      status: response.status,
      contentType: headers["content-type"],
      requestId: headers["x-request-id"],
      body: base64(response.body_base64),
    },
  };
}

function replay(label, verify) {
  assert.equal(typeof verify, "function");
  let parityCount = 0;
  let staticCount = 0;
  let mismatches = 0;
  function check(matches, file, index) {
    if (matches) return;
    mismatches++;
    console.error(`${label}: mismatch in ${file}, case ${index + 1}`);
  }
  for (const family of families) {
    const { cases, strings } = read(`ts-parity-${family}.json`);
    assert.ok(cases.length > 0);
    parityCount += cases.length;
    for (const [index, raw] of cases.entries()) {
      const { intent, calls, proof, decoded, error: expected, hasCause } = parityCase(raw, strings);
      let matches;
      try {
        matches = isDeepStrictEqual(verify(intent, calls, proof == null ? undefined : { proof }).map(callSummary), decoded);
      } catch (error) {
        matches = isDeepStrictEqual(errorSummary(error), expected) && (!hasCause || error.cause !== undefined);
      }
      check(matches, `ts-parity-${family}.json`, index);
    }
  }
  for (const family of staticFamilies) {
    const vectors = Object.values(read(`${family}-vectors.json`).cases);
    assert.ok(vectors.length > 0);
    staticCount += vectors.length;
    for (const [index, { intent, calls }] of vectors.entries()) {
      let matches;
      try {
        const decoded = verify(intent, calls);
        matches = isDeepStrictEqual(decoded.map(({ call }) => call), calls) &&
          decoded.every(({ functionName, args }) =>
            typeof functionName === "string" && functionName.length > 0 &&
            (family !== "vault" || (Array.isArray(args) && args.length > 0)));
        if (matches && intent.action === "midnight.cancel_offers" &&
            (intent.groups.length > 1 || decoded[0]?.functionName === "multicall")) {
          matches = decoded[0]?.calls?.length === intent.groups.length;
        }
      } catch {
        matches = false;
      }
      check(matches, `${family}-vectors.json`, index);
    }
  }
  for (const [index, vector] of intentJSONVectors.entries()) {
    const { intent, calls } = read(`${vector.family}-vectors.json`).cases[vector.base];
    for (const [key, renamed] of Object.entries(vector.rename ?? {})) {
      intent[renamed] = intent[key];
      delete intent[key];
    }
    Object.assign(intent, vector.set);
    let matches;
    try {
      const decoded = verify(intent, calls);
      matches = vector.error === undefined && isDeepStrictEqual(decoded.map(({ call }) => call), calls);
    } catch (error) {
      matches = isDeepStrictEqual(errorSummary(error), vector.error);
    }
    check(matches, "intent-json-vectors.json", index);
  }
  assert.ok(proofVectors.length > 0);
  for (const [index, vector] of proofVectors.entries()) {
    const proof = sharedProof(vector);
    const { accepted, execute_calls: execute } = vector.expected;
    const calls = accepted ? JSON.parse(Buffer.from(proof.response.body).toString("utf8")).data?.calls ?? [] : [];
    let matches;
    try {
      const result = verify(vaultIntent, calls, { proof });
      matches = execute && isDeepStrictEqual(result.map(({ call }) => call), calls);
    } catch (error) {
      matches = !execute && error.constructor.name === "CalldataProofError" &&
        (accepted ? error.field === "proof.decision" && error.callId === undefined
                  : !["proof.calls", "proof.decision"].includes(error.field));
    }
    check(matches, "action-proof-vectors.json", index);
  }
  console.log(`${label}: ${parityCount} parity, ${staticCount} shared static, ${proofVectors.length} shared proof, ${intentJSONVectors.length} shared intent JSON; ${mismatches} mismatches`);
  return mismatches;
}

const esm = await import("@morpho-org/calldata-verifier");
const cjs = require("@morpho-org/calldata-verifier");
if (replay("ESM", esm.verifyCalldata) + replay("CJS", cjs.verifyCalldata) !== 0) process.exitCode = 1;
