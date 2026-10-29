// Session view webview: renders the stats posted by the host (computeStats,
// on every change of the conversation) like the TUI's side panel: title,
// context gauge, tokens, modified files (a click opens the diff), sovereignty.
// Strings come with the stats; built with createElement / textContent only,
// no network request.
import { type FromSessionView, themeOf, type ToSessionView } from "../src/protocol";
import type { SendStat, SessionStats } from "../src/stats";
import { applyTheme } from "./home";

interface VsCodeApi {
  postMessage(m: FromSessionView): void;
}

declare function acquireVsCodeApi(): VsCodeApi;

const vscode = acquireVsCodeApi();

/** Cells of the context gauge, as in the TUI. */
const GAUGE_CELLS = 10;

let strings: Record<string, string> = {};

function t(key: string, ...args: (string | number)[]): string {
  return (strings[key] ?? key).replace(/\{(\d+)\}/g, (m, i: string) => {
    const v = args[Number(i)];
    return v === undefined ? m : String(v);
  });
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  if (text !== undefined) {
    e.textContent = text;
  }
  return e;
}

/** 1234 → 1.2k, 2500000 → 2.5M. */
function compact(n: number): string {
  const unit = (v: number, s: string) => `${v.toFixed(1).replace(/\.0$/, "")}${s}`;
  if (n >= 1_000_000) {
    return unit(n / 1_000_000, "M");
  }
  if (n >= 1000) {
    return unit(n / 1000, "k");
  }
  return String(n);
}

function gauge(used: number | undefined, size: number | undefined): string {
  const pct = used !== undefined && size !== undefined && size > 0 ? Math.min(100, Math.floor((used * 100) / size)) : 0;
  const filled = Math.floor((pct * GAUGE_CELLS) / 100);
  return "█".repeat(filled) + "░".repeat(GAUGE_CELLS - filled) + " " + t("percent", pct);
}

function jurisdiction(j: string | undefined): string {
  switch (j) {
    case "eu":
      return t("jurisdiction_eu");
    case "non-eu":
      return t("jurisdiction_noneu");
    case "local":
      return t("jurisdiction_local");
    case undefined:
    case "":
      return t("jurisdiction_unknown");
    default:
      return j;
  }
}

function sendLine(s: SendStat): HTMLElement {
  const parts = [s.provider];
  if (s.country) {
    parts.push(s.country);
  }
  parts.push(jurisdiction(s.jurisdiction));
  const line = el("div", s.alert ? "s-send alert" : "s-send ok", `● ${s.n} → ${parts.join(" · ")}`);
  return line;
}

function section(title: string, ...children: HTMLElement[]): HTMLElement {
  const sec = el("section", "s-section");
  sec.append(el("h2", "s-head", title), ...children);
  return sec;
}

function render(s: SessionStats): void {
  const root = el("div", "session");
  root.append(el("div", "s-title", s.title ?? t("untitled")));

  const tokens = el("div", "s-tokens muted", t("tokens", compact(s.tokensIn ?? 0), compact(s.tokensOut ?? 0)));
  root.append(section(t("context"), el("div", "s-gauge", gauge(s.used, s.size)), tokens));

  const files = el("div", "s-files");
  if (s.files.length === 0) {
    files.append(el("span", "muted", t("noFiles")));
  }
  for (const f of s.files) {
    const b = el("button", `s-file op-${f.op}`);
    b.type = "button";
    b.dataset.action = "openFileDiff";
    b.dataset.toolCallId = f.toolCallId;
    b.title = f.path;
    b.append(el("span", "s-op", f.op), " ", el("span", "s-path", f.path));
    files.append(b);
  }
  root.append(section(t("files"), files));

  const sends = el("div", "s-sends");
  if (s.sends.length === 0) {
    sends.append(el("span", "muted", t("noSends")));
  }
  sends.append(...s.sends.map(sendLine));
  const sovereignty: HTMLElement[] = [sends];
  if (s.sandboxed !== undefined) {
    sovereignty.push(el("div", s.sandboxed ? "s-sandbox ok" : "s-sandbox alert", t(s.sandboxed ? "sandboxed" : "notSandboxed")));
  }
  if (s.profile) {
    sovereignty.push(el("div", "s-profile muted", t("profile", s.profile)));
  }
  if (s.policy) {
    sovereignty.push(el("div", "s-policy muted", t("policy", s.policy)));
  }
  root.append(section(t("sovereignty"), ...sovereignty));

  (document.getElementById("app") as HTMLElement).replaceChildren(root);
}

document.addEventListener("click", (e) => {
  const target = e.target instanceof Element ? e.target.closest<HTMLElement>("[data-action=openFileDiff]") : null;
  const id = target?.dataset.toolCallId;
  if (id) {
    vscode.postMessage({ type: "openFileDiff", toolCallId: id });
  }
});

window.addEventListener("message", (e: MessageEvent<unknown>) => {
  const m = e.data as Partial<ToSessionView> | null;
  if (!m || m.type !== "stats" || typeof m.stats !== "object" || m.stats === null) {
    return;
  }
  strings = typeof m.strings === "object" && m.strings !== null ? m.strings : {};
  applyTheme(document.documentElement, themeOf(m.theme));
  render(m.stats);
});

vscode.postMessage({ type: "ready" });
