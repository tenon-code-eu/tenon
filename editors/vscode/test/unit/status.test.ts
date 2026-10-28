import { describe, expect, it } from "vitest";
import { statusText } from "../../src/status";

const en = { t: (s: string) => s };
const FR: Record<string, string> = {
  EU: "UE",
  "non-EU": "hors UE",
  local: "local",
  sandboxed: "isolé",
  "not sandboxed": "non isolé",
};
const fr = { t: (s: string) => FR[s] ?? s };

describe("statusText", () => {
  it("EU provider, sandboxed", () => {
    expect(statusText({ provider: "Mistral", jurisdiction: "eu", sandbox: { mechanism: "seatbelt" } }, en)).toEqual({
      text: "tenon · Mistral · EU · sandboxed",
      warning: false,
    });
  });

  it("non-EU without a mechanism is a warning", () => {
    expect(statusText({ provider: "Big", jurisdiction: "non-eu", sandbox: { mode: "off", mechanism: "" } }, en)).toEqual({
      text: "tenon · Big · non-EU · not sandboxed",
      warning: true,
    });
    expect(statusText({ provider: "Big", jurisdiction: "non-eu" }, en).text).toBe("tenon · Big · non-EU · not sandboxed");
  });

  it("local, and an unknown jurisdiction shown as is", () => {
    expect(statusText({ provider: "ollama", jurisdiction: "local", sandbox: { mechanism: "landlock" } }, en)).toEqual({
      text: "tenon · ollama · local · sandboxed",
      warning: false,
    });
    expect(statusText({ provider: "x", jurisdiction: "mars", sandbox: {} }, en)).toEqual({
      text: "tenon · x · mars · not sandboxed",
      warning: false,
    });
  });

  it("no session: tenon alone", () => {
    expect(statusText(undefined, en)).toEqual({ text: "tenon", warning: false });
  });

  it("French labels", () => {
    expect(statusText({ provider: "Mistral", jurisdiction: "eu", sandbox: { mechanism: "seatbelt" } }, fr).text).toBe(
      "tenon · Mistral · UE · isolé",
    );
    expect(statusText({ provider: "Big", jurisdiction: "non-eu", sandbox: { mechanism: "" } }, fr).text).toBe(
      "tenon · Big · hors UE · non isolé",
    );
  });

  it("ignores non-string fields from the wire", () => {
    expect(statusText({ provider: 3, jurisdiction: null, sandbox: { mechanism: 1 } }, en)).toEqual({
      text: "tenon · not sandboxed",
      warning: false,
    });
  });
});
