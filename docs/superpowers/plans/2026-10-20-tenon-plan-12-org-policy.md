# tenon signed organisation policy — Implementation Plan (plan 12)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A minisign-signed `policy.toml`, deployed by the organisation in a root-owned system directory, imposes a floor (profile, provider host allowlist, deny/ask rules, redaction rules, audit level, MCP allowlist) that user and project config cannot relax; tenon refuses to start on any policy anomaly, and the applied policy is recorded in `SessionStarted` and the compliance export.

**Architecture:** A new package `internal/orgpolicy` verifies minisign signatures, parses and checks the policy, and loads it from the system directory with a permission check. `internal/config` gains `ApplyPolicy`, a pure layer applied after `Load`. `cmd/tenon` loads the policy in `loadSetup` (single entry point for every command that reads config), refuses a non-allowed provider before any network contact, filters MCP servers, and adds `tenon policy check`. `policy.Outbound` re-checks the provider host before each send; the policy reference flows into `SessionStarted`, the welcome screen, `tenon config check` and the export.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), `crypto/ed25519`, `golang.org/x/crypto/blake2b` (new, vendored), `github.com/BurntSushi/toml`.

**Spec:** `docs/superpowers/specs/2026-10-20-tenon-org-policy-design.md`

## Global Constraints

- One new dependency only: `golang.org/x/crypto` (package `blake2b` used), vendored with `go mod vendor`. `make ci` (govulncheck) must pass.
- `internal/orgpolicy` imports only the standard library, `golang.org/x/crypto/blake2b`, `github.com/BurntSushi/toml` and `internal/profile`. It never imports `internal/config` or `internal/policy`.
- System directory: `/etc/tenon` (Linux), `/Library/Application Support/tenon` (macOS). Files: `policy.toml`, `policy.toml.minisig`, `policy.pub`.
- `TENON_POLICY_DIR` is read only when the system directory has no `policy.toml`; no permission check on that directory.
- Permission check (system directory only): refused if the directory or any of the three files has `mode & 0o022 != 0`, or is owned by the current effective uid while that uid is not 0. `os.Stat` (symlinks followed).
- minisign formats: public key line = base64(`"Ed"` + keyID[8] + pubkey[32]); signature line = base64(alg[2] + keyID[8] + sig[64]) with alg `"Ed"` (signs the message) or `"ED"` (signs BLAKE2b-512 of the message); line 3 `trusted comment: <text>`; line 4 base64(globalSig[64]) = Ed25519 over `sig[64] || <text>`. Both signatures must verify; keyIDs must match.
- `KeyID` string = `fmt.Sprintf("%016X", binary.LittleEndian.Uint64(keyID[:]))` (as printed by minisign).
- Merge order: defaults → global → project → policy. Policy only tightens.
- Host match: host of `base_url`/URL, lowercased, port stripped; entry `x.eu` matches exactly; entry `*.x.eu` matches any strict subdomain of `x.eu`, never `x.eu`. `allow_local = true` admits `profile.IsLocalHost` hosts (providers only).
- Messages (exact, EN; FR equivalents in the i18n catalog):
  - `policy: <file>: <reason>` (load failure, exit 1)
  - `provider "<pid>" (host "<h>") not allowed by policy <id>` (exit 1)
  - `policy <id>: provider "<pid>" from config ignored (defined by policy)`
  - `policy <id>: audit level "<p>" imposed (config: "<c>")`
  - `policy <id>: MCP server "<sid>" skipped (not allowed)`
  - `profile "<c>" from config is weaker than policy profile "<p>" (policy <id>): raised`
  - `policy: <id> v<n> (key <KeyID>)` (welcome screen and `tenon config check`)
