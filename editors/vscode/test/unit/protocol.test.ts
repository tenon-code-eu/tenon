import { describe, expect, it } from "vitest";
import { isFromWebview } from "../../src/protocol";

describe("isFromWebview", () => {
  it("accepts every well-formed message", () => {
    for (const m of [
      { type: "send", text: "hi" },
      { type: "cancel" },
      { type: "permission", requestId: "p1", optionId: "allow" },
      { type: "openDiff", toolCallId: "t1" },
      { type: "load", sessionId: "s1" },
      { type: "newSession" },
      { type: "pickFolder" },
      { type: "ready" },
      { type: "restart" },
      { type: "showLogs" },
    ]) {
      expect(isFromWebview(m), JSON.stringify(m)).toBe(true);
    }
  });

  it("rejects malformed messages", () => {
    for (const m of [
      null,
      undefined,
      "send",
      42,
      [],
      {},
      { type: "eval", code: "1" },
      { type: "send" },
      { type: "send", text: 1 },
      { type: "send", text: "hi", extra: true },
      { type: "permission", requestId: "p1" },
      { type: "permission", requestId: 1, optionId: "x" },
      { type: "openDiff", toolCallId: null },
      { type: "load", sessionId: "" },
      { type: "cancel", now: true },
      { type: "__proto__" },
      { type: "toString" },
    ]) {
      expect(isFromWebview(m), JSON.stringify(m)).toBe(false);
    }
  });
});
