// Permission cards and native diffs against the real `tenon acp`: a
// write_file allowed from the card (file written, diff documents), Stop while
// a request is pending (file unchanged, notification when the view is
// hidden, or hidden while the request is open), and the "allow with network"
// option of a sandboxed shell.
import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import type { TenonTestApi } from "../../../src/extension";
import type { Item } from "../../../src/model";
import { makeEnv } from "../env";
import { type FakeLLM, sseCall, sseText, startFakeLLM } from "../fakeLLM";
import { assert, test, waitFor } from "./harness";

type Card = Extract<Item, { kind: "permission" }>;
type Tool = Extract<Item, { kind: "tool" }>;

let api: TenonTestApi;
let fake: FakeLLM | undefined;
let file = "";
const KEY = "cle-de-test";

function cards(): Card[] {
  return api.state().items.filter((i): i is Card => i.kind === "permission");
}

function tools(): Tool[] {
  return api.state().items.filter((i): i is Tool => i.kind === "tool");
}

/** The card of a request still waiting for an answer. */
function pendingCard(): Card | undefined {
  return cards().find((c) => c.answered === undefined && !c.refused);
}

async function useEnv(script: Parameters<typeof startFakeLLM>[0], extra?: string): Promise<string> {
  await fake?.close();
  fake = await startFakeLLM(script);
  const workspace = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  assert(workspace, "the main suite runs with a workspace folder");
  Object.assign(process.env, makeEnv(fake, { projectDir: workspace, extra }).env);
  await vscode.commands.executeCommand("tenon.restart");
  await api.agentReady();
  await vscode.commands.executeCommand("tenon.newSession");
  return workspace;
}

test("permission: allow from the card writes the file; the diff opens as tenon-diff documents", async () => {
  const ext = vscode.extensions.getExtension<TenonTestApi | undefined>("tenon-code-eu.tenon");
  assert(ext, "extension not found");
  const exported = await ext.activate();
  assert(exported, "test API missing");
  api = exported;

  // Like real providers, every turn's first call is call_1.
  const workspace = await useEnv([
    sseCall("write_file", JSON.stringify({ path: "a.txt", content: "v2" })),
    sseText("written"),
    sseCall("write_file", JSON.stringify({ path: "a.txt", content: "v3" })),
    sseCall("write_file", JSON.stringify({ path: "a.txt", content: "v4" })),
    sseText("written again"),
  ]);
  file = path.join(workspace, "a.txt");
  // The old side comes from tenon, masked: the provider key never shows.
  fs.writeFileSync(file, `v1 ${KEY}\n`);
  await vscode.commands.executeCommand("tenon.openChat");
  await waitFor(() => api.panelReady(), 15_000, "the chat webview");
  const before = api.notifications().length;

  const sending = api.send("write a.txt");
  const card = await waitFor(pendingCard, 15_000, "a permission card");
  assert(/^p\d+$/.test(card.requestId), `request id ${card.requestId}`);
  assert(card.title.includes("a.txt"), `card title ${card.title}`);
  const ids = card.options.map((o) => o.optionId);
  assert(ids.includes("allow") && ids.includes("reject"), `options ${JSON.stringify(card.options)}`);
  assert(card.toolCallId?.endsWith("call_1"), `card tool call ${card.toolCallId}`);
  assert(tools().some((t) => t.id === card.toolCallId), "the card's tool item is missing");
  // The view is visible: no notification.
  assert(
    !api.notifications().slice(before).some((n) => n.kind === "info"),
    `notification while visible: ${JSON.stringify(api.notifications().slice(before))}`,
  );

  api.answer("allow");
  const stop = await sending;
  assert(stop === "end_turn", `stop reason ${stop}`);
  assert(fs.readFileSync(file, "utf8") === "v2", `file ${fs.readFileSync(file, "utf8")}`);
  const answered = cards().find((c) => c.requestId === card.requestId);
  assert(answered?.answered === "allow", `card ${JSON.stringify(answered)}`);
  const tool = tools().find((t) => t.id === card.toolCallId);
  assert(tool?.diff, `tool item without a diff: ${JSON.stringify(tool)}`);

  await api.openDiff(tool.id);
  const d = api.lastDiff();
  assert(d, "no diff opened");
  assert(d.title === "a.txt (tenon)", `title ${d.title}`);
  assert(d.left.includes("v1") && !d.left.includes(KEY), `left ${JSON.stringify(d.left)}`);
  assert(d.right === "v2", `right ${JSON.stringify(d.right)}`);
  const docs = await waitFor(
    () => {
      const ds = vscode.workspace.textDocuments.filter((t) => t.uri.scheme === "tenon-diff");
      return ds.length >= 2 ? ds : undefined;
    },
    5_000,
    "the tenon-diff documents",
  );
  const texts = docs.map((t) => t.getText());
  assert(texts.includes("v2") && texts.some((t) => t.includes("v1")), `documents ${JSON.stringify(texts)}`);
  for (const t of docs) {
    assert(!t.uri.toString().includes("v1") && !t.uri.toString().includes("v2"), `content in URI ${t.uri.toString()}`);
  }
  await vscode.commands.executeCommand("workbench.action.closeAllEditors");
}, 60_000);