- Engine error: `event.ErrorPayload{Code: "policy"}`; the turn ends like a profile violation (no send).
- `SessionStartedPayload.Policy *PolicyRef \`json:"policy,omitempty"\``; old events serialize unchanged.
- Without a policy, behaviour is unchanged (all existing tests pass untouched).
- New user-visible strings through `internal/i18n` (EN and FR). Code comments in French.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- A user sets `TENON_POLICY_DIR` to an empty directory while a system policy exists: the system policy still applies — test in Task 3.
- A user redefines provider `interne` (defined by the policy) with `base_url = "https://evil.example"`: the policy definition wins and the notice is shown; a user-defined provider `mistral` pointed at `https://evil.example` is refused by host — tests in Task 4 and Task 6.
- `allow_hosts = ["*.scaleway.ai"]` with `base_url = "https://scaleway.ai/v1"` (apex) or `https://api.scaleway.ai.evil.eu/v1` (suffix trick): both refused — test in Task 2.
- A policy file whose trusted comment was edited (signature line untouched): refused — test in Task 1.
- `--resume` of a session started before the policy was deployed, provider now not allowed: refused at startup like a new session, and the resumed `SessionStarted` carries the policy — test in Task 6.

---

### Task 1: minisign verification

**Files:**
- Create: `internal/orgpolicy/minisign.go`, `internal/orgpolicy/minisign_test.go`, `internal/orgpolicy/testdata/` (vectors), `internal/orgpolicy/orgpolicytest/sign.go`
- Modify: `go.mod`, `go.sum`, `vendor/` (`go get golang.org/x/crypto@latest && go mod tidy && go mod vendor`)

