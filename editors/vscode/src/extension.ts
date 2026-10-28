// Activation and commands. One `tenon acp` per window, started on first use:
// the chat view shown, a tenon command, or a test API call.
import * as fs from "node:fs";
import * as os from "node:os";
import type { InitializeResponse, RequestPermissionResponse, StopReason } from "@agentclientprotocol/sdk";
import * as vscode from "vscode";
import { Agent, type ClientHandlers } from "./agent";
import { Chat } from "./chat";
import { formatVersion, meetsMinimum, MINIMUM_VERSION, parseVersion, readVersion, resolveBinary } from "./binary";
import { type Log, OutputLog } from "./log";
import { Conversation, type ConversationState } from "./model";
import { onDidShow, showError, showWarning, type Shown } from "./notify";
import { CHAT_VIEW, ChatViewProvider } from "./panel";

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
 * Updates go to the conversation; permission requests are answered
 * "cancelled" until permission cards exist (Task 4), adding nothing to the
 * thread.
 */
function clientHandlers(log: Log, conv: Conversation): ClientHandlers {
  return {
    sessionUpdate: (n) => {
      conv.apply(n);
      return Promise.resolve();
    },
    requestPermission: (r): Promise<RequestPermissionResponse> => {
      log.info(`session/request_permission "${r.toolCall.title ?? ""}" cancelled (no permission cards yet)`);
      return Promise.resolve({ outcome: { outcome: "cancelled" } });
    },
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

export function activate(ctx: vscode.ExtensionContext): Promise<TenonTestApi | undefined> {
  const log = new OutputLog();
  const conv = new Conversation();
  const c = new Controller(ctx, log, clientHandlers(log, conv));
  controller = c;
  const chat = new Chat(c, conv, log, () => log.show());
  const panel = new ChatViewProvider(ctx.extensionUri, chat, log);
  ctx.subscriptions.push(
    log,
    c,
    chat,
    vscode.window.registerWebviewViewProvider(CHAT_VIEW, panel, {
      webviewOptions: { retainContextWhenHidden: false },
    }),
    vscode.commands.registerCommand("tenon.restart", () => c.restart()),
    vscode.commands.registerCommand("tenon.showLogs", () => log.show()),
    vscode.commands.registerCommand("tenon.openChat", async () => {
      chat.touch();
      await vscode.commands.executeCommand(`${CHAT_VIEW}.focus`);
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
      panelReady: () => panel.scriptLoaded,
    };
  }

  return Promise.resolve(api);
}

export async function deactivate(): Promise<void> {
  await controller?.stop();
  controller = undefined;
}
