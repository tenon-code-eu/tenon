// Isolated tenon environment for integration tests: temporary config, data
// and HOME, mirroring newEnvExtra in cmd/tenon/e2e_test.go.
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

export interface EnvOptions {
  /** Project directory; defaults to a new `projet` directory in the temp base. */
  projectDir?: string;
  /** Provider jurisdiction (default "eu"). */
  jurisdiction?: string;
  /** TOML appended to the global config. */
  extra?: string;
}

export interface TenonEnv {
  env: Record<string, string>;
  projectDir: string;
}

export function makeEnv(fake: { url: string } | undefined, opts: EnvOptions = {}): TenonEnv {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "tenon-vscode-"));
  const projectDir = opts.projectDir ?? path.join(base, "projet");
  const env: Record<string, string> = {
    XDG_CONFIG_HOME: path.join(base, "config"),
    XDG_DATA_HOME: path.join(base, "data"),
    HOME: path.join(base, "home"),
    TEST_KEY: "cle-de-test",
  };
  for (const d of [projectDir, path.join(env.XDG_CONFIG_HOME, "tenon"), env.HOME]) {
    fs.mkdirSync(d, { recursive: true, mode: 0o700 });
  }
  const url = fake?.url ?? "http://127.0.0.1:1";
  const cfg =
    `lang = "en"
default_provider = "test"

[[providers]]
id = "test"
name = "Test SAS"
base_url = ${JSON.stringify(url)}
api_key_env = "TEST_KEY"
model = "test-model"
country = "FR"
jurisdiction = ${JSON.stringify(opts.jurisdiction ?? "eu")}
` + (opts.extra ?? "");
  fs.writeFileSync(path.join(env.XDG_CONFIG_HOME, "tenon", "config.toml"), cfg, { mode: 0o600 });
  return { env, projectDir };
}
