// Chat webview: renders the state posted by the extension host (whole state
// on every change) and posts user actions back. Strings come with the state;
// this bundle has no l10n and makes no network request.
import { type ConversationState, homeVisible, type Item } from "../src/model";
import { type FromWebview, isLinkId, themeOf, type ToChatWebview } from "../src/protocol";
import { applyTheme, renderHome, sloganText } from "./home";
import {
  badgedAnswers,
  enterCompletes,
  matchCommands,
  renderAttachments,
  renderBadge,
  renderCompletions,
  renderItem,
  renderSessions,
  type Strings,
} from "./render";

interface VsCodeApi {
  postMessage(m: FromWebview): void;
  getState(): unknown;
  setState(s: unknown): void;
}

declare function acquireVsCodeApi(): VsCodeApi;

const vscode = acquireVsCodeApi();
const post = (m: FromWebview) => vscode.postMessage(m);

let strings: Strings = {};
let state: ConversationState | undefined;
/** tenon's version, from the host once tenon answered initialize. */
let version: string | undefined;
/** Tip of the day: drawn once per webview load, among the strings tip1…tipN. */
const tipDraw = Math.random();

function t(key: string): string {
  return strings[key] ?? key;
}

function make<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  return e;
}

function button(cls: string, action: string): HTMLButtonElement {
  const b = make("button", cls);
  b.type = "button";
  b.dataset.action = action;
  return b;
}

// ---- static layout ----

const app = document.getElementById("app") as HTMLElement;
const banner = make("div", "banner");
banner.hidden = true;
const sessions = make("div", "sessions-slot");
const thread = make("div", "thread");
const composer = make("div", "composer");
const chips = make("div", "chips-slot");
const completions = make("div", "completions-slot");
const input = make("textarea", "input");
input.rows = 3;
const actions = make("div", "actions");
const busyLabel = make("span", "busy");
const sendButton = button("send", "send");
const stopButton = button("stop", "cancel");
const paletteButton = button("palette", "palette");
actions.append(paletteButton, busyLabel, stopButton, sendButton);
composer.append(chips, completions, input, actions);
app.append(banner, sessions, thread, composer);

const saved = vscode.getState() as { draft?: unknown } | undefined;
if (typeof saved?.draft === "string") {
  input.value = saved.draft;
}

// ---- rendering ----

/**
 * The home screen while shown, and the strings it was built from: rebuilt
 * only when they change, so the logo animates once and the input keeps its
 * focus; the version, known later, updates the slogan in place.
 */
let home: { el: HTMLElement; key: string } | undefined;

function tip(): string {
  const keys = Object.keys(strings)
    .filter((k) => /^tip\d+$/.test(k))
    .sort((a, b) => Number(a.slice(3)) - Number(b.slice(3)));
  return keys.length > 0 ? t(keys[Math.floor(tipDraw * keys.length)]) : "";
}

/** Moves the input, keeping its focus (a moved element loses it). */
function place(parent: HTMLElement, before: Node | null): void {
  if (composer.parentElement === parent && composer.nextSibling === before) {
    return;
  }
  const focused = document.activeElement === input;
  parent.insertBefore(composer, before);
  if (focused) {
    input.focus();
  }
}

/** Home until the first message: logo, slogan, the input in the middle, shortcuts, tip, links. */
function renderHomeScreen(s: ConversationState): void {
  if (!homeVisible(s)) {
    home?.el.remove();
    home = undefined;
    place(app, null);
    return;
  }
  const text = tip();
  const key = JSON.stringify([text, strings.slogan, strings.shortcuts, strings.link_docs, strings.link_github, strings.link_bug]);
  if (home?.key !== key) {
    const el = renderHome(strings, version, text);
    if (home) {
      home.el.replaceWith(el);
    } else {
      app.insertBefore(el, thread);
    }
    home = { el, key };
  }
  const slogan = home.el.querySelector(".home-slogan");
  if (slogan) {
    slogan.textContent = sloganText(strings, version);
  }
  const slot = home.el.querySelector<HTMLElement>(".home-input-slot");
  if (slot) {
    place(slot, null);
  }
}

