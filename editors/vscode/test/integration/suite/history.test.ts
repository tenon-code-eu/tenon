// Session history, project skills, editor context and the status bar against
// the real `tenon acp`: two sessions listed newest first and one reloaded, a
// `/fix` skill of the project expanded by tenon, a selection attached to a
// prompt, and the status bar built from the session's `_meta.tenon`.
import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import type { TenonTestApi } from "../../../src/extension";
import type { Item } from "../../../src/model";
import { makeEnv } from "../env";
import { type FakeLLM, sseText, startFakeLLM } from "../fakeLLM";
import { assert, test, waitFor } from "./harness";

let api: TenonTestApi;
let fake: FakeLLM;
let workspace = "";
let sessionA = "";
const SKILL_BODY = "FIX-SKILL-BODY: find the bug first.";

function thread(): { kind: string; text: string }[] {
  return api
    .state()
    .items.filter((i): i is Extract<Item, { kind: "user" | "agent" }> => i.kind === "user" || i.kind === "agent")
    .map((i) => ({ kind: i.kind, text: i.text }));
}

test("history: two sessions are listed newest first, with their titles", async () => {
  const ext = vscode.extensions.getExtension<TenonTestApi | undefined>("tenon-code-eu.tenon");
  assert(ext, "extension not found");
  const exported = await ext.activate();
  assert(exported, "test API missing");
  api = exported;

  fake = await startFakeLLM([
    sseText("one"),
    sseText("two"),
    sseText("three"),
    sseText("fixed"),
    sseText("explained"),
  ]);
  const ws = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  assert(ws, "the main suite runs with a workspace folder");
  workspace = ws;
  // A project skill: no approval step in tenon, the catalog reads it at session start.
  const skillDir = path.join(workspace, ".tenon", "skills", "fix");
  fs.mkdirSync(skillDir, { recursive: true });
  fs.writeFileSync(path.join(skillDir, "SKILL.md"), `---\nname: fix\ndescription: Fixes the bug.\n---\n${SKILL_BODY}\n`);
  Object.assign(process.env, makeEnv(fake, { projectDir: workspace }).env);
  await vscode.commands.executeCommand("tenon.restart");
  await api.agentReady();
  await vscode.commands.executeCommand("tenon.newSession");
  await vscode.commands.executeCommand("tenon.openChat");
  await waitFor(() => api.panelReady(), 15_000, "the chat webview");

  assert((await api.send("first of A")) === "end_turn", "turn 1");
  assert((await api.send("second of A")) === "end_turn", "turn 2");
  const a = api.state().sessionId;
  assert(a, "no session A");
  sessionA = a;
  // updatedAt has a one-second resolution at worst: B is strictly newer.
  await new Promise((r) => setTimeout(r, 1100));
  await vscode.commands.executeCommand("tenon.newSession");
  assert((await api.send("only B")) === "end_turn", "turn B");
  const b = api.state().sessionId;
  assert(b && b !== a, `session B ${b}`);

  const sessions = await waitFor(
    () => {
      const s = api.state().sessions;
      return s.length >= 2 && s[0].sessionId === b ? s : undefined;
    },
    10_000,
    `the session list to show B first, got ${JSON.stringify(api.state().sessions)}`,
  );
  assert(sessions.length === 2, `sessions ${JSON.stringify(sessions)}`);
  assert(sessions[1].sessionId === a, `second entry ${JSON.stringify(sessions[1])}`);
  assert(sessions[0].title === "only B" && sessions[1].title === "first of A", `titles ${JSON.stringify(sessions)}`);
  assert(sessions.every((s) => !Number.isNaN(Date.parse(s.updatedAt))), `dates ${JSON.stringify(sessions)}`);
}, 60_000);

test("history: loading A rebuilds its thread from the replay", async () => {
  await api.load(sessionA);
  assert(api.state().sessionId === sessionA, "not on A");
  const want = [
    { kind: "user", text: "first of A" },
    { kind: "agent", text: "one" },
    { kind: "user", text: "second of A" },
    { kind: "agent", text: "two" },
  ];
  await waitFor(() => JSON.stringify(thread()) === JSON.stringify(want), 10_000, `A's thread, got ${JSON.stringify(thread())}`);
  // The list still shows both sessions.
  assert(api.state().sessions.length === 2, `sessions ${JSON.stringify(api.state().sessions)}`);
}, 30_000);