**Interfaces:**
- Produces:
  - `var ErrPolicy = errors.New("policy violation")` (in `minisign.go` or a `errors.go`; every policy error in this package wraps it).
  - `type PublicKey struct{ KeyID [8]byte; Key ed25519.PublicKey }`; `func (k PublicKey) ID() string` (format from Global Constraints).
  - `func ParsePublicKey(b []byte) (PublicKey, error)` — accepts the 2-line `.pub` file (untrusted comment + base64) or the bare base64 line.
  - `func Verify(pk PublicKey, msg, sig []byte) (trustedComment string, err error)`.
  - Package `orgpolicytest` (test helper, non-test file so other packages' tests can import it): `func Sign(t testing.TB, dir string, policyTOML string) (keyID string)` — generates a fresh Ed25519 key, writes `policy.toml`, `policy.pub`, `policy.toml.minisig` (format `ED`, trusted comment `timestamp:0\tfile:policy.toml`) into `dir`, files 0644.

- [ ] **Step 1: Generate real vectors once** (in the scratchpad, never commit the secret key):

```bash
minisign -G -W -p testdata/test.pub -s $SCRATCH/test.key
printf 'id = "vec"\nversion = 1\n' > testdata/policy.toml
minisign -S -s $SCRATCH/test.key -m testdata/policy.toml -x testdata/policy.toml.ED.minisig -t "vector ED"
minisign -S -l -s $SCRATCH/test.key -m testdata/policy.toml -x testdata/policy.toml.Ed.minisig -t "vector Ed"
minisign -G -W -p testdata/other.pub -s $SCRATCH/other.key
```

- [ ] **Step 2: Failing tests** — `TestVerifyMinisignVectors`: `ED` and `Ed` vectors verify with `test.pub`, returned trusted comments `vector ED` / `vector Ed`; `PublicKey.ID()` equals the ID printed in the `untrusted comment: minisign public key <ID>` line of `test.pub`. `TestVerifyRejects` (each `errors.Is(err, ErrPolicy)`): message with one byte changed; trusted comment line edited (`vector ED!`) with signature line untouched (Review Focus 4); `other.pub` (keyID mismatch); signature truncated; base64 garbage; unknown algorithm `"Xx"`; missing line 3 or 4. `TestParsePublicKeyRejects`: wrong length, wrong algorithm, empty. `TestOrgpolicytestSignRoundTrip`: files written by `Sign` verify with `ParsePublicKey` + `Verify`.

- [ ] **Step 3: Run** `GOFLAGS=-mod=vendor go test ./internal/orgpolicy/ -run 'Verify|ParsePublicKey|Sign'` — FAIL (undefined).

- [ ] **Step 4: Implement** per the format in Global Constraints. `ED` prehash = `blake2b.Sum512(msg)`. Compare keyIDs before verifying. Signature file lines are split on `\n` with a trailing `\r` trimmed.

- [ ] **Step 5: Run** the tests — PASS; full gates.

- [ ] **Step 6: Commit** `feat(orgpolicy): minisign signature verification`

---

### Task 2: policy parsing and host matching

**Files:**
- Create: `internal/orgpolicy/policy.go`, `internal/orgpolicy/policy_test.go`

**Interfaces:**
- Consumes: `ErrPolicy` (Task 1), `profile.Parse`, `profile.IsLocalHost`.
- Produces:
  - ```go
    type Policy struct {
        ID        string       `toml:"id"`
        Version   int          `toml:"version"`
        Profile   profile.Name `toml:"-"`        // parsed from the raw "profile" key
        Providers *Providers   `toml:"providers"` // nil: no allowlist
        Rules     []Rule       // from [[permissions.rules]]
        Redaction []Redaction  // from [[redaction.rules]]
        AuditLevel string      // from [audit] level; "" = not imposed
        MCP       MCP          // [mcp]
        KeyID     string       `toml:"-"` // set by Load
    }
    type Providers struct { AllowHosts []string; AllowLocal bool; Define []ProviderDef }
    type ProviderDef struct { ID, Name, BaseURL, APIKeyEnv, Model, Country, Jurisdiction string; ContextWindow int }
    type Rule struct { Tool, Match, Decision string }
    type Redaction struct { Name, Pattern string }
    type MCP struct { Mode string; AllowHosts, AllowCommands []string } // Mode "" ≡ "any"
    ```
    TOML keys exactly as in spec §4 (`allow_hosts`, `allow_local`, `define`, `base_url`, `api_key_env`, `context_window`, `allow_commands`, …). The decoding shape (intermediate struct for `[permissions]`/`[redaction]`/`[audit]`) is the implementer's choice.
  - `func Parse(b []byte) (Policy, error)` — strict: `md.Undecoded()` non-empty → error naming the key. Refusals (spec §4), each wrapping `ErrPolicy`: `id` not matching `^[a-z0-9_-]{1,64}$`; `version < 1`; unknown profile; rule decision `allow` (`"allow" rules are not allowed`), decision not `ask`/`deny`, empty `tool`; redaction pattern that does not compile; host entry empty, containing `/`, `:`, `://`, or a `*` other than a leading `*.` followed by at least two labels; `allow_commands` entry not absolute (`filepath.IsAbs`); `mcp.mode` not in `"" any allowlist off`; `audit.level` not in `"" full metadata`; `define` entry with empty `id`, empty `base_url`, or `jurisdiction` not in `eu non-eu local`.
  - `func HostOf(rawURL string) string` — `url.Parse(...).Hostname()` lowercased; `""` on error.
  - `func (p *Policy) AllowsHost(host string) bool` — `true` when `p.Providers == nil`; else exact/wildcard match (Global Constraints) or `AllowLocal && profile.IsLocalHost(host)`.
  - `func (p *Policy) AllowsMCP(transport, rawURL, command0 string) bool` — mode `""`/`any` → true; `off` → false; `allowlist`: `http` → `HostOf(rawURL)` matches `MCP.AllowHosts` (no local exception); `stdio` → `command0` equals an `AllowCommands` entry exactly.

- [ ] **Step 1: Failing tests** — `TestParseMinimal` (`id = "acme-dev"\nversion = 3` → ID, Version, Providers nil, MCP.Mode ""); `TestParseFull` (spec §4 example verbatim → every field); `TestParseRejects` table, one case per refusal above plus an unknown key `[foo]` and `[[permissions.rules]] decision = "allow"`; every error `errors.Is(err, ErrPolicy)`. `TestAllowsHost` with `allow_hosts = ["api.mistral.ai", "*.scaleway.ai"]`, `allow_local = false`: `api.mistral.ai` ✓, `API.Mistral.AI` ✓ (via `HostOf("https://API.Mistral.AI:443/v1")`), `api.scaleway.ai` ✓, `a.b.scaleway.ai` ✓, `scaleway.ai` ✗, `api.scaleway.ai.evil.eu` ✗, `mistral.ai` ✗, `localhost` ✗; with `allow_local = true`: `localhost` ✓, `127.0.0.1` ✓, `gpu.lan` ✗ (Review Focus 3). `TestAllowsHostNoProviders` (nil → always true). `TestAllowsMCP`: any/off/allowlist × http (`https://mcp.acme.eu/x` ✓, `http://localhost:9000` ✗) × stdio (`/usr/local/bin/mcp-git` ✓, `mcp-git` ✗, `/usr/local/bin/mcp-git2` ✗).

- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(orgpolicy): policy parsing and host allowlist`

---

### Task 3: loading from the system directory

**Files:**
- Create: `internal/orgpolicy/load.go`, `internal/orgpolicy/load_test.go`

**Interfaces:**
- Consumes: Tasks 1–2; `orgpolicytest.Sign` in tests.
- Produces:
  - `func SystemDir() string` — `runtime.GOOS == "darwin"` → `/Library/Application Support/tenon`, else `/etc/tenon`.
  - `func Resolve(getenv func(string) string) (dir string, system bool)` — delegates to `resolve(SystemDir(), getenv)`.
  - `func resolve(systemDir string, getenv func(string) string) (string, bool)` — `policy.toml` exists in `systemDir` (`os.Stat` succeeds, or fails with an error other than `fs.ErrNotExist`) → `(systemDir, true)`; else `TENON_POLICY_DIR` non-empty → `(it, false)`; else `("", false)`.
  - `func Load(dir string, checkPerms bool) (*Policy, error)` — `dir == ""` or `policy.toml` absent → `nil, nil`. Otherwise: perms (if `checkPerms`) on dir and the three files, read the three files (a missing `.minisig`/`.pub` is an error), `ParsePublicKey`, `Verify`, `Parse`, set `KeyID = pk.ID()`. Every error is `fmt.Errorf("%s: %w", path, err)` for the file at fault and wraps `ErrPolicy`.
  - `func checkPerms(path string) error` — Global Constraints rule; messages `writable by group or others` / `owned by the current user`.

- [ ] **Step 1: Failing tests** — `TestLoadAbsent` (empty dir and `""` → `nil, nil`); `TestLoadValid` (`orgpolicytest.Sign` then `Load(dir, false)` → ID, KeyID == returned keyID); `TestLoadMissingSig`, `TestLoadMissingPub`, `TestLoadTampered` (append a byte to `policy.toml` after signing) → error containing the file path; `TestLoadPerms` (`Load(dir, true)` with files 0644 owned by the test user → `owned by the current user`, skipped when `os.Geteuid() == 0`; file chmod 0666 → `writable by group or others`); `TestResolve`: system dir with `policy.toml` + `TENON_POLICY_DIR=/other` → `(system, true)` (Review Focus 1); system dir empty + env set → `(env, false)`; neither → `("", false)`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** (`syscall.Stat_t` for the owner uid; linux and darwin only).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(orgpolicy): load and verify the system policy`

---

### Task 4: `config.ApplyPolicy`

**Files:**
- Modify: `internal/config/config.go` (fields), `internal/config/load.go` (or new `internal/config/policy.go`)
- Test: `internal/config/policy_test.go`

**Ruling vs spec §7:** the spec names `config.Load(..., pol)`; the plan keeps `Load`'s signature and adds a separate pure `ApplyPolicy`, called by `cmd/tenon` right after `Load` — same behaviour, no churn in the many existing `Load` callers and tests.

**Interfaces:**
- Consumes: `orgpolicy.Policy` (Task 2).
- Produces:
  - `Config.Policy *orgpolicy.Policy \`toml:"-"\``; `Rule.Policy bool \`toml:"-"\``.
  - Notice fields (all `toml:"-"`): `PolicyOverriddenProviders []string`, `PolicyAuditFrom string` (config level replaced, `""` if unchanged), `PolicyRaisedProfile profile.Name` (config profile, when non-`None` and weaker than the policy's).
  - `func ApplyPolicy(cfg Config, pol *orgpolicy.Policy) (Config, error)` — `pol == nil` → `cfg` unchanged. Otherwise: `cfg.Policy = pol`; profile = `profile.Stricter(cfg.Profile, pol.Profile)` (set `PolicyRaisedProfile` per above); each `Define` replaces/appends a provider (`mergeProvider` without detect inheritance), recording replaced IDs in `PolicyOverriddenProviders`; policy rules prepended with `Policy: true`; redaction rules appended; `AuditLevel != ""` replaces `cfg.Audit.Level` (record previous in `PolicyAuditFrom` if different); then `cfg.validate()`. MCP filtering and the host allowlist are NOT applied here (startup/engine, Tasks 5–6).

- [ ] **Step 1: Failing tests** — `TestApplyPolicyNil` (unchanged, deep-equal); `TestApplyPolicyProfileFloor` (config none + policy eu-only → eu-only, `PolicyRaisedProfile` None; config eu-only + policy strict-local → strict-local, raised = eu-only; config strict-local + policy eu-only → strict-local); `TestApplyPolicyDefineWins` (user provider `interne` with `https://evil.example/v1` → policy's `base_url`, `PolicyOverriddenProviders == ["interne"]`; Review Focus 2); `TestApplyPolicyRules` (policy rules first, `Policy` true, user rules after); `TestApplyPolicyAudit` (config metadata + policy full → full, `PolicyAuditFrom == "metadata"`; equal levels → `""`); `TestApplyPolicyRedaction` (appended).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(config): apply the organisation policy as a floor`

---

### Task 5: engine guard and `SessionStarted.policy`

**Files:**
- Modify: `internal/policy/outbound.go`, `internal/engine/engine.go`, `internal/engine/loop.go`, `internal/event/payloads.go`
- Test: `internal/policy/outbound_test.go`, `internal/engine/*_test.go` (existing profile test file as model)

**Interfaces:**
- Consumes: `orgpolicy.Policy`, `orgpolicy.ErrPolicy`, `orgpolicy.HostOf`.
- Produces:
  - `func (o *Outbound) SetHostPolicy(p *orgpolicy.Policy)`; `Check` order: profile, then `if p != nil && !p.AllowsHost(orgpolicy.HostOf(info.BaseURL))` → `fmt.Errorf("%w: provider %q (host %q) not allowed by policy %s", orgpolicy.ErrPolicy, ...)`, then jurisdiction.
  - `engine.Deps.Policy *orgpolicy.Policy`; `engine.New` calls `d.Outbound.SetHostPolicy(d.Policy)` next to `SetProfile` (single source).
  - `turn.outbound`: `errors.Is(err, orgpolicy.ErrPolicy)` handled like `profile.ErrProfile` with `Code: "policy"`.
  - `event.PolicyRef{ID string \`json:"id"\`; Version int \`json:"version"\`; KeyID string \`json:"key_id"\`}`; `SessionStartedPayload.Policy *PolicyRef \`json:"policy,omitempty"\``, filled from `d.Policy` (nil without policy).

- [ ] **Step 1: Failing tests** — `TestOutboundPolicyHost` (policy allowing `api.mistral.ai`; provider `https://evil.example/v1` → `errors.Is(err, orgpolicy.ErrPolicy)`, consent recorded does not bypass; allowed host passes; nil policy passes). Engine test: provider host not allowed → `Error{code:"policy"}` event, no provider request, turn reason `blocked`; `SessionStarted.policy == {acme-dev, 3, <keyID>}` with policy, absent from JSON (`"policy"` substring not present) without.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(engine): policy host guard before each send, policy in SessionStarted`

---

### Task 6: startup wiring

**Files:**
- Modify: `cmd/tenon/run.go`, `internal/ui/tui/model.go`, `internal/ui/tui/welcome.go`, `internal/i18n/catalog.go`
- Test: `cmd/tenon/policy_e2e_test.go` (new), TUI welcome test

**Interfaces:**
- Consumes: Tasks 3–5.
- Produces:
  - `loadSetup`: `dir, system := orgpolicy.Resolve(getenv)`; `pol, err := orgpolicy.Load(dir, system)` → error returned as i18n `err.policy_load` (`policy: %v`); then `config.ApplyPolicy`. `setup` unchanged otherwise; rules mapped with `Project: r.Project || r.Policy`.
  - `startEngine`: notices for `PolicyRaisedProfile`, `PolicyOverriddenProviders`, `PolicyAuditFrom` (messages in Global Constraints); host check of the selected provider right after `profileProviderErr`, BEFORE `detectLocal` → `err.policy_provider`; `Deps.Policy`.
  - `startMCP`: after the profile check, `!cfg.Policy.AllowsMCP(s.Transport, s.URL, first(s.Command))` (nil-safe) → skipped with `policy.mcp_skipped`, same path as profile skips.
  - `tenon config check`: `config.policy` line (`policy: %s v%d (key %s)`) before the profile line, and the provider host check.
  - TUI: `Options.Policy string` (the formatted `policy: …` line), shown on the welcome screen under the profile line.
  - i18n keys: `err.policy_load`, `err.policy_provider`, `policy.provider_overridden`, `policy.audit_imposed`, `policy.mcp_skipped`, `policy.profile_raised`, `config.policy`, `tui.welcome.policy` (EN/FR).

- [ ] **Step 1: Failing e2e tests** (`TENON_POLICY_DIR` in `e.vars`, `orgpolicytest.Sign`; fake LLM is on `127.0.0.1`):
  - `TestPolicyHostRefusedE2E`: `[providers] allow_hosts = ["api.mistral.ai"]` → `-p salut` exit 1, stderr contains `not allowed by policy acme-dev`, fake LLM received no request.
  - `TestPolicyAllowLocalE2E`: `allow_local = true` → exit 0, `SessionStarted.policy.id == "acme-dev"`, `version == 3`.
  - `TestPolicyBadSignatureE2E`: `policy.toml` modified after signing → exit 1, stderr starts with `error: policy:` (existing `err.prefix`) and names `policy.toml`.
  - `TestPolicyMCPSkippedE2E`: `[mcp] mode = "off"` with one stdio server → notice `policy acme-dev: MCP server "<id>" skipped (not allowed)`, server not launched.
  - `TestPolicyProfileRaisedE2E`: env with jurisdiction `local` (fake on `127.0.0.1`), global `profile = "eu-only"`, policy `profile = "strict-local"` → exit 0, stderr notice `profile "eu-only" from config is weaker than policy profile "strict-local" (policy acme-dev): raised`, `SessionStarted.profile == "strict-local"`; same policy with an env of jurisdiction `eu` → exit 1 with the existing strict-local provider message.
  - `TestPolicyResumeE2E`: session run without policy, then policy refusing its host + `--resume <id>` → exit 1 (Review Focus 5).
  - `TestPolicyRedefinedProviderE2E`: policy defines `interne` → `127.0.0.1` fake; user config redefines `interne` with `https://evil.example/v1` → request reaches the fake, notice `provider "interne" from config ignored` (Review Focus 2).
  - `TestConfigCheckPolicyE2E`: `config check` prints `policy: acme-dev v3 (key <ID>)`.
  - `TestNoPolicyUnchangedE2E`: without `TENON_POLICY_DIR`, `SessionStarted` JSON has no `"policy"`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./cmd/tenon/ -run Policy` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): apply the organisation policy at startup`

---

### Task 7: `tenon policy check`

**Files:**
- Create: `cmd/tenon/policy.go`
- Modify: `cmd/tenon/run.go` (dispatch, usage), `internal/i18n/catalog.go`
- Test: `cmd/tenon/policy_e2e_test.go`

**Interfaces:**
- Consumes: `orgpolicy.SystemDir`, `orgpolicy.Load` (with `checkPerms = false`).
- Produces: `func policyCheck(lang i18n.Lang, args []string, stdout, stderr io.Writer) int` — `args` = `[]` (system dir) or `[dir]`; more → usage error, exit 1. No `policy.toml` → `policy.check_absent` (`no policy in <dir>`), exit 1. Invalid → `error: policy: …`, exit 1. Valid → lines (i18n keys `policy.check_*`): `policy: <id> v<n> (key <ID>)`, `profile: <p|none>`, `providers: <n> allowed hosts, local <yes|no>, <m> defined` or `providers: no allowlist`, `rules: <n>`, `redaction: <n>`, `audit: <level|not imposed>`, `mcp: <mode>`; exit 0. Does not read user/project config.

- [ ] **Step 1: Failing tests** — `TestPolicyCheckValidE2E` (exit 0, each line); `TestPolicyCheckInvalidE2E` (tampered → exit 1); `TestPolicyCheckAbsentE2E` (empty dir → exit 1, `no policy in`); `TestPolicyCheckArgsE2E` (two args → exit 1).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): tenon policy check`

---

### Task 8: compliance export and docs

**Files:**
- Modify: `internal/compliance/report.go`, `internal/compliance/build.go`, `internal/compliance/report.html.tmpl`, `internal/compliance/testdata/report.golden.json`, `internal/i18n/catalog.go`
- Modify docs: `docs/security.md`, `docs/fr/securite.md`, `docs/usage.md`, `docs/fr/utilisation.md` (export JSON schema section), `README.md` (one line in features)
- Create docs: `docs/policy.md`, `docs/fr/politique.md`
- Test: `internal/compliance/build_test.go`, `html_test.go`, `cmd/tenon/audit_export_e2e_test.go`

**Interfaces:**
- Consumes: `event.PolicyRef` (Task 5).
- Produces: `type PolicyUse struct{ ID string \`json:"id"\`; Version int \`json:"version"\`; KeyID string \`json:"key_id"\`; Sessions int \`json:"sessions"\` }`; `Report.Policies []PolicyUse \`json:"policies"\`` (after `profiles`, never nil), one entry per distinct (id, version, key) among in-scope sessions' `SessionStarted` (same session attribution as `profilesOf`), sorted by id then version; sessions without policy not listed. HTML: row `report.label.policies` (`Politique d'organisation` / `Organisation policy`) — `report.unavailable` when `!ContentAvailable`, `report.none` when empty, else `acme-dev v3 (clé <ID>) : 2 sessions`.

- [ ] **Step 1: Failing tests** — `TestBuildPolicies` (two sessions with `acme-dev v3`, one with `v4`, one without → 2 entries, counts 2 and 1); metadata level → content unavailable, HTML shows unavailable; golden JSON regenerated (`policies: []`) and reviewed; e2e export after a policy session contains `"policies":[{"id":"acme-dev"`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**; then write `docs/policy.md` / `docs/fr/politique.md`: locations, the three files, signing with `minisign -Sm policy.toml`, full annotated example (spec §4), merge semantics (spec §5), failure cases (spec §3), `tenon policy check`, `TENON_POLICY_DIR` semantics; security docs: one section linking to it; export schema: `policies` field.
- [ ] **Step 4: Run** — PASS; full gates; `make ci` with go1.26.9 (`PATH=/opt/homebrew/opt/go@1.26/bin:$PATH`) for govulncheck on the new dependency.
- [ ] **Step 5: Commit** `feat(compliance): policies in the export; docs: organisation policy (EN/FR)`
