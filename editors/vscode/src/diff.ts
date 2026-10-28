// Native diffs of tenon's writes: the `diff` content of a tool_call_update
// (old and new text as tenon sent them, so already masked) is kept in memory
// per tool call id and served as two read-only `tenon-diff:` documents. The
// URIs carry only the tool call id, the side and the file name, never text.
import * as path from "node:path";
import type { SessionNotification } from "@agentclientprotocol/sdk";
import * as vscode from "vscode";
import type { Log } from "./log";

export const DIFF_SCHEME = "tenon-diff";

/** Diffs kept; older ones are dropped (their documents then come up empty). */
export const MAX_DIFFS = 100;

export interface Diff {
  path: string;
  oldText?: string;
  newText: string;
}

export type Side = "old" | "new";

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

/** The last MAX_DIFFS diffs, by tool call id. No vscode runtime use. */
export class DiffStore {
  private readonly diffs = new Map<string, Diff>();

  get size(): number {
    return this.diffs.size;
  }

  get(toolCallId: string): Diff | undefined {
    return this.diffs.get(toolCallId);
  }

  /** Keeps the diff of a tool_call / tool_call_update, if it has one. */
  record(n: SessionNotification): void {
    const u = n.update;
    if (u.sessionUpdate !== "tool_call" && u.sessionUpdate !== "tool_call_update") {
      return;
    }
    for (const c of u.content ?? []) {
      if (c.type !== "diff") {
        continue;
      }
      const d: Diff = { path: c.path, newText: c.newText };
      if (typeof c.oldText === "string") {
        d.oldText = c.oldText;
      }
      // Re-inserted last, so the newest diffs survive the bound.
      this.diffs.delete(u.toolCallId);
      this.diffs.set(u.toolCallId, d);
      while (this.diffs.size > MAX_DIFFS) {
        const oldest = this.diffs.keys().next().value as string;
        this.diffs.delete(oldest);
      }
    }
  }
}

export interface OpenedDiff {
  left: string;
  right: string;
  title: string;
}

export class DiffProvider implements vscode.TextDocumentContentProvider {
  readonly store = new DiffStore();
  private last: { left: vscode.Uri; right: vscode.Uri; title: string } | undefined;
  private version = 0;

  constructor(private readonly log: Log) {}

  provideTextDocumentContent(uri: vscode.Uri): string {
    const ref = parseDiffPath(uri.path);
    const d = ref && this.store.get(ref.id);
    if (!ref || !d) {
      return "";
    }
    return ref.side === "old" ? (d.oldText ?? "") : d.newText;
  }

  /** Opens VS Code's diff editor on the write of toolCallId (empty old side for a new file). */
  async open(toolCallId: string): Promise<void> {
    const d = this.store.get(toolCallId);
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
