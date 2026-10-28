// The chat of the window: one Conversation shown by the panel, fed by the
// SessionController of the current `tenon acp`. tenon starts on first use; a
// window without a folder never starts it nor sends session/new.
import type { ContentBlock, StopReason } from "@agentclientprotocol/sdk";
import * as vscode from "vscode";
import type { Agent } from "./agent";
import { buildPrompt } from "./context";
import type { Log } from "./log";
import type { Conversation } from "./model";
import type { FromWebview } from "./protocol";
import { SessionController } from "./session";

/** Permission cards and diffs, as the chat uses them (Permissions, DiffProvider). */
export interface ChatExtras {
  permissions?: { answer(requestId: string, optionId: string): void; cancelAll(): void };
  diffs?: { open(toolCallId: string): Promise<void> };
}

/** What the chat needs from the extension's Controller. */
export interface AgentSource {
  ready(): Promise<Agent>;
  readonly onDidStart: vscode.Event<Agent>;
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** Strings of the webview (it has no l10n of its own). */
export function webviewStrings(): Record<string, string> {
  return {
    send: vscode.l10n.t("Send"),
    stop: vscode.l10n.t("Stop"),
    placeholder: vscode.l10n.t("Ask tenon (Enter to send, Shift+Enter for a new line)"),
    thought: vscode.l10n.t("Thinking"),
    copy: vscode.l10n.t("Copy"),
    copied: vscode.l10n.t("Copied"),
    openDiff: vscode.l10n.t("Open diff"),
    tool_pending: vscode.l10n.t("pending"),
    tool_in_progress: vscode.l10n.t("running"),
    tool_completed: vscode.l10n.t("done"),
    tool_failed: vscode.l10n.t("failed"),
    restart: vscode.l10n.t("Restart"),
    showLogs: vscode.l10n.t("Show logs"),
    openFolder: vscode.l10n.t("Open folder"),
    busy: vscode.l10n.t("Working…"),
    refused: vscode.l10n.t("Refused"),
    detach: vscode.l10n.t("Remove"),
    sessions: vscode.l10n.t("Sessions"),
    untitled: vscode.l10n.t("Untitled session"),
  };
}

export class Chat implements vscode.Disposable {
  private session: SessionController | undefined;
  /** The turn in progress, claimed synchronously by send(). */
  private turn: Promise<StopReason> | undefined;
  /** Stop was pressed before the turn's prompt went out. */
  private stopRequested = false;
  /** A session/load after a restart; ensureSession waits for it. */
  private resuming: Promise<void> | undefined;
  private readonly subs: vscode.Disposable[] = [];
  /** Attachments of the next prompt, built from the editor (never from the webview). */
  private attachments: { id: string; block: ContentBlock; label: string }[] = [];
  private nextAttachment = 1;

  constructor(
    private readonly agents: AgentSource,
    readonly conv: Conversation,
    private readonly log: Log,
    private readonly showLogs: () => void,
    private readonly extras: ChatExtras = {},
  ) {
    this.subs.push(
      agents.onDidStart((a) => this.attach(a)),
      vscode.workspace.onDidChangeWorkspaceFolders(() => this.refreshFolders()),
    );
    this.refreshFolders();
  }

  /** First use (panel shown, command, test API): starts tenon when a folder is open. */
  touch(): void {
    if (this.refreshFolders()) {
      void this.agents.ready().catch(() => undefined);
    }
  }

  /**
   * Sends one prompt; resolves with the stop reason. The turn is claimed
   * before anything is awaited (starting tenon and session/new take
   * seconds), so a second send meanwhile is refused.
   */
  send(text: string): Promise<StopReason> {
    if (this.turn) {
      return Promise.reject(new Error("a turn is in progress"));
    }
    this.stopRequested = false;
    this.conv.setBusy(true);
    const blocks = buildPrompt(text, this.attachments.map((a) => a.block));
    this.setAttachments([]);
    const turn = (async (): Promise<StopReason> => {
      try {
        const s = await this.ensureSession();
        if (this.stopRequested) {
          return "cancelled";
        }
        return await s.prompt(blocks);
      } finally {
        // A card never outlives its turn.
        this.extras.permissions?.cancelAll();
        // Only one turn exists at a time: send() refuses while it is set.
        this.turn = undefined;
        this.conv.setBusy(false);
        // The turn is in tenon's journal now; a dead tenon is not restarted for this.
        void this.refreshSessions(false);
      }
    })();
    this.turn = turn;
    return turn;
  }

  /**
   * Stop: session/cancel first, so tenon ends the turn, then every pending
   * permission request is answered "cancelled" (a refusal for tenon).
   */
  async cancel(): Promise<void> {
    if (this.turn) {
      this.stopRequested = true;
    }
    try {
      await this.session?.cancel();
    } finally {
      this.extras.permissions?.cancelAll();
    }
  }

  /** Adds a chip; its block goes out with the next prompt. */
  addAttachment(block: ContentBlock, label: string): void {
    this.setAttachments([...this.attachments, { id: `a${this.nextAttachment++}`, block, label }]);
  }

  removeAttachment(id: string): void {
    this.setAttachments(this.attachments.filter((a) => a.id !== id));
  }

  /**
   * session/list for the open session's folder (the first folder before
   * any session); failures go to the log and keep the list shown. With
   * start false, only a running tenon is asked.
   */
  async refreshSessions(start = true): Promise<void> {
    const cwd = this.conv.state.cwd ?? this.folders()[0];
    if (!cwd) {
      return;
    }
    try {
      const s = start ? await this.current() : this.session;
      if (!s || s.agent.hasExited) {
        return;
      }
      await s.list(cwd);
    } catch (err) {
      this.log.info(`session/list: ${errorMessage(err)}`);
    }
  }

