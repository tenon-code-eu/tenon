// Safe rendering for the chat webview. Elements are created one by one with
// createElement / textContent from a fixed tag set (p, pre, code, em, strong,
// ul, ol, li, a, h1–h4, blockquote, plus div/button for the Copy control):
// raw HTML in model output stays text, and only `https:` links become `a`.
// The composer extras (chips, `/` completions, sessions) use span, details,
// summary, time and button, with text only.
// Nothing here ever assigns innerHTML.
import type { Item } from "../src/model";

export type Strings = Record<string, string>;

function str(strings: Strings, key: string): string {
  return strings[key] ?? key;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) {
    e.className = cls;
  }
  if (text !== undefined) {
    e.textContent = text;
  }
  return e;
}

/** A link target is kept only when it is an absolute https URL. */
export function safeHref(url: string): string | undefined {
  if (!/^https:\/\/[^\s]/.test(url)) {
    return undefined;
  }
  try {
    return new URL(url).protocol === "https:" ? url : undefined;
  } catch {
    return undefined;
  }
}

// ---- inline ----

/** Appends the inline Markdown of src (code, strong, em, links) to parent. */
function inline(parent: Node, src: string, links = true): void {
  let text = "";
  const flush = () => {
    if (text) {
      parent.appendChild(document.createTextNode(text));
      text = "";
    }
  };
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    if (c === "\\" && i + 1 < src.length && /[\\`*_[\]()#>\-+.!]/.test(src[i + 1])) {
      text += src[i + 1];
      i += 2;
      continue;
    }
    if (c === "`") {
      let n = 1;
      while (src[i + n] === "`") {
        n++;
      }
      const fence = "`".repeat(n);
      const end = src.indexOf(fence, i + n);
      if (end > i + n - 1) {
        flush();
        parent.appendChild(el("code", undefined, src.slice(i + n, end).replace(/^ (.*) $/, "$1")));
        i = end + n;
        continue;
      }
      text += fence;
      i += n;
      continue;
    }
    if ((c === "*" || c === "_") && src[i + 1] === c) {
      const end = src.indexOf(c + c, i + 2);
      if (end > i + 2) {
        flush();
        const strong = el("strong");
        inline(strong, src.slice(i + 2, end), links);
        parent.appendChild(strong);
        i = end + 2;
        continue;
      }
    }
    if ((c === "*" || c === "_") && src[i + 1] !== undefined && src[i + 1] !== " " && src[i + 1] !== c) {
      // `_` only opens at a word start, so snake_case stays text.
      const opens = c === "*" || i === 0 || !/[\p{L}\p{N}]/u.test(src[i - 1]);
      let end = src.indexOf(c, i + 1);
      while (end > 0 && src[end + 1] === c) {
        end = src.indexOf(c, end + 2);
      }
      const closes = end > i + 1 && src[end - 1] !== " " && (c === "*" || !/[\p{L}\p{N}]/u.test(src[end + 1] ?? ""));
      if (opens && closes) {
        flush();
        const em = el("em");
        inline(em, src.slice(i + 1, end), links);
        parent.appendChild(em);
        i = end + 1;
        continue;
      }
    }
    if (c === "[" && links) {
      const m = /^\[([^\]\n]*)\]\(\s*([^\s()]*)(?:\s+"[^"\n]*")?\s*\)/.exec(src.slice(i));
      if (m) {
        const href = safeHref(m[2]);
        if (href) {
          flush();
          const a = el("a");
          a.setAttribute("href", href);
          inline(a, m[1], false);
          parent.appendChild(a);
        } else {
          // Not https: the whole source stays visible as text.
          text += m[0];
        }
        i += m[0].length;
        continue;
      }
    }
    text += c;
    i++;
  }
  flush();
}

// ---- blocks ----

const FENCE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
const HEADING = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const QUOTE = /^ {0,3}> ?(.*)$/;
const BULLET = /^ {0,3}[-*+]\s+(.*)$/;
const ORDERED = /^ {0,3}\d{1,9}[.)]\s+(.*)$/;

function startsBlock(line: string): boolean {
  return FENCE.test(line) || HEADING.test(line) || QUOTE.test(line) || BULLET.test(line) || ORDERED.test(line);
}

