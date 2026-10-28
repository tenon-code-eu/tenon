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
  const extensions = path.join(base, "ext");
  fs.mkdirSync(workspace, { recursive: true });
  fs.mkdirSync(extensions, { recursive: true });
  // One user-data-dir per launch: VS Code would otherwise reopen the folder
  // of the first launch in the second.
  const userData = (name: string): string => {
    const dir = path.join(base, name);
    fs.mkdirSync(path.join(dir, "User"), { recursive: true });
    fs.writeFileSync(
      path.join(dir, "User", "settings.json"),
      JSON.stringify(
        {
          "tenon.path": tenon,
          "telemetry.telemetryLevel": "off",
          "update.mode": "none",
          "window.restoreWindows": "none",
        },
        null,
        2,
      ),
    );
    return dir;
  };
  const args = (dir: string): string[] => [
    "--user-data-dir",
    dir,
    "--extensions-dir",
    extensions,
    "--disable-extensions",
    "--disable-workspace-trust",
    "--skip-welcome",
    "--skip-release-notes",
    // CI containers run as root, where Chromium only starts without its sandbox.
    ...(process.platform === "linux" && process.getuid?.() === 0 ? ["--no-sandbox"] : []),
  ];

  // TENON_VSCODE_SUITE=main or nofolder runs only that launch.
  const only = process.env.TENON_VSCODE_SUITE;
  try {
    if (only !== "nofolder") {
      // Main suite: one workspace folder.
      await runTests({
        version: process.env.VSCODE_VERSION ?? "stable",
        cachePath: cacheDir,
        extensionDevelopmentPath: extensionDir,
        extensionTestsPath: path.join(__dirname, "suite", "index.js"),
        extensionTestsEnv: { TENON_VSCODE_TEST: "1" },
        launchArgs: [workspace, ...args(userData("user"))],
      });
    }
    if (only !== "main") {
      // Second launch without a folder (ruling R1, Review Focus 5).
      await runTests({
        version: process.env.VSCODE_VERSION ?? "stable",
        cachePath: cacheDir,
        extensionDevelopmentPath: extensionDir,
        extensionTestsPath: path.join(__dirname, "suite", "indexNoFolder.js"),
        extensionTestsEnv: { TENON_VSCODE_TEST: "1" },
        launchArgs: args(userData("user-nofolder")),
      });
    }
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
}

main().catch((err: unknown) => {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
});
