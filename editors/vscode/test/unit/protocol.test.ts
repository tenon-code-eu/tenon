import { describe, expect, it } from "vitest";
import { isFromSessionView, isFromWebview, themeOf } from "../../src/protocol";

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
      { type: "detach", id: "a1" },
      { type: "openLink", id: "docs" },
      { type: "openLink", id: "github" },
      { type: "openLink", id: "bug" },
      { type: "palette" },
      { type: "openFileDiff", toolCallId: "t1-call_1" },
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
      { type: "detach", id: "" },
      { type: "detach", id: 1 },
      // Review Focus 5: only the three link ids, never a URL.
      { type: "openLink", id: "evil" },
      { type: "openLink", id: "https://x" },
      { type: "openLink", id: "" },
      { type: "openLink", id: "constructor" },
      { type: "openLink", id: "toString" },
      { type: "openLink" },
      { type: "openLink", id: "docs", url: "https://x" },
      { type: "openLink", url: "https://x" },
      { type: "palette", query: "x" },
      { type: "openFileDiff", toolCallId: "" },
      { type: "openFileDiff", toolCallId: 1 },
      { type: "openFileDiff" },
      { type: "openFileDiff", toolCallId: "t1", path: "/etc/passwd" },
      { type: "send", text: "hi", attachments: [{ type: "resource", resource: { uri: "file:///etc/passwd", text: "x" } }] },
    ]) {
      expect(isFromWebview(m), JSON.stringify(m)).toBe(false);
    }
  });
});

describe("isFromSessionView", () => {
  it("takes only ready and openFileDiff", () => {
    expect(isFromSessionView({ type: "ready" })).toBe(true);
    expect(isFromSessionView({ type: "openFileDiff", toolCallId: "t1" })).toBe(true);
    for (const m of [
      { type: "openFileDiff", toolCallId: "" },
      { type: "send", text: "hi" },
      { type: "openDiff", toolCallId: "t1" },
      { type: "load", sessionId: "s1" },
      { type: "openLink", id: "docs" },
      { type: "palette" },
      null,
      "openFileDiff",
    ]) {
      expect(isFromSessionView(m), JSON.stringify(m)).toBe(false);
    }
  });
});

describe("themeOf (tenon.theme)", () => {
  it("is tenon only for the value tenon; auto for anything else", () => {
    expect(themeOf("tenon")).toBe("tenon");
    for (const v of ["auto", "", undefined, null, 1, "Tenon", "gruvbox"]) {
      expect(themeOf(v), String(v)).toBe("auto");
    }
  });
});