function codeBlock(code: string): HTMLElement {
  const box = el("div", "code");
  const copy = el("button", "copy");
  copy.setAttribute("type", "button");
  copy.dataset.action = "copy";
  const pre = el("pre");
  pre.appendChild(el("code", undefined, code));
  box.append(copy, pre);
  return box;
}

function blocks(parent: Node, lines: string[]): void {
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === "") {
      i++;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const marker = fence[1];
      const body: string[] = [];
      i++;
      while (i < lines.length) {
        const close = /^ {0,3}(`{3,}|~{3,})\s*$/.exec(lines[i]);
        if (close && close[1][0] === marker[0] && close[1].length >= marker.length) {
          i++;
          break;
        }
        body.push(lines[i]);
        i++;
      }
      parent.appendChild(codeBlock(body.join("\n")));
      continue;
    }
    const heading = HEADING.exec(line);
    if (heading) {
      const level = Math.min(heading[1].length, 4) as 1 | 2 | 3 | 4;
      const h = el(`h${level}`);
      inline(h, heading[2]);
      parent.appendChild(h);
      i++;
      continue;
    }
    if (QUOTE.test(line)) {
      const inner: string[] = [];
      for (let m; i < lines.length && (m = QUOTE.exec(lines[i])); i++) {
        inner.push(m[1]);
      }
      const q = el("blockquote");
      blocks(q, inner);
      parent.appendChild(q);
      continue;
    }
    const bullet = BULLET.test(line);
    if (bullet || ORDERED.test(line)) {
      const re = bullet ? BULLET : ORDERED;
      const list = el(bullet ? "ul" : "ol");
      const items: string[][] = [];
      while (i < lines.length) {
        const m = re.exec(lines[i]);
        if (m) {
          items.push([m[1]]);
        } else if (lines[i].trim() !== "" && /^\s+/.test(lines[i]) && items.length > 0) {
          items[items.length - 1].push(lines[i].trim());
        } else {
          break;
        }
        i++;
      }
      for (const it of items) {
        const li = el("li");
        inline(li, it.join("\n"));
        list.appendChild(li);
      }
      parent.appendChild(list);
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== "" && (para.length === 0 || !startsBlock(lines[i]))) {
      para.push(lines[i]);
      i++;
    }
    const p = el("p");
    inline(p, para.join("\n"));
    parent.appendChild(p);
  }
}

/** Renders Markdown without raw HTML; see the file comment for the tag set. */
export function renderMarkdown(src: string): DocumentFragment {
  const frag = document.createDocumentFragment();
  blocks(frag, src.replace(/\r\n?/g, "\n").split("\n"));
  return frag;
}

// ---- items ----

/** CSS class per ACP permission option kind; anything else is "other". */
const OPTION_CLASS: Record<string, string> = {
  allow_once: "allow-once",
  allow_always: "allow-always",
  reject_once: "reject-once",
  reject_always: "reject-always",
};

function toolSummary(item: Extract<Item, { kind: "tool" }>, strings: Strings): HTMLElement {
  const summary = el("summary");
  summary.append(
    el("span", "state", str(strings, `tool_${item.status}`)),
    el("span", "title", item.title),
  );
  return summary;
}

/** One thread entry; buttons carry data-action for the webview's event delegation. */
export function renderItem(item: Item, strings: Strings): HTMLElement {
  switch (item.kind) {
    case "user": {
      const e = el("div", "item user");
      e.appendChild(el("div", "text", item.text));
      return e;
    }
    case "agent": {
      const e = el("div", "item agent");
      e.appendChild(renderMarkdown(item.text));
      return e;
    }
    case "thought": {
      const e = el("details", "item thought");
      e.append(el("summary", undefined, str(strings, "thought")), el("div", "text", item.text));
      return e;
    }
    case "tool": {
      const e = el("details", "item tool");
      e.dataset.status = item.status;
      e.dataset.toolCallId = item.id;
      e.appendChild(toolSummary(item, strings));
      if (item.output) {
        e.appendChild(el("pre", "output", item.output));
      }
      if (item.diff) {
        const b = el("button", "open-diff", str(strings, "openDiff"));
        b.setAttribute("type", "button");
        b.dataset.action = "openDiff";
        b.dataset.toolCallId = item.id;
        e.appendChild(b);
      }
      return e;
    }
    case "permission": {
      // Answered cards stay in the thread, disabled, with the answer shown.
      const closed = item.answered !== undefined || item.refused === true;
      const e = el("div", "item permission");
      if (item.toolCallId) {
        e.dataset.toolCallId = item.toolCallId;
      }
      e.appendChild(el("div", "title", item.title));
      const row = el("div", "options");
      for (const o of item.options) {
        const b = el("button", `option ${Object.prototype.hasOwnProperty.call(OPTION_CLASS, o.kind) ? OPTION_CLASS[o.kind] : "other"}`, o.name);
        b.setAttribute("type", "button");
        b.dataset.action = "permission";
        b.dataset.requestId = item.requestId;
        b.dataset.optionId = o.optionId;
        b.disabled = closed;
        if (item.answered === o.optionId) {
          b.classList.add("chosen");
        }
        row.appendChild(b);
      }
      e.appendChild(row);
      if (closed) {
        e.dataset.answered = "true";
        const chosen = item.options.find((o) => o.optionId === item.answered);
        e.appendChild(el("div", "answer", item.refused || !chosen ? str(strings, "refused") : chosen.name));
      }
      return e;
    }
  }
}

export interface Command {
  name: string;
  description: string;
}

/** Commands completing a leading `/name` (no space typed yet); none otherwise. */
export function matchCommands(input: string, commands: Command[]): Command[] {
  const m = /^\/(\S*)$/.exec(input);
  if (!m) {
    return [];
  }
  return commands.filter((c) => c.name.startsWith(m[1]));
}

/** Enter completes the highlighted command, unless the input already is that command in full (then it sends). */
export function enterCompletes(input: string, matches: Command[], active: number): boolean {
  const m = matches[active];
  return m !== undefined && input !== `/${m.name}`;
}

/** The `/` completion list; active is the highlighted index. */
export function renderCompletions(matches: Command[], active: number): HTMLElement {
  const list = el("div", "completions");
  list.setAttribute("role", "listbox");
  matches.forEach((c, i) => {
    const b = el("button", i === active ? "completion active" : "completion");
    b.setAttribute("type", "button");
    b.setAttribute("role", "option");
    b.dataset.action = "complete";
    b.dataset.name = c.name;
    b.append(el("span", "name", `/${c.name}`), el("span", "description", c.description));
    list.appendChild(b);
  });
  return list;
}

/** Attachment chips above the input, each with a remove button. */
export function renderAttachments(list: { id: string; label: string }[], strings: Strings): HTMLElement {
  const row = el("div", "attachments");
  for (const a of list) {
    const chip = el("span", "chip");
    chip.appendChild(el("span", "label", a.label));
    const x = el("button", "detach", "×");
    x.setAttribute("type", "button");
    x.setAttribute("aria-label", str(strings, "detach"));
    x.title = str(strings, "detach");
    x.dataset.action = "detach";
    x.dataset.id = a.id;
    chip.appendChild(x);
    row.appendChild(chip);
  }
  return row;
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (iso === "" || Number.isNaN(d.getTime())) {
    return "";
  }
  return d.toLocaleString(document.documentElement.lang || undefined, { dateStyle: "short", timeStyle: "short" });
}

/** Past sessions (tenon's order, most recent first); a click loads one. */
export function renderSessions(
  sessions: { sessionId: string; title: string; updatedAt: string }[],
  current: string | undefined,
  strings: Strings,
): HTMLDetailsElement {
  const d = el("details", "sessions");
  d.appendChild(el("summary", undefined, `${str(strings, "sessions")} (${sessions.length})`));
  const list = el("div", "session-list");
  for (const s of sessions) {
    const b = el("button", s.sessionId === current ? "session current" : "session");
    b.setAttribute("type", "button");
    b.dataset.action = "load";
    b.dataset.sessionId = s.sessionId;
    b.appendChild(el("span", "title", s.title || str(strings, "untitled")));
    const when = formatDate(s.updatedAt);
    if (when) {
      const t = el("time", undefined, when);
      t.setAttribute("datetime", s.updatedAt);
      b.appendChild(t);
    }
    list.appendChild(b);
  }
  d.appendChild(list);
  return d;
}
