// Notifications shown by the extension. Every call goes through here so the
// test API can observe what was shown (onDidShow) without patching vscode.
import * as vscode from "vscode";

export interface Shown {
  kind: "error" | "warning" | "info";
  message: string;
  actions: string[];
}

const shown = new vscode.EventEmitter<Shown>();

/** Fires for every notification, before it is displayed. */
export const onDidShow: vscode.Event<Shown> = shown.event;

export function showError(message: string, ...actions: string[]): Thenable<string | undefined> {
  shown.fire({ kind: "error", message, actions });
  return vscode.window.showErrorMessage(message, ...actions);
}

export function showWarning(message: string, ...actions: string[]): Thenable<string | undefined> {
  shown.fire({ kind: "warning", message, actions });
  return vscode.window.showWarningMessage(message, ...actions);
}

export function showInfo(message: string, ...actions: string[]): Thenable<string | undefined> {
  shown.fire({ kind: "info", message, actions });
  return vscode.window.showInformationMessage(message, ...actions);
}