/** Keeps a user-opened <details> open across re-renders. */
const opened = new Set<string>();

function itemKey(item: Item, index: number): string {
  switch (item.kind) {
    case "tool":
      return `tool:${item.id}`;
    case "permission":
      return `permission:${item.requestId}`;
    default:
      return `${item.kind}:${index}`;
  }
}

function renderBanner(s: ConversationState): void {
  banner.replaceChildren();
  const b = s.banner;
  banner.hidden = !b;
  if (!b) {
    return;
  }
  banner.dataset.kind = b.kind;
  const text = make("span", "text");
  text.textContent = b.text;
  banner.append(text);
  const add = (key: string, action: string) => {
    const el = button("banner-action", action);
    el.textContent = t(key);
    banner.append(el);
  };
  if (b.kind === "stopped") {
    add("restart", "restart");
    add("showLogs", "showLogs");
  } else if (b.kind === "noFolder") {
    add("openFolder", "pickFolder");
  } else {
    add("showLogs", "showLogs");
  }
}

function renderThread(s: ConversationState): void {
  const atBottom = thread.scrollHeight - thread.scrollTop - thread.clientHeight < 40;
  const badged = badgedAnswers(s.items, s.busy);
  const nodes = s.items.map((item, i) => {
    const el = renderItem(item, strings);
    if (item.kind === "agent" && item.provider && badged.has(i)) {
      el.appendChild(renderBadge(item.provider, strings));
    }
    const key = itemKey(item, i);
    el.dataset.key = key;
    if (el instanceof HTMLDetailsElement && opened.has(key)) {
      el.open = true;
    }
    return el;
  });
  thread.replaceChildren(...nodes);
  for (const b of thread.querySelectorAll<HTMLButtonElement>("button.copy")) {
    b.textContent = t("copy");
  }
  if (atBottom) {
    thread.scrollTop = thread.scrollHeight;
  }
}

let sessionsOpen = false;

function renderHistory(s: ConversationState): void {
  if (s.sessions.length === 0) {
    sessions.replaceChildren();
    return;
  }
  const d = renderSessions(s.sessions, s.sessionId, strings);
  d.open = sessionsOpen;
  d.addEventListener("toggle", () => (sessionsOpen = d.open));
  sessions.replaceChildren(d);
}

/** `/` completion: highlighted entry, and Escape hides the list until the next edit. */
let active = 0;
let dismissed = false;

function currentMatches(): { name: string; description: string }[] {
  return dismissed || !state ? [] : matchCommands(input.value, state.commands);
}

function renderCompletionList(): void {
  const matches = currentMatches();
  if (active >= matches.length) {
    active = 0;
  }
  completions.replaceChildren(...(matches.length > 0 ? [renderCompletions(matches, active)] : []));
}

function complete(name: string): void {
  insert(`/${name} `);
}

function render(s: ConversationState): void {
  state = s;
  sendButton.textContent = t("send");
  stopButton.textContent = t("stop");
  busyLabel.textContent = t("busy");
  paletteButton.textContent = "⋯";
  paletteButton.title = t("palette");
  paletteButton.setAttribute("aria-label", t("palette"));
  input.placeholder = t("placeholder");
  const noFolder = s.banner?.kind === "noFolder";
  input.disabled = noFolder;
  stopButton.hidden = !s.busy;
  busyLabel.hidden = !s.busy;
  sendButton.hidden = s.busy;
  sendButton.disabled = noFolder;
  renderBanner(s);
  renderHistory(s);
  chips.replaceChildren(...(s.attachments.length > 0 ? [renderAttachments(s.attachments, strings)] : []));
  renderCompletionList();
  renderHomeScreen(s);
  renderThread(s);
}

// ---- events ----

function send(): void {
  const text = input.value;
  if (!state || state.busy || (text.trim() === "" && state.attachments.length === 0) || input.disabled) {
    return;
  }
  post({ type: "send", text });
  input.value = "";
  vscode.setState({ draft: "" });
}

