// Bundles the extension (dist/) and, with --tests, the integration runner and
// suite (out/). Usage: node esbuild.mjs [--production | --dev | --tests]
// --production (vscode:prepublish, so vsce never ships a dev build): minified,
// no source map, stale dist/ removed first.
import * as fs from "node:fs";
import * as esbuild from "esbuild";

const production = process.argv.includes("--production");
const tests = !production && process.argv.includes("--tests");
const dev = !production && (tests || process.argv.includes("--dev"));

if (production) {
  fs.rmSync("dist", { recursive: true, force: true });
}

const common = {
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node20",
  external: ["vscode"],
  sourcemap: dev,
  logLevel: "warning",
};

await esbuild.build({
  ...common,
  entryPoints: ["src/extension.ts"],
  outfile: "dist/extension.js",
  minify: !dev,
});

if (tests) {
  await esbuild.build({
    ...common,
    entryPoints: {
      runTest: "test/integration/runTest.ts",
      "suite/index": "test/integration/suite/index.ts",
    },
    outdir: "out",
    external: ["vscode", "@vscode/test-electron"],
  });
}
