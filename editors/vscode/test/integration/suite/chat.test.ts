// Chat panel against the real `tenon acp` and a fake provider: the home
// screen until the first prompt, a prompt and its answer with its
// jurisdiction badge, the home links, Stop mid-answer, and the "tenon
// stopped" banner after a crash.
import * as vscode from "vscode";
import type { TenonTestApi } from "../../../src/extension";
import type { Item } from "../../../src/model";
import { makeEnv } from "../env";
import { type FakeLLM, slow, sseChunks, sseText, startFakeLLM } from "../fakeLLM";
import { assert, test, waitFor } from "./harness";

let api: TenonTestApi;
let fake: FakeLLM;

function thread(): { kind: string; text?: string }[] {
  // tenon's notices (thought items) depend on the machine: left out.
  return api
    .state()
    .items.filter((i): i is Extract<Item, { kind: "user" | "agent" }> => i.kind === "user" || i.kind === "agent")
    .map((i) => ({ kind: i.kind, text: i.text }));
}

test("chat: a prompt shows the user message and the streamed answer", async () => {
  const ext = vscode.extensions.getExtension<TenonTestApi | undefined>("tenon-code-eu.tenon");
  assert(ext, "extension not found");
  const exported = await ext.activate();
  assert(exported, "test API missing");
  api = exported;

  // Request 1 answers at once; every later request streams slowly (Stop test).
  fake = await startFakeLLM([sseText("hello"), slow(sseChunks(Array.from({ length: 100 }, () => "x")), 100)]);
  const workspace = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  assert(workspace, "the main suite runs with a workspace folder");
  Object.assign(process.env, makeEnv(fake, { projectDir: workspace }).env);
  await vscode.commands.executeCommand("tenon.restart");
  await vscode.commands.executeCommand("tenon.openChat");
  // The webview script loaded under the CSP and asked for the state.
  await waitFor(() => api.panelReady(), 15_000, "the chat webview to post ready");
  // First open: the home screen, until the first message.
  assert(api.homeVisible(), "the home is not shown on first open");

  const stop = await api.send("hi");
  assert(!api.homeVisible(), "the home is still shown after the first prompt");
  assert(stop === "end_turn", `stop reason ${stop}`);
  const items = thread();
  assert(
    JSON.stringify(items) === JSON.stringify([{ kind: "user", text: "hi" }, { kind: "agent", text: "hello" }]),
    `items ${JSON.stringify(api.state().items)}`,
  );
  assert(!api.state().busy, "still busy after the turn");
  assert(api.state().sessionId, "no session id");
  assert(fake.requests().length === 1, `requests ${fake.requests().length}`);
  assert(fake.requests()[0].includes('"hi"'), "prompt did not reach the provider");
  // The answer carries the badge of the provider that produced it (makeEnv: "Test SAS", eu).
  const answer = api.state().items.find((i) => i.kind === "agent");
  assert(
    answer?.kind === "agent" && answer.provider?.name === "Test SAS" && answer.provider.jurisdiction === "eu",
    `badge ${JSON.stringify(answer)}`,
  );
}, 60_000);

test("chat: home links open their constant URL; forged ids and URLs open nothing (Review Focus 5)", () => {
  const before = api.openedLinks().length;
  for (const forged of [
    { type: "openLink", id: "evil" },
    { type: "openLink", id: "https://evil.example" },
    { type: "openLink", id: "docs", url: "https://evil.example" },
    { type: "openLink", url: "https://evil.example" },
  ]) {
    api.webviewMessage(forged);
  }
  assert(api.openedLinks().length === before, `opened ${JSON.stringify(api.openedLinks())}`);
  api.webviewMessage({ type: "openLink", id: "docs" });
  api.webviewMessage({ type: "openLink", id: "bug" });
  const opened = api.openedLinks().slice(before);
  assert(
    JSON.stringify(opened) ===
      JSON.stringify(["https://tenon-code-eu.github.io/tenon/", "https://github.com/tenon-code-eu/tenon/issues/new"]),
    `opened ${JSON.stringify(opened)}`,
  );
});

test("chat: Stop during a slow answer ends the turn as cancelled", async () => {
  const sending = api.send("long");
  await waitFor(() => api.state().busy, 5_000, "busy");
  await waitFor(
    () => thread().some((i) => i.kind === "agent" && i.text?.startsWith("x")),
    10_000,
    "the slow answer to start streaming",
  );
  const start = Date.now();
  await api.cancel();
  const stop = await sending;
  assert(stop === "cancelled", `stop reason ${stop}`);
  assert(Date.now() - start < 5_000, `cancel took ${Date.now() - start} ms`);
  assert(!api.state().busy, "still busy after Stop");
  const text = thread().find((i) => i.kind === "agent" && i.text?.startsWith("x"))?.text ?? "";
  assert(text.length < 100, `the whole answer streamed (${text.length} chunks)`);
}, 30_000);

test("chat: a killed tenon shows the stopped banner; tenon.restart clears it", async () => {
  const pid = api.agentPid();
  assert(pid !== undefined, "no agent pid");
  process.kill(pid, "SIGKILL");
  const banner = await waitFor(() => api.state().banner, 5_000, "stopped banner");
  assert(banner.kind === "stopped", `banner ${JSON.stringify(banner)}`);
  assert(banner.text.includes("tenon"), `banner text ${banner.text}`);
  assert(!api.state().busy, "busy after the crash");

  await vscode.commands.executeCommand("tenon.restart");
  const init = await api.agentReady();
  assert(init.agentInfo?.name === "tenon", "agent not ready after restart");
  await waitFor(() => api.state().banner === undefined, 5_000, "banner to clear");
  // The thread comes back from session/load's replay.
  await waitFor(
    () => thread().some((i) => i.kind === "user" && i.text === "hi") && thread().some((i) => i.text === "hello"),
    10_000,
    `the thread to be replayed, got ${JSON.stringify(api.state().items)}`,
  );
  await api.stopAgent();
  await fake.close();
}, 30_000);
