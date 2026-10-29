// @vitest-environment happy-dom
// The home screen of the chat webview, the jurisdiction badges and the
// tenon.theme class, rendered by the real webview script (webview/main.ts)
// fed with state messages as the host posts them.
import * as fs from "node:fs";
import * as path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConversationState, Item } from "../../src/model";
import { homeVisible } from "../../src/model";
import type { FromWebview, ToWebview } from "../../src/protocol";
import { applyTheme, renderHome, welcomeVersion } from "../../webview/home";

const STRINGS: Record<string, string> = {
  slogan: "sovereign coding agent",
  shortcuts: "/ skills · Shift+Enter new line · Tab complete",
  link_docs: "docs",
  link_github: "github",
  link_bug: "report a bug",
  tip1: "Tip: one",
  tip2: "Tip: two",
  jurisdiction_eu: "EU",
  jurisdiction_noneu: "non-EU",
  jurisdiction_local: "local",
  jurisdiction_unknown: "unknown jurisdiction",
};

function state(over: Partial<ConversationState> = {}): ConversationState {
  return { items: [], busy: false, commands: [], sessions: [], attachments: [], ...over };
}

describe("welcomeVersion", () => {
  it("follows the TUI: dev when empty, v before a number, as is otherwise", () => {
    expect(welcomeVersion("")).toBe("dev");
    expect(welcomeVersion("0.9.0")).toBe("v0.9.0");
    expect(welcomeVersion("v0.9.0")).toBe("v0.9.0");
    expect(welcomeVersion("devel")).toBe("devel");
  });
});

describe("renderHome", () => {
  it("shows the logo, slogan with version, shortcuts, tip and the three link buttons", () => {
    const home = renderHome(STRINGS, "0.9.0", "Tip: one");
    expect(home.querySelector("svg.logo")).not.toBeNull();
    expect(home.querySelectorAll("svg.logo .half")).toHaveLength(2);
    expect(home.querySelector(".home-word")?.textContent).toBe("T E N O N");
    expect(home.querySelector(".home-slogan")?.textContent).toBe("sovereign coding agent v0.9.0");
    expect(home.querySelector(".home-shortcuts")?.textContent).toBe(STRINGS.shortcuts);
    expect(home.querySelector(".home-tip")?.textContent).toBe("Tip: one");
    const links = [...home.querySelectorAll<HTMLButtonElement>("button[data-action=openLink]")];
    expect(links.map((b) => [b.dataset.link, b.textContent])).toEqual([
      ["docs", "docs"],
      ["github", "github"],
      ["bug", "report a bug"],
    ]);
    // Buttons only: the webview never holds a URL nor navigates.
    expect(home.querySelector("a")).toBeNull();
    expect(home.outerHTML).not.toMatch(/https:|href/);
    expect(home.querySelector(".home-input-slot")).not.toBeNull();
  });

  it("shows the slogan alone while tenon's version is not known", () => {
    expect(renderHome(STRINGS, undefined, "").querySelector(".home-slogan")?.textContent).toBe("sovereign coding agent");
  });
});

describe("homeVisible", () => {
  it("is true until the first message (notices aside), false while busy or under the noFolder and stopped banners", () => {
    expect(homeVisible(state())).toBe(true);
    // tenon's notices at session start come as thoughts: the home stays.
    expect(homeVisible(state({ items: [{ kind: "thought", text: "notice" }] }))).toBe(true);
    expect(homeVisible(state({ banner: { kind: "error", text: "x" } }))).toBe(true);
    expect(homeVisible(state({ items: [{ kind: "user", text: "hi" }] }))).toBe(false);
    expect(homeVisible(state({ items: [{ kind: "agent", text: "x" }] }))).toBe(false);
    expect(homeVisible(state({ busy: true }))).toBe(false);
    expect(homeVisible(state({ banner: { kind: "noFolder", text: "x" } }))).toBe(false);
    expect(homeVisible(state({ banner: { kind: "stopped", text: "x" } }))).toBe(false);
    // A session/load in progress (thread emptied, replay to come).
    expect(homeVisible(state({ sessionId: "s1", loading: true }))).toBe(false);
  });
});

describe("applyTheme", () => {
  it("tenon adds the theme class, auto removes it", () => {
    const root = document.createElement("html");
    applyTheme(root, "tenon");
    expect(root.classList.contains("theme-tenon")).toBe(true);
    applyTheme(root, "auto");
    expect(root.classList.contains("theme-tenon")).toBe(false);
  });
});

