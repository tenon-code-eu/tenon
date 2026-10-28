import type { SessionNotification } from "@agentclientprotocol/sdk";
import { describe, expect, it } from "vitest";
import { DIFF_SCHEME, DiffStore, diffTitle, parseDiffPath } from "../../src/diff";

function update(toolCallId: string, content: unknown[]): SessionNotification {
  return {
    sessionId: "s1",
    update: { sessionUpdate: "tool_call_update", toolCallId, content },
  } as SessionNotification;
}

describe("DiffStore", () => {
  it("keeps the diff of a tool_call_update by tool call id", () => {
    const s = new DiffStore();
    s.record(update("c1", [{ type: "diff", path: "/p/a.txt", oldText: "v1", newText: "v2" }]));
    expect(s.get("c1")).toEqual({ path: "/p/a.txt", oldText: "v1", newText: "v2" });
  });

  it("a new file has no old text", () => {
    const s = new DiffStore();
    s.record(update("c1", [{ type: "diff", path: "/p/new.txt", newText: "x" }]));
    expect(s.get("c1")).toEqual({ path: "/p/new.txt", newText: "x" });
  });

  it("ignores updates without a diff", () => {
    const s = new DiffStore();
    s.record(update("c1", [{ type: "content", content: { type: "text", text: "ok" } }]));
    s.record({ sessionId: "s1", update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "x" } } });
    expect(s.get("c1")).toBeUndefined();
  });

  it("keeps only the last 100 diffs", () => {
    const s = new DiffStore();
    for (let i = 0; i < 101; i++) {
      s.record(update(`c${i}`, [{ type: "diff", path: "/p/a", newText: String(i) }]));
    }
    expect(s.get("c0")).toBeUndefined();
    expect(s.get("c1")?.newText).toBe("1");
    expect(s.get("c100")?.newText).toBe("100");
    expect(s.size).toBe(100);
  });
});

describe("diff URIs", () => {
  it("uses the tenon-diff scheme and a title with the file name", () => {
    expect(DIFF_SCHEME).toBe("tenon-diff");
    expect(diffTitle("/p/dir/a.txt")).toBe("a.txt (tenon)");
  });

  it("parses back the tool call id and side, never the content", () => {
    expect(parseDiffPath(`/${encodeURIComponent("parent/call 1")}/old/a.txt`)).toEqual({ id: "parent/call 1", side: "old" });
    expect(parseDiffPath("/c1/new/a.txt")).toEqual({ id: "c1", side: "new" });
    expect(parseDiffPath("/c1/both/a.txt")).toBeUndefined();
    expect(parseDiffPath("/%E0%A4%A/old/a")).toBeUndefined();
  });
});
