import type { SessionNotification, SessionUpdate } from "@agentclientprotocol/sdk";
import { describe, expect, it } from "vitest";
import { Conversation, type ConversationState } from "../../src/model";
import { computeStats } from "../../src/stats";

const SID = "s1";

function n(update: SessionUpdate, sessionId = SID): SessionNotification {
  return { sessionId, update };
}

function usage(provider: string, providerName: string, jurisdiction: string, used: number, size: number, input = used, output = 5) {
  return n({
    sessionUpdate: "usage_update",
    used,
    size,
    _meta: { tenon: { provider, providerName, jurisdiction, input, output, sessionTokens: 0 } },
  });
}

function write(id: string, path: string, oldText: string | undefined, status: "completed" | "failed" = "completed") {
  return [
    n({ sessionUpdate: "tool_call", toolCallId: id, title: `write_file · ${path}`, kind: "edit", status: "pending" }),
    n({
      sessionUpdate: "tool_call_update",
      toolCallId: id,
      status,
      content: [oldText === undefined ? { type: "diff", path, newText: "new" } : { type: "diff", path, oldText, newText: "new" }],
    }),
  ];
}

function conv(cwd = "/p"): Conversation {
  const c = new Conversation();
  c.begin(SID, cwd);
  return c;
}

function base(): ConversationState {
  return { items: [], busy: false, commands: [], sessions: [], attachments: [] };
}

describe("Conversation records usage for the session view", () => {
  it("keeps used and size of the last usage_update, the token sums and one send per call", () => {
    const c = conv();
    c.apply(usage("mistral", "Mistral AI", "eu", 100, 1000, 100, 7));
    c.apply(usage("mistral", "Mistral AI", "eu", 400, 1000, 400, 3));
    expect(c.state.usage).toEqual({ used: 400, size: 1000 });
    expect(c.state.tokens).toEqual({ input: 500, output: 10 });
    expect(c.state.sends).toEqual([{ provider: "Mistral AI", jurisdiction: "eu", n: 2 }]);
  });

  it("records the gauge even without _meta.tenon, and no send", () => {
    const c = conv();
    c.apply(n({ sessionUpdate: "usage_update", used: 5, size: 50 }));
    expect(c.state.usage).toEqual({ used: 5, size: 50 });
    expect(c.state.sends ?? []).toEqual([]);
  });

  it("a new session starts with no usage, tokens or sends", () => {
    const c = conv();
    c.apply(usage("mistral", "Mistral AI", "eu", 100, 1000));
    c.begin("s2", "/p");
    expect(c.state.usage).toBeUndefined();
    expect(c.state.tokens).toBeUndefined();
    expect(c.state.sends ?? []).toEqual([]);
  });

  it("a failed load puts the usage back with the snapshot", () => {
    const c = conv();
    c.apply(usage("mistral", "Mistral AI", "eu", 100, 1000));
    const snap = c.snapshot();
    c.begin("s2", "/p");
    c.restore(snap);
    expect(c.state.usage).toEqual({ used: 100, size: 1000 });
    expect(c.state.sends).toEqual([{ provider: "Mistral AI", jurisdiction: "eu", n: 1 }]);
  });
});

describe("computeStats", () => {
  it("groups the sends by provider and jurisdiction and flags non-EU and unknown", () => {
    const c = conv();
    c.apply(usage("mistral", "Mistral AI", "eu", 100, 1000));
    c.apply(usage("openai", "OpenAI", "non-eu", 100, 1000));
    c.apply(usage("mistral", "Mistral AI", "eu", 100, 1000));
    c.apply(usage("ollama", "Ollama", "local", 100, 1000));
    c.apply(usage("x", "X", "", 100, 1000));
    const s = computeStats(c.state);
    expect(s.sends).toEqual([
      { provider: "Mistral AI", jurisdiction: "eu", n: 2, alert: false },
      { provider: "OpenAI", jurisdiction: "non-eu", n: 1, alert: true },
      { provider: "Ollama", jurisdiction: "local", n: 1, alert: false },
      { provider: "X", n: 1, alert: true },
    ]);
  });

  it("merges sends of the same provider and jurisdiction given separately", () => {
    const state = {
      ...base(),
      sends: [
        { provider: "A", jurisdiction: "eu", n: 1 },
        { provider: "A", jurisdiction: "eu", n: 2 },
        { provider: "A", jurisdiction: "non-eu", n: 1 },
      ],
    };
    expect(computeStats(state).sends).toEqual([
      { provider: "A", jurisdiction: "eu", n: 3, alert: false },
      { provider: "A", jurisdiction: "non-eu", n: 1, alert: true },
    ]);
  });

  it("the gauge comes from the last usage_update", () => {
    const c = conv();
    c.apply(usage("mistral", "Mistral AI", "eu", 100, 1000));
    c.apply(usage("mistral", "Mistral AI", "eu", 250, 2000));
    const s = computeStats(c.state);
    expect(s.used).toBe(250);
    expect(s.size).toBe(2000);
    expect(s.tokensIn).toBe(350);
    expect(s.tokensOut).toBe(10);
  });

  it("no usage yet: no gauge, no sends", () => {
    const s = computeStats(base());
    expect(s.used).toBeUndefined();
    expect(s.size).toBeUndefined();
    expect(s.sends).toEqual([]);
    expect(s.files).toEqual([]);
  });

  it("lists written files once, relative to the folder, A for a created file, the latest diff to open", () => {
    const c = conv("/p");
    for (const u of [
      ...write("t1", "/p/src/a.go", "old"),
      ...write("t2", "/p/new.txt", undefined),
      ...write("t3", "/p/src/a.go", "new"),
      ...write("t4", "/p/new.txt", "new"),
      ...write("t5", "/elsewhere/b.txt", "x"),
      ...write("t6", "/p/failed.txt", "x", "failed"),
    ]) {
      c.apply(u);
    }
    expect(computeStats(c.state).files).toEqual([
      { path: "src/a.go", toolCallId: "t3", op: "M" },
      { path: "new.txt", toolCallId: "t4", op: "A" },
      { path: "/elsewhere/b.txt", toolCallId: "t5", op: "M" },
    ]);
  });

  it("the title is the session's listed title, else the first message", () => {
    const state: ConversationState = {
      ...base(),
      sessionId: SID,
      items: [
        { kind: "user", text: "fix the\nbug" },
        { kind: "user", text: "second" },
      ],
    };
    expect(computeStats(state).title).toBe("fix the bug");
    state.sessions = [{ sessionId: SID, title: "Fix the bug", updatedAt: "" }];
    expect(computeStats(state).title).toBe("Fix the bug");
    expect(computeStats(base()).title).toBeUndefined();
  });

  it("sovereignty: sandbox, profile and policy from the session meta", () => {
    const state: ConversationState = {
      ...base(),
      meta: { sandbox: { mode: "auto", mechanism: "landlock", confined: true }, profile: "strict-local", policy: "acme v2" },
    };
    const s = computeStats(state);
    expect(s.sandboxed).toBe(true);
    expect(s.profile).toBe("strict-local");
    expect(s.policy).toBe("acme v2");
    expect(computeStats({ ...base(), meta: { sandbox: { mechanism: "none" } } }).sandboxed).toBe(false);
    expect(computeStats(base()).sandboxed).toBeUndefined();
  });
});
