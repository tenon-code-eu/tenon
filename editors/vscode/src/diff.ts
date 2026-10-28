// Native diffs of tenon's writes. The source of truth is the tool item's
// `diff` in the Conversation (from tenon's tool_call_update: old and new text
// as tenon sent them, so already masked; tool call ids are unique per session
// since tenon prefixes them with the turn). The two sides are served as
// read-only `tenon-diff:` documents whose URIs carry only the tool call id,
// the side and the file name, never text.
import * as path from "node:path";
import * as vscode from "vscode";
import type { Log } from "./log";

export const DIFF_SCHEME = "tenon-diff";

export interface Diff {
  path: string;
  oldText?: string;
  newText: string;
}

export type Side = "old" | "new";

/** Where the diffs come from (Conversation.toolDiff). */
export interface DiffSource {
  toolDiff(toolCallId: string): Diff | undefined;
}

export function diffTitle(file: string): string {
  return `${path.basename(file)} (tenon)`;
}

/** `/<encoded tool call id>/<old|new>/<file name>` → id and side. */
export function parseDiffPath(p: string): { id: string; side: Side } | undefined {
  const m = /^\/([^/]+)\/(old|new)\//.exec(p);
  if (!m) {
    return undefined;
  }
  try {
    return { id: decodeURIComponent(m[1]), side: m[2] as Side };
  } catch {
    return undefined;
  }
}

/** Text of one side; empty old side for a new file, empty when the item is gone. */
export function sideText(source: DiffSource, uriPath: string): string {
  const ref = parseDiffPath(uriPath);
  const d = ref && source.toolDiff(ref.id);
  if (!ref || !d) {
    return "";
  }
  return ref.side === "old" ? (d.oldText ?? "") : d.newText;
}

export interface OpenedDiff {
  left: string;
  right: string;
  title: string;
}

export class DiffProvider implements vscode.TextDocumentContentProvider {
  private last: { left: vscode.Uri; right: vscode.Uri; title: string } | undefined;
  private version = 0;

  constructor(
    private readonly source: DiffSource,
    private readonly log: Log,
  ) {}

  provideTextDocumentContent(uri: vscode.Uri): string {
    return sideText(this.source, uri.path);
  }

  /** Opens VS Code's diff editor on the write of toolCallId (empty old side for a new file). */
  async open(toolCallId: string): Promise<void> {
    const d = this.source.toolDiff(toolCallId);
    if (!d) {
      this.log.info(`diff ${toolCallId}: not available`);
      return;
    }
    const title = diffTitle(d.path);
    // A fresh query per opening: a document VS Code still holds is not reused
    // with stale text.
    const v = String(++this.version);
    const uri = (side: Side) =>
      vscode.Uri.from({
        scheme: DIFF_SCHEME,
        path: `/${encodeURIComponent(toolCallId)}/${side}/${path.basename(d.path)}`,
        query: v,
      });
    const left = uri("old");
    const right = uri("new");
    this.last = { left, right, title };
    await vscode.commands.executeCommand("vscode.diff", left, right, title, { preview: true });
  }

  /** The last diff opened, as its two documents show it (test API). */
  lastOpened(): OpenedDiff | undefined {
    const l = this.last;
    if (!l) {
      return undefined;
    }
    return {
      left: this.provideTextDocumentContent(l.left),
      right: this.provideTextDocumentContent(l.right),
      title: l.title,
    };
  }
}
