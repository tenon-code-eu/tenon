// Activation and commands. One `tenon acp` per window, started on activation
// (Task 3 moves the start to the first use of the panel or a command).
import * as fs from "node:fs";
import * as os from "node:os";
import type { InitializeResponse, RequestPermissionResponse } from "@agentclientprotocol/sdk";
import * as vscode from "vscode";
import { Agent, type ClientHandlers } from "./agent";
import { formatVersion, meetsMinimum, MINIMUM_VERSION, parseVersion, readVersion, resolveBinary } from "./binary";
import { type Log, OutputLog } from "./log";
import { onDidShow, showError, showWarning, type Shown } from "./notify";

export type { Shown } from "./notify";

/** Returned by activate() only when TENON_VSCODE_TEST=1; grows with later tasks. */
export interface TenonTestApi {
  agentReady(): Promise<InitializeResponse>;
  agentPid(): number | undefined;
  stopAgent(): Promise<void>;
  notifications(): Shown[];
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

/** Placeholder handlers until the chat panel (Task 3) and permissions (Task 4). */
function stubHandlers(log: Log): ClientHandlers {
  return {
    sessionUpdate: (n) => {
      log.info(`session/update ${n.update.sessionUpdate} (no panel yet)`);
      return Promise.resolve();
    },
    requestPermission: (r): Promise<RequestPermissionResponse> => {
      log.info(`session/request_permission "${r.toolCall.title ?? ""}" cancelled (no panel yet)`);
      return Promise.resolve({ outcome: { outcome: "cancelled" } });
    },
  };
}

class Controller implements vscode.Disposable {
  private starting: Promise<Agent> | undefined;
  private agent: Agent | undefined;

  constructor(
    private readonly ctx: vscode.ExtensionContext,
    private readonly log: OutputLog,
  ) {}

  /** Starts tenon; failures are reported to the user and reject the returned promise. */
  start(): Promise<Agent> {
    const p = this.launch();
    this.starting = p;
    p.catch(() => undefined);
    return p;
  }

  ready(): Promise<Agent> {
    return this.starting ?? this.start();
  }

  get pid(): number | undefined {
    return this.agent?.pid;
  }

  async restart(): Promise<void> {
    await this.stop();
    await this.start().catch(() => undefined);
  }

  async stop(): Promise<void> {
    const starting = this.starting;
    this.starting = undefined;
    const agent = await starting?.catch(() => undefined);
    if (this.agent === agent) {
      this.agent = undefined;
    }
    await agent?.dispose();
  }

  dispose(): void {
    void this.stop();
  }

  private async launch(): Promise<Agent> {
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
    let agent: Agent;
    try {
      agent = await Agent.start(bin, cwd, this.log, stubHandlers(this.log));
    } catch (err) {
      const showLogs = vscode.l10n.t("Show logs");
      void showError(vscode.l10n.t("tenon failed to start: {0}", errorMessage(err)), showLogs).then((choice) => {
        if (choice === showLogs) {
          this.log.show();
        }
      });
      throw err;
    }
    this.agent = agent;
    agent.onExit(() => {
      if (this.agent === agent) {
        this.agent = undefined;
      }
    });
    return agent;
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
  const c = new Controller(ctx, log);
  controller = c;
  ctx.subscriptions.push(
    log,
    vscode.commands.registerCommand("tenon.restart", () => c.restart()),
    vscode.commands.registerCommand("tenon.showLogs", () => log.show()),
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
    };
  }

  void c.start();
  return Promise.resolve(api);
}

export async function deactivate(): Promise<void> {
  await controller?.stop();
  controller = undefined;
}