test("permission: Stop while a request is pending refuses it; a hidden view gets a notification", async () => {
  await vscode.commands.executeCommand("workbench.action.closeSidebar");
  const before = api.notifications().length;
  const sending = api.send("write again");
  const card = await waitFor(pendingCard, 15_000, "a second permission card");
  const shown = await waitFor(
    () => api.notifications().slice(before).find((n) => n.kind === "info"),
    5_000,
    "a notification for the hidden view",
  );
  assert(
    JSON.stringify(shown.actions) === JSON.stringify(card.options.map((o) => o.name)),
    `notification actions ${JSON.stringify(shown.actions)} vs ${JSON.stringify(card.options)}`,
  );
  assert(shown.message.includes("a.txt"), `notification ${shown.message}`);

  await api.cancel();
  const stop = await sending;
  assert(stop === "cancelled", `stop reason ${stop}`);
  assert(fs.readFileSync(file, "utf8") === "v2", `file changed: ${fs.readFileSync(file, "utf8")}`);
  const refused = cards().find((c) => c.requestId === card.requestId);
  assert(refused?.refused === true && refused.answered === undefined, `card ${JSON.stringify(refused)}`);
  assert(!api.state().busy, "busy after Stop");
  await vscode.commands.executeCommand("tenon.openChat");
}, 30_000);

test("permission: two turns whose calls are both call_1 keep two tool items, each with its own diff", async () => {
  // Turn 1 wrote v1 → v2, turn 2 was stopped; turn 3 writes v2 → v4.
  await waitFor(() => api.panelVisible() || undefined, 10_000, "the chat view on screen");
  const before = api.notifications().length;
  const sending = api.send("write v4");
  await waitFor(pendingCard, 15_000, "a third permission card");
  assert(api.notifications().length === before, `notification while visible: ${JSON.stringify(api.notifications().slice(before))}`);
  // The view goes off screen while the request is open: the notification comes then.
  await vscode.commands.executeCommand("workbench.action.closeSidebar");
  const shown = await waitFor(
    () => api.notifications().slice(before).find((n) => n.kind === "info"),
    5_000,
    "a notification once the view is hidden",
  );
  assert(shown.message.includes("a.txt"), `notification ${shown.message}`);
  api.answer("allow");
  const stop = await sending;
  assert(stop === "end_turn", `stop reason ${stop}`);
  assert(fs.readFileSync(file, "utf8") === "v4", `file ${fs.readFileSync(file, "utf8")}`);
  const writes = tools().filter((t) => t.id.endsWith("call_1"));
  assert(new Set(writes.map((t) => t.id)).size === writes.length && writes.length === 3, `tool items ${JSON.stringify(writes)}`);
  const withDiff = writes.filter((t) => t.diff);
  assert(withDiff.length === 2, `diffs ${JSON.stringify(withDiff)}`);

  await api.openDiff(withDiff[0].id);
  const first = api.lastDiff();
  assert(first?.left.includes("v1") && first.right === "v2", `first diff ${JSON.stringify(first)}`);
  await api.openDiff(withDiff[1].id);
  const second = api.lastDiff();
  assert(second?.left === "v2" && second.right === "v4", `second diff ${JSON.stringify(second)}`);
  await vscode.commands.executeCommand("workbench.action.closeAllEditors");
  await vscode.commands.executeCommand("tenon.openChat");
}, 30_000);

test("permission: a sandboxed shell call offers allow_network", async () => {
  await useEnv(
    [sseCall("shell", JSON.stringify({ command: "echo hi" }), "call_s1"), sseText("done")],
    '\n[sandbox]\nmode = "auto"\n',
  );
  const sending = api.send("run it");
  const card = await waitFor(pendingCard, 15_000, "the shell permission card");
  const sandbox = (api.state().meta?.sandbox ?? {}) as { mechanism?: string; network?: string; confined?: boolean };
  const ids = card.options.map((o) => o.optionId);
  if (process.platform === "darwin") {
    // sandbox-exec ships with macOS: tenon confines commands, the bar says so.
    assert(sandbox.mechanism === "sandbox-exec" && sandbox.confined === true, `sandbox ${JSON.stringify(sandbox)}`);
    assert(api.status().text === "tenon · Test SAS · EU · sandboxed", `status ${api.status().text}`);
  }
  if (sandbox.confined !== true || sandbox.network === "none") {
    console.log(`  SKIP allow_network: no network-confining sandbox here (${JSON.stringify(sandbox)})`);
  } else {
    assert(ids.includes("allow_network"), `options ${JSON.stringify(card.options)} sandbox ${JSON.stringify(sandbox)}`);
    assert(ids.indexOf("allow_network") === ids.indexOf("allow") + 1, `order ${JSON.stringify(ids)}`);
  }
  api.answer("reject");
  const stop = await sending;
  assert(stop === "end_turn", `stop reason ${stop}`);
  await api.stopAgent();
  await fake?.close();
  fake = undefined;
}, 60_000);
