import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as vscode from "vscode";
import { lineRange } from "../../src/context";
import { closeTenonTerminal, editorReference, openTenonTerminal, referenceFor } from "../../src/terminal";

type FakeTerminal = { name: string; exitStatus: unknown; show: ReturnType<typeof vi.fn> };
type Stub = {
  createTerminal: ReturnType<typeof vi.fn>;
  activeTerminal: unknown;
  closeTerminal: vscode.EventEmitter<unknown>;
  activeChanged: vscode.EventEmitter<unknown>;
};

describe("referenceFor", () => {
  it("a range of lines", () => {
    expect(referenceFor("src/a.go", 10, 20)).toBe("@src/a.go#L10-20");
  });

  it("a single line", () => {
    expect(referenceFor("src/a.go", 5, 5)).toBe("@src/a.go#L5");
  });

  it("no selection: the file alone", () => {
    expect(referenceFor("src/a.go")).toBe("@src/a.go");
    expect(referenceFor("src/a.go", 0, 0)).toBe("@src/a.go");
  });
});

describe("lineRange", () => {
  it("1-based lines; a selection ending at column 0 stops on the line before", () => {
    expect(lineRange(9, 19, 4)).toEqual([10, 20]);
    expect(lineRange(9, 12, 0)).toEqual([10, 12]);
    expect(lineRange(4, 4, 0)).toEqual([5, 5]);
  });
});

describe("editorReference", () => {
  it("path relative to the terminal folder, with the selected lines", () => {
    expect(editorReference("/p", "/p/src/a.go", { empty: false, startLine: 9, endLine: 19, endCharacter: 3 })).toBe(
      "@src/a.go#L10-20",
    );
  });

  it("an empty selection names the file", () => {
    expect(editorReference("/p", "/p/src/a.go", { empty: true, startLine: 3, endLine: 3, endCharacter: 0 })).toBe(
      "@src/a.go",
    );
  });
});

describe("openTenonTerminal", () => {
  let stub: Stub;
  let contexts: [string, unknown][];

  beforeEach(() => {
    contexts = [];
    const closeTerminal = new vscode.EventEmitter<unknown>();
    const activeChanged = new vscode.EventEmitter<unknown>();
    stub = {
      createTerminal: vi.fn((opts: { name: string }) => {
        const t: FakeTerminal = { name: opts.name, exitStatus: undefined, show: vi.fn() };
        return t;
      }),
      activeTerminal: undefined,
      closeTerminal,
      activeChanged,
    };
    const w = vscode.window as unknown as Record<string, unknown>;
    w.createTerminal = stub.createTerminal;
    w.onDidCloseTerminal = closeTerminal.event;
    w.onDidChangeActiveTerminal = activeChanged.event;
    Object.defineProperty(w, "activeTerminal", { get: () => stub.activeTerminal, configurable: true });
    (vscode.commands as unknown as Record<string, unknown>).executeCommand = (cmd: string, key: string, v: unknown) => {
      if (cmd === "setContext") {
        contexts.push([key, v]);
      }
      return Promise.resolve(undefined);
    };
  });

  afterEach(() => {
    closeTenonTerminal();
  });

  it("creates the tenon terminal beside the editor, running the TUI in the folder", () => {
    const t = openTenonTerminal("/bin/tenon", "/p");
    expect(stub.createTerminal).toHaveBeenCalledTimes(1);
    expect(stub.createTerminal.mock.calls[0][0]).toMatchObject({
      name: "tenon",
      shellPath: "/bin/tenon",
      cwd: "/p",
      location: { viewColumn: vscode.ViewColumn.Beside },
    });
    expect(t.name).toBe("tenon");
    expect(contexts).toContainEqual(["tenon.terminalVisible", true]);
  });

  it("reuses the terminal while it is open, and shows it again", () => {
    const a = openTenonTerminal("/bin/tenon", "/p") as unknown as FakeTerminal;
    const b = openTenonTerminal("/bin/tenon", "/p") as unknown as FakeTerminal;
    expect(b).toBe(a);
    expect(stub.createTerminal).toHaveBeenCalledTimes(1);
    expect(a.show).toHaveBeenCalledTimes(2);
  });

  it("a closed terminal is replaced and the context key goes back to false", () => {
    const a = openTenonTerminal("/bin/tenon", "/p");
    stub.closeTerminal.fire(a);
    expect(contexts[contexts.length - 1]).toEqual(["tenon.terminalVisible", false]);
    const b = openTenonTerminal("/bin/tenon", "/p");
    expect(b).not.toBe(a);
    expect(stub.createTerminal).toHaveBeenCalledTimes(2);
  });

  it("the context key follows the active terminal", () => {
    const a = openTenonTerminal("/bin/tenon", "/p");
    stub.activeTerminal = { name: "bash" };
    stub.activeChanged.fire(stub.activeTerminal);
    expect(contexts[contexts.length - 1]).toEqual(["tenon.terminalVisible", false]);
    stub.activeTerminal = a;
    stub.activeChanged.fire(a);
    expect(contexts[contexts.length - 1]).toEqual(["tenon.terminalVisible", true]);
  });

  it("another terminal closing changes nothing", () => {
    openTenonTerminal("/bin/tenon", "/p");
    const n = contexts.length;
    stub.closeTerminal.fire({ name: "bash" });
    expect(contexts.length).toBe(n);
    openTenonTerminal("/bin/tenon", "/p");
    expect(stub.createTerminal).toHaveBeenCalledTimes(1);
  });
});
