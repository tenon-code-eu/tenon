// Messages of the Session view, checked on the host: only ready, and
// openFileDiff naming a tool item of the thread that has a diff. No vscode
// runtime import (unit-tested under Node).
import type { DiffSource } from "./diff";
import { isFromSessionView } from "./protocol";

/**
 * Handles one message of the Session view; returns its type when it was
 * taken, undefined when ignored (malformed, another type, or a tool call
 * with no diff in the thread).
 */
export function handleSessionMessage(
  m: unknown,
  source: DiffSource,
  open: (toolCallId: string) => Promise<void>,
): "ready" | "openFileDiff" | undefined {
  if (!isFromSessionView(m)) {
    return undefined;
  }
  if (m.type === "ready") {
    return "ready";
  }
  if (!source.toolDiff(m.toolCallId)) {
    return undefined;
  }
  void open(m.toolCallId).catch(() => undefined);
  return "openFileDiff";
}
