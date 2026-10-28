import { describe, expect, it } from "vitest";
import { buildPrompt, fileBlock, selectionBlock } from "../../src/context";

describe("editor context blocks", () => {
  it("a selection is a resource block whose uri carries the 1-based lines", () => {
    const b = selectionBlock("file:///p/src/a.go", "x := 1\n", 10, 20);
    expect(b).toEqual({
      type: "resource",
      resource: { uri: "file:///p/src/a.go#L10-20", text: "x := 1\n", mimeType: "text/plain" },
    });
    expect(b.type === "resource" && b.resource.uri.endsWith("#L10-20")).toBe(true);
  });

  it("a file is a resource_link", () => {
    expect(fileBlock("file:///p/src/a.go", "src/a.go")).toEqual({
      type: "resource_link",
      uri: "file:///p/src/a.go",
      name: "src/a.go",
    });
  });

  it("buildPrompt puts the text first, then the attachments in order", () => {
    const file = fileBlock("file:///p/a.go", "a.go");
    const sel = selectionBlock("file:///p/b.go", "y", 1, 2);
    expect(buildPrompt("fix", [file, sel])).toEqual([{ type: "text", text: "fix" }, file, sel]);
  });

  it("an empty text with attachments still sends the attachments", () => {
    const file = fileBlock("file:///p/a.go", "a.go");
    expect(buildPrompt("", [file])).toEqual([file]);
    expect(buildPrompt("  \n", [file])).toEqual([file]);
    expect(buildPrompt("hi", [])).toEqual([{ type: "text", text: "hi" }]);
  });
});
