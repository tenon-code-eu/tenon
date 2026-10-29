import { RequestError } from "@agentclientprotocol/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "../../src/agent";
import { Conversation, homeVisible } from "../../src/model";
import { SessionController } from "../../src/session";

type Conn = Record<string, ReturnType<typeof vi.fn>>;

function setup(conn: Conn): { s: SessionController; conv: Conversation } {
  const conv = new Conversation();
  const agent = { connection: conn } as unknown as Agent;
  return { s: new SessionController(agent, conv), conv };
}

describe("SessionController", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("creates a session with the folder as cwd and no MCP server", async () => {
    const newSession = vi.fn().mockResolvedValue({ sessionId: "s1", _meta: { tenon: { provider: "Test SAS" } } });
    const { s, conv } = setup({ newSession });
    await s.newSession("/p");
    expect(newSession).toHaveBeenCalledWith({ cwd: "/p", mcpServers: [] });
    expect(conv.state).toMatchObject({ sessionId: "s1", cwd: "/p", meta: { provider: "Test SAS" }, items: [] });
  });

  it("prompt adds the user item, is busy during the turn and returns the stop reason", async () => {
    let finish: (v: { stopReason: string }) => void = () => undefined;
    const prompt = vi.fn().mockReturnValue(new Promise((r) => (finish = r)));
    const { s, conv } = setup({ newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt });
    await s.newSession("/p");
    const p = s.prompt([{ type: "text", text: "hi" }]);
    expect(conv.state.busy).toBe(true);
    expect(conv.state.items).toEqual([{ kind: "user", text: "hi" }]);
    expect(prompt).toHaveBeenCalledWith({ sessionId: "s1", prompt: [{ type: "text", text: "hi" }] });
    finish({ stopReason: "end_turn" });
    await vi.runAllTimersAsync();
    expect(await p).toBe("end_turn");
    expect(conv.state.busy).toBe(false);
  });

  it("an error response shows tenon's message verbatim in an error banner", async () => {
    const prompt = vi.fn().mockRejectedValue(new RequestError(-32603, "refused by policy: network"));
    const { s, conv } = setup({ newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt });
    await s.newSession("/p");
    await expect(s.prompt([{ type: "text", text: "hi" }])).rejects.toThrow("refused by policy: network");
    expect(conv.state.banner).toEqual({ kind: "error", text: "refused by policy: network" });
    expect(conv.state.busy).toBe(false);
  });

  it("a new prompt clears a previous error banner", async () => {
    const prompt = vi.fn().mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce({ stopReason: "end_turn" });
    const { s, conv } = setup({ newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), prompt });
    await s.newSession("/p");
    await s.prompt([{ type: "text", text: "a" }]).catch(() => undefined);
    const p = s.prompt([{ type: "text", text: "b" }]);
    expect(conv.state.banner).toBeUndefined();
    await vi.runAllTimersAsync();
    await p;
  });

  it("cancel sends session/cancel for the current session", async () => {
    const cancel = vi.fn().mockResolvedValue(undefined);
    const { s } = setup({ newSession: vi.fn().mockResolvedValue({ sessionId: "s1" }), cancel });
    await s.newSession("/p");
    await s.cancel();
    expect(cancel).toHaveBeenCalledWith({ sessionId: "s1" });
  });

  it("load switches to the session before the replay arrives", async () => {
    const conv0 = { conv: undefined as Conversation | undefined };
    const loadSession = vi.fn().mockImplementation(() => {
      // tenon replays after its reply; the id must already be accepted.
      conv0.conv?.apply({ sessionId: "s0", update: { sessionUpdate: "user_message_chunk", content: { type: "text", text: "old" } } });
      return Promise.resolve({ _meta: { tenon: { provider: "P" } } });
    });
    const { s, conv } = setup({ loadSession });
    conv0.conv = conv;
    conv.addUser("stale");
    const loading = s.load("s0", "/p");
    await vi.runAllTimersAsync();
    await loading;
    expect(loadSession).toHaveBeenCalledWith({ sessionId: "s0", cwd: "/p", mcpServers: [] });
    expect(conv.state.sessionId).toBe("s0");
    expect(conv.state.items).toEqual([{ kind: "user", text: "old" }]);
    expect(conv.state.meta).toEqual({ provider: "P" });
  });

  it("load shows no home while the history loads: tenon replays it after its reply", async () => {
    let reply: (v: object) => void = () => undefined;
    const loadSession = vi.fn().mockReturnValue(new Promise((r) => (reply = r)));
    const { s, conv } = setup({ loadSession });
    const loading = s.load("s0", "/p");
    // begin() emptied the thread: still no home.
    expect(conv.state.items).toEqual([]);
    expect(conv.state.loading).toBe(true);
    expect(homeVisible(conv.state)).toBe(false);
    reply({});
    await Promise.resolve();
    await Promise.resolve();
    // The reply is in, the replay right behind it on the wire: still loading.
    expect(homeVisible(conv.state)).toBe(false);
    conv.apply({ sessionId: "s0", update: { sessionUpdate: "user_message_chunk", content: { type: "text", text: "old" } } });
    await vi.runAllTimersAsync();
    await loading;
    expect(conv.state.loading).toBe(false);
    expect(homeVisible(conv.state)).toBe(false);
  });

  it("an empty loaded session ends on the home; a failed load clears loading", async () => {
    const { s, conv } = setup({ loadSession: vi.fn().mockResolvedValueOnce({}).mockRejectedValueOnce(new Error("gone")) });
    const loading = s.load("s0", "/p");
    await vi.runAllTimersAsync();
    await loading;
    expect(homeVisible(conv.state)).toBe(true);
    await expect(s.load("s1", "/p")).rejects.toThrow("gone");
    expect(conv.state.loading).toBe(false);
  });

  it("a failed load rolls back to the session that was open", async () => {
    const loadSession = vi.fn().mockRejectedValue(new RequestError(-32603, "unknown session: gone"));
    const { s, conv } = setup({ newSession: vi.fn().mockResolvedValue({ sessionId: "s1", _meta: { tenon: { provider: "P" } } }), loadSession });
    await s.newSession("/p");
    conv.addUser("kept");
    conv.apply({ sessionId: "s1", update: { sessionUpdate: "available_commands_update", availableCommands: [{ name: "fix", description: "d" }] } });
    await expect(s.load("gone", "/p")).rejects.toThrow("unknown session: gone");
    expect(conv.state).toMatchObject({
      sessionId: "s1",
      cwd: "/p",
      meta: { provider: "P" },
      items: [{ kind: "user", text: "kept" }],
      commands: [{ name: "fix", description: "d" }],
      banner: { kind: "error", text: "unknown session: gone" },
    });
    // Updates of the restored session are applied again.
    conv.apply({ sessionId: "s1", update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "ok" } } });
    expect(conv.state.items).toHaveLength(2);
  });

  it("session/new and session/load replace the meta entirely (a fallback does not leak)", async () => {
    const newSession = vi.fn().mockResolvedValue({ sessionId: "s1", _meta: { tenon: { provider: "a", jurisdiction: "eu" } } });
    const loadSession = vi.fn().mockResolvedValue({ _meta: { tenon: { provider: "b", providerName: "B", jurisdiction: "local" } } });
    const { s, conv } = setup({ newSession, loadSession });
    await s.newSession("/p");
    conv.apply({
      sessionId: "s1",
      update: { sessionUpdate: "usage_update", used: 1, size: 2, _meta: { tenon: { provider: "us1", providerName: "US", jurisdiction: "non-eu" } } },
    });
    expect(conv.state.meta).toEqual({ provider: "us1", providerName: "US", jurisdiction: "non-eu" });
    const loading = s.load("s0", "/p");
    await vi.runAllTimersAsync();
    await loading;
    expect(conv.state.meta).toEqual({ provider: "b", providerName: "B", jurisdiction: "local" });
    conv.apply({
      sessionId: "s0",
      update: { sessionUpdate: "usage_update", used: 1, size: 2, _meta: { tenon: { provider: "us1", jurisdiction: "non-eu" } } },
    });
    await s.newSession("/p");
    expect(conv.state.meta).toEqual({ provider: "a", jurisdiction: "eu" });
  });

  it("list fills the sessions with title and date", async () => {
    const listSessions = vi.fn().mockResolvedValue({
      sessions: [
        { sessionId: "b", cwd: "/p", title: "second", updatedAt: "2026-10-28T10:00:00Z" },
        { sessionId: "a", cwd: "/p" },
      ],
    });
    const { s, conv } = setup({ listSessions });
    await s.list("/p");
    expect(listSessions).toHaveBeenCalledWith({ cwd: "/p" });
    expect(conv.state.sessions).toEqual([
      { sessionId: "b", title: "second", updatedAt: "2026-10-28T10:00:00Z" },
      { sessionId: "a", title: "", updatedAt: "" },
    ]);
  });
});
