// Bundles the extension (dist/) and, with --tests, the integration runner and
// suite (out/). Usage: node esbuild.mjs [--tests] [--dev]
import * as esbuild from "esbuild";

const tests = process.argv.includes("--tests");
const dev = tests || process.argv.includes("--dev");

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