  private setAttachments(list: { id: string; block: ContentBlock; label: string }[]): void {
    this.attachments = list;
    this.conv.setAttachments(list.map(({ id, label }) => ({ id, label })));
  }

  /** Stops the turn in progress and waits for its end. */
  private async endTurn(): Promise<void> {
    const turn = this.turn;
    if (turn) {
      await this.cancel();
      await turn.catch(() => undefined);
    }
  }

  /** New session in the folder (picker when the workspace has several). */
  async newSession(): Promise<void> {
    if (!this.refreshFolders()) {
      return;
    }
    const cwd = await this.pickFolder();
    if (!cwd) {
      return;
    }
    const s = await this.current();
    await this.endTurn();
    await s.newSession(cwd);
  }

  async load(sessionId: string): Promise<void> {
    const cwd = this.conv.state.cwd ?? this.folders()[0];
    if (!cwd) {
      return;
    }
    const s = await this.current();
    await this.endTurn();
    await s.load(sessionId, cwd);
  }

  /** Handles a validated webview message; failures are already in the banner, the log gets the rest. */
  handle(m: FromWebview): void {
    const run = (p: Promise<unknown>) =>
      void p.catch((err: unknown) => this.log.info(`chat ${m.type}: ${errorMessage(err)}`));
    switch (m.type) {
      case "send":
        if (!this.turn && (m.text.trim() !== "" || this.attachments.length > 0)) {
          run(this.send(m.text));
        }
        break;
      case "cancel":
        run(this.cancel());
        break;
      case "newSession":
        run(this.newSession());
        break;
      case "load":
        run(this.load(m.sessionId));
        break;
      case "pickFolder":
        run(
          this.folders().length === 0
            ? Promise.resolve(vscode.commands.executeCommand("workbench.action.files.openFolder"))
            : this.newSession(),
        );
        break;
      case "restart":
        run(Promise.resolve(vscode.commands.executeCommand("tenon.restart")));
        break;
      case "showLogs":
        this.showLogs();
        break;
      case "permission":
        this.extras.permissions?.answer(m.requestId, m.optionId);
        break;
      case "openDiff":
        if (this.extras.diffs) {
          run(this.extras.diffs.open(m.toolCallId));
        }
        break;
      case "detach":
        this.removeAttachment(m.id);
        break;
      case "ready":
        break;
    }
  }

  dispose(): void {
    for (const s of this.subs) {
      s.dispose();
    }
  }

  private folders(): string[] {
    return (vscode.workspace.workspaceFolders ?? []).map((f) => f.uri.fsPath);
  }

  /** Sets or clears the noFolder banner; true when a folder is open. */
  private refreshFolders(): boolean {
    const has = this.folders().length > 0;
    const banner = this.conv.state.banner;
    if (!has && banner?.kind !== "noFolder") {
      this.conv.setBanner({ kind: "noFolder", text: vscode.l10n.t("Open a folder to use tenon") });
    } else if (has && banner?.kind === "noFolder") {
      this.conv.setBanner(undefined);
    }
    return has;
  }

  private async pickFolder(): Promise<string | undefined> {
    const folders = vscode.workspace.workspaceFolders ?? [];
    if (folders.length <= 1) {
      return folders[0]?.uri.fsPath;
    }
    const picked = await vscode.window.showWorkspaceFolderPick({
      placeHolder: vscode.l10n.t("Folder for the new tenon session"),
    });
    return picked?.uri.fsPath;
  }

  /** The SessionController of the running tenon (started if needed). */
  private async current(): Promise<SessionController> {
    const agent = await this.agents.ready();
    if (this.session?.agent !== agent) {
      this.attach(agent);
    }
    await this.resuming;
    return this.session as SessionController;
  }

  private async ensureSession(): Promise<SessionController> {
    if (!this.refreshFolders()) {
      throw new Error(vscode.l10n.t("Open a folder to use tenon"));
    }
    const s = await this.current();
    if (this.conv.state.sessionId === undefined) {
      const cwd = await this.pickFolder();
      if (!cwd) {
        throw new Error("no folder chosen");
      }
      await s.newSession(cwd);
    }
    return s;
  }

  /** A new tenon is up: clears "stopped" and reloads the session that was open. */
  private attach(agent: Agent): void {
    if (this.session?.agent === agent) {
      return;
    }
    const s = new SessionController(agent, this.conv);
    this.session = s;
    agent.onExit(() => {
      if (this.session !== s) {
        return;
      }
      this.session = undefined;
      this.extras.permissions?.cancelAll();
      this.conv.setBusy(false);
      this.conv.setBanner({ kind: "stopped", text: vscode.l10n.t("tenon stopped") });
    });
    if (this.conv.state.banner?.kind === "stopped") {
      this.conv.setBanner(undefined);
    }
    const { sessionId, cwd } = this.conv.state;
    if (sessionId === undefined || cwd === undefined) {
      this.resuming = undefined;
      return;
    }
    this.resuming = s.load(sessionId, cwd).catch((err: unknown) => {
      // The session cannot come back (never saved, or gone): start afresh.
      this.log.info(`session/load ${sessionId} after restart: ${errorMessage(err)}`);
      this.conv.begin(undefined, undefined);
      this.conv.setBanner(undefined);
    });
  }
}
