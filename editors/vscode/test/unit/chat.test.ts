import { beforeEach, describe, expect, it, vi } from "vitest";
import * as vscode from "vscode";
import type { Agent } from "../../src/agent";
import { Chat } from "../../src/chat";
import { Conversation } from "../../src/model";

type Conn = Record<string, ReturnType<typeof vi.fn>>;

/** A deferred promise. */
function later<T>(): { promise: Promise<T>; resolve: (v: T) => void } {
  let resolve: (v: T) => void = () => undefined;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function setup(conn: Conn, ready?: Promise<void>): { chat: Chat; conv: Conversation } {
  const agent = { connection: conn, hasExited: false, onExit: () => ({ dispose: () => undefined }) } as unknown as Agent;
  const started = new vscode.EventEmitter<Agent>();
  const conv = new Conversation();
  const source = {
    ready: async () => {
      await ready;
      return agent;
    },
    onDidStart: started.event,
  };
  const log = { info: () => undefined, stderr: () => undefined };
  return { chat: new Chat(source, conv, log, () => undefined), conv };
}

describe("Chat", () => {
  beforeEach(() => {
    (vscode.workspace as { workspaceFolders: unknown }).workspaceFolders = [{ uri: { fsPath: "/p" } }];
  });

  it("claims the turn at once: two back-to-back sends make one session and one prompt", async () => {
    // tenon is still starting when both sends arrive.
    const start = later<void>();
    const newSession = vi.fn().mockResolvedValue({ sessionId: "s1" });
    const prompt = vi.fn().mockResolvedValue({ stopReason: "end_turn" });
    const { chat, conv } = setup({ newSession, prompt }, start.promise);
    const first = chat.send("one");
    expect(conv.state.busy).toBe(true);
    const second = chat.send("two");
    await expect(second).rejects.toThrow("a turn is in progress");
    chat.handle({ type: "send", text: "three" });
    start.resolve();
    expect(await first).toBe("end_turn");
    expect(newSession).toHaveBeenCalledTimes(1);
    expect(prompt).toHaveBeenCalledTimes(1);
    expect(conv.state.items).toEqual([{ kind: "user", text: "one" }]);
    expect(conv.state.busy).toBe(false);
  });

  it("releases the turn when the session cannot be created", async () => {
    const newSession = vi.fn().mockRejectedValueOnce(new Error("refused")).mockResolvedValue({ sessionId: "s1" });
    const prompt = vi.fn().mockResolvedValue({ stopReason: "end_turn" });
    const { chat, conv } = setup({ newSession, prompt });
    await expect(chat.send("one")).rejects.toThrow("refused");
    expect(conv.state.busy).toBe(false);
    expect(await chat.send("two")).toBe("end_turn");
  });

  for (const op of ["newSession", "load"] as const) {
    it(`${op} during a turn stops and awaits the turn first`, async () => {
      const turn = later<{ stopReason: string }>();
      const order: string[] = [];
      const conn: Conn = {
        newSession: vi.fn().mockImplementation(() => {
          order.push("session/new");
          return Promise.resolve({ sessionId: `s${order.length}` });
        }),
        prompt: vi.fn().mockReturnValue(turn.promise),
        cancel: vi.fn().mockImplementation(() => {
          order.push("cancel");
          // tenon ends the turn after session/cancel.
          setTimeout(() => {
            order.push("turn ended");
            turn.resolve({ stopReason: "cancelled" });
          }, 5);
          return Promise.resolve();
        }),
        loadSession: vi.fn().mockImplementation(() => {
          order.push("session/load");
          return Promise.resolve({});
        }),
      };
      const { chat, conv } = setup(conn);
      const sending = chat.send("long");
      await vi.waitFor(() => expect(conn.prompt).toHaveBeenCalled());
      await (op === "newSession" ? chat.newSession() : chat.load("old"));
      expect(await sending).toBe("cancelled");
      expect(order).toEqual(["session/new", "cancel", "turn ended", op === "newSession" ? "session/new" : "session/load"]);
      expect(conv.state.busy).toBe(false);
    });
  }
});
