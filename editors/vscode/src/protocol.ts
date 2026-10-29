// Messages between the extension host and the chat webview. The host posts
// the whole state; the webview posts user actions, validated strictly here
// (anything else is ignored).
import type { ConversationState } from "./model";

/** Links of the home screen, by id: the URLs stay on the host (panel.ts). */
export const LINK_IDS = ["docs", "github", "bug"] as const;
export type LinkId = (typeof LINK_IDS)[number];

export function isLinkId(x: unknown): x is LinkId {
  return typeof x === "string" && (LINK_IDS as readonly string[]).includes(x);
}

/** `tenon.theme`: auto follows the editor theme (--vscode-*), tenon applies the tenon colours. */
export type Theme = "auto" | "tenon";

/** The theme of a `tenon.theme` value; anything but "tenon" is auto. */
export function themeOf(v: unknown): Theme {
  return v === "tenon" ? "tenon" : "auto";
}

export type ToWebview = {
  type: "state";
  state: ConversationState;
  strings: Record<string, string>;
  /** tenon's version (initialize agentInfo); absent until tenon answered. */
  version?: string;
  theme: Theme;
};

export type FromWebview =
  | { type: "send"; text: string }
  | { type: "cancel" }
  | { type: "permission"; requestId: string; optionId: string }
  | { type: "openDiff"; toolCallId: string }
  | { type: "load"; sessionId: string }
  | { type: "newSession" }
  | { type: "pickFolder" }
  | { type: "ready" }
  | { type: "restart" }
  | { type: "showLogs" }
  | { type: "detach"; id: string }
  | { type: "openLink"; id: LinkId };

/**
 * Required string fields per message type; empty identifiers are refused,
 * and a field with `oneOf` takes only those values.
 */
const SHAPES: Record<FromWebview["type"], { field: string; nonEmpty: boolean; oneOf?: readonly string[] }[]> = {
  send: [{ field: "text", nonEmpty: false }],
  cancel: [],
  permission: [
    { field: "requestId", nonEmpty: true },
    { field: "optionId", nonEmpty: true },
  ],
  openDiff: [{ field: "toolCallId", nonEmpty: true }],
  load: [{ field: "sessionId", nonEmpty: true }],
  newSession: [],
  pickFolder: [],
  ready: [],
  restart: [],
  showLogs: [],
  detach: [{ field: "id", nonEmpty: true }],
  openLink: [{ field: "id", nonEmpty: true, oneOf: LINK_IDS }],
};

export function isFromWebview(x: unknown): x is FromWebview {
  if (typeof x !== "object" || x === null || Array.isArray(x)) {
    return false;
  }
  const m = x as Record<string, unknown>;
  const type = m.type;
  if (typeof type !== "string" || !Object.prototype.hasOwnProperty.call(SHAPES, type)) {
    return false;
  }
  const shape = SHAPES[type as FromWebview["type"]];
  const keys = Object.keys(m);
  if (keys.length !== shape.length + 1) {
    return false;
  }
  return shape.every(({ field, nonEmpty, oneOf }) => {
    const v = m[field];
    return typeof v === "string" && (!nonEmpty || v !== "") && (!oneOf || oneOf.includes(v));
  });
}
