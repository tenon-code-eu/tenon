// Integration runner: builds tenon from the repository, then launches VS Code
// (downloaded by @vscode/test-electron) with the extension under development
// and tenon.path set to the fresh binary. Linux CI runs it under xvfb-run.
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { runTests } from "@vscode/test-electron";

async function main(): Promise<void> {
  // out/runTest.js lives in editors/vscode/out.
  const extensionDir = path.resolve(__dirname, "..");
  const repoRoot = path.resolve(extensionDir, "..", "..");
  const cacheDir = path.join(extensionDir, ".vscode-test");
  fs.mkdirSync(cacheDir, { recursive: true });

  const tenon = path.join(cacheDir, "tenon");
  execFileSync(process.env.GO ?? "go", ["build", "-o", tenon, "./cmd/tenon"], {
    cwd: repoRoot,
    stdio: "inherit",
    env: { ...process.env, GOFLAGS: process.env.GOFLAGS ?? "-mod=vendor" },
  });

  const base = fs.mkdtempSync(path.join(os.tmpdir(), "tenon-vsc-"));
  const workspace = path.join(base, "workspace");
  const userData = path.join(base, "user");
  const extensions = path.join(base, "ext");
  for (const d of [workspace, path.join(userData, "User"), extensions]) {
    fs.mkdirSync(d, { recursive: true });
  }
  fs.writeFileSync(
    path.join(userData, "User", "settings.json"),
    JSON.stringify({ "tenon.path": tenon, "telemetry.telemetryLevel": "off", "update.mode": "none" }, null, 2),
  );

  try {
    await runTests({
      version: process.env.VSCODE_VERSION ?? "stable",
      cachePath: cacheDir,
      extensionDevelopmentPath: extensionDir,
      extensionTestsPath: path.join(__dirname, "suite", "index.js"),
      extensionTestsEnv: { TENON_VSCODE_TEST: "1" },
      launchArgs: [
        workspace,
        "--user-data-dir",
        userData,
        "--extensions-dir",
        extensions,
        "--disable-extensions",
        "--disable-workspace-trust",
        "--skip-welcome",
        "--skip-release-notes",
      ],
    });
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
}

main().catch((err: unknown) => {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
});
