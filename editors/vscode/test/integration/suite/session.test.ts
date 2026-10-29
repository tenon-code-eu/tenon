// The Session view and the palette against the real `tenon acp`: after a
// write turn the view lists the file, the sends and the gauge; a click on
// the file (openFileDiff) opens its diff; a forged openFileDiff opens nothing;
// tenon.palette is registered.
import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import type { TenonTestApi } from "../../../src/extension";
import type { Item } from "../../../src/model";
import { makeEnv } from "../env";
import { type FakeLLM, sseCall, sseText, startFakeLLM } from "../fakeLLM";
import { assert, test, waitFor } from "./harness";

type Card = Extract<Item, { kind: "permission" }>;

let api: TenonTestApi;
let fake: FakeLLM | undefined;
let workspace = "";

test("session view: after a write turn it lists the file, the send and the gauge; a click opens the diff", async () => {
  const ext = vscode.extensions.getExtension<TenonTestApi | undefined>("tenon-code-eu.tenon");
  assert(ext, "extension not found");
  const exported = await ext.activate();
  assert(exported, "test API missing");
  api = exported;

  fake = await startFakeLLM([sseCall("write_file", JSON.stringify({ path: "sv.txt", content: "new\n" })), sseText("written")]);
  const ws = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  assert(ws, "the main suite runs with a workspace folder");
  workspace = ws;
  fs.rmSync(path.join(workspace, "sv.txt"), { force: true });
  Object.assign(process.env, makeEnv(fake, { projectDir: workspace, extra: '\n[sandbox]\nmode = "off"\n' }).env);
  await vscode.commands.executeCommand("tenon.restart");
  await api.agentReady();
  await vscode.commands.executeCommand("tenon.newSession");
  await vscode.commands.executeCommand("tenon.openChat");
  await waitFor(() => api.panelReady(), 15_000, "the chat webview");
  await vscode.commands.executeCommand("tenon.session.focus");
  await waitFor(() => api.sessionViewReady(), 15_000, "the Session view webview");

  const sending = api.send("write sv.txt");
  await waitFor(
    () => api.state().items.find((i): i is Card => i.kind === "permission" && i.answered === undefined && !i.refused),
    15_000,
    "the permission card",
  );
  api.answer("allow");
  assert((await sending) === "end_turn", "the write turn did not end");
  assert(fs.readFileSync(path.join(workspace, "sv.txt"), "utf8") === "new\n", "sv.txt not written");

  const stats = await waitFor(
    () => {
      const s = api.sessionViewStats();
      return s && s.files.length > 0 && s.sends.length > 0 ? s : undefined;
    },
    5_000,
    "the Session view stats",
  );
  assert(stats.title === "write sv.txt", `title ${stats.title}`);
  assert(stats.files.length === 1 && stats.files[0].path === "sv.txt" && stats.files[0].op === "A", `files ${JSON.stringify(stats.files)}`);
  const send = stats.sends[0];
  assert(send.provider === "Test SAS" && send.jurisdiction === "eu" && !send.alert && send.n === 2, `sends ${JSON.stringify(stats.sends)}`);
  assert(typeof stats.used === "number" && typeof stats.size === "number" && stats.size > 0, `gauge ${stats.used}/${stats.size}`);
  assert(stats.sandboxed === false, `sandboxed ${stats.sandboxed}`);

  // A forged message opens nothing.
  const before = JSON.stringify(api.lastDiff());
  api.sessionViewMessage({ type: "openFileDiff", toolCallId: "no-such-call" });
  api.sessionViewMessage({ type: "openFileDiff", toolCallId: stats.files[0].toolCallId, path: "/etc/passwd" });
  await new Promise((r) => setTimeout(r, 300));
  assert(JSON.stringify(api.lastDiff()) === before, "a forged openFileDiff opened a diff");

  api.sessionViewMessage({ type: "openFileDiff", toolCallId: stats.files[0].toolCallId });
  const d = await waitFor(() => (api.lastDiff()?.title === "sv.txt (tenon)" ? api.lastDiff() : undefined), 5_000, "the diff");
  assert(d.left === "" && d.right === "new\n", `diff ${JSON.stringify(d)}`);
  await vscode.commands.executeCommand("workbench.action.closeAllEditors");
}, 60_000);

test("palette: tenon.palette is registered", async () => {
  const commands = await vscode.commands.getCommands(true);
  assert(commands.includes("tenon.palette"), "tenon.palette missing");
  await api.stopAgent();
  await fake?.close();
  fake = undefined;
  fs.rmSync(path.join(workspace, "sv.txt"), { force: true });
}, 15_000);
