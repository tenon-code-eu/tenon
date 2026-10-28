// @vitest-environment happy-dom
import { describe, expect, it } from "vitest";
import type { Item } from "../../src/model";
import { matchCommands, renderAttachments, renderCompletions, renderItem, renderMarkdown, renderSessions } from "../../webview/render";

function html(src: string): HTMLElement {
  const div = document.createElement("div");
  div.append(renderMarkdown(src));
  return div;
}

const ALLOWED = new Set(["P", "PRE", "CODE", "EM", "STRONG", "UL", "OL", "LI", "A", "H1", "H2", "H3", "H4", "BLOCKQUOTE", "BUTTON", "DIV"]);

function tags(el: Element): string[] {
  return [...el.querySelectorAll("*")].map((e) => e.tagName);
}

describe("renderMarkdown (Review Focus 3)", () => {
  it("renders raw HTML as text", () => {
    const el = html("look <img src=x onerror=alert(1)> here");
    expect(el.querySelector("img")).toBeNull();
    expect(el.textContent).toContain("<img src=x onerror=alert(1)>");
  });

  it("drops a javascript: link but keeps its text", () => {
    const el = html("[a](javascript:alert(1))");
    expect(el.querySelector("a")).toBeNull();
    expect(el.textContent).toContain("a");
  });

  it("keeps an https link", () => {
    const el = html("see [a](https://x.eu) now");
    const a = el.querySelector("a");
    expect(a).not.toBeNull();
    expect(a?.getAttribute("href")).toBe("https://x.eu");
    expect(a?.textContent).toBe("a");
  });

  it("drops http, data and relative links", () => {
    for (const url of ["http://x.eu", "data:text/html,hi", "/etc/passwd", "vscode:extension/x", "HTTPS:x"]) {
      const el = html(`[a](${url})`);
      expect(el.querySelector("a"), url).toBeNull();
    }
  });

  it("puts a fenced block with <script> in pre > code as text", () => {
    const el = html("```html\n<script>alert(1)</script>\n```");
    expect(el.querySelector("script")).toBeNull();
    const code = el.querySelector("pre > code");
    expect(code?.textContent).toBe("<script>alert(1)</script>");
  });

  it("renders headings, lists, emphasis, inline code and quotes with the fixed tag set", () => {
    const el = html(
      "# Title\n\n- **bold** item\n- *em* and `code`\n\n1. one\n2. two\n\n> quoted\n\n#### four\n\ntext <b>x</b>",
    );
    expect(el.querySelector("h1")?.textContent).toBe("Title");
    expect(el.querySelectorAll("ul > li")).toHaveLength(2);
    expect(el.querySelector("strong")?.textContent).toBe("bold");
    expect(el.querySelector("em")?.textContent).toBe("em");
    expect(el.querySelector("li code")?.textContent).toBe("code");
    expect(el.querySelectorAll("ol > li")).toHaveLength(2);
    expect(el.querySelector("blockquote")?.textContent).toContain("quoted");
    expect(el.querySelector("h4")?.textContent).toBe("four");
    expect(el.querySelector("b")).toBeNull();
    for (const t of tags(el)) {
      expect(ALLOWED.has(t), t).toBe(true);
    }
  });

  it("never sets an attribute other than href, class or data-*", () => {
    const el = html('[x](https://a.eu "t") <a href="javascript:1" onclick="x">y</a> ```\nz\n```');
    for (const e of el.querySelectorAll("*")) {
      for (const a of e.getAttributeNames()) {
        expect(a === "href" || a === "class" || a === "type" || a.startsWith("data-"), a).toBe(true);
      }
    }
  });

  it("gives code blocks a Copy button", () => {
    const el = html("```\nnpm test\n```");
    expect(el.querySelector("button[data-action=copy]")).not.toBeNull();
  });
});

