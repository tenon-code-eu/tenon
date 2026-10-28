// Terminal mode: "Open in terminal" runs the tenon binary in a terminal named
// "tenon" beside the editor, in the workspace folder, reused while open; and
// "Insert reference" types `@<path>#L<a>-<b>` of the selection into it. The
// binary is a stub that records its folder and the keys it receives.
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as vscode from "vscode";
import type { TenonTestApi } from "../../../src/extension";
import { assert, test, waitFor } from "./harness";

test("terminal: opens the TUI beside the editor, reuses it, and receives references", async () => {
  const ext = vscode.extensions.getExtension<TenonTestApi | undefined>("tenon-code-eu.tenon");
  assert(ext, "extension not found");
  const api = await ext.activate();
  assert(api, "test API missing");
  const ws = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  assert(ws, "the main suite runs with a workspace folder");

  // The terminal's environment is the pty host's, not the extension host's:
  // the stub carries its output paths itself.
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tenon-term-"));
  const out = path.join(dir, "keys");
  const stub = path.join(dir, "tenon-tui");
  fs.writeFileSync(
    stub,
    `#!/bin/sh\npwd > '${out}.cwd'\nstty -icanon -echo 2>/dev/null\nexec cat > '${out}'\n`,
    { mode: 0o755 },
  );
  const config = vscode.workspace.getConfiguration("tenon");
  const previous = config.get<string>("path");
  await config.update("path", stub, vscode.ConfigurationTarget.Global);
  try {
    const file = path.join(ws, "term.txt");
    fs.writeFileSync(file, "one\ntwo\nthree\nfour\n");
    const doc = await vscode.workspace.openTextDocument(file);
    const ed = await vscode.window.showTextDocument(doc, vscode.ViewColumn.One);
    ed.selection = new vscode.Selection(1, 0, 2, 3);

    await vscode.commands.executeCommand("tenon.openTerminal");
    const t = await waitFor(() => api.terminal(), 5_000, "the tenon terminal");
    assert(t.name === "tenon", `name ${t.name}`);
    assert(t.cwd === ws, `cwd ${t.cwd}`);
    assert(t.visible, "tenon.terminalVisible is false");
    const cwd = await waitFor(
      () => (fs.existsSync(`${out}.cwd`) ? fs.readFileSync(`${out}.cwd`, "utf8").trim() : undefined),
      10_000,
      "the stub to start",
    );
    assert(fs.realpathSync(cwd) === fs.realpathSync(ws), `the TUI runs in ${cwd}`);

    await vscode.commands.executeCommand("tenon.openTerminal");
    const named = vscode.window.terminals.filter((x) => x.name === "tenon");
    assert(named.length === 1, `${named.length} tenon terminals`);

    // The terminal is shown: the selection is the text editor's.
    await vscode.commands.executeCommand("tenon.insertReference");
    const typed = await waitFor(
      () => {
        const s = fs.existsSync(out) ? fs.readFileSync(out, "utf8") : "";
        return s.length >= "@term.txt#L2-3".length ? s : undefined;
      },
      10_000,
      "the reference to reach the TUI",
    );
    assert(typed === "@term.txt#L2-3", `typed ${JSON.stringify(typed)}`);

    // Cmd+Alt+K reaches the extension while the terminal has the focus.
    const skip = vscode.workspace.getConfiguration("terminal.integrated").get<string[]>("commandsToSkipShell") ?? [];
    assert(skip.includes("tenon.insertReference"), `commandsToSkipShell ${JSON.stringify(skip)}`);

    named[0].dispose();
    await waitFor(() => (api.terminal() === undefined ? true : undefined), 5_000, "the terminal to be forgotten");
  } finally {
    await config.update("path", previous, vscode.ConfigurationTarget.Global);
    await vscode.commands.executeCommand("workbench.action.closeAllEditors");
  }
}, 40_000);
