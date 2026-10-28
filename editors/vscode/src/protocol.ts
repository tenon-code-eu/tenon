// Messages between the extension host and the chat webview. The host posts
// the whole state; the webview posts user actions, validated strictly here
// (anything else is ignored).
import type { ConversationState } from "./model";

export type ToWebview = { type: "state"; state: ConversationState; strings: Record<string, string> };

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
  | { type: "showLogs" };

/** Required string fields per message type; empty identifiers are refused. */
const SHAPES: Record<FromWebview["type"], { field: string; nonEmpty: boolean }[]> = {
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
  return shape.every(({ field, nonEmpty }) => {
    const v = m[field];
    return typeof v === "string" && (!nonEmpty || v !== "");
  });
}
