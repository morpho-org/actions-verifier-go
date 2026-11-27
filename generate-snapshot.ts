import { execFileSync } from "node:child_process";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const revision = "cbb912ca2585b8590cc5fb71189a9387b8cd06ca";
const checkout = process.argv[2];
if (!checkout) throw new Error("Usage: bun generate-snapshot.ts <pinned SDK checkout>");
const root = resolve(checkout);
const git = (...args: string[]) =>
  execFileSync("git", ["-C", root, ...args], { encoding: "utf8" }).trim();
if (git("rev-parse", "HEAD") !== revision || git("status", "--porcelain", "--untracked-files=no")) {
  throw new Error(`Expected a clean SDK checkout at ${revision}`);
}
execFileSync(
  process.platform === "win32" ? "python" : "python3",
  [join(import.meta.dir, "generate-parity.py"), root],
  { stdio: "inherit" },
);
const source = (path: string) => import(pathToFileURL(join(root, path)).href);
const [registry, common, bundles, blue, midnight, { erc20Abi }] = await Promise.all([
  source("packages/morpho-ts/src/addresses.ts"),
  source("packages/morpho-ts/src/abis.ts"),
  source("packages/morpho-sdk/src/abis.ts"),
  source("packages/blue-sdk-viem/src/abis.ts"),
  source("packages/midnight-sdk/src/abis.ts"),
  source("node_modules/viem/_esm/index.js"),
]);
const deployments = Object.fromEntries(
  Object.entries(registry.addressesRegistry).map(([chain, raw]) => {
    const address = raw as (typeof registry.addressesRegistry)[number];
    return [chain, {
      blue: address.blue,
      adaptiveCurveIrm: address.adaptiveCurveIrm,
      wNative: address.wNative,
      vaultBundlesV1: address.bundles?.vaultBundlesV1,
      vaultExitBundlesV1: address.bundles?.vaultExitBundlesV1,
      blueBundlesV1: address.bundles?.blueBundlesV1,
      midnight: address.midnight,
      midnightBundlesV2: address.midnightBundlesV2,
      priceRatifierV1: address.priceRatifierV1,
      rateRatifierV1: address.rateRatifierV1,
    }];
  }),
);
const abis = {
  erc20: erc20Abi,
  vault: common.vaultV2Abi,
  vaultBundle: bundles.vaultBundlesV1Abi,
  vaultExit: bundles.vaultExitBundlesV1Abi,
  blue: blue.blueAbi,
  blueBundle: bundles.blueBundlesV1Abi,
  midnight: midnight.midnightAbi,
  midnightBundle: midnight.midnightBundlesV2Abi,
};
const snapshot = {
  source: "@morpho-org/calldata-verifier",
  revision,
  deployments,
  abis: Object.fromEntries(Object.entries(abis).map(([name, abi]) =>
    [name, abi.filter((item: { type: string }) => item.type === "function")],
  )),
  permitParameters: bundles.bundlesPermitParameters,
  marketParameters: [common.marketParamsAbi],
};
await Bun.write(join(import.meta.dir, "testdata/sdk-snapshot.json"), JSON.stringify(snapshot) + "\n");

for (const [name, exported, file = name] of [["vault", "vaultCases"], ["blue", "blueCases"], ["midnight", "midnightCases"], ["midnight-offers", "midnightOfferCases", "midnightOffers"]]) {
  const fixtures = await source(`packages/calldata-verifier/src/test/${file}.fixtures.ts`);
  await Bun.write(
    join(import.meta.dir, `testdata/${name}-vectors.json`),
    JSON.stringify({ revision, cases: fixtures[exported] }, null, 2) + "\n",
  );
}
for (const file of ["action-proof-vectors.json", "action-proof-jwks.json"]) {
  await Bun.write(
    join(import.meta.dir, "testdata", file),
    Bun.file(join(root, "packages/calldata-verifier/src/test", file)),
  );
}
await Bun.write(join(import.meta.dir, "testdata/action-proof-revision.txt"), revision + "\n");
