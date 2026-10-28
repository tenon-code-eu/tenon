// tenon binary resolution and version check. Pure functions except
// readVersion, so the resolution order is unit-tested without VS Code.
import { execFile } from "node:child_process";
import * as path from "node:path";

export type Version = [number, number, number];

export const MINIMUM_VERSION: Version = [0, 9, 0];

export interface ResolveOptions {
  /** Value of the `tenon.path` setting. */
  setting?: string;
  extensionPath: string;
  /** Value of the PATH environment variable. */
  envPath: string;
  exists: (p: string) => boolean;
}

/**
 * Resolution order: `tenon.path` → bundled `<extensionPath>/bin/tenon` →
 * `tenon` on PATH. An explicit setting that points to a missing file yields
 * undefined: the user chose that binary, falling back would hide the mistake.
 */
export function resolveBinary(opts: ResolveOptions): string | undefined {
  const setting = opts.setting?.trim();
  if (setting) {
    return opts.exists(setting) ? setting : undefined;
  }
  const bundled = path.join(opts.extensionPath, "bin", "tenon");
  if (opts.exists(bundled)) {
    return bundled;
  }
  for (const dir of opts.envPath.split(":")) {
    if (!dir) {
      continue;
    }
    const candidate = path.join(dir, "tenon");
    if (opts.exists(candidate)) {
      return candidate;
    }
  }
  return undefined;
}

/**
 * Parses `tenon vX.Y.Z` (output of `tenon --version`). Dev builds ("tenon dev",
 * Go pseudo-versions, pre-releases) yield undefined.
 */
export function parseVersion(output: string): Version | undefined {
  const m = /^tenon v(\d+)\.(\d+)\.(\d+)$/.exec(output.trim());
  if (!m) {
    return undefined;
  }
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

export function meetsMinimum(v: Version, min: Version = MINIMUM_VERSION): boolean {
  for (let i = 0; i < 3; i++) {
    if (v[i] !== min[i]) {
      return v[i] > min[i];
    }
  }
  return true;
}

export function formatVersion(v: Version): string {
  return v.join(".");
}

/** Runs `bin --version` with a 5 s timeout and returns its trimmed stdout. */
export function readVersion(bin: string, timeoutMs = 5_000): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(bin, ["--version"], { timeout: timeoutMs, killSignal: "SIGKILL" }, (err, stdout) => {
      if (err) {
        reject(new Error(err.killed ? `timed out after ${timeoutMs} ms` : err.message, { cause: err }));
        return;
      }
      resolve(stdout.trim());
    });
  });
}
