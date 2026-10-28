// Activation and commands. One `tenon acp` per window, started on first use:
// the chat view shown, a tenon command, or a test API call.
import * as fs from "node:fs";
import * as os from "node:os";
import type { InitializeResponse, StopReason } from "@agentclientprotocol/sdk";
import * as vscode from "vscode";
import { Agent, type ClientHandlers } from "./agent";
import { Chat } from "./chat";
import { fileBlock, selectionBlock, selectionLabel } from "./context";
import { DIFF_SCHEME, DiffProvider, type OpenedDiff } from "./diff";
import { formatVersion, meetsMinimum, MINIMUM_VERSION, parseVersion, readVersion, resolveBinary } from "./binary";
import { OutputLog } from "./log";
import { Conversation, type ConversationState } from "./model";
import { onDidShow, showError, showInfo, showInfoChoice, showWarning, type Shown } from "./notify";
import { CHAT_VIEW, ChatViewProvider } from "./panel";
import { Permissions } from "./permissions";
import { StatusBar } from "./status";

export type { Shown } from "./notify";

/** Returned by activate() only when TENON_VSCODE_TEST=1; grows with later tasks. */
export interface TenonTestApi {
  agentReady(): Promise<InitializeResponse>;
  agentPid(): number | undefined;
  stopAgent(): Promise<void>;
  notifications(): Shown[];
  /** The chat's conversation state (counts as a first use). */
  state(): ConversationState;
  /** Sends a prompt like the panel's input; resolves with the stop reason. */
  send(text: string): Promise<string>;
  /** The panel's Stop button. */
  cancel(): Promise<void>;
  /** True once the chat webview's script ran under its CSP and posted "ready". */
  panelReady(): boolean;
  /** Answers the oldest pending permission request, like a click on its card. */
  answer(optionId: string): void;
  /** The card's "Open diff" button. */
  openDiff(toolCallId: string): Promise<void>;
  /** The two documents and the title of the last diff opened. */
  lastDiff(): OpenedDiff | undefined;
  /** A click on an entry of the Sessions list. */
  load(sessionId: string): Promise<void>;
  /** The status bar item: its text and whether it shows the warning colour. */
  status(): { text: string; warning: boolean };
}

/** The only URL the extension opens. */
const INSTALL_URL = "https://tenon-code-eu.github.io/tenon/editors/";

