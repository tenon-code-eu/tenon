// French UI completeness, read from the real sources: every key of
// package.nls.json has its French entry, every `l10n.t` string of src/ has
// its French translation, and every %key% of package.json is defined.
import * as fs from "node:fs";
import * as path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "../..");

function readJson(file: string): Record<string, string> {
  return JSON.parse(fs.readFileSync(path.join(root, file), "utf8")) as Record<string, string>;
}

function sources(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      return sources(p);
    }
    return e.name.endsWith(".ts") ? [p] : [];
  });
}

/** Every `l10n.t(` call of the file, and the string literals among them. */
function l10nCalls(text: string): { calls: number; literals: string[] } {
  const calls = text.match(/\bl10n\.t\(/g)?.length ?? 0;
  const literals = [...text.matchAll(/\bl10n\.t\(\s*"((?:[^"\\]|\\.)*)"/g)].map((m) => JSON.parse(`"${m[1]}"`) as string);
  return { calls, literals };
}

describe("French UI", () => {
  it("every key of package.nls.json is in package.nls.fr.json, translated", () => {
    const en = readJson("package.nls.json");
    const fr = readJson("package.nls.fr.json");
    const missing = Object.keys(en).filter((k) => typeof fr[k] !== "string" || fr[k].trim() === "");
    expect(missing).toEqual([]);
    expect(Object.keys(fr).filter((k) => !(k in en))).toEqual([]);
  });

  it("every %key% of package.json is defined in package.nls.json", () => {
    const en = readJson("package.nls.json");
    const manifest = fs.readFileSync(path.join(root, "package.json"), "utf8");
    const keys = [...manifest.matchAll(/"%([^%"]+)%"/g)].map((m) => m[1]);
    expect(keys.length).toBeGreaterThan(5);
    expect(keys.filter((k) => !(k in en))).toEqual([]);
  });

  it("every l10n.t string of src/ is in l10n/bundle.l10n.fr.json", () => {
    const fr = readJson("l10n/bundle.l10n.fr.json");
    const files = sources(path.join(root, "src"));
    let calls = 0;
    const strings = new Set<string>();
    for (const f of files) {
      const r = l10nCalls(fs.readFileSync(f, "utf8"));
      calls += r.calls;
      r.literals.forEach((s) => strings.add(s));
      // A call whose message is not a string literal would escape this check.
      expect(r.literals.length, `non-literal l10n.t message in ${path.relative(root, f)}`).toBe(r.calls);
    }
    expect(calls).toBeGreaterThan(30);
    const missing = [...strings].filter((s) => typeof fr[s] !== "string" || fr[s].trim() === "");
    expect(missing).toEqual([]);
  });

  it("the scanner sees literals split over lines and escaped quotes", () => {
    const r = l10nCalls('vscode.l10n.t(\n  "a {0}",\n  x,\n); l10n.t("say \\"hi\\"")');
    expect(r).toEqual({ calls: 2, literals: ["a {0}", 'say "hi"'] });
  });
});
