// `tenon acp` child process and its ACP client connection (stdin/stdout,
// one JSON message per line). stderr goes to the Output channel.
import { type ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { Readable, Writable } from "node:stream";
import {
  ClientSideConnection,
  type InitializeResponse,
  ndJsonStream,
  PROTOCOL_VERSION,
  type RequestPermissionRequest,
  type RequestPermissionResponse,
  type SessionNotification,
} from "@agentclientprotocol/sdk";
import * as vscode from "vscode";
import { version as extensionVersion } from "../package.json";
import type { Log } from "./log";

/** Requests and notifications from tenon to the editor. */
export interface ClientHandlers {
  sessionUpdate(n: SessionNotification): Promise<void>;
  requestPermission(r: RequestPermissionRequest): Promise<RequestPermissionResponse>;
}

/** Time given to tenon to exit after stdin is closed, before SIGKILL. */
const EXIT_GRACE_MS = 5_000;

export interface Exit {
  code: number | null;
}

export class Agent {
  private readonly exitEmitter = new vscode.EventEmitter<Exit>();
  /** Fires once when the child exits, whatever the cause. */
  readonly onExit: vscode.Event<Exit> = this.exitEmitter.event;
  private disposing: Promise<void> | undefined;

  private constructor(
    private readonly child: ChildProcessWithoutNullStreams,
    private readonly exited: Promise<Exit>,
    readonly connection: ClientSideConnection,
    readonly init: InitializeResponse,
  ) {
    void exited.then((e) => {
      this.exitEmitter.fire(e);
      this.exitEmitter.dispose();
    });
  }

  get pid(): number | undefined {
    return this.child.pid;
  }

  /**
   * Spawns `bin acp` in cwdForSpawn and sends `initialize`. Rejects when the
   * process cannot be spawned, exits first, or refuses initialize.
   */
  static async start(bin: string, cwdForSpawn: string, log: Log, client: ClientHandlers): Promise<Agent> {
    log.info(`starting ${bin} acp (cwd ${cwdForSpawn})`);
    const child = spawn(bin, ["acp"], { cwd: cwdForSpawn, stdio: ["pipe", "pipe", "pipe"], env: process.env });

    const exited = new Promise<Exit>((resolve) => {
      child.once("exit", (code, signal) => {
        log.info(`tenon exited (code ${code ?? "none"}${signal ? `, signal ${signal}` : ""})`);
        resolve({ code });
      });
    });
    const failed = new Promise<never>((_, reject) => {
      child.once("error", (err) => reject(new Error(`cannot start ${bin}: ${err.message}`)));
      void exited.then(({ code }) =>
        reject(new Error(`tenon exited before initialize (code ${code ?? "none"})`)),
      );
    });
    failed.catch(() => undefined);

    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (text: string) => log.stderr(text));
    // Writes after the child died raise EPIPE on stdin: the exit is reported
    // through onExit, the error itself only goes to the log.
    child.stdin.on("error", (err) => log.info(`tenon stdin: ${err.message}`));

    const stream = ndJsonStream(
      Writable.toWeb(child.stdin) as WritableStream<Uint8Array>,
      Readable.toWeb(child.stdout) as ReadableStream<Uint8Array>,
    );
    const connection = new ClientSideConnection(
      () => ({
        sessionUpdate: (n) => client.sessionUpdate(n),
        requestPermission: (r) => client.requestPermission(r),
      }),
      stream,
    );

    try {
      const init = await Promise.race([
        connection.initialize({
          protocolVersion: PROTOCOL_VERSION,
          clientInfo: { name: "tenon-vscode", version: extensionVersion },
          clientCapabilities: {},
        }),
        failed,
      ]);
      log.info(`tenon ready (pid ${child.pid ?? "?"}, protocol ${init.protocolVersion})`);
      return new Agent(child, exited, connection, init);
    } catch (err) {
      if (child.exitCode === null && child.signalCode === null) {
        child.kill("SIGKILL");
      }
      throw err;
    }
  }

  /** Closes stdin, waits up to 5 s for tenon to exit, then sends SIGKILL. */
  dispose(): Promise<void> {
    this.disposing ??= this.stop();
    return this.disposing;
  }

  private async stop(): Promise<void> {
    if (this.child.exitCode !== null || this.child.signalCode !== null) {
      return;
    }
    this.child.stdin.end();
    let timer: NodeJS.Timeout | undefined;
    const graceful = await Promise.race([
      this.exited.then(() => true),
      new Promise<boolean>((resolve) => {
        timer = setTimeout(() => resolve(false), EXIT_GRACE_MS);
      }),
    ]);
    clearTimeout(timer);
    if (!graceful) {
      this.child.kill("SIGKILL");
      await this.exited;
    }
  }
}
