// Minimal `vscode` module for unit tests of code that needs a few runtime
// APIs (vitest.config.ts aliases "vscode" here). Tests set the fields they need.
export class EventEmitter<T> {
  private readonly listeners = new Set<(e: T) => unknown>();
  readonly event = (f: (e: T) => unknown) => {
    this.listeners.add(f);
    return { dispose: () => this.listeners.delete(f) };
  };
  fire(e: T): void {
    for (const f of [...this.listeners]) {
      f(e);
    }
  }
  dispose(): void {
    this.listeners.clear();
  }
}

export const l10n = {
  t: (message: string, ...args: unknown[]): string =>
    message.replace(/\{(\d+)\}/g, (_, i: string) => String(args[Number(i)])),
};

export const workspace: {
  workspaceFolders: { uri: { fsPath: string } }[] | undefined;
  onDidChangeWorkspaceFolders: (f: () => unknown) => { dispose(): void };
} = {
  workspaceFolders: undefined,
  onDidChangeWorkspaceFolders: () => ({ dispose: () => undefined }),
};

export const ViewColumn = { Active: -1, Beside: -2 } as const;

const noEvent = (): { dispose(): void } => ({ dispose: () => undefined });

export const window: Record<string, unknown> = {
  showWorkspaceFolderPick: (): Promise<undefined> => Promise.resolve(undefined),
  createTerminal: (): never => {
    throw new Error("createTerminal: set by the test");
  },
  onDidCloseTerminal: noEvent,
  onDidChangeActiveTerminal: noEvent,
  activeTerminal: undefined,
};

export const commands = {
  executeCommand: (): Promise<undefined> => Promise.resolve(undefined),
};

export const QuickPickItemKind = { Separator: -1, Default: 0 } as const;
