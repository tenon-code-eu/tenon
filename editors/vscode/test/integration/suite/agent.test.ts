import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as vscode from "vscode";
import type { TenonTestApi } from "../../../src/extension";
import { makeEnv } from "../env";
import { type FakeLLM, startFakeLLM, sseText } from "../fakeLLM";
import { assert, test, waitFor } from "./harness";

const EXTENSION_ID = "tenon-code-eu.tenon";

let api: TenonTestApi;
let builtTenon: string | undefined;
let fake: FakeLLM | undefined;

function config(): vscode.WorkspaceConfiguration {
  return vscode.workspace.getConfiguration("tenon");
}

async function setPath(p: string | undefined): Promise<void> {
  await config().update("path", p, vscode.ConfigurationTarget.Global);
}

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

/** Live `… acp` children of this extension host. */
function acpChildren(): number[] {
  const out = execFileSync("ps", ["-A", "-o", "pid=,ppid=,command="], { encoding: "utf8" });
  return out
    .split("\n")
    .map((l) => /^\s*(\d+)\s+(\d+)\s+(.*)$/.exec(l))
    .filter((m): m is RegExpExecArray => m !== null && Number(m[2]) === process.pid && / acp$/.test(m[3]))
    .map((m) => Number(m[1]));
}

/** A tenon stand-in that reads stdin and never answers initialize; writes its pid to <path>.pid. */
function hungStub(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tenon-hung-"));
  const bin = path.join(dir, "tenon");
  fs.writeFileSync(
    bin,
    `#!/bin/sh
if [ "$1" = "--version" ]; then echo "tenon v0.9.0"; exit 0; fi
echo $$ > "$0.pid"
# Reads requests and never answers; stdout stays open.
while read -r line; do :; done
`,
    { mode: 0o755 },
  );
  return bin;
}

async function stubPid(bin: string): Promise<number> {
  return waitFor(
    () => {
      try {
        return Number(fs.readFileSync(`${bin}.pid`, "utf8").trim()) || undefined;
      } catch {
        return undefined;
      }
    },
    5_000,
    "stub pid file",
  );
}

test("activates and initializes tenon acp", async () => {
  fake = await startFakeLLM([sseText("hello")]);
  const workspace = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  const { env } = makeEnv(fake, workspace ? { projectDir: workspace } : {});
  // The extension host is this process: tenon inherits this environment.
  Object.assign(process.env, env);
  builtTenon = config().inspect<string>("path")?.globalValue;
  assert(builtTenon, "runner must set tenon.path to the built binary");

  const ext = vscode.extensions.getExtension<TenonTestApi | undefined>(EXTENSION_ID);
  assert(ext, `extension ${EXTENSION_ID} not found`);
  const exported = await ext.activate();
  assert(exported, "test API missing (TENON_VSCODE_TEST=1 not set?)");
  api = exported;

  const init = await api.agentReady();
  assert(init.agentCapabilities?.loadSession === true, `loadSession: ${JSON.stringify(init.agentCapabilities)}`);
  assert(init.agentInfo?.name === "tenon", `agentInfo: ${JSON.stringify(init.agentInfo)}`);
});

test("dispose ends the tenon child within 5 s", async () => {
  await api.agentReady();
  const pid = api.agentPid();
  assert(pid !== undefined, "no agent pid");
  assert(alive(pid), "agent not running before dispose");
  await api.stopAgent();
  await waitFor(() => !alive(pid), 5_000, `pid ${pid} to exit`);
});

test("missing binary shows an error with Install and Choose binary", async () => {
  const before = api.notifications().length;
  await setPath(path.join(os.tmpdir(), "tenon-does-not-exist", "tenon"));
  await vscode.commands.executeCommand("tenon.restart");
  const n = await waitFor(
    () => api.notifications().slice(before).find((x) => x.kind === "error"),
    10_000,
    "error notification",
  );
  assert(n.message.includes("tenon"), `message: ${n.message}`);
  assert(n.actions.includes("Install"), `actions: ${n.actions.join(", ")}`);
  assert(n.actions.includes("Choose binary"), `actions: ${n.actions.join(", ")}`);
  let rejected = false;
  await api.agentReady().catch(() => (rejected = true));
  assert(rejected, "agentReady should reject without a binary");
});

