// opencode-style terminal mode: the tenon TUI in an integrated terminal
// beside the editor, one per window, reused while it is open. The context key
// `tenon.terminalVisible` is true while that terminal is the active one, so
// Cmd+Alt+K inserts a reference to the selection there instead of attaching
// it to the chat.
import * as path from "node:path";
import * as vscode from "vscode";
import { lineRange } from "./context";

export const TERMINAL_NAME = "tenon";
export const TERMINAL_CONTEXT = "tenon.terminalVisible";

/** `@<relPath>#L<a>-<b>`, `#L<a>` for one line, `@<relPath>` without a selection. */
export function referenceFor(relPath: string, startLine?: number, endLine?: number): string {
  if (!startLine || startLine < 1) {
    return `@${relPath}`;
  }
  const end = endLine && endLine > startLine ? endLine : startLine;
  return end === startLine ? `@${relPath}#L${startLine}` : `@${relPath}#L${startLine}-${end}`;
}

/** An editor selection: 0-based lines, end column, empty or not. */
export interface SelectionShape {
  empty: boolean;
  startLine: number;
  endLine: number;
  endCharacter: number;
}

/** The reference for a file and selection, the path relative to the TUI's folder. */
export function editorReference(cwd: string, fsPath: string, sel: SelectionShape): string {
  const rel = path.relative(cwd, fsPath).split(path.sep).join("/");
  if (sel.empty) {
    return referenceFor(rel);
  }
  const [a, b] = lineRange(sel.startLine, sel.endLine, sel.endCharacter);
  return referenceFor(rel, a, b);
}

let terminal: vscode.Terminal | undefined;
let terminalCwd: string | undefined;
let listeners: vscode.Disposable[] = [];
let visible: boolean | undefined;

function setVisible(v: boolean): void {
  if (v !== visible) {
    visible = v;
    void vscode.commands.executeCommand("setContext", TERMINAL_CONTEXT, v);
  }
}

function track(): void {
  if (listeners.length > 0) {
    return;
  }
  listeners = [
    vscode.window.onDidCloseTerminal((t) => {
      if (t === terminal) {
        terminal = undefined;
        terminalCwd = undefined;
        setVisible(false);
      }
    }),
    vscode.window.onDidChangeActiveTerminal((t) => {
      if (terminal) {
        setVisible(t === terminal);
      }
    }),
  ];
}

/**
 * Opens the tenon TUI (`bin`, no argument) in a terminal named "tenon" beside
 * the editor, in `cwd`; shows the existing one while it is open.
 */
export function openTenonTerminal(bin: string, cwd: string): vscode.Terminal {
  track();
  if (terminal && terminal.exitStatus === undefined) {
    terminal.show(false);
    setVisible(true);
    return terminal;
  }
  terminal = vscode.window.createTerminal({
    name: TERMINAL_NAME,
    shellPath: bin,
    cwd,
    location: { viewColumn: vscode.ViewColumn.Beside },
    // A window reload does not bring back a TUI with a lost session.
    isTransient: true,
  });
  terminalCwd = cwd;
  terminal.show(false);
  setVisible(true);
  return terminal;
}

/** The open tenon terminal and its folder, if any. */
export function tenonTerminal(): { terminal: vscode.Terminal; cwd: string } | undefined {
  return terminal && terminalCwd !== undefined && terminal.exitStatus === undefined
    ? { terminal, cwd: terminalCwd }
    : undefined;
}

/** The value last given to `tenon.terminalVisible`. */
export function isTerminalVisible(): boolean {
  return visible === true;
}

/** Forgets the terminal and its listeners (deactivation, tests); the terminal itself stays. */
export function closeTenonTerminal(): void {
  for (const d of listeners) {
    d.dispose();
  }
  listeners = [];
  terminal = undefined;
  terminalCwd = undefined;
  visible = undefined;
}
