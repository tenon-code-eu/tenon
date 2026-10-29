// Status bar: `tenon · <provider name> · <EU|non-EU|local> · <sandboxed|not
// sandboxed>` from the session's `_meta.tenon` (provider fields replaced by
// each usage_update); anything but EU or local in the theme's warning
// colour; a click opens the chat. statusText has no vscode runtime use.
import type * as vscode from "vscode";

/** Translation function (vscode.l10n in the extension, a stub in tests). */
export interface L10n {
  t(message: string): string;
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

/** Label of a tenon jurisdiction (internal/llm: eu, non-eu, local); anything else as is, none as unknown. */
export function jurisdictionLabel(j: string, l10n: L10n): string {
  switch (j) {
    case "eu":
      return l10n.t("EU");
    case "non-eu":
      return l10n.t("non-EU");
    case "local":
      return l10n.t("local");
    case "":
      return l10n.t("unknown jurisdiction");
    default:
      return j;
  }
}

/**
 * Whether tenon confines shell commands (ruling R8): `sandbox.confined === true`.
 * An older tenon without `confined` sends mechanism "none" (never "") when
 * off or unavailable: "", "none" or mode "off" mean not sandboxed.
 */
export function isConfined(sandbox: Record<string, unknown>): boolean {
  if ("confined" in sandbox) {
    return sandbox.confined === true;
  }
  const mechanism = str(sandbox.mechanism);
  return mechanism !== "" && mechanism !== "none" && str(sandbox.mode) !== "off";
}

export function statusText(meta: Record<string, unknown> | undefined, l10n: L10n): { text: string; warning: boolean } {
  if (!meta) {
    return { text: "tenon", warning: false };
  }
  const jurisdiction = str(meta.jurisdiction);
  const sandbox = typeof meta.sandbox === "object" && meta.sandbox !== null ? (meta.sandbox as Record<string, unknown>) : {};
  const sandboxed = isConfined(sandbox);
  const parts = [
    "tenon",
    str(meta.providerName) || str(meta.provider),
    jurisdictionLabel(jurisdiction, l10n),
    sandboxed ? l10n.t("sandboxed") : l10n.t("not sandboxed"),
  ].filter((p) => p !== "");
  // Only EU and local are known safe: non-EU, unknown or missing get the warning.
  return { text: parts.join(" · "), warning: jurisdiction !== "eu" && jurisdiction !== "local" };
}

/** What StatusBar needs from vscode (injected, so the extension owns the runtime import). */
export interface StatusDeps {
  item: vscode.StatusBarItem;
  warningColor: vscode.ThemeColor;
  l10n: L10n;
}

/** The status bar item, kept in step with the session's meta. */
export class StatusBar implements vscode.Disposable {
  private last = "";

  constructor(private readonly deps: StatusDeps) {
    deps.item.command = "tenon.openChat";
    deps.item.name = "tenon";
    this.update(undefined);
    deps.item.show();
  }

  get text(): string {
    return this.deps.item.text;
  }

  get warning(): boolean {
    return this.deps.item.backgroundColor !== undefined;
  }

  update(meta: Record<string, unknown> | undefined): void {
    const s = statusText(meta, this.deps.l10n);
    const key = `${s.text}\n${s.warning}`;
    if (key === this.last) {
      return;
    }
    this.last = key;
    this.deps.item.text = s.text;
    this.deps.item.backgroundColor = s.warning ? this.deps.warningColor : undefined;
  }

  dispose(): void {
    this.deps.item.dispose();
  }
}
