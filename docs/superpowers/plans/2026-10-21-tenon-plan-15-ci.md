# tenon CI integration — Implementation Plan (plan 15)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** tenon runs in GitLab CI, Forgejo/Gitea Actions and GitHub Actions: `--strict` fails the job when a turn does not end cleanly, `--config <file>` names the global config, `install.sh` installs from a sovereign mirror, and three copy-ready templates publish the evidence as artifacts.

**Architecture:** `internal/ui/headless` gains a strict mode (exit 4). `cmd/tenon` resolves the global config path once (flag or XDG default) and threads it to every config load. `install.sh` gains `TENON_DOWNLOAD_BASE`. `docs/ci/` holds the templates and an example config, checked by Go tests.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), POSIX sh, standard library only.

**Spec:** `docs/superpowers/specs/2026-10-21-tenon-ci-design.md`

## Global Constraints

- No new module.
- Exit codes: `0`, `1`, `3`, `130` unchanged; `4` = turn ended with `max_iterations`, `incomplete`, `stopped_by_hook` or `cancelled` under `--strict`; `3` wins over `4`; `1` wins over both.
- Messages (exact EN; FR in the i18n catalog except config errors, which stay EN):
  - `--strict requires -p` (exit 1)
  - `strict: turn ended with <reason>` (stderr, text output only)
  - `config: <file>: no such file or directory` (exit 1, explicit `--config` only)
  - `config: <path>` / `config: <path> (absent)` (`config check`, first line)
  - `TENON_DOWNLOAD_BASE requires TENON_VERSION=vX.Y.Z.`
  - `TENON_DOWNLOAD_BASE must be https.`
- `--config` replaces only the global config path; `.tenon/config.toml` and the org policy still apply. No environment variable side channel for the path.
- Mirror layout: `<base>/<version>/<archive>`, `<base>/<version>/SHA256SUMS`, `<base>/<version>/SHA256SUMS.minisig`. `http://` allowed only for `localhost` and `127.0.0.1`. Hash and minisign checks unchanged (key embedded in the script).
- Templates: install with `TENON_VERSION=<pinned vX.Y.Z> TENON_REQUIRE_SIGNATURE=1`; run `tenon --config ci/tenon.toml -p "$TENON_TASK" --strict --output json > tenon-events.jsonl` with `XDG_DATA_HOME="$PWD/.tenon-data"`; always run `tenon audit verify` and `tenon audit export --all-projects --out tenon-audit`; artifacts `tenon-events.jsonl`, `tenon-audit/`, `.tenon-data/tenon/audit/`; never `latest`.
- Template and doc comments in English; code comments in French; new user-visible strings via `internal/i18n` (EN/FR).
- Every task: `gofmt -l cmd internal scripts` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- `--strict` with `--output json`: exit 4 still applies and stdout stays pure JSON lines (the `strict:` line goes nowhere) — test in Task 1.
- `--config` pointing to a directory or an unreadable file: exit 1 with the read error, never a silent fallback to the default path — test in Task 2.
- `--config` given together with a subcommand that loads config (`tenon --config x audit timestamp`, `config check`, `audit export`): the named file is used — test in Task 2.
- `TENON_DOWNLOAD_BASE` with a trailing slash: no `//` in fetched URLs, install works — test in Task 3.
- A template edited to drop `--strict` or to use `latest`: the template test fails — test in Task 4.

---

### Task 1: `--strict`

**Files:**
- Modify: `internal/ui/headless/headless.go`, `cmd/tenon/run.go`, `internal/i18n/catalog.go`, `docs/usage.md`, `docs/fr/utilisation.md`
- Test: `internal/ui/headless/headless_test.go`, `cmd/tenon/e2e_test.go` (or a new `strict_e2e_test.go`)

**Interfaces:**
- Produces: `headless.Options.Strict bool`; `const exitIncomplete = 4`; flag `--strict` (bool) on the root flag set; i18n keys `err.strict_requires_p`, `strict.turn_ended`, `flag.strict`.

