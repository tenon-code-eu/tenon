// @vitest-environment happy-dom
// The Session view: its webview script (webview/session.ts) fed with stats
// messages as the host posts them, and the host's handling of its messages.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ToSessionView } from "../../src/protocol";
import { handleSessionMessage } from "../../src/sessionMessages";
import type { SessionStats } from "../../src/stats";

const STRINGS: Record<string, string> = {
  untitled: "Untitled session",
  context: "Context",
  percent: "{0}%",
  tokens: "in {0} · out {1}",
  files: "Modified files",
  noFiles: "no files",
  sovereignty: "Sovereignty",
  noSends: "no sends",
  sandboxed: "sandboxed",
  notSandboxed: "not sandboxed",
  profile: "profile {0}",
  policy: "policy {0}",
  jurisdiction_eu: "EU",
  jurisdiction_noneu: "non-EU",
  jurisdiction_local: "local",
  jurisdiction_unknown: "unknown jurisdiction",
};

function stats(over: Partial<SessionStats> = {}): SessionStats {
  return { files: [], sends: [], ...over };
}

describe("session webview (session.ts)", () => {
  let posted: unknown[];
  let innerHTML: ReturnType<typeof vi.fn<(html: string) => void>>;

  beforeEach(async () => {
    const mine: unknown[] = [];
    posted = mine;
    document.documentElement.className = "";
    document.body.replaceChildren();
    const app = document.createElement("div");
    app.id = "app";
    document.body.append(app);
    (globalThis as Record<string, unknown>).acquireVsCodeApi = () => ({
      postMessage: (m: unknown) => mine.push(m),
      getState: () => undefined,
      setState: () => undefined,
    });
    innerHTML = vi.fn<(html: string) => void>();
    vi.spyOn(Element.prototype, "innerHTML", "set").mockImplementation(innerHTML);
    vi.spyOn(Element.prototype, "outerHTML", "set").mockImplementation(innerHTML);
    vi.spyOn(Element.prototype, "insertAdjacentHTML").mockImplementation((_, html) => innerHTML(html));
    vi.resetModules();
    await import("../../webview/session");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  function show(s: SessionStats, theme: "auto" | "tenon" = "auto"): void {
    const msg: ToSessionView = { type: "stats", stats: s, strings: STRINGS, theme };
    window.dispatchEvent(new MessageEvent("message", { data: msg }));
  }

  const text = (sel: string) => document.querySelector(sel)?.textContent;

  it("posts ready once loaded", () => {
    expect(posted).toEqual([{ type: "ready" }]);
  });

  it("shows the title, the gauge (10 cells and %), the tokens", () => {
    show(stats({ title: "Fix the bug", used: 250, size: 1000, tokensIn: 1500, tokensOut: 300 }));
    expect(text(".s-title")).toBe("Fix the bug");
    expect(text(".s-gauge")).toBe("██░░░░░░░░ 25%");
    expect(text(".s-tokens")).toBe("in 1.5k · out 300");
  });

  it("an untitled session and no usage yet", () => {
    show(stats());
    expect(text(".s-title")).toBe("Untitled session");
    expect(text(".s-gauge")).toBe("░░░░░░░░░░ 0%");
    expect(text(".s-files")).toBe("no files");
    expect(text(".s-sends")).toBe("no sends");
  });

  it("a modified file is a button that posts openFileDiff with its tool call id", () => {
    show(stats({ files: [{ path: "src/a.go", toolCallId: "t1-call_1", op: "M" }, { path: "new.txt", toolCallId: "t2-call_1", op: "A" }] }));
    const buttons = [...document.querySelectorAll<HTMLButtonElement>("button[data-action=openFileDiff]")];
    expect(buttons.map((b) => b.textContent)).toEqual(["M src/a.go", "A new.txt"]);
    buttons[1].click();
    expect(posted.slice(1)).toEqual([{ type: "openFileDiff", toolCallId: "t2-call_1" }]);
    expect(innerHTML).not.toHaveBeenCalled();
  });

  it("sends per provider; non-EU and unknown in the alert colour; sandbox, profile, policy", () => {
    show(
      stats({
        sends: [
          { provider: "Mistral AI", jurisdiction: "eu", n: 3, alert: false },
          { provider: "OpenAI", jurisdiction: "non-eu", n: 1, alert: true },
          { provider: "X", n: 2, alert: true },
        ],
        sandboxed: false,
        profile: "strict-local",
        policy: "acme v2",
      }),
    );
    const sends = [...document.querySelectorAll<HTMLElement>(".s-send")];
    expect(sends.map((s) => [s.textContent, s.classList.contains("alert")])).toEqual([
      ["● 3 → Mistral AI · EU", false],
      ["● 1 → OpenAI · non-EU", true],
      ["● 2 → X · unknown jurisdiction", true],
    ]);
    expect(document.querySelector(".s-sandbox")?.textContent).toBe("not sandboxed");
    expect(document.querySelector(".s-sandbox")?.classList.contains("alert")).toBe(true);
    expect(text(".s-profile")).toBe("profile strict-local");
    expect(text(".s-policy")).toBe("policy acme v2");
  });

  it("tenon.theme tenon puts the theme class on the root", () => {
    show(stats(), "tenon");
    expect(document.documentElement.classList.contains("theme-tenon")).toBe(true);
    show(stats(), "auto");
    expect(document.documentElement.classList.contains("theme-tenon")).toBe(false);
  });
});

describe("handleSessionMessage (host)", () => {
  const diffs: Record<string, { path: string; newText: string }> = { "t1-call_1": { path: "/p/a.txt", newText: "v2" } };
  const source = { toolDiff: (id: string) => diffs[id] };

  it("openFileDiff of a tool item with a diff opens it", () => {
    const open = vi.fn().mockResolvedValue(undefined);
    expect(handleSessionMessage({ type: "openFileDiff", toolCallId: "t1-call_1" }, source, open)).toBe("openFileDiff");
    expect(open).toHaveBeenCalledWith("t1-call_1");
  });

  it("ignores an unknown tool call, a malformed message or another type", () => {
    const open = vi.fn().mockResolvedValue(undefined);
    for (const m of [
      { type: "openFileDiff", toolCallId: "nope" },
      { type: "openFileDiff", toolCallId: "" },
      { type: "openFileDiff", toolCallId: "t1-call_1", path: "/etc/passwd" },
      { type: "openDiff", toolCallId: "t1-call_1" },
      { type: "send", text: "hi" },
      { type: "constructor" },
      "x",
    ]) {
      expect(handleSessionMessage(m, source, open), JSON.stringify(m)).toBeUndefined();
    }
    expect(open).not.toHaveBeenCalled();
  });

  it("ready is reported to the caller", () => {
    const open = vi.fn();
    expect(handleSessionMessage({ type: "ready" }, source, open)).toBe("ready");
  });
});
