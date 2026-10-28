import * as path from "node:path";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    // Unit tests never load the real editor API: a small stub stands in.
    alias: { vscode: path.resolve(__dirname, "test/unit/stubs/vscode.ts") },
  },
  test: {
    include: ["test/unit/**/*.test.ts"],
    environment: "node",
  },
});
