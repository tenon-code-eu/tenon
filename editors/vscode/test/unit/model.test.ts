import type { SessionNotification, SessionUpdate } from "@agentclientprotocol/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Conversation } from "../../src/model";

const SID = "s1";

function n(update: SessionUpdate, sessionId = SID): SessionNotification {
  return { sessionId, update };
}

function chunk(kind: "agent_message_chunk" | "agent_thought_chunk" | "user_message_chunk", text: string) {
  return n({ sessionUpdate: kind, content: { type: "text", text } });
}

function conv(): Conversation {
  const c = new Conversation();
  c.begin(SID, "/p");
  return c;
}

describe("Conversation.apply", () => {
  it("merges consecutive agent and thought chunks into one item each", () => {
    const c = conv();
    c.apply(chunk("agent_thought_chunk", "let me "));
    c.apply(chunk("agent_thought_chunk", "think"));
    c.apply(chunk("agent_message_chunk", "hel"));
    c.apply(chunk("agent_message_chunk", "lo"));
    expect(c.state.items).toEqual([
      { kind: "thought", text: "let me think" },
      { kind: "agent", text: "hello" },
    ]);
  });

  it("starts a new agent item after a tool call", () => {
    const c = conv();
    c.apply(chunk("agent_message_chunk", "a"));
    c.apply(n({ sessionUpdate: "tool_call", toolCallId: "t1", title: "read_file", kind: "read", status: "pending" }));
    c.apply(chunk("agent_message_chunk", "b"));
    expect(c.state.items.map((i) => i.kind)).toEqual(["agent", "tool", "agent"]);
  });

  it("folds tool_call and its updates into one tool item", () => {
    const c = conv();
    c.apply(
      n({ sessionUpdate: "tool_call", toolCallId: "t1", title: "shell · ls", kind: "execute", status: "pending" }),
    );
    c.apply(n({ sessionUpdate: "tool_call_update", toolCallId: "t1", status: "in_progress" }));
    c.apply(
      n({
        sessionUpdate: "tool_call_update",
        toolCallId: "t1",
        status: "completed",
        content: [{ type: "content", content: { type: "text", text: "a.txt\nb.txt" } }],
      }),
    );
    expect(c.state.items).toEqual([
      { kind: "tool", id: "t1", title: "shell · ls", toolKind: "execute", status: "completed", output: "a.txt\nb.txt" },
    ]);
  });

  it("keeps a diff from tool content", () => {
    const c = conv();
    c.apply(n({ sessionUpdate: "tool_call", toolCallId: "t1", title: "write_file · a.txt", kind: "edit" }));
    c.apply(
      n({
        sessionUpdate: "tool_call_update",
        toolCallId: "t1",
        status: "completed",
        content: [{ type: "diff", path: "/p/a.txt", oldText: "v1", newText: "v2" }],
      }),
    );
    const item = c.state.items[0];
    expect(item).toMatchObject({ kind: "tool", status: "completed", diff: { path: "/p/a.txt", oldText: "v1", newText: "v2" } });
  });

  it("rebuilds the live items from a replay", () => {
    const live = conv();
    live.addUser("hi");
    live.apply(chunk("agent_message_chunk", "hel"));
    live.apply(chunk("agent_message_chunk", "lo"));
    live.addUser("again");
    live.apply(chunk("agent_message_chunk", "ok"));

    const replay = conv();
    replay.apply(chunk("user_message_chunk", "hi"));
    replay.apply(chunk("agent_message_chunk", "hello"));
    replay.apply(chunk("user_message_chunk", "again"));
    replay.apply(chunk("agent_message_chunk", "ok"));
    expect(replay.state.items).toEqual(live.state.items);
    expect(live.state.items).toEqual([
      { kind: "user", text: "hi" },
      { kind: "agent", text: "hello" },
      { kind: "user", text: "again" },
      { kind: "agent", text: "ok" },
    ]);
  });

  it("merges consecutive user chunks of a replayed message into one item", () => {
    const c = conv();
    c.apply(chunk("user_message_chunk", "fix this"));
    c.apply(n({ sessionUpdate: "user_message_chunk", content: { type: "resource_link", uri: "file:///p/a.go", name: "a.go" } }));
    c.apply(chunk("agent_message_chunk", "done"));
    c.apply(chunk("user_message_chunk", "next"));
    expect(c.state.items).toEqual([
      { kind: "user", text: "fix this\na.go" },
      { kind: "agent", text: "done" },
      { kind: "user", text: "next" },
    ]);
  });

  it("records available commands", () => {
    const c = conv();
    c.apply(
      n({
        sessionUpdate: "available_commands_update",
        availableCommands: [{ name: "fix", description: "Fix it" }],
      }),
    );
    expect(c.state.commands).toEqual([{ name: "fix", description: "Fix it" }]);
  });

  it("ignores updates of another session", () => {
    const c = conv();
    c.apply(n({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "x" } }, "other"));
    expect(c.state.items).toEqual([]);
  });

  it("keeps updates that arrive before the session id is known", () => {
    const c = new Conversation();
    c.apply(chunk("agent_thought_chunk", "notice"));
    c.apply(n({ sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "x" } }, "other"));
    expect(c.state.items).toEqual([]);
    c.begin(SID, "/p");
    expect(c.state.items).toEqual([{ kind: "thought", text: "notice" }]);
    expect(c.state.sessionId).toBe(SID);
  });
});

describe("Conversation.onDidChange", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("coalesces 1000 synchronous chunks into at most one event per 16 ms", () => {
    const c = conv();
    vi.advanceTimersByTime(100);
    const listener = vi.fn();
    c.onDidChange(listener);
    for (let i = 0; i < 1000; i++) {
      c.apply(chunk("agent_message_chunk", "x"));
    }
    // Nothing fires synchronously; the next timer tick fires once.
    expect(listener.mock.calls.length).toBeLessThanOrEqual(2);
    expect(listener).toHaveBeenCalledTimes(0);
    vi.advanceTimersByTime(16);
    expect(listener).toHaveBeenCalledTimes(1);
    expect((listener.mock.calls[0][0] as { items: { text: string }[] }).items[0].text).toHaveLength(1000);

    // A change right after a fire waits for the rest of the 16 ms window.
    c.apply(chunk("agent_message_chunk", "y"));
    vi.advanceTimersByTime(8);
    expect(listener).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(8);
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("does not fire without a change", () => {
    const c = conv();
    vi.advanceTimersByTime(100);
    const listener = vi.fn();
    c.onDidChange(listener);
    vi.advanceTimersByTime(1000);
    expect(listener).not.toHaveBeenCalled();
  });

  it("stops firing for a disposed listener", () => {
    const c = conv();
    const listener = vi.fn();
    c.onDidChange(listener).dispose();
    c.apply(chunk("agent_message_chunk", "x"));
    vi.advanceTimersByTime(16);
    expect(listener).not.toHaveBeenCalled();
  });
});
