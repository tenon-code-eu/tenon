import { beforeEach, describe, expect, it, vi } from "vitest";
import * as vscode from "vscode";
import type { Agent } from "../../src/agent";
import { Chat } from "../../src/chat";
import { Conversation } from "../../src/model";

type Conn = Record<string, ReturnType<typeof vi.fn>>;

/** A deferred promise. */
function later<T>(): { promise: Promise<T>; resolve: (v: T) => void } {
  let resolve: (v: T) => void = () => undefined;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function setup(conn: Conn, ready?: Promise<void>): { chat: Chat; conv: Conversation } {
  const agent = { connection: conn, hasExited: false, onExit: () => ({ dispose: () => undefined }) } as unknown as Agent;
  const started = new vscode.EventEmitter<Agent>();
  const conv = new Conversation();
  const source = {
    ready: async () => {
      await ready;
      return agent;
    },
    onDidStart: started.event,
  };
  const log = { info: () => undefined, stderr: () => undefined };
  return { chat: new Chat(source, conv, log, () => undefined), conv };
}

describe("Chat", () => {
  beforeEach(() => {
    (vscode.workspace as { workspaceFolders: unknown }).workspaceFolders = [{ uri: { fsPath: "/p" } }];
  });

  it("claims the turn at once: two back-to-back sends make one session and one prompt", async () => {
    // tenon is still starting when both sends arrive.
    const start = later<void>();
    const newSession = vi.fn().mockResolvedValue({ sessionId: "s1" });
    const prompt = vi.fn().mockResolvedValue({ stopReason: "end_turn" });
    const { chat, conv } = setup({ newSession, prompt }, start.promise);
    const first = chat.send("one");
    expect(conv.state.busy).toBe(true);
    const second = chat.send("two");
    await expect(second).rejects.toThrow("a turn is in progress");
    chat.handle({ type: "send", text: "three" });
    start.resolve();
    expect(await first).toBe("end_turn");
    expect(newSession).toHaveBeenCalledTimes(1);
    expect(prompt).toHaveBeenCalledTimes(1);
    expect(conv.state.items).toEqual([{ kind: "user", text: "one" }]);
    expect(conv.state.busy).toBe(false);
  });

  it("releases the turn when the session cannot be created", async () => {
    const newSession = vi.fn().mockRejectedValueOnce(new Error("refused")).mockResolvedValue({ sessionId: "s1" });
    const prompt = vi.fn().mockResolvedValue({ stopReason: "end_turn" });
    const { chat, conv } = setup({ newSession, prompt });
    await expect(chat.send("one")).rejects.toThrow("refused");
    expect(conv.state.busy).toBe(false);
    expect(await chat.send("two")).toBe("end_turn");
  });

  for (const op of ["newSession", "load"] as const) {
    it(`${op} during a turn stops and awaits the turn first`, async () => {
      const turn = later<{ stopReason: string }>();
      const order: string[] = [];
      const conn: Conn = {
        newSession: vi.fn().mockImplementation(() => {
          order.push("session/new");
          return Promise.resolve({ sessionId: `s${order.length}` });
        }),
        prompt: vi.fn().mockReturnValue(turn.promise),
        cancel: vi.fn().mockImplementation(() => {
          order.push("cancel");
          // tenon ends the turn after session/cancel.
          setTimeout(() => {
            order.push("turn ended");
            turn.resolve({ stopReason: "cancelled" });
          }, 5);
          return Promise.resolve();
        }),
        loadSession: vi.fn().mockImplementation(() => {
          order.push("session/load");
          return Promise.resolve({});
        }),
      };
      const { chat, conv } = setup(conn);
      const sending = chat.send("long");
      await vi.waitFor(() => expect(conn.prompt).toHaveBeenCalled());
      await (op === "newSession" ? chat.newSession() : chat.load("old"));
      expect(await sending).toBe("cancelled");
      expect(order).toEqual(["session/new", "cancel", "turn ended", op === "newSession" ? "session/new" : "session/load"]);
      expect(conv.state.busy).toBe(false);
    });
  }
});

describe("Chat permissions and diffs", () => {
  beforeEach(() => {
    (vscode.workspace as { workspaceFolders: unknown }).workspaceFolders = [{ uri: { fsPath: "/p" } }];
  });

  function withPerms(conn: Conn, order: string[] = []) {
    const exit = new vscode.EventEmitter<{ code: number | null }>();
    const agent = { connection: conn, hasExited: false, onExit: exit.event } as unknown as Agent;
    const started = new vscode.EventEmitter<Agent>();
    const conv = new Conversation();
    const permissions = {
      answer: vi.fn(),
      cancelAll: vi.fn(() => order.push("cancelAll")),
    };
    const diffs = { open: vi.fn().mockResolvedValue(undefined) };
    const log = { info: () => undefined, stderr: () => undefined };
    let current = agent;
    const chat = new Chat({ ready: () => Promise.resolve(current), onDidStart: started.event }, conv, log, () => undefined, {
      permissions,
      diffs,
    });
    /** A new tenon replaces the current one (restart). */
    const restart = (next: Agent) => {
      current = next;
      started.fire(next);
    };
    return { chat, conv, permissions, diffs, exit, order, restart };
  }

  it("Stop sends session/cancel, then answers every pending request cancelled", async () => {
    const turn = later<{ stopReason: string }>();
    const order: string[] = [];
    const conn: Conn = {
      newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }),
      prompt: vi.fn().mockReturnValue(turn.promise),
      cancel: vi.fn().mockImplementation(() => {
        order.push("session/cancel");
        return Promise.resolve();
      }),
    };
    const { chat } = withPerms(conn, order);
    const sending = chat.send("go");
    await vi.waitFor(() => expect(conn.prompt).toHaveBeenCalled());
    await chat.cancel();
    expect(order).toEqual(["session/cancel", "cancelAll"]);
    turn.resolve({ stopReason: "cancelled" });
    expect(await sending).toBe("cancelled");
  });

  it("tenon exiting answers every pending request cancelled", async () => {
    const conn: Conn = { newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt: vi.fn().mockReturnValue(new Promise(() => undefined)) };
    const { chat, permissions, exit } = withPerms(conn);
    void chat.send("go").catch(() => undefined);
    await vi.waitFor(() => expect(conn.prompt).toHaveBeenCalled());
    exit.fire({ code: null });
    expect(permissions.cancelAll).toHaveBeenCalled();
  });

  it("tenon exiting clears the meta: the status bar shows tenon alone (spec §7)", async () => {
    const meta = { provider: "mistral", jurisdiction: "eu", sandbox: { mode: "auto", mechanism: "sandbox-exec", network: "full", confined: true } };
    const conn: Conn = {
      newSession: vi.fn().mockResolvedValue({ sessionId: "s1", _meta: { tenon: meta } }),
      prompt: vi.fn().mockResolvedValue({ stopReason: "end_turn" }),
    };
    const { chat, conv, exit } = withPerms(conn);
    await chat.send("go");
    expect(conv.state.meta).toEqual(meta);
    exit.fire({ code: null });
    expect(conv.state.meta).toBeUndefined();
    expect(conv.state.sessionId).toBe("s1");
  });

  it("a superseded tenon exiting later does not cancel the new one's requests", async () => {
    const conn: Conn = { newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt: vi.fn().mockResolvedValue({ stopReason: "end_turn" }) };
    const { chat, permissions, exit, restart } = withPerms(conn);
    await chat.send("go");
    permissions.cancelAll.mockClear();
    const next = { connection: conn, hasExited: false, onExit: new vscode.EventEmitter<{ code: number | null }>().event } as unknown as Agent;
    restart(next);
    exit.fire({ code: 0 });
    expect(permissions.cancelAll).not.toHaveBeenCalled();
  });

  it("a card never outlives its turn: the end of the turn cancels what is left", async () => {
    const turn = later<{ stopReason: string }>();
    const conn: Conn = { newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt: vi.fn().mockReturnValue(turn.promise) };
    const { chat, permissions } = withPerms(conn);
    const sending = chat.send("go");
    await vi.waitFor(() => expect(conn.prompt).toHaveBeenCalled());
    expect(permissions.cancelAll).not.toHaveBeenCalled();
    turn.resolve({ stopReason: "end_turn" });
    await sending;
    expect(permissions.cancelAll).toHaveBeenCalledTimes(1);
  });

  it("routes card clicks and Open diff", async () => {
    const { chat, permissions, diffs } = withPerms({});
    chat.handle({ type: "permission", requestId: "p2", optionId: "allow" });
    expect(permissions.answer).toHaveBeenCalledWith("p2", "allow");
    chat.handle({ type: "openDiff", toolCallId: "c1" });
    await vi.waitFor(() => expect(diffs.open).toHaveBeenCalledWith("c1"));
  });
});