- [ ] **Step 1: Failing tests:**
  - `TestStrictReasons` (headless, table over the 4 reasons): `Strict: true` → 4; `Strict: false` → 0; text output → stderr contains `strict: turn ended with <reason>` only when strict.
  - `TestStrictRefusedWins`: a refused action then `incomplete` → 3.
  - `TestStrictJSON`: `Output: JSON`, `Strict: true`, `incomplete` → 4, every stdout line decodes as JSON, stderr has no `strict:` line (Review Focus 1).
  - e2e `TestStrictRequiresP`: `tenon --strict` without `-p` → 1, stderr `--strict requires -p`.
  - e2e `TestStrictE2E`: fake provider forcing the iteration limit (reuse the existing max-iterations e2e setup) → `-p x --strict` exit 4; without `--strict` exit 0.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**; the existing `turn.ended` line stays; the strict line is an extra line. Document `--strict` and code `4` in the usage options table and exit-code table (EN/FR).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): --strict exits 4 when a non-interactive turn does not end cleanly`

---

### Task 2: `--config`

**Files:**
- Modify: `cmd/tenon/run.go` (and every file that calls `loadSetup`/`uiLang`), `internal/i18n/catalog.go`, `docs/usage.md`, `docs/fr/utilisation.md`
- Test: `cmd/tenon/config_e2e_test.go` (new)

**Interfaces:**
- Produces:
  - flag `--config` (string) on the root flag set; i18n `flag.config`, `config.path`, `config.path_absent`.
  - `func globalConfigPath(flagValue string, getenv func(string) string) (path string, explicit bool)` — flag value as given (no `~` expansion) or `config.GlobalPath(getenv)`.
  - `func checkExplicitConfig(path string) error` — `os.Stat`; missing → `config: <file>: no such file or directory`; a directory → `config: <file>: is a directory`; other stat error → `config: <file>: <err>`. Called in `run` right after flag parsing when `explicit`, before any command (exit 1).
  - `loadSetup` and `uiLang` take the resolved `globalPath string` instead of computing `config.GlobalPath(getenv)`; all callers updated (`config check`, `audit timestamp`, `audit verify`, `audit export`, interactive, `-p`, hooks/skills/agents listings if they load config).
  - `config check` prints `config.path` (`config: <path>`) first, or `config.path_absent` (`config: <path> (absent)`) when the default path does not exist.

- [ ] **Step 1: Failing e2e tests:**
  - `TestConfigFlagMissing`: `--config /nope.toml -p x` → 1, exact message, zero provider requests.
  - `TestConfigFlagDirectory`: `--config <tmpdir>` → 1, `is a directory` (Review Focus 2).
  - `TestConfigFlagUsed`: file with a provider pointing to the fake provider and `default_provider` → `-p x` exit 0, request reached the fake; the XDG default config (with another, unreachable provider) is ignored.
  - `TestConfigFlagProjectAndPolicy`: `--config` file + project `.tenon/config.toml` deny rule → the deny applies; signed policy (`orgpolicytest.Sign`, `TENON_POLICY_DIR`) raising the profile → applies.
  - `TestConfigFlagSubcommands`: `tenon --config <f> config check` prints `config: <f>`; `tenon --config <f> audit timestamp` reads `[audit.timestamp]` from `<f>` (unconfigured message absent) (Review Focus 3).
  - `TestConfigCheckDefaultAbsent`: no default file → `config: <path> (absent)`.
- [ ] **Step 2: Run** `go test ./cmd/tenon/ -run Config` — FAIL.
- [ ] **Step 3: Implement**; document `--config` (EN/FR): replaces the global file only, project config and policy still apply.
- [ ] **Step 4: Run** — PASS; full gates (existing `config check` assertions gain the first line).
- [ ] **Step 5: Commit** `feat(cli): --config names the global configuration file`

---

### Task 3: `install.sh` mirror

**Files:**
- Modify: `install.sh`, `README.md` (install section)
- Create: `scripts/install_test.go` (package `scripts`)

**Interfaces:**
- Produces: env `TENON_DOWNLOAD_BASE` (trailing `/` stripped); when set: version required, scheme check, `base="$TENON_DOWNLOAD_BASE/$version"`; `fetch` allows `http` only when the mirror host is `localhost` or `127.0.0.1` (curl `--proto '=https,http'` / wget without `--https-only` in that case only); GitHub path unchanged when unset.

- [ ] **Step 1: Failing tests** (`scripts/install_test.go`: skip if `sh`, `tar`, `curl` missing; `httptest.NewServer` serving `/<version>/tenon_<version>_<os>_<arch>.tar.gz` (archive with `tenon_<v>_<os>_<arch>/tenon`, a sh script printing `tenon <v>` for `--version`), `SHA256SUMS`; run `sh ../install.sh` with `HOME`, `TENON_INSTALL_DIR` in `t.TempDir()` and `PATH` = a temp dir holding symlinks to `sh`, `curl`, `tar`, `uname`, `mktemp`, `awk`, `sed`, `grep`, `cut`, `cp`, `mv`, `chmod`, `mkdir`, `rm`, `printf`-capable shell builtins, and `shasum` or `sha256sum` — never `minisign`):
  - `TestInstallMirrorOK`: hash right, no minisign → exit 0, `<installdir>/tenon --version` prints `tenon <v>`, stderr warns signature not verified.
  - `TestInstallMirrorTrailingSlash`: base with `/` → exit 0, server saw no `//` path (Review Focus 4).
  - `TestInstallMirrorNoVersion` → exit 1, exact message.
  - `TestInstallMirrorHTTPRemote`: `TENON_DOWNLOAD_BASE=http://example.invalid` → exit 1, `must be https`, no network.
  - `TestInstallMirrorBadHash` → exit 1, `wrong SHA-256 hash`.
  - `TestInstallMirrorRequireSignature`: `TENON_REQUIRE_SIGNATURE=1`, no minisign → exit 1.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./scripts/` — FAIL.
- [ ] **Step 3: Implement**; update the header comment of `install.sh` and the README install section (variable, layout, version required).
- [ ] **Step 4: Run** — PASS; full gates (`gofmt` now includes `scripts`).
- [ ] **Step 5: Commit** `feat(install): TENON_DOWNLOAD_BASE for sovereign mirrors`

---

### Task 4: templates and CI docs

**Files:**
- Create: `docs/ci/gitlab-ci.yml`, `docs/ci/forgejo.yml`, `docs/ci/github.yml`, `docs/ci/tenon.toml`, `docs/ci.md`, `docs/fr/ci.md`
- Create: `cmd/tenon/ci_templates_test.go`
- Modify: `README.md` (CI link), `docs/usage.md`, `docs/fr/utilisation.md` (link to CI page)

**Interfaces:**
- Consumes: `--strict` (Task 1), `--config` (Task 2), `TENON_DOWNLOAD_BASE` (Task 3).

- [ ] **Step 1: Failing tests** in `ci_templates_test.go`:
  - `TestCITemplates`: for each of the three YAML files: contains `--strict`, `--config ci/tenon.toml`, `TENON_REQUIRE_SIGNATURE=1`, `tenon audit verify`, `tenon audit export --all-projects --out tenon-audit`, `XDG_DATA_HOME`, `tenon-events.jsonl`, `.tenon-data/tenon/audit`, a `TENON_VERSION=v[0-9]+\.[0-9]+\.[0-9]+` match, and no `latest` (Review Focus 5); the always-run construct of each forge is present (`when: always` for GitLab, `if: always()` for Forgejo and GitHub).
  - `TestCIExampleConfig`: e2e `tenon --config ../../docs/ci/tenon.toml config check` with the provider key env set → exit 0.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Write** the templates (spec §5: pinned version = the upcoming release `v0.7.0`, minisign install step per image, `TENON_DOWNLOAD_BASE` commented, manual or MR/PR trigger, masked protected key variable) and the docs (spec §6, EN/FR consistent, security warning verbatim in substance: MR can edit the config like the pipeline; minimal allow rules; no secrets on fork MRs; ephemeral runners keep nothing — archive tokens off the runner).
- [ ] **Step 4: Run** — PASS; full gates; `PATH=/opt/homebrew/opt/go@1.26/bin:$PATH make ci` (known: govulncheck GO-2026-6809 on go1.26.9 — report, don't fix).
- [ ] **Step 5: Commit** `feat(ci): GitLab, Forgejo/Gitea and GitHub templates; docs: CI (EN/FR)`
