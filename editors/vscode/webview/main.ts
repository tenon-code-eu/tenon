// Chat webview: renders the state posted by the extension host (whole state
// on every change) and posts user actions back. Strings come with the state;
// this bundle has no l10n and makes no network request.
import type { ConversationState, Item } from "../src/model";
import type { FromWebview, ToWebview } from "../src/protocol";
import { renderItem, type Strings } from "./render";

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
const thread = make("div", "thread");
const composer = make("div", "composer");
const input = make("textarea", "input");
input.rows = 3;
const actions = make("div", "actions");
const busyLabel = make("span", "busy");
const sendButton = button("send", "send");
const stopButton = button("stop", "cancel");
actions.append(busyLabel, stopButton, sendButton);
composer.append(input, actions);
app.append(banner, thread, composer);

const saved = vscode.getState() as { draft?: unknown } | undefined;
if (typeof saved?.draft === "string") {
  input.value = saved.draft;
}

// ---- rendering ----

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
  const nodes = s.items.map((item, i) => {
    const el = renderItem(item, strings);
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

function render(s: ConversationState): void {
  state = s;
  sendButton.textContent = t("send");
  stopButton.textContent = t("stop");
  busyLabel.textContent = t("busy");
  input.placeholder = t("placeholder");
  const noFolder = s.banner?.kind === "noFolder";
  input.disabled = noFolder;
  stopButton.hidden = !s.busy;
  busyLabel.hidden = !s.busy;
  sendButton.hidden = s.busy;
  sendButton.disabled = noFolder;
  renderBanner(s);
  renderThread(s);
}

// ---- events ----

function send(): void {
  const text = input.value;
  if (!state || state.busy || text.trim() === "" || input.disabled) {
    return;
  }
  post({ type: "send", text });
  input.value = "";
  vscode.setState({ draft: "" });
}

input.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    send();
  }
});
input.addEventListener("input", () => vscode.setState({ draft: input.value }));

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
    case "openDiff":
      if (d.toolCallId) {
        post({ type: "openDiff", toolCallId: d.toolCallId });
      }
      break;
    case "permission":
      if (d.requestId && d.optionId) {
        post({ type: "permission", requestId: d.requestId, optionId: d.optionId });
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

window.addEventListener("message", (e: MessageEvent<unknown>) => {
  const m = e.data as Partial<ToWebview> | null;
  if (!m || m.type !== "state" || typeof m.state !== "object" || m.state === null) {
    return;
  }
  strings = typeof m.strings === "object" && m.strings !== null ? m.strings : {};
  render(m.state);
});

post({ type: "ready" });