test("too old a binary shows a warning with Choose binary", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tenon-old-"));
  const old = path.join(dir, "tenon");
  fs.writeFileSync(old, '#!/bin/sh\necho "tenon v0.8.0"\n', { mode: 0o755 });
  const before = api.notifications().length;
  await setPath(old);
  await vscode.commands.executeCommand("tenon.restart");
  const n = await waitFor(
    () => api.notifications().slice(before).find((x) => x.kind === "warning"),
    10_000,
    "warning notification",
  );
  assert(n.message === "tenon 0.8.0 is too old (0.9.0 required)", `message: ${n.message}`);
  assert(n.actions.includes("Choose binary"), `actions: ${n.actions.join(", ")}`);
});

test("dispose kills a child that ignores stdin closing", async () => {
  // Answers initialize, then becomes `sleep`, which never reads stdin.
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tenon-stuck-"));
  const stuck = path.join(dir, "tenon");
  fs.writeFileSync(
    stuck,
    `#!/bin/sh
if [ "$1" = "--version" ]; then echo "tenon v0.9.0"; exit 0; fi
read -r line
id=$(printf '%s' "$line" | sed 's/.*"id":\\([0-9]*\\).*/\\1/')
printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{},"agentInfo":{"name":"stuck","version":"0"}}}\\n' "$id"
exec sleep 60
`,
    { mode: 0o755 },
  );
  await setPath(stuck);
  await vscode.commands.executeCommand("tenon.restart");
  const init = await api.agentReady();
  assert(init.agentInfo?.name === "stuck", `agentInfo: ${JSON.stringify(init.agentInfo)}`);
  const pid = api.agentPid();
  assert(pid !== undefined && alive(pid), "stuck agent not running");
  const start = Date.now();
  await api.stopAgent();
  await waitFor(() => !alive(pid), 1_000, `pid ${pid} to be killed`);
  const elapsed = Date.now() - start;
  assert(elapsed >= 4_500 && elapsed < 7_000, `killed after ${elapsed} ms, want about 5 s`);
}, 15_000);

test("a hung initialize times out and leaves no child", async () => {
  process.env.TENON_VSCODE_INIT_TIMEOUT_MS = "1000";
  try {
    const bin = hungStub();
    await setPath(bin);
    const start = Date.now();
    await vscode.commands.executeCommand("tenon.restart");
    let message = "";
    await api.agentReady().catch((err: unknown) => (message = err instanceof Error ? err.message : String(err)));
    assert(message.includes("initialize"), `agentReady should reject on timeout, got "${message}"`);
    assert(Date.now() - start < 5_000, `took ${Date.now() - start} ms`);
    const pid = await stubPid(bin);
    assert(!alive(pid), `hung child ${pid} still alive`);
  } finally {
    delete process.env.TENON_VSCODE_INIT_TIMEOUT_MS;
  }
}, 15_000);

test("stop during a hung initialize returns at once and kills the child", async () => {
  const bin = hungStub();
  await setPath(bin);
  const restarting = vscode.commands.executeCommand("tenon.restart");
  const pid = await stubPid(bin);
  assert(alive(pid), "hung child not running");
  const start = Date.now();
  await api.stopAgent();
  await restarting;
  assert(Date.now() - start < 3_000, `stop took ${Date.now() - start} ms`);
  await waitFor(() => !alive(pid), 1_000, `pid ${pid} to exit`);
}, 15_000);

test("restart with the built binary initializes again", async () => {
  await setPath(builtTenon);
  await vscode.commands.executeCommand("tenon.restart");
  const init = await api.agentReady();
  assert(init.agentInfo?.name === "tenon", "agent not ready after restart");
});

test("concurrent restarts leave exactly one child", async () => {
  await api.agentReady();
  await Promise.all([
    vscode.commands.executeCommand("tenon.restart"),
    vscode.commands.executeCommand("tenon.restart"),
    vscode.commands.executeCommand("tenon.restart"),
  ]);
  await api.agentReady();
  const pid = api.agentPid();
  const children = acpChildren();
  assert(children.length === 1 && children[0] === pid, `children ${children.join(", ")}, agent pid ${pid}`);
}, 30_000);

test("after a crash, agentReady starts a new agent", async () => {
  await api.agentReady();
  const pid = api.agentPid();
  assert(pid !== undefined, "no agent pid");
  process.kill(pid, "SIGKILL");
  await waitFor(() => api.agentPid() === undefined, 5_000, "exit to be noticed");
  const init = await api.agentReady();
  assert(init.agentInfo?.name === "tenon", "no agent after crash");
  const again = api.agentPid();
  assert(again !== undefined && again !== pid && alive(again), `pid after crash: ${again}`);
  await api.stopAgent();
  assert(acpChildren().length === 0, `children left: ${acpChildren().join(", ")}`);
  await fake?.close();
}, 30_000);
