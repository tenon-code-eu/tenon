import { describe, expect, it } from "vitest";
import { meetsMinimum, parseVersion, resolveBinary } from "../../src/binary";

function existing(...paths: string[]): (p: string) => boolean {
  const set = new Set(paths);
  return (p) => set.has(p);
}

describe("resolveBinary", () => {
  const extensionPath = "/ext";
  const bundled = "/ext/bin/tenon";

  it("prefers the setting over the bundled binary and PATH", () => {
    expect(
      resolveBinary({
        setting: "/opt/tenon",
        extensionPath,
        envPath: "/usr/bin",
        exists: existing("/opt/tenon", bundled, "/usr/bin/tenon"),
      }),
    ).toBe("/opt/tenon");
  });

  it("does not fall back when the setting points to a missing file", () => {
    expect(
      resolveBinary({
        setting: "/missing/tenon",
        extensionPath,
        envPath: "/usr/bin",
        exists: existing(bundled, "/usr/bin/tenon"),
      }),
    ).toBeUndefined();
  });

  it("ignores an empty or blank setting", () => {
    expect(
      resolveBinary({ setting: "  ", extensionPath, envPath: "", exists: existing(bundled) }),
    ).toBe(bundled);
  });

  it("prefers the bundled binary over PATH", () => {
    expect(
      resolveBinary({ extensionPath, envPath: "/usr/bin", exists: existing(bundled, "/usr/bin/tenon") }),
    ).toBe(bundled);
  });

  it("looks up PATH entries split on ':' in order", () => {
    expect(
      resolveBinary({
        extensionPath,
        envPath: "/a::/b:/c",
        exists: existing("/b/tenon", "/c/tenon"),
      }),
    ).toBe("/b/tenon");
  });

  it("returns undefined when nothing is found", () => {
    expect(resolveBinary({ extensionPath, envPath: "/a:/b", exists: existing() })).toBeUndefined();
  });
});

describe("parseVersion", () => {
  it("parses tenon --version output", () => {
    expect(parseVersion("tenon v0.9.1\n")).toEqual([0, 9, 1]);
  });

  it("rejects a dev build", () => {
    expect(parseVersion("tenon dev")).toBeUndefined();
  });

  it("treats a Go pseudo-version or pre-release as a dev build", () => {
    // go build from a checkout stamps the module pseudo-version.
    expect(parseVersion("tenon v0.8.1-0.20261028151136-c623b6ed6d2d")).toBeUndefined();
    expect(parseVersion("tenon v0.9.0-rc.1")).toBeUndefined();
  });

  it("rejects unrelated output", () => {
    expect(parseVersion("other v1.2.3")).toBeUndefined();
    expect(parseVersion("")).toBeUndefined();
  });
});

describe("meetsMinimum", () => {
  it("compares against 0.9.0 by default", () => {
    expect(meetsMinimum([0, 8, 9])).toBe(false);
    expect(meetsMinimum([0, 9, 0])).toBe(true);
    expect(meetsMinimum([0, 10, 0])).toBe(true);
    expect(meetsMinimum([1, 0, 0])).toBe(true);
  });

  it("accepts an explicit minimum", () => {
    expect(meetsMinimum([1, 2, 3], [1, 2, 4])).toBe(false);
    expect(meetsMinimum([1, 3, 0], [1, 2, 4])).toBe(true);
  });
});
