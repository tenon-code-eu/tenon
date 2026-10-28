// Fake OpenAI-compatible provider driven by a script: request n receives
// script[n] (the last entry once the script is exhausted). Same wire format as
// cmd/tenon/e2e_test.go (newFakeLLM, sseText, sseCall).
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
export function sseCall(name: string, args: string): string[] {
  return [
    `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":${JSON.stringify(name)},"arguments":${JSON.stringify(args)}}}]},"finish_reason":"tool_calls"}]}`,
  ];
}

export async function startFakeLLM(script: string[][]): Promise<FakeLLM> {
  const bodies: string[] = [];
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const i = Math.min(bodies.length, script.length - 1);
      bodies.push(Buffer.concat(chunks).toString("utf8"));
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      for (const line of script[i] ?? []) {
        res.write(`data: ${line}\n\n`);
      }
      res.end("data: [DONE]\n\n");
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
