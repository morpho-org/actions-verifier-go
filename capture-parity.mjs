import { createRequire } from "node:module";
import { globSync } from "node:fs";
import { join } from "node:path";

const { PARITY_SDK_CHECKOUT: root, PARITY_CAPTURE: capture } = process.env;
if (!root || !capture) throw new Error("Run generate-parity.py to capture verifier cases");
const parsers = globSync("node_modules/.bun/@babel+parser@*/node_modules/@babel/parser", { cwd: root });
if (parsers.length !== 1) throw new Error("Expected one Babel parser in the pinned lockfile install");
const { parse } = createRequire(join(root, "package.json"))(join(root, parsers[0]));

function instrument(source, id) {
  if (!/\/calldata-verifier\/src\/[^/]+\.test\.ts$/.test(id)) return;
  const ast = parse(source, { sourceType: "module", plugins: ["typescript"] }).program;
  const edits = [];
  let hasVerifier = false;
  function walk(n) {
    if (n == null || typeof n !== "object") return;
    if (Array.isArray(n)) return n.forEach(walk);
    if (n.type === "ImportDeclaration") {
      for (const spec of n.specifiers) {
        if (spec.imported?.name === "verifyCalldata") {
          hasVerifier = true;
          edits.push([spec.start, spec.end, "verifyCalldata as __realVerify"]);
        }
      }
    }
    if (n.type === "CallExpression" && n.arguments[0]?.type === "StringLiteral") {
      const c = n.callee;
      const test = name => name === "it" || name === "test";
      if (test(c.name) || (c.type === "CallExpression" && c.callee.property?.name === "each" && test(c.callee.object?.name))) {
        const callback = n.arguments[1];
        if (callback?.type !== "ArrowFunctionExpression") throw Error("Unhandled test callback " + id + ":" + n.loc.start.line);
        edits.push([callback.start, callback.start, "__auditLabel(" + n.loc.start.line + ", "], [callback.end, callback.end, ")"]);
      }
    }
    for (const [key, child] of Object.entries(n)) if (key !== "loc" && key !== "extra") walk(child);
  }
  walk(ast);
  if (!hasVerifier) return;
  for (const [start,end,replacement] of edits.sort((a,b) => b[0]-a[0])) source = source.slice(0,start)+replacement+source.slice(end);
  if (id.endsWith("/actionProof.test.ts")) {
    const cryptoImport = 'import { createHash, generateKeyPairSync, sign } from "node:crypto";';
    if (!source.includes(cryptoImport)) throw new Error("Review the proof fixture signing adapter");
    source = source.replace(cryptoImport, `
import { createHash, createPrivateKey, createPublicKey } from "node:crypto";
import { p256 as __parityP256 } from "@noble/curves/p256";
let __parityKeyNumber = 0;
function generateKeyPairSync(type, options) {
  if (type !== "ec" || options.namedCurve !== "prime256v1") throw new Error("Expected a P-256 test key");
  // These keys are public test fixtures. Never use them outside this capture.
  const scalar = createHash("sha256").update("calldata parity test key:" + ++__parityKeyNumber).digest();
  const point = __parityP256.getPublicKey(scalar, false);
  const encode = bytes => Buffer.from(bytes).toString("base64url");
  const privateKey = createPrivateKey({
    format: "jwk",
    key: { kty: "EC", crv: "P-256", x: encode(point.slice(1, 33)), y: encode(point.slice(33)), d: encode(scalar) },
  });
  return { privateKey, publicKey: createPublicKey(privateKey) };
}
function sign(algorithm, message, { key, dsaEncoding }) {
  if (algorithm !== "sha256" || dsaEncoding !== "ieee-p1363") throw new Error("Expected an ES256 test signature");
  const scalar = Buffer.from(key.export({ format: "jwk" }).d, "base64url");
  const digest = createHash("sha256").update(message).digest();
  return Buffer.from(__parityP256.sign(digest, scalar, { lowS: false }).toCompactRawBytes());
}
`);
  }
  return source + `
import { appendFileSync as __appendAudit } from "node:fs";
let __auditLine = 0;
function __auditLabel(line, f) {
  return (...a) => { __auditLine = line; return f(...a); };
}
function verifyCalldata(...a) {
  let out, error;
  try { out = __realVerify(...a); return out; }
  catch (e) { error = { type: e.constructor.name, field: e.field, callID: e.callId }; throw e; }
  finally {
    // Do not serialize the proof getter and mocked SHA mutation inputs.
    if (!("actionProof.test.ts" === ${JSON.stringify(id.split("/").at(-1))} && [626,643].includes(__auditLine))) {
      if (!__auditLine) throw new Error("Unlabelled verifier call");
      const item = { file: ${JSON.stringify(id.split("/").at(-1))}, line: __auditLine, input: a, out, error };
      __appendAudit(${JSON.stringify(capture)}, JSON.stringify(item, (_k, value) =>
        typeof value === "bigint" ? { $bigint: value.toString() } :
        value instanceof Uint8Array ? { $bytes: Array.from(value) } :
        typeof value === "number" && !Number.isFinite(value) ? { $number: String(value) } :
        value) + "\\n");
    }
  }
}
`;
}

export default {
  root,
  plugins: [{name: "capture-calldata-audit", enforce:"pre", transform:instrument}],
  test: {include: ["packages/calldata-verifier/src/**/*.test.ts"], environment:"node", maxWorkers:1},
};
