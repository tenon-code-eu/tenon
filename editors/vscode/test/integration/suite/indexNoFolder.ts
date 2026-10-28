// Entry point of the second VS Code launch, without a workspace folder
// (ruling R1): only the no-folder cases run there.
import { runAll } from "./harness";
import "./nofolder.test";

export async function run(): Promise<void> {
  const failed = await runAll();
  if (failed > 0) {
    throw new Error(`${failed} integration test(s) failed`);
  }
}
