import { describe, expect, it, vi } from "vitest";
import * as vscode from "vscode";
import type { ConversationState } from "../../src/model";
import { escapeIcons, type PaletteItem, paletteItems, runPaletteAction, setTheme } from "../../src/palette";

function state(over: Partial<ConversationState> = {}): ConversationState {
  return { items: [], busy: false, commands: [], sessions: [], attachments: [], ...over };
}

const SKILLS = [
  { name: "review", description: "Review the diff" },
  { name: "test", description: "Run the tests" },
];
const SESSIONS = [
  { sessionId: "s1", title: "Current", updatedAt: "2026-10-29T10:00:00Z" },
  { sessionId: "s2", title: "", updatedAt: "2026-10-28T10:00:00Z" },
];

function kinds(items: PaletteItem[]): string[] {
  return items.filter((i) => i.action).map((i) => i.action?.type ?? "");
}

describe("paletteItems", () => {
  it("lists commands, skills, sessions, themes, then the provider, each section under a separator", () => {
    const items = paletteItems(state({ sessionId: "s1", meta: { providerName: "Mistral AI", provider: "mistral" } }), SKILLS, SESSIONS, "auto");
    const order = kinds(items);
    const firstOf = (k: string) => order.indexOf(k);
    const lastOf = (k: string) => order.lastIndexOf(k);
    expect(lastOf("command")).toBeLessThan(firstOf("skill"));
    expect(lastOf("skill")).toBeLessThan(firstOf("session"));
    expect(lastOf("session")).toBeLessThan(firstOf("theme"));
    expect(lastOf("theme")).toBeLessThan(firstOf("info"));
    expect(items.filter((i) => i.kind === vscode.QuickPickItemKind.Separator).length).toBe(5);

    const commands = items.filter((i) => i.action?.type === "command").map((i) => (i.action as { command: string }).command);
    expect(commands).toEqual([
      "tenon.newSession",
      "tenon.attachSelection",
      "tenon.attachFile",
      "tenon.openTerminal",
      "tenon.restart",
      "tenon.showLogs",
    ]);
    const skills = items.filter((i) => i.action?.type === "skill");
    expect(skills.map((i) => i.label)).toEqual(["/review", "/test"]);
    expect(skills[0].detail).toBe("Review the diff");
    // The open session is not offered; an untitled one gets a label.
    const sessions = items.filter((i) => i.action?.type === "session");
    expect(sessions.map((i) => i.action)).toEqual([{ type: "session", sessionId: "s2" }]);
    expect(sessions[0].label).toContain("Untitled session");
    const themes = items.filter((i) => i.action?.type === "theme");
    expect(themes.map((i) => i.action)).toEqual([
      { type: "theme", theme: "auto" },
      { type: "theme", theme: "tenon" },
    ]);
    expect(themes[0].description).toBe("current");
    expect(themes[1].description).toBeUndefined();
    const info = items.find((i) => i.action?.type === "info");
    expect(info?.label).toContain("Provider: Mistral AI");
    expect(info?.detail).toContain("default_provider");
  });

  it("skips the separator of an empty section", () => {
    const items = paletteItems(state(), [], [], "tenon");
    const seps = items.filter((i) => i.kind === vscode.QuickPickItemKind.Separator).map((i) => i.label);
    expect(seps).toEqual(["Commands", "Theme", "Provider"]);
    expect(items.find((i) => i.action?.type === "info")?.label).toContain("not known");
  });
});

describe("escaping codicons", () => {
  it("skill names, descriptions, session titles and the provider cannot inject a $(icon)", () => {
    const items = paletteItems(
      state({ sessionId: "s1", meta: { providerName: "$(alert) Evil" } }),
      [{ name: "$(zap)x", description: "do $(trash) it" }],
      [{ sessionId: "s9", title: "fix $(check) now", updatedAt: "$(clock)" }],
    );
    const skill = items.find((i) => i.action?.type === "skill");
    expect(skill?.label).toBe("/\\$(zap)x");
    expect(skill?.detail).toBe("do \\$(trash) it");
    expect(skill?.action).toEqual({ type: "skill", name: "$(zap)x" });
    const session = items.find((i) => i.action?.type === "session");
    expect(session?.label).toBe("$(history) fix \\$(check) now");
    expect(session?.description).toBe("\\$(clock)");
    expect(items.find((i) => i.action?.type === "info")?.label).toBe("$(info) Provider: \\$(alert) Evil");
  });

  it("escapeIcons leaves plain text and an already escaped icon alone", () => {
    expect(escapeIcons("plain (text) $5")).toBe("plain (text) $5");
    expect(escapeIcons("\\$(x)")).toBe("\\$(x)");
  });
});

describe("setTheme", () => {
  function config(inspect: Record<string, unknown> | undefined) {
    return { inspect: vi.fn().mockReturnValue(inspect), update: vi.fn().mockResolvedValue(undefined) };
  }

  it("writes the user settings when no workspace value shadows them", async () => {
    const c = config({ key: "tenon.theme", defaultValue: "auto", globalValue: "auto" });
    await setTheme(c, "tenon");
    expect(c.update).toHaveBeenCalledWith("theme", "tenon", vscode.ConfigurationTarget.Global);
  });

  it("writes the workspace settings when they set tenon.theme (they win over the user settings)", async () => {
    const c = config({ key: "tenon.theme", defaultValue: "auto", globalValue: "tenon", workspaceValue: "auto" });
    await setTheme(c, "tenon");
    expect(c.update).toHaveBeenCalledWith("theme", "tenon", vscode.ConfigurationTarget.Workspace);
  });
});

describe("runPaletteAction", () => {
  function deps() {
    return {
      executeCommand: vi.fn().mockResolvedValue(undefined),
      load: vi.fn().mockResolvedValue(undefined),
      insert: vi.fn().mockResolvedValue(undefined),
      setTheme: vi.fn().mockResolvedValue(undefined),
    };
  }

  it("a command runs it", async () => {
    const d = deps();
    await runPaletteAction({ type: "command", command: "tenon.restart" }, d);
    expect(d.executeCommand).toHaveBeenCalledWith("tenon.restart");
  });

  it("a session loads it", async () => {
    const d = deps();
    await runPaletteAction({ type: "session", sessionId: "s2" }, d);
    expect(d.load).toHaveBeenCalledWith("s2");
  });

  it("a theme updates tenon.theme", async () => {
    const d = deps();
    await runPaletteAction({ type: "theme", theme: "tenon" }, d);
    expect(d.setTheme).toHaveBeenCalledWith("tenon");
  });

  it("a skill puts /name in the chat input", async () => {
    const d = deps();
    await runPaletteAction({ type: "skill", name: "review" }, d);
    expect(d.insert).toHaveBeenCalledWith("/review ");
  });

  it("the provider entry does nothing", async () => {
    const d = deps();
    await runPaletteAction({ type: "info" }, d);
    for (const f of Object.values(d)) {
      expect(f).not.toHaveBeenCalled();
    }
  });
});