input.addEventListener("keydown", (e) => {
  const matches = currentMatches();
  if (matches.length > 0 && !e.isComposing) {
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp":
        e.preventDefault();
        active = (active + (e.key === "ArrowDown" ? 1 : matches.length - 1)) % matches.length;
        renderCompletionList();
        return;
      case "Tab":
        // Shift+Tab keeps its usual meaning: focus moves back.
        if (!e.shiftKey) {
          e.preventDefault();
          complete(matches[active].name);
          return;
        }
        break;
      case "Enter":
        if (!e.shiftKey && enterCompletes(input.value, matches, active)) {
          e.preventDefault();
          complete(matches[active].name);
          return;
        }
        break;
      case "Escape":
        e.preventDefault();
        dismissed = true;
        renderCompletionList();
        return;
    }
  }
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    send();
  }
});
input.addEventListener("input", () => {
  vscode.setState({ draft: input.value });
  dismissed = false;
  active = 0;
  renderCompletionList();
});

thread.addEventListener(
  "toggle",
  (e) => {
    const d = e.target;
    if (d instanceof HTMLDetailsElement && d.dataset.key) {
      if (d.open) {
        opened.add(d.dataset.key);
      } else {
        opened.delete(d.dataset.key);
      }
    }
  },
  true,
);

document.addEventListener("click", (e) => {
  const target = e.target instanceof Element ? e.target.closest<HTMLElement>("[data-action]") : null;
  if (!target) {
    return;
  }
  const d = target.dataset;
  switch (d.action) {
    case "send":
      send();
      break;
    case "cancel":
      post({ type: "cancel" });
      break;
    case "restart":
      post({ type: "restart" });
      break;
    case "showLogs":
      post({ type: "showLogs" });
      break;
    case "pickFolder":
      post({ type: "pickFolder" });
      break;
    case "palette":
      post({ type: "palette" });
      break;
    case "openLink":
      // The id only: the host maps it to its URL.
      if (isLinkId(d.link)) {
        post({ type: "openLink", id: d.link });
      }
      break;
    case "detach":
      if (d.id) {
        post({ type: "detach", id: d.id });
      }
      break;
    case "load":
      if (d.sessionId && !state?.busy) {
        post({ type: "load", sessionId: d.sessionId });
      }
      break;
    case "complete":
      if (d.name) {
        complete(d.name);
      }
      break;
    case "openDiff":
      if (d.toolCallId) {
        post({ type: "openDiff", toolCallId: d.toolCallId });
      }
      break;
    case "permission":
      if (d.requestId && d.optionId && !(target as HTMLButtonElement).disabled) {
        post({ type: "permission", requestId: d.requestId, optionId: d.optionId });
        // One answer per card: the next state shows it answered.
        for (const b of target.closest(".permission")?.querySelectorAll("button") ?? []) {
          b.disabled = true;
        }
      }
      break;
    case "copy": {
      const code = target.closest(".code")?.querySelector("code")?.textContent ?? "";
      void navigator.clipboard.writeText(code).then(() => {
        target.textContent = t("copied");
        setTimeout(() => (target.textContent = t("copy")), 1200);
      });
      break;
    }
  }
});

/** Text for the input from the host (a skill picked in the palette), like a completion. */
function insert(text: string): void {
  if (input.disabled) {
    return;
  }
  input.value = text;
  vscode.setState({ draft: input.value });
  dismissed = true;
  renderCompletionList();
  input.focus();
}

window.addEventListener("message", (e: MessageEvent<unknown>) => {
  const m = e.data as Partial<ToChatWebview> | null;
  if (m?.type === "insert") {
    if (typeof m.text === "string" && m.text.length <= 1000) {
      insert(m.text);
    }
    return;
  }
  if (!m || m.type !== "state" || typeof m.state !== "object" || m.state === null) {
    return;
  }
  strings = typeof m.strings === "object" && m.strings !== null ? m.strings : {};
  version = typeof m.version === "string" ? m.version : undefined;
  applyTheme(document.documentElement, themeOf(m.theme));
  render(m.state);
});

post({ type: "ready" });
