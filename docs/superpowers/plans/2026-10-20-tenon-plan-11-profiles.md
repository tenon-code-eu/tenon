# tenon compliance profiles — Implementation Plan (plan 11)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `profile = "eu-only" | "strict-local"` locks where tenon sends conversation data (model provider, MCP servers), cannot be relaxed by project config or `--allow-non-eu`, and is recorded in `SessionStarted` and the compliance export.

**Architecture:** A new pure package `internal/profile` holds the rules (`CheckProvider`, `CheckMCP`, `IsLocalHost`). `internal/config` reads the key (global and project, project can only tighten). `cmd/tenon` applies it at startup (selected provider before any network contact, `--allow-non-eu`, MCP filtering), `policy.Outbound` re-checks before each send, and the profile flows into `SessionStarted`, the welcome screen, `tenon config check` and the export.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), standard library (`net/netip`).

**Spec:** `docs/superpowers/specs/2026-10-20-tenon-compliance-profiles-design.md`

## Global Constraints

- No new dependency. `internal/profile` imports only the standard library and `internal/llm`.
- Severity order: none < `eu-only` < `strict-local`. Effective profile = the stricter of global and project.
- `eu-only`: provider and MCP servers of jurisdiction `eu` or `local`; `--allow-non-eu` refused.
- `strict-local`: provider of jurisdiction `local` AND `base_url` host local; MCP stdio of jurisdiction `local`; MCP http of jurisdiction `local` AND URL host local; `--allow-non-eu` refused.
- Local host: `localhost` (case-insensitive), or an IP literal in 127.0.0.0/8, ::1, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7. Any other host name refused. No DNS resolution.
- Only the selected provider (`--provider`, else `default_provider`) is checked at startup, BEFORE any network contact (local model detection included).
- Messages (exact, EN; FR equivalents in the i18n catalog):
  - `config: unknown profile "<x>" (eu-only|strict-local)`
  - `project profile "<p>" is weaker than global profile "<g>": ignored`
  - `profile <p>: provider "<id>" (<jurisdiction>) not allowed`
  - `profile strict-local: provider "<id>" url host "<host>" is not a local address`
  - `profile <p>: --allow-non-eu not allowed`
  - `profile <p>: MCP server "<id>" (<reason>) skipped`
- Exit code 1 for startup violations; a refused MCP server does not stop startup.
- `SessionStartedPayload.Profile string \`json:"profile,omitempty"\``; old events serialize unchanged.
- Without a profile, behaviour is unchanged (all existing tests pass untouched).
- New user-visible strings through `internal/i18n` (EN and FR). Code comments in French.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- `strict-local` with the `ollama` preset whose `base_url` was changed to a remote host: local model detection must NOT contact it — the profile check runs before `detectLocal` — test in Task 4.
- Host spelled `LOCALHOST`, `[::1]` (bracketed in a URL), `127.0.0.1:11434` (with port), `localhost.` (trailing dot), `127.1` (short form): bracketed/port forms are local, `localhost.` and `127.1` are refused (not exact literals) — test in Task 1.
- `--resume` of a session started without profile, now under `eu-only` with a `non-eu` provider: refused at startup like a new session, and the resumed `SessionStarted` carries `eu-only` — test in Task 4.
- Project config sets `profile = "strict-local"` while global has none: applied (tightening is allowed) — test in Task 2.
- TUI with `eu-only` and a non-eu MCP server declared: the server is skipped with the notice, and no consent prompt is ever shown for it — test in Task 4.

---

### Task 1: `internal/profile`

**Files:**
- Create: `internal/profile/profile.go`, `internal/profile/profile_test.go`

