import { describe, expect, it, vi } from "vitest";
import type * as vscode from "vscode";
import type { Chat } from "../../src/chat";
import { ChatViewProvider } from "../../src/panel";

function provider() {
  const handle = vi.fn();
  const chat = { handle } as unknown as Chat;
  const host = { version: () => undefined, theme: () => "auto" as const, openUrl: vi.fn(), palette: vi.fn() };
  const log = { info: vi.fn(), stderr: vi.fn() };
  const p = new ChatViewProvider({} as vscode.Uri, chat, log, host);
  return { p, chat: { handle }, host };
}

describe("ChatViewProvider.receive", () => {
  it("palette opens the palette, nothing reaches the chat", () => {
    const { p, chat, host } = provider();
    p.receive({ type: "palette" });
    expect(host.palette).toHaveBeenCalledTimes(1);
    expect(chat.handle).not.toHaveBeenCalled();
  });

  it("openFileDiff is the Session view's message: ignored by the chat", () => {
    const { p, chat, host } = provider();
    p.receive({ type: "openFileDiff", toolCallId: "t1" });
    p.receive({ type: "palette", extra: 1 });
    expect(chat.handle).not.toHaveBeenCalled();
    expect(host.palette).not.toHaveBeenCalled();
  });
});