function isFile(p: string): boolean {
  try {
    return fs.statSync(p).isFile();
  } catch {
    return false;
  }
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * Updates go to the conversation, synchronously (the prompt reply waits one
 * tick for them); permission requests become cards and stay pending until an
 * answer.
 */
function clientHandlers(conv: Conversation, permissions: Permissions): ClientHandlers {
  return {
    sessionUpdate: (n) => {
      conv.apply(n);
      return Promise.resolve();
    },
    requestPermission: (r) => permissions.request(r),
  };
}

/** Integration tests shorten the initialize timeout; production keeps the default. */
function testInitTimeout(): number | undefined {
  if (process.env.TENON_VSCODE_TEST !== "1") {
    return undefined;
  }
  const ms = Number(process.env.TENON_VSCODE_INIT_TIMEOUT_MS);
  return ms > 0 ? ms : undefined;
}

/**
 * Owns the one `tenon acp` of the window. Lifecycle operations run one after
 * the other on a single promise chain, so overlapping restarts cannot leave
 * two children; stop() first aborts a start in progress so it never waits on
 * a hung initialize.
 */
class Controller implements vscode.Disposable {
  private starting: Promise<Agent> | undefined;
  private agent: Agent | undefined;
  private abort: AbortController | undefined;
  private queue: Promise<unknown> = Promise.resolve();
  private readonly startEmitter = new vscode.EventEmitter<Agent>();
  /** Fires when a new tenon has answered initialize. */
  readonly onDidStart: vscode.Event<Agent> = this.startEmitter.event;

  constructor(
    private readonly ctx: vscode.ExtensionContext,
    private readonly log: OutputLog,
    private readonly handlers: ClientHandlers,
  ) {}

  /** Starts tenon; failures are reported to the user and reject the returned promise. */
  start(): Promise<Agent> {
    const p = this.serial(() => this.startNow());
    this.starting = p;
    p.catch(() => undefined);
    void p.then(
      (agent) =>
        agent.onExit(() => {
          // A crash: the next ready() starts a new agent.
          if (this.starting === p) {
            this.starting = undefined;
          }
        }),
      () => undefined,
    );
    return p;
  }

  ready(): Promise<Agent> {
    return this.starting ?? this.start();
  }

  get pid(): number | undefined {
    return this.agent?.pid;
  }

  async restart(): Promise<void> {
    void this.stop();
    await this.start().catch(() => undefined);
  }

  stop(): Promise<void> {
    this.abort?.abort();
    this.starting = undefined;
    return this.serial(() => this.stopNow());
  }

  dispose(): void {
    void this.stop();
    this.startEmitter.dispose();
  }

  private serial<T>(op: () => Promise<T>): Promise<T> {
    const p = this.queue.then(op);
    this.queue = p.catch(() => undefined);
    return p;
  }

  private async stopNow(): Promise<void> {
    const agent = this.agent;
    this.agent = undefined;
    await agent?.dispose();
  }

  private async startNow(): Promise<Agent> {
    // Whatever runs is superseded by this start.
    await this.stopNow();
    const abort = new AbortController();
    this.abort = abort;
    try {
      const agent = await this.launch(abort.signal);
      if (abort.signal.aborted) {
        await agent.dispose();
        throw new Error("tenon start cancelled");
      }
      this.agent = agent;
      agent.onExit(() => {
        if (this.agent === agent) {
          this.agent = undefined;
        }
      });
      this.startEmitter.fire(agent);
      return agent;
    } finally {
      if (this.abort === abort) {
        this.abort = undefined;
      }
    }
  }

  private async launch(signal: AbortSignal): Promise<Agent> {
    const setting = vscode.workspace.getConfiguration("tenon").get<string>("path");
    const bin = resolveBinary({
      setting,
      extensionPath: this.ctx.extensionPath,
      envPath: process.env.PATH ?? "",
      exists: isFile,
    });
    if (!bin) {
      const where = setting?.trim();
      this.log.info(where ? `tenon not found at ${where}` : "tenon not found (setting, bundled binary, PATH)");
      void this.offerInstall(
        where
          ? vscode.l10n.t("tenon was not found at {0}. Install it or choose the binary.", where)
          : vscode.l10n.t("tenon was not found. Install it or choose the binary."),
      );
      throw new Error("tenon binary not found");
    }

    let output: string;
    try {
      output = await readVersion(bin);
    } catch (err) {
      this.log.info(`${bin} --version failed: ${errorMessage(err)}`);
      void this.offerInstall(vscode.l10n.t("tenon could not run ({0}): {1}", bin, errorMessage(err)));
      throw err;
    }
    const version = parseVersion(output);
    if (!version) {
      this.log.info(`${bin} --version printed "${output}": not a release version, accepted`);
    } else if (!meetsMinimum(version)) {
      const message = vscode.l10n.t(
        "tenon {0} is too old ({1} required)",
        formatVersion(version),
        formatVersion(MINIMUM_VERSION),
      );
      this.log.info(message);
      void this.offerChoose(showWarning(message, vscode.l10n.t("Choose binary")));
      throw new Error(message);
    } else {
      this.log.info(`${bin} version ${formatVersion(version)}`);
    }

    const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? os.homedir();
    signal.throwIfAborted();
    try {
      return await Agent.start(bin, cwd, this.log, this.handlers, {
        signal,
        initTimeoutMs: testInitTimeout(),
      });
    } catch (err) {
      if (!signal.aborted) {
        const showLogs = vscode.l10n.t("Show logs");
        void showError(vscode.l10n.t("tenon failed to start: {0}", errorMessage(err)), showLogs).then((choice) => {
          if (choice === showLogs) {
            this.log.show();
          }
        });
      }
      throw err;
    }
  }

  private async offerInstall(message: string): Promise<void> {
    const install = vscode.l10n.t("Install");
    const choice = await this.offerChoose(showError(message, install, vscode.l10n.t("Choose binary")));
    if (choice === install) {
      await vscode.env.openExternal(vscode.Uri.parse(INSTALL_URL));
    }
  }

  /** Handles "Choose binary" on a notification; returns the choice otherwise. */
  private async offerChoose(shown: Thenable<string | undefined>): Promise<string | undefined> {
    const choice = await shown;
    if (choice !== vscode.l10n.t("Choose binary")) {
      return choice;
    }
    const picked = await vscode.window.showOpenDialog({
      canSelectFiles: true,
      canSelectFolders: false,
      canSelectMany: false,
      openLabel: vscode.l10n.t("Use this tenon binary"),
    });
    const file = picked?.[0];
    if (file) {
      await vscode.workspace
        .getConfiguration("tenon")
        .update("path", file.fsPath, vscode.ConfigurationTarget.Global);
      await this.restart();
    }
    return undefined;
  }
}

let controller: Controller | undefined;

/** "Attach selection": the selection as a resource (the file when nothing is selected). */
function attachSelection(chat: Chat): boolean {
  const ed = vscode.window.activeTextEditor;
  if (!ed) {
    void showInfo(vscode.l10n.t("Open a file to attach its selection to the tenon chat."));
    return false;
  }
  const doc = ed.document;
  const name = vscode.workspace.asRelativePath(doc.uri, false);
  const sel = ed.selection;
  if (sel.isEmpty) {
    return attachFile(chat, doc.uri);
  }
  const start = sel.start.line + 1;
  // A selection ending at column 0 stops on the line before.
  const end = sel.end.character === 0 && sel.end.line > sel.start.line ? sel.end.line : sel.end.line + 1;
  chat.addAttachment(selectionBlock(doc.uri.toString(), doc.getText(sel), start, end), selectionLabel(name, start, end));
  return true;
}

/** "Attach file": a link to a file on disk (tenon reads it, under permission). */
function attachFile(chat: Chat, uri: vscode.Uri | undefined): boolean {
  const target = uri instanceof vscode.Uri ? uri : vscode.window.activeTextEditor?.document.uri;
  if (target?.scheme !== "file") {
    void showInfo(vscode.l10n.t("Only a file saved on disk can be attached to the tenon chat."));
    return false;
  }
  const name = vscode.workspace.asRelativePath(target, false);
  chat.addAttachment(fileBlock(target.toString(), name), name);
  return true;
}

export function activate(ctx: vscode.ExtensionContext): Promise<TenonTestApi | undefined> {
  const log = new OutputLog();
  const conv = new Conversation();
  const diffs = new DiffProvider(conv, log);
  const permissions = new Permissions(conv, {
    // view is set below, before tenon can send any request.
    visible: (): boolean => view.visible,
    notify: (message, names) => showInfoChoice(message, names),
    log,
  });
  const c = new Controller(ctx, log, clientHandlers(conv, permissions));
  controller = c;
  const chat = new Chat(c, conv, log, () => log.show(), { permissions, diffs });
  const view = new ChatViewProvider(ctx.extensionUri, chat, log);
  const status = new StatusBar({
    item: vscode.window.createStatusBarItem("tenon.status", vscode.StatusBarAlignment.Left, 0),
    warningColor: new vscode.ThemeColor("statusBarItem.warningBackground"),
    l10n: vscode.l10n,
  });
  const openChat = async (): Promise<void> => {
    chat.touch();
    await vscode.commands.executeCommand(`${CHAT_VIEW}.focus`);
  };
  ctx.subscriptions.push(
    status,
    conv.onDidChange((s) => status.update(s.meta)),
    log,
    c,
    chat,
    vscode.workspace.registerTextDocumentContentProvider(DIFF_SCHEME, diffs),
    vscode.window.registerWebviewViewProvider(CHAT_VIEW, view, {
      webviewOptions: { retainContextWhenHidden: false },
    }),
    vscode.commands.registerCommand("tenon.restart", () => c.restart()),
    vscode.commands.registerCommand("tenon.showLogs", () => log.show()),
    vscode.commands.registerCommand("tenon.openChat", openChat),
    vscode.commands.registerCommand("tenon.attachSelection", async () => {
      if (attachSelection(chat)) {
        await openChat();
      }
    }),
    vscode.commands.registerCommand("tenon.attachFile", async (uri?: vscode.Uri) => {
      if (attachFile(chat, uri)) {
        await openChat();
      }
    }),
    vscode.commands.registerCommand("tenon.newSession", async () => {
      await vscode.commands.executeCommand(`${CHAT_VIEW}.focus`);
      await chat.newSession();
    }),
  );

  let api: TenonTestApi | undefined;
  if (process.env.TENON_VSCODE_TEST === "1") {
    const shown: Shown[] = [];
    ctx.subscriptions.push(onDidShow((n) => shown.push(n)));
    api = {
      agentReady: async () => (await c.ready()).init,
      agentPid: () => c.pid,
      stopAgent: () => c.stop(),
      notifications: () => [...shown],
      state: () => {
        chat.touch();
        return chat.conv.state;
      },
      send: (text: string): Promise<StopReason> => chat.send(text),
      cancel: () => chat.cancel(),
      panelReady: () => view.scriptLoaded,
      answer: (optionId: string) => {
        const oldest = permissions.pendingIds()[0];
        if (oldest !== undefined) {
          chat.handle({ type: "permission", requestId: oldest, optionId });
        }
      },
      openDiff: (toolCallId: string) => diffs.open(toolCallId),
      lastDiff: () => diffs.lastOpened(),
      load: (sessionId: string) => chat.load(sessionId),
      status: () => ({ text: status.text, warning: status.warning }),
    };
  }

  return Promise.resolve(api);
}

export async function deactivate(): Promise<void> {
  await controller?.stop();
  controller = undefined;
}