describe("Chat attachments and history", () => {
  beforeEach(() => {
    (vscode.workspace as { workspaceFolders: unknown }).workspaceFolders = [{ uri: { fsPath: "/p" } }];
  });

  const file = { type: "resource_link" as const, uri: "file:///p/a.go", name: "a.go" };
  const sel = { type: "resource" as const, resource: { uri: "file:///p/b.go#L1-2", text: "y", mimeType: "text/plain" } };

  it("sends the text then the attachments, and clears them", async () => {
    const prompt = vi.fn().mockResolvedValue({ stopReason: "end_turn" });
    const { chat, conv } = setup({ newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt, listSessions: vi.fn().mockResolvedValue({ sessions: [] }) });
    chat.addAttachment(file, "a.go");
    chat.addAttachment(sel, "b.go:1-2");
    expect(conv.state.attachments.map((a) => a.label)).toEqual(["a.go", "b.go:1-2"]);
    await chat.send("fix");
    expect((prompt.mock.calls[0][0] as { prompt: unknown }).prompt).toEqual([{ type: "text", text: "fix" }, file, sel]);
    expect(conv.state.attachments).toEqual([]);
  });

  it("detach removes one chip; an empty text with attachments is sent from the webview", async () => {
    const prompt = vi.fn().mockResolvedValue({ stopReason: "end_turn" });
    const { chat, conv } = setup({ newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt, listSessions: vi.fn().mockResolvedValue({ sessions: [] }) });
    chat.addAttachment(file, "a.go");
    chat.addAttachment(sel, "b.go:1-2");
    chat.handle({ type: "detach", id: conv.state.attachments[0].id });
    chat.handle({ type: "detach", id: "unknown" });
    expect(conv.state.attachments.map((a) => a.label)).toEqual(["b.go:1-2"]);
    chat.handle({ type: "send", text: "" });
    await vi.waitFor(() => expect(prompt).toHaveBeenCalled());
    expect((prompt.mock.calls[0][0] as { prompt: unknown }).prompt).toEqual([sel]);
  });

  it("attachments come back when the turn fails before tenon accepted the prompt", async () => {
    const newSession = vi.fn().mockRejectedValueOnce(new Error("tenon failed to start")).mockResolvedValue({ sessionId: "s1" });
    const prompt = vi.fn().mockRejectedValueOnce(new Error("refused by policy")).mockResolvedValue({ stopReason: "end_turn" });
    const { chat, conv } = setup({ newSession, prompt, listSessions: vi.fn().mockResolvedValue({ sessions: [] }) });
    chat.addAttachment(file, "a.go");
    await expect(chat.send("fix")).rejects.toThrow("tenon failed to start");
    expect(conv.state.attachments.map((a) => a.label)).toEqual(["a.go"]);
    await expect(chat.send("fix")).rejects.toThrow("refused by policy");
    expect(conv.state.attachments.map((a) => a.label)).toEqual(["a.go"]);
    expect(await chat.send("fix")).toBe("end_turn");
    expect(conv.state.attachments).toEqual([]);
    expect(prompt).toHaveBeenCalledTimes(2);
    expect((prompt.mock.calls[1][0] as { prompt: unknown }).prompt).toEqual([{ type: "text", text: "fix" }, file]);
  });

  it("attachments added during a failing turn stay after the restored ones", async () => {
    const start = later<void>();
    const newSession = vi.fn().mockRejectedValue(new Error("no"));
    const agent = { connection: { newSession }, hasExited: false, onExit: () => ({ dispose: () => undefined }) } as unknown as Agent;
    const conv = new Conversation();
    const chat = new Chat({ ready: async () => { await start.promise; return agent; }, onDidStart: new vscode.EventEmitter<Agent>().event }, conv, { info: () => undefined, stderr: () => undefined }, () => undefined);
    chat.addAttachment(file, "a.go");
    const sending = chat.send("x");
    chat.addAttachment(sel, "b.go:1-2");
    start.resolve();
    await expect(sending).rejects.toThrow("no");
    expect(conv.state.attachments.map((a) => a.label)).toEqual(["a.go", "b.go:1-2"]);
  });

  it("an empty text without attachments is not sent", async () => {
    const prompt = vi.fn();
    const { chat } = setup({ prompt });
    chat.handle({ type: "send", text: " " });
    await new Promise((r) => setTimeout(r, 5));
    expect(prompt).not.toHaveBeenCalled();
  });

  it("refreshes the session list after each turn, for the session's folder", async () => {
    const listSessions = vi.fn().mockResolvedValue({
      sessions: [{ sessionId: "s1", cwd: "/p", title: "fix", updatedAt: "2026-10-28T10:00:00Z" }],
    });
    const { chat, conv } = setup({
      newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }),
      prompt: vi.fn().mockResolvedValue({ stopReason: "end_turn" }),
      listSessions,
    });
    await chat.send("fix");
    await vi.waitFor(() => expect(conv.state.sessions).toHaveLength(1));
    expect(listSessions).toHaveBeenCalledWith({ cwd: "/p" });
    expect(conv.state.sessions[0]).toEqual({ sessionId: "s1", title: "fix", updatedAt: "2026-10-28T10:00:00Z" });
  });

  it("refreshSessions logs a failure and keeps the list", async () => {
    const lines: string[] = [];
    const agent = { connection: { listSessions: vi.fn().mockRejectedValue(new Error("boom")) }, hasExited: false, onExit: () => ({ dispose: () => undefined }) } as unknown as Agent;
    const conv = new Conversation();
    conv.setSessions([{ sessionId: "old", title: "t", updatedAt: "" }]);
    const chat = new Chat({ ready: () => Promise.resolve(agent), onDidStart: new vscode.EventEmitter<Agent>().event }, conv, { info: (l: string) => lines.push(l), stderr: () => undefined }, () => undefined);
    await chat.refreshSessions();
    expect(conv.state.sessions).toHaveLength(1);
    expect(lines.join("\n")).toContain("boom");
  });

  it("a webview load message loads that session", async () => {
    const loadSession = vi.fn().mockResolvedValue({});
    const { chat } = setup({ loadSession });
    chat.handle({ type: "load", sessionId: "s9" });
    await vi.waitFor(() => expect(loadSession).toHaveBeenCalledWith({ sessionId: "s9", cwd: "/p", mcpServers: [] }));
  });
});
