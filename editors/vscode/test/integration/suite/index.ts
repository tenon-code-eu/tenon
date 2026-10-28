// Entry point loaded by VS Code (--extensionTestsPath): registers every test
// file, then runs them in order.
import { runAll } from "./harness";
import "./agent.test";
import "./chat.test";
import "./permission.test";

export async function run(): Promise<void> {
  const failed = await runAll();
  if (failed > 0) {
    throw new Error(`${failed} integration test(s) failed`);
  }
}
