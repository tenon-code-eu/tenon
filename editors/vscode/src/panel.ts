// Webview view `tenon.chat`: strict CSP with a fresh nonce per resolve, the
// whole state posted on every (coalesced) change, messages validated before
// they reach the Chat. The webview never opens a URL: its home links post an
// id, mapped here to a constant URL.
import { randomBytes } from "node:crypto";
import * as vscode from "vscode";
import { type Chat, webviewStrings } from "./chat";
import type { Log } from "./log";
import { isFromWebview, type LinkId, type Theme, type ToChatWebview, type ToWebview } from "./protocol";

export const CHAT_VIEW = "tenon.chat";

/** The URLs of the home links; the webview only knows their ids. */
export const LINKS = {
  docs: "https://tenon-code-eu.github.io/tenon/",
  github: "https://github.com/tenon-code-eu/tenon",
  bug: "https://github.com/tenon-code-eu/tenon/issues/new",
} as const satisfies Record<LinkId, string>;

/** What the panel shows besides the conversation, and how it opens a link. */
export interface PanelHost {
  /** tenon's version once it answered initialize. */
  version(): string | undefined;
  theme(): Theme;
  /** Opens a home link (vscode.env.openExternal; recorded in test mode). */
  openUrl(url: string): void;
  /** Opens the palette (tenon.palette). */
  palette(): void;
}

export function nonce(): string {
  return randomBytes(18).toString("base64");
}

/**
 * The page of a tenon webview view: the shared stylesheet and one script
 * (webview.js for the chat, session.js for the Session view).
 */
export function chatHtml(webview: vscode.Webview, extensionUri: vscode.Uri, n: string, script = "webview.js"): string {
  const asset = (name: string) => webview.asWebviewUri(vscode.Uri.joinPath(extensionUri, "dist", name)).toString();
  const csp = [
    "default-src 'none'",
    `script-src 'nonce-${n}'`,
    `style-src ${webview.cspSource} 'nonce-${n}'`,
    `img-src ${webview.cspSource} data:`,
    `font-src ${webview.cspSource}`,
  ].join("; ");
  const lang = vscode.env.language.replace(/[^A-Za-z0-9-]/g, "");
  return `<!DOCTYPE html>
<html lang="${lang}">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="${asset("webview.css")}" nonce="${n}">
</head>
<body>
<div id="app"></div>
<script nonce="${n}" src="${asset(script)}"></script>
</body>
</html>`;
}

export class ChatViewProvider implements vscode.WebviewViewProvider {
  /** True once a webview script has started and posted "ready" (test API). */
  scriptLoaded = false;
  private view: vscode.WebviewView | undefined;
  /** Posts the state to the webview on screen, if any. */
  private post: (() => void) | undefined;
  /** Text for the input, kept until the webview is ready (a skill picked in the palette). */
  private pendingInsert: string | undefined;
  /** The script of the webview on screen posted ready (its content is dropped when hidden). */
  private live = false;
  private readonly hiddenEmitter = new vscode.EventEmitter<void>();
  /** The chat view went off screen (hidden or disposed). */
  readonly onDidHide = this.hiddenEmitter.event;

  /** True while the chat view is on screen. */
  get visible(): boolean {
    return this.view?.visible ?? false;
  }

  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly chat: Chat,
    private readonly log: Log,
    private readonly host: PanelHost,
  ) {}

  /**
   * Puts text in the chat input: now when the webview is ready, else once
   * it posts ready.
   */
  insert(text: string): void {
    this.pendingInsert = text;
    if (this.view && this.live) {
      this.flushInsert();
    }
  }

  private flushInsert(): void {
    const text = this.pendingInsert;
    if (text === undefined || !this.view) {
      return;
    }
    this.pendingInsert = undefined;
    const msg: ToChatWebview = { type: "insert", text };
    void this.view.webview.postMessage(msg);
  }

  /** Re-posts the state (tenon started, tenon.theme changed). */
  refresh(): void {
    this.post?.();
  }

  /**
   * A message of the webview, validated first; anything malformed is
   * ignored. openLink is handled here, the rest goes to the Chat.
   */
  receive(m: unknown): void {
    if (!isFromWebview(m)) {
      this.log.info("chat: invalid webview message ignored");
      return;
    }
    switch (m.type) {
      case "ready":
        this.scriptLoaded = true;
        this.live = true;
        this.post?.();
        this.flushInsert();
        break;
      case "openLink":
        this.host.openUrl(LINKS[m.id]);
        return;
      case "palette":
        this.host.palette();
        return;
      case "openFileDiff":
        // The Session view's message, not the chat's.
        this.log.info("chat: openFileDiff ignored");
        return;
    }
    this.chat.handle(m);
  }

  resolveWebviewView(view: vscode.WebviewView): void {
    this.view = view;
    this.live = false;
    const webview = view.webview;
    webview.options = {
      enableScripts: true,
      enableCommandUris: false,
      localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, "dist")],
    };
    webview.html = chatHtml(webview, this.extensionUri, nonce());

    const post = () => {
      const msg: ToWebview = {
        type: "state",
        state: this.chat.conv.state,
        strings: webviewStrings(),
        version: this.host.version(),
        theme: this.host.theme(),
      };
      void webview.postMessage(msg);
    };
    this.post = post;
    const subs = [
      this.chat.conv.onDidChange(post),
      webview.onDidReceiveMessage((m: unknown) => this.receive(m)),
      view.onDidChangeVisibility(() => {
        if (view.visible) {
          post();
          void this.chat.refreshSessions(false);
        } else {
          this.live = false;
          this.hiddenEmitter.fire();
        }
      }),
    ];
    view.onDidDispose(() => {
      if (this.view === view) {
        this.view = undefined;
        this.post = undefined;
        this.hiddenEmitter.fire();
      }
      for (const s of subs) {
        s.dispose();
      }
    });
    this.chat.touch();
    void this.chat.refreshSessions();
  }
}
