// What the Session view shows (the TUI's side panel): title, context gauge,
// tokens, modified files and sovereignty (calls per provider, sandbox,
// profile, policy). A pure function of the conversation state: no vscode
// runtime import, so the unit tests and the webview share it.
import type { ConversationState } from "./model";
import { isConfined } from "./status";

export interface FileStat {
  /** Relative to the session's folder when inside it. */
  path: string;
  /** The tool call of the file's latest write: its diff opens on a click. */
  toolCallId: string;
  /** A: created during the session (its first write had no old text); M: modified. */
  op: "M" | "A";
}

export interface SendStat {
  provider: string;
  /** tenon sends none (`_meta.tenon` has no country): kept for parity with the TUI. */
  country?: string;
  jurisdiction?: string;
  n: number;
  /** Neither EU nor local (non-EU or unknown): the alert colour. */
  alert: boolean;
}

export interface SessionStats {
  title?: string;
  /** Input tokens of the last call, and its provider's context window. */
  used?: number;
  size?: number;
  tokensIn?: number;
  tokensOut?: number;
  files: FileStat[];
  sends: SendStat[];
  /** undefined: tenon sent no sandbox (no session yet). */
  sandboxed?: boolean;
  profile?: string;
  policy?: string;
}

/** One line of at most 80 characters (a title from the first message). */
function oneLine(s: string): string {
  const t = s.replace(/\s+/g, " ").trim();
  return t.length > 80 ? `${t.slice(0, 79)}…` : t;
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v !== "" ? v : undefined;
}

/** path relative to dir when inside it (either separator). */
function relative(path: string, dir: string | undefined): string {
  if (!dir) {
    return path;
  }
  const base = dir.replace(/[\\/]+$/, "");
  for (const sep of ["/", "\\"]) {
    if (path.startsWith(base + sep) && path.length > base.length + 1) {
      return path.slice(base.length + 1);
    }
  }
  return path;
}

export function computeStats(state: ConversationState): SessionStats {
  const out: SessionStats = { files: [], sends: [] };

  const listed = state.sessionId !== undefined ? state.sessions.find((s) => s.sessionId === state.sessionId) : undefined;
  const firstUser = state.items.find((i) => i.kind === "user");
  const title = str(listed?.title) ?? (firstUser ? str(oneLine(firstUser.text)) : undefined);
  if (title) {
    out.title = title;
  }

  if (state.usage) {
    out.used = state.usage.used;
    out.size = state.usage.size;
  }
  if (state.tokens) {
    out.tokensIn = state.tokens.input;
    out.tokensOut = state.tokens.output;
  }

  // Completed writes, one entry per file in the order first written.
  const byPath = new Map<string, FileStat>();
  for (const it of state.items) {
    if (it.kind !== "tool" || !it.diff || it.status !== "completed") {
      continue;
    }
    const path = relative(it.diff.path, state.cwd);
    const created = it.diff.oldText === undefined;
    const known = byPath.get(path);
    if (known) {
      known.toolCallId = it.id;
    } else {
      const f: FileStat = { path, toolCallId: it.id, op: created ? "A" : "M" };
      byPath.set(path, f);
      out.files.push(f);
    }
  }

  for (const s of state.sends ?? []) {
    const same = out.sends.find((x) => x.provider === s.provider && x.jurisdiction === s.jurisdiction);
    if (same) {
      same.n += s.n;
      continue;
    }
    const j = s.jurisdiction;
    const stat: SendStat = j ? { provider: s.provider, jurisdiction: j, n: s.n, alert: false } : { provider: s.provider, n: s.n, alert: false };
    stat.alert = j !== "eu" && j !== "local";
    out.sends.push(stat);
  }

  const meta = state.meta;
  if (meta) {
    if (typeof meta.sandbox === "object" && meta.sandbox !== null) {
      out.sandboxed = isConfined(meta.sandbox as Record<string, unknown>);
    }
    const profile = str(meta.profile);
    if (profile) {
      out.profile = profile;
    }
    const policy = str(meta.policy);
    if (policy) {
      out.policy = policy;
    }
  }
  return out;
}
