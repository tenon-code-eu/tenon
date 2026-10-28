// Editor context sent with a prompt: the selection as an embedded `resource`
// (its text, `#L<a>-<b>` on the uri), a file as a `resource_link`. Built on
// the host from the editor state, never from webview data. No vscode runtime
// import (unit-tested under plain Node).
import type { ContentBlock } from "@agentclientprotocol/sdk";

/** A selection of uri, lines startLine..endLine (1-based, inclusive). */
export function selectionBlock(uri: string, text: string, startLine: number, endLine: number): ContentBlock {
  return { type: "resource", resource: { uri: `${uri}#L${startLine}-${endLine}`, text, mimeType: "text/plain" } };
}

/** A link to a file; tenon reads it with its tools, under permission. */
export function fileBlock(uri: string, name: string): ContentBlock {
  return { type: "resource_link", uri, name };
}

/** The prompt: the typed text first (left out when blank), then the attachments in order. */
export function buildPrompt(text: string, attachments: ContentBlock[]): ContentBlock[] {
  const blocks: ContentBlock[] = text.trim() === "" ? [] : [{ type: "text", text }];
  return [...blocks, ...attachments];
}

/** Chip label of a selection: `<name>:<a>-<b>`. */
export function selectionLabel(name: string, startLine: number, endLine: number): string {
  return startLine === endLine ? `${name}:${startLine}` : `${name}:${startLine}-${endLine}`;
}

/**
 * 1-based first and last lines of a selection given by its 0-based start
 * line, end line and end column. A selection ending at column 0 of a later
 * line stops on the line before.
 */
export function lineRange(startLine: number, endLine: number, endCharacter: number): [number, number] {
  const end = endCharacter === 0 && endLine > startLine ? endLine : endLine + 1;
  return [startLine + 1, end];
}
