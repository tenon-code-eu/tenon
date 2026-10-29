// ACP sessions of one `tenon acp`: new, prompt, cancel, load, list. Results
// and errors land in the Conversation; no vscode runtime import (unit-tested
// under plain Node with a stub connection).
import type { ContentBlock, StopReason } from "@agentclientprotocol/sdk";
import type { Agent } from "./agent";
import type { Conversation } from "./model";

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** `_meta.tenon` of a session/new or session/load result. */
function tenonMeta(result: { _meta?: Record<string, unknown> | null }): Record<string, unknown> | undefined {
  const t = result._meta?.tenon;
  return typeof t === "object" && t !== null && !Array.isArray(t) ? (t as Record<string, unknown>) : undefined;
}

/** Text shown for the user's own message. */
function userText(blocks: ContentBlock[]): string {
  return blocks
    .map((b) => (b.type === "text" ? b.text : b.type === "resource_link" ? b.name : b.type === "resource" ? b.resource.uri : ""))
    .filter((t) => t !== "")
    .join("\n");
}

/** Lets the notifications that preceded a reply on the wire reach the model. */
function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

export class SessionController {
  constructor(
    readonly agent: Agent,
    private readonly conv: Conversation,
  ) {}

  /** session/new in cwd; the thread is cleared, early updates of the new id are kept. */
  async newSession(cwd: string): Promise<void> {
    this.conv.begin(undefined, cwd);
    try {
      const res = await this.agent.connection.newSession({ cwd, mcpServers: [] });
      this.conv.begin(res.sessionId, cwd, tenonMeta(res));
      this.conv.setBanner(undefined);
    } catch (err) {
      this.conv.setBanner({ kind: "error", text: errorMessage(err) });
      throw err;
    }
  }

  /**
   * One turn: adds the user item, marks the conversation busy until the
   * reply. An error reply shows tenon's message verbatim in an error banner
   * and rejects.
   */
  async prompt(blocks: ContentBlock[]): Promise<StopReason> {
    const sessionId = this.conv.state.sessionId;
    if (sessionId === undefined) {
      throw new Error("no session");
    }
    if (this.conv.state.banner?.kind === "error") {
      this.conv.setBanner(undefined);
    }
    this.conv.addUser(userText(blocks));
    this.conv.setBusy(true);
    try {
      const res = await this.agent.connection.prompt({ sessionId, prompt: blocks });
      await settle();
      return res.stopReason;
    } catch (err) {
      // A dead tenon gets the "stopped" banner instead (Chat).
      if (!this.agent.hasExited) {
        this.conv.setBanner({ kind: "error", text: errorMessage(err) });
      }
      throw err;
    } finally {
      this.conv.setBusy(false);
    }
  }

  async cancel(): Promise<void> {
    const sessionId = this.conv.state.sessionId;
    if (sessionId !== undefined) {
      await this.agent.connection.cancel({ sessionId });
    }
  }

  /**
   * session/load: the thread is rebuilt from the replayed updates. On
   * failure the conversation goes back to the session that was open. The
   * conversation is marked loading from before begin() (which empties the
   * thread) until the replay, which tenon sends right after its reply, has
   * reached the model: the home screen never flashes in between.
   */
  async load(sessionId: string, cwd: string): Promise<void> {
    const before = this.conv.snapshot();
    this.conv.setLoading(true);
    this.conv.begin(sessionId, cwd);
    try {
      const res = await this.agent.connection.loadSession({ sessionId, cwd, mcpServers: [] });
      this.conv.setMeta(tenonMeta(res));
      this.conv.setBanner(undefined);
      await settle();
    } catch (err) {
      this.conv.restore(before);
      this.conv.setBanner({ kind: "error", text: errorMessage(err) });
      throw err;
    } finally {
      this.conv.setLoading(false);
    }
  }

  /** session/list for cwd, most recent first (tenon's order). */
  async list(cwd: string): Promise<void> {
    const res = await this.agent.connection.listSessions({ cwd });
    this.conv.setSessions(
      res.sessions.map((s) => ({ sessionId: s.sessionId, title: s.title ?? "", updatedAt: s.updatedAt ?? "" })),
    );
  }
}