**Interfaces:**
- Produces:
  - `type Name string`; `const None Name = ""`, `EUOnly Name = "eu-only"`, `StrictLocal Name = "strict-local"`.
  - `func Parse(s string) (Name, error)` — `""` → `None`; unknown → `config: unknown profile "<x>" (eu-only|strict-local)`.
  - `func Stricter(a, b Name) Name`.
  - `var ErrProfile = errors.New("profile violation")` — every error below wraps it (`errors.Is`).
  - `func CheckProvider(n Name, p llm.ProviderInfo) error` — messages from Global Constraints; host taken from `url.Parse(p.BaseURL).Hostname()`.
  - `type MCPTarget struct{ ID, Transport, URL, Jurisdiction string }`.
  - `func CheckMCP(n Name, s MCPTarget) error` — `<reason>` = the jurisdiction (e.g. `non-eu`) or `url host "<host>" is not a local address`; full message `profile <p>: MCP server "<id>" (<reason>) skipped`.
  - `func IsLocalHost(host string) bool` — `netip.ParseAddr` (zone stripped) + prefix list; `strings.EqualFold(host, "localhost")`.

- [ ] **Step 1: Failing tests** — `TestParse` (`""`, `eu-only`, `strict-local`, `EU-ONLY` → error, `x` → exact message); `TestStricter` (all 9 pairs); `TestCheckProvider` table: None accepts everything; eu-only: eu ok, local ok, non-eu → `profile eu-only: provider "openai" (non-eu) not allowed`, empty jurisdiction → refused; strict-local: local+`http://localhost:11434/v1` ok, local+`http://[::1]:8000/v1` ok, local+`http://192.168.0.5:8000/v1` ok, local+`http://gpu.lan:8000/v1` → `profile strict-local: provider "vllm" url host "gpu.lan" is not a local address`, eu → refused; `TestCheckMCP` (stdio local ok under strict-local, stdio eu refused, http local+localhost ok, http local+`example.com` refused, non-eu refused under eu-only); `TestIsLocalHost` table: `localhost`, `LOCALHOST`, `127.0.0.1`, `127.255.0.1`, `::1`, `10.1.2.3`, `172.16.0.1`, `172.31.255.255`, `192.168.0.5`, `fd00::1` → true; `fe80::1` (link-local, not listed), `172.32.0.1`, `8.8.8.8`, `example.com`, `gpu.lan`, `localhost.`, `127.1`, `""` → false (Review Focus 2); every error `errors.Is(err, ErrProfile)`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/profile/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS. **Step 5: Commit** `feat(profile): compliance profile rules`.

### Task 2: Configuration key

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: Task 1 (`profile.Parse`, `Stricter`, `Name`).
- Produces:
  - `Config.Profile profile.Name` (`toml:"profile"`, the effective profile after Load).
  - `Config.WeakerProjectProfile profile.Name` (`toml:"-"`) — the project value when it was ignored as weaker; the CLI turns it into the translated notice `profile.weaker_project`.
  - `projectFile.Profile string` (`toml:"profile"`).

Rules: global value parsed (unknown → error); project value parsed (unknown → error, prefixed `project config: `); effective = `Stricter(global, project)`; if project is non-empty and strictly weaker than global → `WeakerProjectProfile = project`.

