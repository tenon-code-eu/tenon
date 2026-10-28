// Webview view `tenon.chat`: strict CSP with a fresh nonce per resolve, the
// whole state posted on every (coalesced) change, messages validated before
// they reach the Chat.
import { randomBytes } from "node:crypto";
import * as vscode from "vscode";
import { type Chat, webviewStrings } from "./chat";
import type { Log } from "./log";
import { isFromWebview, type ToWebview } from "./protocol";

export const CHAT_VIEW = "tenon.chat";

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
  ) {}

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
      const msg: ToWebview = { type: "state", state: this.chat.conv.state, strings: webviewStrings() };
      void webview.postMessage(msg);
    };
    const subs = [
      this.chat.conv.onDidChange(post),
      webview.onDidReceiveMessage((m: unknown) => {
        if (!isFromWebview(m)) {
          this.log.info("chat: invalid webview message ignored");
          return;
        }
        if (m.type === "ready") {
          this.scriptLoaded = true;
          post();
        }
        this.chat.handle(m);
      }),
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
