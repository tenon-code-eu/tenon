// Webview view `tenon.chat`: strict CSP with a fresh nonce per resolve, the
// whole state posted on every (coalesced) change, messages validated before
// they reach the Chat. The webview never opens a URL: its home links post an
// id, mapped here to a constant URL.
import { randomBytes } from "node:crypto";
import * as vscode from "vscode";
import { type Chat, webviewStrings } from "./chat";
import type { Log } from "./log";
import { isFromWebview, type LinkId, type Theme, type ToWebview } from "./protocol";

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
}

function nonce(): string {
  return randomBytes(18).toString("base64");
}

export function chatHtml(webview: vscode.Webview, extensionUri: vscode.Uri, n: string): string {
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
<script nonce="${n}" src="${asset("webview.js")}"></script>
</body>
</html>`;
}

export class ChatViewProvider implements vscode.WebviewViewProvider {
  /** True once a webview script has started and posted "ready" (test API). */
  scriptLoaded = false;
  private view: vscode.WebviewView | undefined;
  /** Posts the state to the webview on screen, if any. */
  private post: (() => void) | undefined;
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
        this.post?.();
        break;
      case "openLink":
        this.host.openUrl(LINKS[m.id]);
        return;
    }
    this.chat.handle(m);
  }

  resolveWebviewView(view: vscode.WebviewView): void {
    this.view = view;
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