- [ ] **Step 1: Failing tests** — `TestProfileGlobal` (`profile = "eu-only"` → `EUOnly`); `TestProfileProjectTightens` (global none, project `strict-local` → `StrictLocal`; Review Focus 4); `TestProfileProjectWeakerIgnored` (global `strict-local`, project `eu-only` → `StrictLocal`, `WeakerProjectProfile == EUOnly`); `TestProfileUnknown` (global `x` → error containing `unknown profile "x"`; project `x` → error starting `project config:`); `TestProfileDefault` (none); `TestConfigExampleLoads` still passes.
- [ ] **Step 2–4:** run `GOFLAGS=-mod=vendor go test ./internal/config/` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(config): profile key, project can only tighten`.

### Task 3: Outbound guard, engine and event

**Files:**
- Modify: `internal/policy/outbound.go`, `internal/policy/outbound_test.go`, `internal/engine/engine.go` (`Deps`), `internal/engine/loop.go`, `internal/engine/*_test.go`, `internal/event/payloads.go`, `internal/event/event_test.go`

**Interfaces:**
- Consumes: Task 1 (`profile.CheckProvider`, `ErrProfile`).
- Produces:
  - `func (o *Outbound) SetProfile(n profile.Name)`; `Check` calls `profile.CheckProvider` first and returns its error (wrapping `profile.ErrProfile`) even if `Consent` was recorded.
  - Engine `outbound`: `errors.Is(err, profile.ErrProfile)` → emit `Error{Code: "profile", Message: err.Error()}`, return `ok = false` (turn ends like a refused jurisdiction: reason `blocked`), no request sent.
  - `Deps.Profile profile.Name`; `SessionStartedPayload.Profile` filled from it (also on resume).

- [ ] **Step 1: Failing tests** — `TestOutboundProfileRefusesConsented` (eu-only, non-eu provider with `Consent` → error, `errors.Is(err, profile.ErrProfile)`); `TestOutboundNoProfileUnchanged`; engine `TestProfileBlocksTurn` (Deps.Profile eu-only, non-eu provider info, fake provider never called, `Error{code:"profile"}`, TurnCompleted reason `blocked`); `TestSessionStartedProfile` (field set, and `omitempty` absent with None); event JSON test (old payload unchanged).
- [ ] **Step 2–4:** run `GOFLAGS=-mod=vendor go test -race ./internal/policy/ ./internal/engine/ ./internal/event/` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(engine): profile guard before each send, profile in SessionStarted`.

### Task 4: CLI wiring

**Files:**
- Modify: `cmd/tenon/run.go`, `internal/ui/tui/model.go` (Options), `internal/ui/tui/welcome.go`, `internal/ui/tui/welcome_test.go`, `internal/i18n/catalog.go`
- Create: `cmd/tenon/profile_e2e_test.go`

**Interfaces:**
- Consumes: Tasks 1–3 (`profile.*`, `Config.Profile`, `Config.WeakerProjectProfile`, `Outbound.SetProfile`, `Deps.Profile`).
- Produces:
  - In `startEngine` (`run.go` ~537): right after `cfg.Provider(providerID)` and BEFORE `detectLocal`, call `profile.CheckProvider(cfg.Profile, <ProviderInfo built from pc>)`; error → translated `err.profile_provider` / `err.profile_host` and, when the profile is `strict-local`, the hint key `profile.hint_local` (`use --provider ollama or set default_provider`); return (exit 1).
  - `--allow-non-eu` with a profile → `err.profile_allow_non_eu`, exit 1, checked before any engine start (both `-p` and interactive).
  - MCP: before `mcp.Start`, servers failing `profile.CheckMCP` are removed from the list and added to the startup notices (`profile.mcp_skipped`) and to `UnavailableMCP` as `McpServerDownPayload{Server, Error: <message>}`; the MCP consent func also returns false for them (defense in depth).
  - `out.SetProfile(cfg.Profile)`; `Deps.Profile = cfg.Profile`.
  - Weaker project profile → startup notice `profile.weaker_project`.
  - `tenon config check`: prints `config.profile` (`profile: %s` / `profil : %s`, or `none`/`aucun`) before `OK`; exit 1 with the provider error if the selected provider (default) violates the profile.
  - TUI `Options.Profile string`; welcome line `tui.welcome.profile` `"profile: %s"` / `"profil : %s"` under the project line, only when non-empty.
  - i18n EN/FR for every key above, texts = Global Constraints messages (FR: `profil <p> : fournisseur « <id> » (<juridiction>) non autorisé`, `profil strict-local : l'hôte « <hôte> » de l'URL du fournisseur « <id> » n'est pas une adresse locale`, `profil <p> : --allow-non-eu non autorisé`, `profil <p> : serveur MCP « <id> » (<raison>) ignoré`, `profil de projet « <p> » plus souple que le profil global « <g> » : ignoré`, hint `utilisez --provider ollama ou default_provider`).

- [ ] **Step 1: Failing e2e tests** (existing env helpers, fake LLM):
  - `TestProfileEUOnlyRefusesNonEUE2E`: global `profile = "eu-only"`, provider `non-eu` → exit 1, message, fake LLM received 0 requests;
  - `TestProfileAllowNonEUE2E`: `--allow-non-eu` with eu-only → exit 1, `--allow-non-eu not allowed`;
  - `TestProfileStrictLocalDefaultE2E`: strict-local + default mistral → exit 1, hint present;
  - `TestProfileStrictLocalNoDetectE2E`: strict-local, `--provider ollama` with `[[providers]] id = "ollama" base_url = "http://gpu.invalid:11434/v1" jurisdiction = "local"` → exit 1, stderr contains `url host "gpu.invalid" is not a local address` and no detection error (proves the check runs before `detectLocal`; Review Focus 1);
  - `TestProfileStrictLocalOKE2E`: strict-local + provider `local` at the fake LLM's `http://127.0.0.1:<port>/v1` → exit 0, `SessionStarted.profile == "strict-local"` in session file and audit;
  - `TestProfileResumeE2E`: session created without profile, then `eu-only` + non-eu provider with `--resume last` → exit 1; with an eu provider → resumed `SessionStarted.profile == "eu-only"` (Review Focus 3);
  - `TestProfileMCPSkippedE2E`: eu-only + declared non-eu http MCP server → notice `MCP server "x" (non-eu) skipped` on stderr, `McpServerDown` in the session, server never contacted, run succeeds (Review Focus 5 for the headless path; TUI: welcome test below);
  - `TestProfileWeakerProjectE2E`: notice printed;
  - `TestConfigCheckProfileE2E`: prints `profile: eu-only`, `OK`; violating default → exit 1.
  - TUI: `TestWelcomeProfile` (line present with profile, absent without), and a TUI model test that no jurisdiction prompt is raised for a skipped MCP server (Review Focus 5).
- [ ] **Step 2–4:** run `GOFLAGS=-mod=vendor go test -race ./cmd/... ./internal/ui/tui/` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(cli): apply compliance profiles at startup`.

### Task 5: Export and documentation

**Files:**
- Modify: `internal/compliance/report.go`, `internal/compliance/build.go`, `internal/compliance/build_test.go`, `internal/compliance/report.html.tmpl`, `internal/compliance/html_test.go`, `internal/compliance/testdata/report.golden.json`, `internal/i18n/catalog.go`, `docs/usage.md`, `docs/fr/utilisation.md`, `docs/security.md`, `docs/fr/securite.md`, `docs/config.example.toml`, `README.md`

**Interfaces:**
- Consumes: Task 3 (`SessionStartedPayload.Profile`).
- Produces:
  - `Report.Profiles []string` (`json:"profiles"`, never null) — distinct non-empty `Started.Profile` of in-range sessions (from `ds.Sessions[sid].Started`, the first `SessionStarted`; a later resumed `SessionStarted` in range with another profile is also collected), sorted; empty at metadata level.
  - HTML header row `report.profiles` (`Profils appliqués` / `Applied profiles`), value joined by `, ` or `report.none`.
  - Docs EN/FR: section "Compliance profiles" / "Profils de conformité" (usage): the two profiles, local host rules, authority (global, project tightens only), what profiles do not cover (shell commands, permissions, audit level), messages, `config check`; security: profiles bound data sent to the model and MCP, not shell; JSON schema row `profiles`; `docs/config.example.toml` commented `# profile = "eu-only"`; README feature line.

- [ ] **Step 1: Failing tests** — `TestBuildProfiles` (two sessions eu-only and none → `["eu-only"]`; resumed session switching to strict-local → both, sorted); metadata → `[]`; HTML row in FR and EN; golden updated with `-update`; `TestConfigExampleLoads` passes.
- [ ] **Step 2–4:** run `GOFLAGS=-mod=vendor go test ./internal/compliance/ ./internal/config/` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(compliance): profiles in the export; docs: compliance profiles (EN/FR)`.
