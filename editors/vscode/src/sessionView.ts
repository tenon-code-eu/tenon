// Webview view `tenon.session`, under the chat: the TUI's side panel for the
// chat's conversation (computeStats on every coalesced change). Same CSP as
// the chat (fresh nonce, no remote source); it only receives stats and posts
// ready and openFileDiff, both checked on the host (sessionMessages.ts).
import * as vscode from "vscode";
import type { Log } from "./log";
import type { Conversation } from "./model";
import { chatHtml, nonce } from "./panel";
import type { Theme, ToSessionView } from "./protocol";
import { handleSessionMessage } from "./sessionMessages";
import { computeStats, type SessionStats } from "./stats";
import { jurisdictionLabel } from "./status";

export const SESSION_VIEW = "tenon.session";

/** Strings of the Session view (its webview has no l10n of its own). */
export function sessionStrings(): Record<string, string> {
  return {
    untitled: vscode.l10n.t("Untitled session"),
    context: vscode.l10n.t("Context"),
    percent: vscode.l10n.t("{0}%"),
    tokens: vscode.l10n.t("in {0} · out {1}"),
    files: vscode.l10n.t("Modified files"),
    noFiles: vscode.l10n.t("none"),
    sovereignty: vscode.l10n.t("Sovereignty"),
    noSends: vscode.l10n.t("no sends yet"),
    sandboxed: vscode.l10n.t("sandboxed"),
    notSandboxed: vscode.l10n.t("not sandboxed"),
    profile: vscode.l10n.t("profile: {0}"),
    policy: vscode.l10n.t("policy: {0}"),
    jurisdiction_eu: jurisdictionLabel("eu", vscode.l10n),
    jurisdiction_noneu: jurisdictionLabel("non-eu", vscode.l10n),
    jurisdiction_local: jurisdictionLabel("local", vscode.l10n),
    jurisdiction_unknown: jurisdictionLabel("", vscode.l10n),
  };
}

export class SessionViewProvider implements vscode.WebviewViewProvider {
  /** The stats last posted to the webview (test API). */
  lastPosted: SessionStats | undefined;
  /** True once the webview's script ran under its CSP and posted ready (test API). */
  scriptLoaded = false;
  private post: (() => void) | undefined;

  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly conv: Conversation,
    /** Opens a write's diff (DiffProvider, the same path as the chat's Open diff). */
    private readonly diffs: { open(toolCallId: string): Promise<void> },
    private readonly log: Log,
    private readonly theme: () => Theme,
  ) {}

  /** Re-posts the stats (tenon.theme changed). */
  refresh(): void {
    this.post?.();
  }

  /** A message of the webview; anything but ready or a known diff is ignored. */
  receive(m: unknown): void {
    const taken = handleSessionMessage(m, this.conv, (id) => this.diffs.open(id));
    if (taken === undefined) {
      this.log.info("session view: message ignored");
    } else if (taken === "ready") {
      this.scriptLoaded = true;
      this.post?.();
    }
  }

  resolveWebviewView(view: vscode.WebviewView): void {
    const webview = view.webview;
    webview.options = {
      enableScripts: true,
      enableCommandUris: false,
      localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, "dist")],
    };
    webview.html = chatHtml(webview, this.extensionUri, nonce(), "session.js");

    const post = () => {
      const stats = computeStats(this.conv.state);
      this.lastPosted = stats;
      const msg: ToSessionView = { type: "stats", stats, strings: sessionStrings(), theme: this.theme() };
      void webview.postMessage(msg);
    };
    this.post = post;
    const subs = [
      this.conv.onDidChange(post),
      webview.onDidReceiveMessage((m: unknown) => this.receive(m)),
      view.onDidChangeVisibility(() => {
        if (view.visible) {
          post();
        }
      }),
    ];
    view.onDidDispose(() => {
      if (this.post === post) {
        this.post = undefined;
      }
      for (const s of subs) {
        s.dispose();
      }
    });
  }
}
