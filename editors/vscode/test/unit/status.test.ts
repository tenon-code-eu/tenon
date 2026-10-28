import { describe, expect, it } from "vitest";
import { Conversation } from "../../src/model";
import { statusText } from "../../src/status";

const en = { t: (s: string) => s };
const FR: Record<string, string> = {
  EU: "UE",
  "non-EU": "hors UE",
  local: "local",
  sandboxed: "isolé",
  "not sandboxed": "non isolé",
  "unknown jurisdiction": "juridiction inconnue",
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
    // Not known to be EU or local: warning colour.
    expect(statusText({ provider: "x", jurisdiction: "mars", sandbox: {} }, en)).toEqual({
      text: "tenon · x · mars · not sandboxed",
      warning: true,
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
      text: "tenon · unknown jurisdiction · not sandboxed",
      warning: true,
    });
  });
});

describe("statusText after a fallback (ruling R7)", () => {
  it("shows the provider name, falling back to the id", () => {
    expect(statusText({ provider: "mistral", providerName: "Mistral AI", jurisdiction: "eu", sandbox: { mechanism: "x" } }, en).text).toBe(
      "tenon · Mistral AI · EU · sandboxed",
    );
    expect(statusText({ provider: "mistral", providerName: "", jurisdiction: "eu" }, en).text).toBe("tenon · mistral · EU · not sandboxed");
  });

  it("EU meta, then a usage_update from a non-EU provider: non-EU with the warning", () => {
    const c = new Conversation();
    c.begin("s1", "/p", { provider: "mistral", providerName: "Mistral AI", jurisdiction: "eu", sandbox: { mechanism: "seatbelt" } });
    expect(statusText(c.state.meta, en)).toEqual({ text: "tenon · Mistral AI · EU · sandboxed", warning: false });
    c.apply({
      sessionId: "s1",
      update: { sessionUpdate: "usage_update", used: 1, size: 2, _meta: { tenon: { provider: "us1", providerName: "US Corp", jurisdiction: "non-eu" } } },
    });
    expect(statusText(c.state.meta, en)).toEqual({ text: "tenon · US Corp · non-EU · sandboxed", warning: true });
  });

  it("a provider change without jurisdiction: unknown, with the warning", () => {
    const c = new Conversation();
    c.begin("s1", "/p", { provider: "mistral", jurisdiction: "eu", sandbox: { mechanism: "" } });
    c.apply({ sessionId: "s1", update: { sessionUpdate: "usage_update", used: 1, size: 2, _meta: { tenon: { provider: "old" } } } });
    expect(statusText(c.state.meta, en)).toEqual({ text: "tenon · old · unknown jurisdiction · not sandboxed", warning: true });
    expect(statusText(c.state.meta, fr).text).toBe("tenon · old · juridiction inconnue · non isolé");
  });
});
