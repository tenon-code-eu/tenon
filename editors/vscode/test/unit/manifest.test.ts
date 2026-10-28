// Contributions of package.json that the docs promise.
import * as fs from "node:fs";
import * as path from "node:path";
import { describe, expect, it } from "vitest";

const manifest = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../package.json"), "utf8")) as {
  contributes: { menus: Record<string, { command: string; when?: string }[]> };
};

describe("menus", () => {
  it("right-click a file in the Explorer attaches it (files only)", () => {
    const entry = manifest.contributes.menus["explorer/context"]?.find((m) => m.command === "tenon.attachFile");
    expect(entry?.when).toBe("resourceScheme == file && !explorerResourceIsFolder");
  });

  it("right-click in the editor attaches the selection or the file", () => {
    const cmds = manifest.contributes.menus["editor/context"].map((m) => m.command);
    expect(cmds).toEqual(["tenon.attachSelection", "tenon.attachFile"]);
  });
});
