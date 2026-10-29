// Contributions of package.json that the docs promise.
import * as fs from "node:fs";
import * as path from "node:path";
import { describe, expect, it } from "vitest";

const manifest = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../package.json"), "utf8")) as {
  contributes: {
    views: Record<string, { id: string; type?: string }[]>;
    commands: { command: string; title: string }[];
    keybindings: { command: string; key: string; mac?: string; when?: string }[];
    menus: Record<string, { command: string; when?: string }[]>;
    configuration: { properties: Record<string, { default?: unknown; enum?: unknown[] }> };
  };
};

describe("settings", () => {
  it("tenon.theme is auto (default) or tenon", () => {
    const theme = manifest.contributes.configuration.properties["tenon.theme"];
    expect(theme?.default).toBe("auto");
    expect(theme?.enum).toEqual(["auto", "tenon"]);
  });
});

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

describe("session view and palette", () => {
  it("the Session view is a webview under the chat in the tenon container", () => {
    expect(manifest.contributes.views.tenon.map((v) => [v.id, v.type])).toEqual([
      ["tenon.chat", "webview"],
      ["tenon.session", "webview"],
    ]);
  });

  it("tenon.palette is Cmd+Shift+; (Ctrl+Shift+;) while the chat view has focus", () => {
    expect(manifest.contributes.commands.some((c) => c.command === "tenon.palette")).toBe(true);
    const keys = manifest.contributes.keybindings.filter((k) => k.command === "tenon.palette");
    expect(keys).toEqual([{ command: "tenon.palette", key: "ctrl+shift+;", mac: "cmd+shift+;", when: "focusedView == tenon.chat" }]);
  });
});