describe("renderItem", () => {
  const strings = { thought: "Thinking", copy: "Copy", openDiff: "Open diff", tool_completed: "fait", refused: "Refusé" };

  it("renders a user message as plain text", () => {
    const el = renderItem({ kind: "user", text: "**hi** <img>" }, strings);
    expect(el.querySelector("strong")).toBeNull();
    expect(el.querySelector("img")).toBeNull();
    expect(el.textContent).toContain("**hi** <img>");
  });

  it("collapses thoughts by default", () => {
    const el = renderItem({ kind: "thought", text: "hmm" }, strings);
    const details = el.tagName === "DETAILS" ? el : el.querySelector("details");
    expect(details).not.toBeNull();
    expect((details as HTMLDetailsElement).open).toBe(false);
  });

  it("renders a tool item with its title, status and escaped output", () => {
    const item: Item = {
      kind: "tool",
      id: "t1",
      title: "shell · <b>ls</b>",
      toolKind: "execute",
      status: "completed",
      output: "<img src=x onerror=alert(1)>",
    };
    const el = renderItem(item, strings);
    expect(el.querySelector("img")).toBeNull();
    expect(el.querySelector("b")).toBeNull();
    expect(el.textContent).toContain("shell · <b>ls</b>");
    expect(el.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(el.dataset.status).toBe("completed");
    expect(el.querySelector("summary .state")?.textContent).toBe("fait");
  });

  it("maps permission option kinds to a fixed class set", () => {
    const el = renderItem(
      {
        kind: "permission",
        requestId: "p1",
        title: "write a.txt",
        options: [
          { optionId: "allow", name: "Allow", kind: "allow_once" },
          { optionId: "x", name: "Odd", kind: 'evil" onclick="x item user' },
        ],
      },
      strings,
    );
    const [allow, odd] = [...el.querySelectorAll("button")];
    expect([...allow.classList]).toEqual(["option", "allow-once"]);
    expect([...odd.classList]).toEqual(["option", "other"]);
    expect(odd.getAttribute("onclick")).toBeNull();
  });

  it("an answered card stays, disabled, and shows the chosen option name", () => {
    const el = renderItem(
      {
        kind: "permission",
        requestId: "p1",
        title: "write a.txt",
        options: [
          { optionId: "allow", name: "Allow", kind: "allow_once" },
          { optionId: "reject", name: "Reject", kind: "reject_once" },
        ],
        answered: "reject",
      },
      strings,
    );
    const buttons = [...el.querySelectorAll("button")];
    expect(buttons.every((b) => b.disabled)).toBe(true);
    expect(el.querySelector(".answer")?.textContent).toBe("Reject");
    expect(el.dataset.answered).toBe("true");
  });

  it("a refused (cancelled) card shows Refused, disabled", () => {
    const el = renderItem(
      {
        kind: "permission",
        requestId: "p1",
        toolCallId: "c1",
        title: "write a.txt",
        options: [{ optionId: "allow", name: "Allow", kind: "allow_once" }],
        refused: true,
      },
      strings,
    );
    expect(el.querySelector("button")?.disabled).toBe(true);
    expect(el.querySelector(".answer")?.textContent).toBe("Refusé");
    expect(el.dataset.toolCallId).toBe("c1");
  });

  it("a pending card has enabled buttons and no answer", () => {
    const el = renderItem(
      { kind: "permission", requestId: "p1", title: "t", options: [{ optionId: "allow", name: "Allow", kind: "allow_once" }] },
      strings,
    );
    expect(el.querySelector("button")?.disabled).toBe(false);
    expect(el.querySelector(".answer")).toBeNull();
  });

  it("offers Open diff for a tool item with a diff", () => {
    const el = renderItem(
      { kind: "tool", id: "t1", title: "write_file", toolKind: "edit", status: "completed", output: "", diff: { path: "/a", newText: "x" } },
      strings,
    );
    const b = el.querySelector<HTMLButtonElement>("button[data-action=openDiff]");
    expect(b?.dataset.toolCallId).toBe("t1");
  });
});

describe("composer extras", () => {
  const commands = [
    { name: "fix", description: "Fix it" },
    { name: "fmt", description: "Format" },
    { name: "review", description: "Review <b>x</b>" },
  ];

  it("matchCommands completes a leading /name prefix only", () => {
    expect(matchCommands("/f", commands).map((c) => c.name)).toEqual(["fix", "fmt"]);
    expect(matchCommands("/", commands)).toHaveLength(3);
    expect(matchCommands("/re", commands).map((c) => c.name)).toEqual(["review"]);
    expect(matchCommands("/fix it", commands)).toEqual([]);
    expect(matchCommands("fix", commands)).toEqual([]);
    expect(matchCommands(" /f", commands)).toEqual([]);
    expect(matchCommands("/zz", commands)).toEqual([]);
  });

  it("renderCompletions lists names and descriptions as text, the active one marked", () => {
    const el = renderCompletions(commands, 2);
    const items = el.querySelectorAll("[data-action=complete]");
    expect(items).toHaveLength(3);
    expect((items[2] as HTMLElement).dataset.name).toBe("review");
    expect(items[2].classList.contains("active")).toBe(true);
    expect(el.querySelector("b")).toBeNull();
    expect(el.textContent).toContain("Review <b>x</b>");
  });

  it("renderAttachments shows removable chips", () => {
    const el = renderAttachments([{ id: "a1", label: "<img src=x>.go:1-2" }], { detach: "Remove" });
    const chip = el.querySelector(".chip");
    expect(chip?.textContent).toContain("<img src=x>.go:1-2");
    expect(el.querySelector("img")).toBeNull();
    const x = el.querySelector<HTMLElement>("[data-action=detach]");
    expect(x?.dataset.id).toBe("a1");
    expect(x?.getAttribute("aria-label")).toBe("Remove");
  });

  it("renderSessions lists sessions with title and date; the current one is marked", () => {
    const el = renderSessions(
      [
        { sessionId: "s2", title: "second <script>", updatedAt: "2026-10-28T10:00:00Z" },
        { sessionId: "s1", title: "", updatedAt: "" },
      ],
      "s1",
      { sessions: "Sessions", untitled: "Untitled" },
    );
    expect(el.querySelector("summary")?.textContent).toContain("Sessions");
    const rows = el.querySelectorAll<HTMLElement>("[data-action=load]");
    expect([...rows].map((r) => r.dataset.sessionId)).toEqual(["s2", "s1"]);
    expect(rows[0].textContent).toContain("second <script>");
    expect(el.querySelector("script")).toBeNull();
    expect(rows[1].textContent).toContain("Untitled");
    expect(rows[1].classList.contains("current")).toBe(true);
    expect(rows[0].querySelector("time")?.getAttribute("datetime")).toBe("2026-10-28T10:00:00Z");
  });
});