describe("chat webview (main.ts)", () => {
  let posted: FromWebview[];
  let innerHTML: ReturnType<typeof vi.fn<(html: string) => void>>;

  beforeEach(async () => {
    // Each test imports a fresh script; earlier ones keep their document
    // listeners, so each gets its own message list.
    const mine: FromWebview[] = [];
    posted = mine;
    document.documentElement.className = "";
    document.body.replaceChildren();
    const app = document.createElement("div");
    app.id = "app";
    document.body.append(app);
    (globalThis as Record<string, unknown>).acquireVsCodeApi = () => ({
      postMessage: (m: FromWebview) => mine.push(m),
      getState: () => undefined,
      setState: () => undefined,
    });
    innerHTML = vi.fn<(html: string) => void>();
    vi.spyOn(Element.prototype, "innerHTML", "set").mockImplementation(innerHTML);
    vi.spyOn(Element.prototype, "outerHTML", "set").mockImplementation(innerHTML);
    vi.spyOn(Element.prototype, "insertAdjacentHTML").mockImplementation((_, html) => innerHTML(html));
    vi.resetModules();
    await import("../../webview/main");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  function show(s: ConversationState, extra: Partial<ToWebview> = {}): void {
    const msg: ToWebview = { type: "state", state: s, strings: STRINGS, version: "0.9.0", theme: "auto", ...extra };
    window.dispatchEvent(new MessageEvent("message", { data: msg }));
  }

  const home = () => document.querySelector<HTMLElement>(".home");

  it("shows the home with the input inside while the thread is empty; the first message hides it", () => {
    show(state({ sessionId: undefined }));
    expect(home()).not.toBeNull();
    expect(home()?.querySelector(".home-slogan")?.textContent).toBe("sovereign coding agent v0.9.0");
    expect(home()?.querySelector("textarea.input")).not.toBeNull();

    // A notice of tenon shows under the home without hiding it.
    show(state({ sessionId: "s1", items: [{ kind: "thought", text: "a notice" }] }));
    expect(home()).not.toBeNull();
    expect(document.querySelector(".thread")?.textContent).toContain("a notice");

    show(state({ sessionId: "s1", items: [{ kind: "user", text: "hi" }] }));
    expect(home()).toBeNull();
    expect(document.querySelector(".thread")?.textContent).toContain("hi");
    // The input is back at the bottom of the panel.
    expect(document.querySelector("#app > .composer textarea.input")).not.toBeNull();
    expect(innerHTML).not.toHaveBeenCalled();
  });

  it("tenon's version, known later, updates the slogan without rebuilding the home (the logo animates once)", () => {
    show(state(), { version: undefined });
    const logo = document.querySelector(".home svg.logo");
    expect(document.querySelector(".home-slogan")?.textContent).toBe("sovereign coding agent");
    show(state(), { version: "" });
    expect(document.querySelector(".home-slogan")?.textContent).toBe("sovereign coding agent dev");
    expect(document.querySelector(".home svg.logo")).toBe(logo);
  });

  it("no home while a session loads (thread emptied before the replay)", () => {
    show(state({ sessionId: "s1", loading: true }));
    expect(home()).toBeNull();
  });

  it("a resumed session shows the thread directly", () => {
    show(state({ sessionId: "s1", items: [{ kind: "user", text: "old" }, { kind: "agent", text: "answer" }] }));
    expect(home()).toBeNull();
  });

  it("never shows the home over the noFolder or stopped banner", () => {
    show(state({ banner: { kind: "noFolder", text: "Open a folder to use tenon" } }));
    expect(home()).toBeNull();
    expect(document.querySelector<HTMLElement>(".banner")?.hidden).toBe(false);
    show(state({ banner: { kind: "stopped", text: "tenon stopped" } }));
    expect(home()).toBeNull();
  });

  it("a link button posts openLink with its id, never a URL", () => {
    show(state());
    document.querySelector<HTMLButtonElement>("button[data-link=github]")?.click();
    expect(posted.filter((m) => m.type === "openLink")).toEqual([{ type: "openLink", id: "github" }]);
  });

  it("a forged link id is not posted", () => {
    show(state());
    const b = document.querySelector<HTMLButtonElement>("button[data-link=docs]");
    if (b) {
      b.dataset.link = "https://evil.example";
      b.click();
    }
    expect(posted.filter((m) => m.type === "openLink")).toEqual([]);
  });

  it("the palette button posts palette", () => {
    show(state());
    document.querySelector<HTMLButtonElement>("button[data-action=palette]")?.click();
    expect(posted.filter((m) => m.type === "palette")).toEqual([{ type: "palette" }]);
  });

  it("an insert message from the host (a skill picked in the palette) fills the input and focuses it", () => {
    show(state());
    window.dispatchEvent(new MessageEvent("message", { data: { type: "insert", text: "/review " } }));
    const input = document.querySelector<HTMLTextAreaElement>("textarea.input");
    expect(input?.value).toBe("/review ");
    expect(document.activeElement).toBe(input);
    // Anything else is ignored.
    window.dispatchEvent(new MessageEvent("message", { data: { type: "insert", text: 42 } }));
    expect(input?.value).toBe("/review ");
  });

  it("tenon.theme: tenon puts the theme class on the root, auto takes it off", () => {
    show(state(), { theme: "tenon" });
    expect(document.documentElement.classList.contains("theme-tenon")).toBe(true);
    show(state(), { theme: "auto" });
    expect(document.documentElement.classList.contains("theme-tenon")).toBe(false);
  });

  function turn(provider: Extract<Item, { kind: "agent" }>["provider"]): Item[] {
    return [
      { kind: "user", text: "hi" },
      { kind: "agent", text: "answer", provider },
    ];
  }

  const badge = () => document.querySelector<HTMLElement>(".item.agent .badge");

  it("an EU answer gets a plain badge: provider · EU", () => {
    show(state({ sessionId: "s1", items: turn({ name: "Mistral", jurisdiction: "eu" }) }));
    expect(badge()?.textContent).toBe("Mistral · EU");
    expect(badge()?.classList.contains("alert")).toBe(false);
  });

  it("a non-EU answer gets the alert badge", () => {
    show(state({ sessionId: "s1", items: turn({ name: "US Corp", jurisdiction: "non-eu" }) }));
    expect(badge()?.textContent).toBe("US Corp · non-EU");
    expect(badge()?.classList.contains("alert")).toBe(true);
  });

  it("an unknown jurisdiction gets the alert badge and the localised unknown label", () => {
    show(state({ sessionId: "s1", items: turn({ name: "Somewhere" }) }));
    expect(badge()?.textContent).toBe("Somewhere · unknown jurisdiction");
    expect(badge()?.classList.contains("alert")).toBe(true);
  });

  it("a known country shows between name and jurisdiction; local is not an alert", () => {
    show(state({ sessionId: "s1", items: turn({ name: "Ollama", country: "FR", jurisdiction: "local" }) }));
    expect(badge()?.textContent).toBe("Ollama · FR · local");
    expect(badge()?.classList.contains("alert")).toBe(false);
  });

  it("badges only the last answer of each completed turn", () => {
    const p = { name: "Mistral", jurisdiction: "eu" };
    const items: Item[] = [
      { kind: "user", text: "one" },
      { kind: "agent", text: "a", provider: p },
      { kind: "tool", id: "t1", title: "read", toolKind: "read", status: "completed", output: "" },
      { kind: "agent", text: "b", provider: p },
      { kind: "user", text: "two" },
      { kind: "agent", text: "c", provider: p },
    ];
    show(state({ sessionId: "s1", items }));
    const badged = () => [...document.querySelectorAll(".item.agent")].map((e) => e.querySelector(".badge") !== null);
    expect(badged()).toEqual([false, true, true]);
    // The turn in progress has no badge yet.
    show(state({ sessionId: "s1", items, busy: true }));
    expect(badged()).toEqual([false, true, false]);
  });
});

describe("logo animation (style.css)", () => {
  const css = fs.readFileSync(path.resolve(__dirname, "../../webview/style.css"), "utf8");

  /** The @media block that starts at index i, braces balanced. */
  function block(i: number): string {
    let depth = 0;
    for (let j = css.indexOf("{", i); j < css.length; j++) {
      depth += css[j] === "{" ? 1 : css[j] === "}" ? -1 : 0;
      if (depth === 0) {
        return css.slice(i, j + 1);
      }
    }
    throw new Error("unbalanced @media block");
  }

  it("runs only without reduced motion, in less than 700 ms", () => {
    const start = css.indexOf("@media (prefers-reduced-motion: no-preference)");
    expect(start).toBeGreaterThan(0);
    const motion = block(start);
    expect(css.replace(motion, "")).not.toMatch(/animation(-name|-duration)?\s*:/);
    const durations = [...motion.matchAll(/(\d+)ms/g)].map((m) => Number(m[1]));
    expect(durations.length).toBeGreaterThan(0);
    expect(Math.max(...durations)).toBeLessThan(700);
  });
});