test("skills: a project skill is offered for / and /fix sends its body", async () => {
  const fix = await waitFor(
    () => api.state().commands.find((c) => c.name === "fix"),
    10_000,
    `the fix command, got ${JSON.stringify(api.state().commands)}`,
  );
  assert(fix.description === "Fixes the bug.", `description ${fix.description}`);
  const before = fake.requests().length;
  assert((await api.send("/fix it")) === "end_turn", "skill turn");
  const reqs = fake.requests();
  assert(reqs.length === before + 1, `requests ${reqs.length}`);
  assert(reqs[reqs.length - 1].includes("FIX-SKILL-BODY"), "the skill body did not reach the provider");
}, 30_000);

test("context: an attached selection goes out with the prompt, lines in its uri", async () => {
  const file = path.join(workspace, "ctx.txt");
  fs.writeFileSync(file, "line one\nline two SELECTED\nline three SELECTED\nline four\n");
  const doc = await vscode.workspace.openTextDocument(file);
  const ed = await vscode.window.showTextDocument(doc);
  ed.selection = new vscode.Selection(1, 0, 2, 19);
  await vscode.commands.executeCommand("tenon.attachSelection");
  const chips = api.state().attachments;
  assert(chips.length === 1 && chips[0].label === "ctx.txt:2-3", `chips ${JSON.stringify(chips)}`);
  await vscode.commands.executeCommand("tenon.attachFile", doc.uri);
  assert(api.state().attachments.length === 2, "file chip");
  const before = fake.requests().length;
  assert((await api.send("explain")) === "end_turn", "attachment turn");
  assert(api.state().attachments.length === 0, "chips not cleared");
  const body = fake.requests()[before];
  assert(body.includes("explain"), "text missing");
  assert(body.includes("line two SELECTED") && body.includes("line three SELECTED"), "selection text missing");
  assert(!body.includes("line four"), "more than the selection went out");
  assert(body.includes("ctx.txt#L2-3"), "the selection uri lacks its lines");
  assert(body.includes("@" + file) || body.includes("@" + fs.realpathSync(file)), "the file link is missing");
  await vscode.commands.executeCommand("workbench.action.closeActiveEditor");
}, 30_000);

test("context: attach with nothing to attach says so", async () => {
  await vscode.commands.executeCommand("workbench.action.closeAllEditors");
  const before = api.notifications().length;
  await vscode.commands.executeCommand("tenon.attachSelection");
  await vscode.commands.executeCommand("tenon.attachFile");
  const shown = api.notifications().slice(before);
  assert(shown.length === 2 && shown.every((n) => n.kind === "info"), `notifications ${JSON.stringify(shown)}`);
  assert(shown[0].message.includes("Open a file"), `message ${shown[0].message}`);
  assert(shown[1].message.includes("saved on disk"), `message ${shown[1].message}`);
  assert(api.state().attachments.length === 0, "a chip appeared");
}, 15_000);

test("status bar: provider, jurisdiction and sandbox of the session", async () => {
  const s = await waitFor(() => (api.status().text !== "tenon" ? api.status() : undefined), 5_000, "the status bar");
  assert(s.text.startsWith("tenon · Test SAS · EU · "), `status ${s.text}`);
  assert(/ · (sandboxed|not sandboxed)$/.test(s.text), `status ${s.text}`);
  assert(!s.warning, "EU shown as a warning");
  const meta = api.state().meta;
  const mech = (meta?.sandbox as { mechanism?: string } | undefined)?.mechanism ?? "";
  assert(s.text.endsWith(mech !== "" ? "· sandboxed" : "· not sandboxed"), `status ${s.text} for mechanism "${mech}"`);
  await api.stopAgent();
  await fake.close();
  fs.rmSync(path.join(workspace, ".tenon"), { recursive: true, force: true });
  fs.rmSync(path.join(workspace, "ctx.txt"), { force: true });
}, 30_000);
