import type { SessionNotification } from "@agentclientprotocol/sdk";
import { describe, expect, it } from "vitest";
import { DIFF_SCHEME, diffTitle, parseDiffPath, sideText } from "../../src/diff";
import { Conversation } from "../../src/model";

function write(toolCallId: string, oldText: string | undefined, newText: string): SessionNotification {
  const diff: Record<string, string> = { type: "diff", path: "/p/a.txt", newText };
  if (oldText !== undefined) {
    diff.oldText = oldText;
  }
  return {
    sessionId: "s1",
    update: { sessionUpdate: "tool_call_update", toolCallId, status: "completed", content: [diff] },
  } as unknown as SessionNotification;
}

function conv(): Conversation {
  const c = new Conversation();
  c.begin("s1", "/p");
  return c;
}

describe("diff documents (source: the tool item's diff)", () => {
  it("serves the old and new side of a tool item", () => {
    const c = conv();
    c.apply(write("t1-call_1", "v1", "v2"));
    expect(sideText(c, "/t1-call_1/old/a.txt")).toBe("v1");
    expect(sideText(c, "/t1-call_1/new/a.txt")).toBe("v2");
  });

  it("a new file has an empty old side", () => {
    const c = conv();
    c.apply(write("t1-call_1", undefined, "x"));
    expect(sideText(c, "/t1-call_1/old/a.txt")).toBe("");
  });

  it("each turn's write keeps its own diff (tenon's ids carry the turn)", () => {
    const c = conv();
    c.apply(write("t1-call_1", "v1", "v2"));
    c.apply(write("t2-call_1", "v2", "v3"));
    expect(sideText(c, "/t1-call_1/new/a.txt")).toBe("v2");
    expect(sideText(c, "/t2-call_1/old/a.txt")).toBe("v2");
    expect(sideText(c, "/t2-call_1/new/a.txt")).toBe("v3");
  });

  it("an unknown id or a thread cleared gives an empty document", () => {
    const c = conv();
    c.apply(write("t1-call_1", "v1", "v2"));
    expect(sideText(c, "/t9-call_1/new/a.txt")).toBe("");
    c.begin("s2", "/p");
    expect(sideText(c, "/t1-call_1/new/a.txt")).toBe("");
  });

  it("uses the tenon-diff scheme and a title with the file name", () => {
    expect(DIFF_SCHEME).toBe("tenon-diff");
    expect(diffTitle("/p/dir/a.txt")).toBe("a.txt (tenon)");
  });

  it("parses back the tool call id and side, never the content", () => {
    expect(parseDiffPath(`/${encodeURIComponent("t2-parent/call 1")}/old/a.txt`)).toEqual({ id: "t2-parent/call 1", side: "old" });
    expect(parseDiffPath("/c1/new/a.txt")).toEqual({ id: "c1", side: "new" });
    expect(parseDiffPath("/c1/both/a.txt")).toBeUndefined();
    expect(parseDiffPath("/%E0%A4%A/old/a")).toBeUndefined();
  });
});
