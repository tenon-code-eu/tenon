// Home screen of the chat webview, shown until the first message: the tenon
// logo (media/logo.svg, bundled as text), slogan and version, a slot for the
// input, shortcuts, the tip of the day and the links. Links are buttons
// carrying an id (docs, github, bug): the host owns the URLs and opens them.
// Built with createElement / textContent; nothing here assigns innerHTML.
import logoSvg from "../media/logo.svg?raw";
import { LINK_IDS, type Theme } from "../src/protocol";

type Strings = Record<string, string>;

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  if (text !== undefined) {
    e.textContent = text;
  }
  return e;
}

/** The version as the TUI shows it: dev when empty, v before a number. */
export function welcomeVersion(v: string): string {
  v = v.trim();
  if (v === "") {
    return "dev";
  }
  return /^\d/.test(v) ? `v${v}` : v;
}

/** Slogan and version; the slogan alone while tenon's version is not known. */
export function sloganText(strings: Strings, version: string | undefined): string {
  const slogan = strings.slogan ?? "";
  return version === undefined ? slogan : `${slogan} ${welcomeVersion(version)}`;
}

/**
 * The logo: an XML parse of the bundled constant (no script runs in an XML
 * document), adopted into the page; its two halves fit together on show.
 */
function logo(): Element {
  const doc = new DOMParser().parseFromString(logoSvg, "image/svg+xml");
  return document.importNode(doc.documentElement, true);
}

/**
 * The home screen; version undefined (tenon not answered yet) shows the
 * slogan alone. The webview moves its input into `.home-input-slot`.
 */
export function renderHome(strings: Strings, version: string | undefined, tip: string): HTMLElement {
  const home = el("div", "home");
  const brand = el("div", "home-logo");
  brand.append(logo(), el("span", "home-word", "T E N O N"));
  home.append(
    brand,
    el("div", "home-slogan", sloganText(strings, version)),
    el("div", "home-input-slot"),
    el("div", "home-shortcuts", strings.shortcuts ?? ""),
  );
  if (tip) {
    home.append(el("div", "home-tip", tip));
  }
  const links = el("div", "home-links");
  LINK_IDS.forEach((id, i) => {
    if (i > 0) {
      links.append(el("span", "sep", " · "));
    }
    const b = el("button", "link", strings[id] ?? id);
    b.type = "button";
    b.dataset.action = "openLink";
    b.dataset.link = id;
    links.append(b);
  });
  home.append(links);
  return home;
}

/** tenon.theme on the webview root: the tenon class, or none (auto). */
export function applyTheme(root: HTMLElement, theme: Theme): void {
  root.classList.toggle("theme-tenon", theme === "tenon");
}
