// tenon.palette: a QuickPick of the tenon commands, the session's skills,
// the past sessions of the folder (session/load), the two themes and an
// informative entry on the provider. paletteItems and runPaletteAction take
// everything as arguments, so the unit tests drive them with the vscode stub.
import * as vscode from "vscode";
import type { ConversationState } from "./model";
import type { Theme } from "./protocol";

export type PaletteAction =
  | { type: "command"; command: string }
  | { type: "skill"; name: string }
  | { type: "session"; sessionId: string }
  | { type: "theme"; theme: Theme }
  | { type: "info" };

/** A QuickPick entry; separators have no action. */
export interface PaletteItem extends vscode.QuickPickItem {
  action?: PaletteAction;
}

/** What a pick does, injected (extension.ts; mocks in the unit tests). */
export interface PaletteDeps {
  executeCommand(command: string): Thenable<unknown>;
  load(sessionId: string): Promise<void>;
  /** Puts text in the chat input (a skill: `/name `). */
  insert(text: string): Promise<void>;
  /** Updates the `tenon.theme` setting. */
  setTheme(theme: Theme): Thenable<void>;
}

function separator(label: string): PaletteItem {
  return { label, kind: vscode.QuickPickItemKind.Separator };
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v !== "" ? v : undefined;
}

/** The entries, section by section: commands, skills, sessions, themes, provider. */
export function paletteItems(
  state: ConversationState,
  skills: ConversationState["commands"],
  sessions: ConversationState["sessions"],
  theme: Theme = "auto",
): PaletteItem[] {
  const items: PaletteItem[] = [separator(vscode.l10n.t("Commands"))];
  const commands: [string, string, string][] = [
    ["tenon.newSession", "$(add)", vscode.l10n.t("New session")],
    ["tenon.attachSelection", "$(selection)", vscode.l10n.t("Attach selection to chat")],
    ["tenon.attachFile", "$(file)", vscode.l10n.t("Attach file to chat")],
    ["tenon.openTerminal", "$(terminal)", vscode.l10n.t("Open in terminal")],
    ["tenon.restart", "$(debug-restart)", vscode.l10n.t("Restart tenon")],
    ["tenon.showLogs", "$(output)", vscode.l10n.t("Show logs")],
  ];
  for (const [command, icon, label] of commands) {
    items.push({ label: `${icon} ${label}`, action: { type: "command", command } });
  }

  if (skills.length > 0) {
    items.push(separator(vscode.l10n.t("Skills")));
    for (const s of skills) {
      items.push({ label: `/${s.name}`, detail: s.description, action: { type: "skill", name: s.name } });
    }
  }

  const past = sessions.filter((s) => s.sessionId !== state.sessionId);
  if (past.length > 0) {
    items.push(separator(vscode.l10n.t("Sessions")));
    for (const s of past) {
      items.push({
        label: `$(history) ${str(s.title) ?? vscode.l10n.t("Untitled session")}`,
        description: s.updatedAt,
        action: { type: "session", sessionId: s.sessionId },
      });
    }
  }

  items.push(separator(vscode.l10n.t("Theme")));
  const themes: [Theme, string][] = [
    ["auto", vscode.l10n.t("Theme: auto (editor colours)")],
    ["tenon", vscode.l10n.t("Theme: tenon (Europe blue and gold)")],
  ];
  for (const [t, label] of themes) {
    const item: PaletteItem = { label: `$(symbol-color) ${label}`, action: { type: "theme", theme: t } };
    if (t === theme) {
      item.description = vscode.l10n.t("current");
    }
    items.push(item);
  }

  items.push(separator(vscode.l10n.t("Provider")));
  const meta = state.meta;
  const provider = str(meta?.providerName) ?? str(meta?.provider);
  items.push({
    label: `$(info) ${provider ? vscode.l10n.t("Provider: {0}", provider) : vscode.l10n.t("Provider: not known until tenon starts")}`,
    detail: vscode.l10n.t("Change it with default_provider in the tenon configuration, then restart tenon."),
    action: { type: "info" },
  });
  return items;
}

/** Runs the action of a picked entry; the provider entry does nothing. */
export async function runPaletteAction(a: PaletteAction, deps: PaletteDeps): Promise<void> {
  switch (a.type) {
    case "command":
      await deps.executeCommand(a.command);
      break;
    case "skill":
      await deps.insert(`/${a.name} `);
      break;
    case "session":
      await deps.load(a.sessionId);
      break;
    case "theme":
      await deps.setTheme(a.theme);
      break;
    case "info":
      break;
  }
}

/** Shows the palette and runs the picked entry. */
export async function showPalette(state: ConversationState, theme: Theme, deps: PaletteDeps): Promise<void> {
  const items = paletteItems(state, state.commands, state.sessions, theme);
  const picked = await vscode.window.showQuickPick(items, {
    title: vscode.l10n.t("tenon palette"),
    placeHolder: vscode.l10n.t("Type to filter commands, skills, sessions and themes"),
    matchOnDescription: true,
    matchOnDetail: true,
  });
  if (picked?.action) {
    await runPaletteAction(picked.action, deps);
  }
}
