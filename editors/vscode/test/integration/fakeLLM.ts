// Fake OpenAI-compatible provider driven by a script: request n receives
// script[n] (the last entry once the script is exhausted). Same wire format as
// cmd/tenon/e2e_test.go (newFakeLLM, sseText, sseCall). An entry made with
// slow() waits between events, so a test can stop a turn mid-answer.
import * as http from "node:http";
import type { AddressInfo } from "node:net";

export interface FakeLLM {
  url: string;
  requests(): string[];
  close(): Promise<void>;
}

/** One SSE event carrying a final text answer. */
export function sseText(s: string): string[] {
  return [`{"choices":[{"delta":{"content":${JSON.stringify(s)}},"finish_reason":"stop"}]}`];
}

/** One SSE event carrying a single tool call (args is the JSON arguments string). */
export function sseCall(name: string, args: string, id = "call_1"): string[] {
  return [
    `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":${JSON.stringify(id)},"function":{"name":${JSON.stringify(name)},"arguments":${JSON.stringify(args)}}}]},"finish_reason":"tool_calls"}]}`,
  ];
}

/** Several SSE events of streamed text; the last one ends the answer. */
export function sseChunks(parts: string[]): string[] {
  return parts.map(
    (p, i) =>
      `{"choices":[{"delta":{"content":${JSON.stringify(p)}},"finish_reason":${i === parts.length - 1 ? '"stop"' : "null"}}]}`,
  );
}

/** A script entry whose events are sent delayMs apart. */
export interface SlowEntry {
  events: string[];
  delayMs: number;
}

export function slow(events: string[], delayMs: number): SlowEntry {
  return { events, delayMs };
}

export type ScriptEntry = string[] | SlowEntry;

export async function startFakeLLM(script: ScriptEntry[]): Promise<FakeLLM> {
  const bodies: string[] = [];
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const i = Math.min(bodies.length, script.length - 1);
      bodies.push(Buffer.concat(chunks).toString("utf8"));
      const entry = script[i] ?? [];
      const events = Array.isArray(entry) ? entry : entry.events;
      const delayMs = Array.isArray(entry) ? 0 : entry.delayMs;
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      void (async () => {
        for (const line of events) {
          if (delayMs > 0) {
            await new Promise((r) => setTimeout(r, delayMs));
          }
          // The client went away (turn stopped): stop streaming.
          if (res.destroyed || res.writableEnded) {
            return;
          }
          res.write(`data: ${line}\n\n`);
        }
        if (!res.destroyed) {
          res.end("data: [DONE]\n\n");
        }
      })();
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}`,
    requests: () => [...bodies],
    close: () =>
      new Promise<void>((resolve) => {
        server.closeAllConnections();
        server.close(() => resolve());
      }),
  };
}
