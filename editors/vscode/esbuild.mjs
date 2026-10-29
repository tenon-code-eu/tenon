// Bundles the extension and the chat webview (dist/) and, with --tests, the
// integration runner and suites (out/). Usage: node esbuild.mjs [--production | --dev | --tests]
// --production (vscode:prepublish, so vsce never ships a dev build): minified,
// no source map, stale dist/ removed first.
import * as fs from "node:fs";
import * as path from "node:path";
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

/** `import x from "./f.svg?raw"`: the file's text, as vitest gives it. */
const raw = {
  name: "raw",
  setup(build) {
    build.onResolve({ filter: /\?raw$/ }, (args) => ({
      path: path.resolve(args.resolveDir, args.path.slice(0, -"?raw".length)),
      namespace: "raw",
    }));
    build.onLoad({ filter: /.*/, namespace: "raw" }, (args) => ({
      contents: fs.readFileSync(args.path, "utf8"),
      loader: "text",
    }));
  },
};

// The webview: one script and one stylesheet, loaded under its CSP; the logo
// (media/logo.svg) is inlined in the script.
await esbuild.build({
  bundle: true,
  platform: "browser",
  format: "iife",
  target: "es2022",
  sourcemap: dev,
  logLevel: "warning",
  minify: !dev,
  // dist/webview.js and dist/webview.css.
  entryPoints: ["webview/main.ts", "webview/style.css"],
  outdir: "dist",
  entryNames: "webview",
  plugins: [raw],
});

if (tests) {
  await esbuild.build({
    ...common,
    entryPoints: {
      runTest: "test/integration/runTest.ts",
      "suite/index": "test/integration/suite/index.ts",
      "suite/indexNoFolder": "test/integration/suite/indexNoFolder.ts",
    },
    outdir: "out",
    external: ["vscode", "@vscode/test-electron"],
  });
}
