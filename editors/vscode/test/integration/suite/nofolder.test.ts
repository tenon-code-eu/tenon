// Review Focus 5: a window without a workspace folder. The panel shows
// "Open a folder to use tenon" and no session/new is sent.
import * as vscode from "vscode";
import type { TenonTestApi } from "../../../src/extension";
import { makeEnv } from "../env";
import { sseText, startFakeLLM } from "../fakeLLM";
import { assert, test } from "./harness";

test("no folder: noFolder banner and no request to the provider", async () => {
  assert(!vscode.workspace.workspaceFolders?.length, "this suite runs without a workspace folder");
  const fake = await startFakeLLM([sseText("hello")]);
  Object.assign(process.env, makeEnv(fake).env);
  const ext = vscode.extensions.getExtension<TenonTestApi | undefined>("tenon-code-eu.tenon");
  assert(ext, "extension not found");
  const api = await ext.activate();
  assert(api, "test API missing");

  await vscode.commands.executeCommand("tenon.openChat");
  const banner = api.state().banner;
  assert(banner?.kind === "noFolder", `banner ${JSON.stringify(banner)}`);
  assert(banner.text === "Open a folder to use tenon", `banner text ${banner.text}`);

  let refused = false;
  await api.send("hi").catch(() => (refused = true));
  assert(refused, "send should be refused without a folder");
  await vscode.commands.executeCommand("tenon.newSession");
  await new Promise((r) => setTimeout(r, 500));
  assert(api.state().sessionId === undefined, `session ${api.state().sessionId}`);
  assert(api.state().items.length === 0, `items ${JSON.stringify(api.state().items)}`);
  assert(api.agentPid() === undefined, "tenon was started without a folder");
  assert(fake.requests().length === 0, `provider got ${fake.requests().length} request(s)`);
  await fake.close();
});
