# tenon provider allowlist and secnumcloud profile — Implementation Plan (plan 13)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `[provider_allowlist]` in global and project config restricts provider hosts (every present list must accept), and a `secnumcloud` profile accepts only local or declared-`secnumcloud` destinations; declarations are traced and, under an organisation policy, only the policy's declarations count.

**Architecture:** Host matching moves from `internal/orgpolicy` into a new `internal/hostmatch` package shared by the policy and the allowlist. `internal/profile` gains the `secnumcloud` level, reading `Qualifications` from `llm.ProviderInfo` / `MCPTarget`. `internal/config` parses the allowlists and qualifications and strips user qualifications under a policy. `policy.Outbound` re-checks the allowlists before each send; `cmd/tenon` refuses at startup; the export shows declared qualifications.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), standard library.

**Spec:** `docs/superpowers/specs/2026-10-21-tenon-allowlist-secnumcloud-design.md`

## Global Constraints

- No new dependency. `internal/hostmatch` imports only the standard library and `internal/profile`.
- Host match (unchanged from plan 12): host of the URL lowercased, port stripped; entry `x.eu` exact; `*.x.eu` strict subdomains only, never `x.eu`; entries refused if empty, containing a scheme, path, port, or a `*` other than a leading `*.` followed by at least two labels; `Local` admits `profile.IsLocalHost`.
- Profile order: none < `eu-only` < `secnumcloud` < `strict-local`.
- `secnumcloud` provider: `jurisdiction = "local"` with a local host, or `jurisdiction = "eu"` with `"secnumcloud"` in `qualifications`. MCP: stdio `local`; http local (jurisdiction `local` and local host) or `eu` + qualified.
- Only qualification value accepted: `secnumcloud` (exact, lowercase). Presets declare none.
- Under an organisation policy, qualifications from user config (providers and MCP servers) are removed; `providers.define` keep theirs.
- Every present allowlist (policy, global, project) must accept the provider host. The allowlist never applies to MCP servers.
- Messages (exact, EN; FR equivalents in the i18n catalog):
  - `config: unknown profile "<x>" (eu-only|secnumcloud|strict-local)`
  - `config: unknown qualification "<x>" (secnumcloud)`
  - `config: provider_allowlist is empty`
  - `provider "<id>" (host "<h>") not in provider_allowlist (<global|project>)` (exit 1)
  - `profile secnumcloud: provider "<id>" is not declared secnumcloud-qualified` (exit 1)
  - MCP reason `not declared secnumcloud-qualified` inside the existing `profile <p>: MCP server "<id>" (<reason>) skipped`
  - `policy <id>: qualifications declared in config ignored (<ids>)` (ids comma-separated, sorted)
  - `provider_allowlist: global <n> hosts, project <m> hosts` (`config check`; ` + local` appended to a side with `local = true`; a missing side prints `-`)
- Engine error: `event.ErrorPayload{Code: "allowlist"}`, turn ends `blocked` (like profile/policy).
- `llm.ProviderInfo.Qualifications []string \`json:"qualifications,omitempty"\``; old events serialize unchanged.
- Without allowlist, qualification or `secnumcloud`, behaviour is unchanged (all existing tests pass untouched, except the profile `Parse` message which gains `secnumcloud`).
- New user-visible strings through `internal/i18n` (EN and FR). Code comments in French.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- Project config with `[provider_allowlist]` while the global config has none: the project list applies alone (it restricts) — test in Task 5.
- `qualifications = ["SecNumCloud"]` (wrong case): load error naming the valid value, never a silently unqualified provider — test in Task 3.
- `profile = "secnumcloud"` with the default `ollama` preset on `localhost`: accepted without any qualification (local) — test in Task 2 (and e2e in Task 5).
- Policy imposing `profile = "secnumcloud"` and defining a qualified provider `souverain`, user config `default_provider = "souverain"`: session runs and `SessionStarted.provider.qualifications == ["secnumcloud"]` — test in Task 5.
- `--provider <other>` naming a provider outside the global allowlist while `default_provider` is inside: refused — test in Task 5.

---

### Task 1: `internal/hostmatch`

**Files:**
- Create: `internal/hostmatch/hostmatch.go`, `internal/hostmatch/hostmatch_test.go`
- Modify: `internal/orgpolicy/policy.go` (delegate), `internal/orgpolicy/policy_test.go` (host-only tests move)

**Interfaces:**
- Produces:
  - `func HostOf(rawURL string) string` — `url.Parse(...).Hostname()` lowercased; `""` on error.
  - `func CheckEntry(e string) error` — the entry rules (Global Constraints); plain `error` (callers wrap).
  - `func Match(entries []string, host string) bool`.
  - `type List struct{ Name string; Hosts []string; Local bool }`; `func (l List) Allows(host string) bool` — `Match(l.Hosts, host) || (l.Local && profile.IsLocalHost(host))`; empty `host` → false.
- `orgpolicy`: `HostOf` stays exported and delegates; `checkHost`/`matchHost` replaced by `hostmatch.CheckEntry`/`hostmatch.Match` (error still wraps `ErrPolicy`); behaviour unchanged.

- [ ] **Step 1: Failing tests** — move the host cases from `internal/orgpolicy/policy_test.go` (`TestAllowsHost` matrix: exact, wildcard, apex refused, `api.scaleway.ai.evil.eu` refused, case, port) into `TestMatch` and `TestListAllows` (`Local` true/false with `localhost`, `127.0.0.1`, `gpu.lan`, `""`); `TestCheckEntry` (valid: `api.mistral.ai`, `*.scaleway.ai`; invalid: `""`, `https://x.eu`, `x.eu/v1`, `x.eu:443`, `*.eu`, `a.*.eu`, `*`, `x..eu`); `TestHostOf` (`https://API.Mistral.AI:443/v1` → `api.mistral.ai`, `http://[::1]:8000` → `::1`, `::bad` → `""`). Keep orgpolicy's `AllowsHost`/`AllowsMCP`/`Parse` tests (they must still pass).
- [ ] **Step 2: Run** `go test ./internal/hostmatch/` — FAIL (undefined).
- [ ] **Step 3: Implement**; switch `orgpolicy` to it.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `refactor(hostmatch): shared host matching for policy and allowlist`

---

### Task 2: `secnumcloud` profile

**Files:**
- Modify: `internal/llm/*.go` (`ProviderInfo`), `internal/profile/profile.go`, `internal/profile/profile_test.go`

**Interfaces:**
- Produces:
  - `llm.ProviderInfo.Qualifications []string \`json:"qualifications,omitempty"\``.
  - `profile.SecNumCloud Name = "secnumcloud"`; `const QualSecNumCloud = "secnumcloud"`.
  - `Parse` accepts it; unknown message per Global Constraints; `Stricter` uses the new order.
  - `profile.MCPTarget` gains `Qualifications []string`.
  - `CheckProvider(SecNumCloud, p)`: rules in Global Constraints; non-local without qualification → `profile secnumcloud: provider "<id>" is not declared secnumcloud-qualified`; a `local` provider with a remote host → the existing strict-local-style host message with profile `secnumcloud` (`profile secnumcloud: provider "<id>" url host "<host>" is not a local address`); non-eu/empty jurisdiction → the existing jurisdiction message.
  - `CheckMCP(SecNumCloud, t)`: reason `not declared secnumcloud-qualified` for an `eu` server without qualification; existing reasons otherwise.
  - `func Qualified(quals []string) bool` — contains `QualSecNumCloud`.

- [ ] **Step 1: Failing tests** — `TestParse` adds `secnumcloud` and the new unknown message; `TestStricter` all 16 pairs; `TestCheckProviderSecNumCloud`: local+`http://localhost:11434/v1` ✓ (Review Focus 3), local+`http://gpu.lan:8000/v1` ✗ (host message), eu+`["secnumcloud"]` ✓, eu+nil ✗ (exact message), non-eu+`["secnumcloud"]` ✗, empty jurisdiction ✗; `TestCheckMCPSecNumCloud`: stdio local ✓, stdio eu ✗, http local+localhost ✓, http eu qualified ✓, http eu unqualified ✗ with reason `not declared secnumcloud-qualified`; existing eu-only/strict-local tests unchanged and passing; JSON of a `ProviderInfo` without qualifications has no `qualifications` key.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Fix the one existing message expectation for `Parse` wherever it appears in other packages' tests (grep `eu-only|strict-local)`).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(profile): secnumcloud profile from declared qualifications`

---

### Task 3: config — qualifications and allowlists

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`, `internal/config/policy.go`, `internal/orgpolicy/policy.go` (`ProviderDef.Qualifications`, validation), `docs/config.example.toml`
- Test: `internal/config/config_test.go` (or new `allowlist_test.go`), `internal/config/policy_test.go`, `internal/orgpolicy/policy_test.go`

**Interfaces:**
- Consumes: `hostmatch.CheckEntry` (Task 1), `profile.QualSecNumCloud` (Task 2).
- Produces:
  - `Provider.Qualifications []string \`toml:"qualifications"\``; `MCPServer.Qualifications []string \`toml:"qualifications"\``; `orgpolicy.ProviderDef.Qualifications` (TOML `qualifications`), copied by `ApplyPolicy` into the defined `Provider`.
  - `type ProviderAllowlist struct{ Hosts []string \`toml:"hosts"\`; Local bool \`toml:"local"\` }`; `Config.Allowlist *ProviderAllowlist \`toml:"provider_allowlist"\`` (global); `Config.ProjectAllowlist *ProviderAllowlist \`toml:"-"\``, filled from `provider_allowlist` in `projectFile`.
  - Validation (in `validate()`; orgpolicy validates its own `define`): unknown qualification → `config: unknown qualification "<x>" (secnumcloud)` (orgpolicy: wraps `ErrPolicy`); allowlist present with no hosts and `Local == false` → `config: provider_allowlist is empty` (project: `project config: provider_allowlist is empty`); bad entry → `config: provider_allowlist: <CheckEntry error>`.
  - `ApplyPolicy`: when `pol != nil`, clear `Qualifications` of every provider not defined by the policy and of every MCP server; record their IDs (non-empty before clearing) sorted in `PolicyStrippedQualifications []string \`toml:"-"\``.

- [ ] **Step 1: Failing tests** — `TestQualificationValid` (`["secnumcloud"]` loads); `TestQualificationUnknown` (`["SecNumCloud"]` → exact message, Review Focus 2; `["hds"]` → same form); MCP server qualification parsed; `TestAllowlistGlobal` (hosts + local loaded); `TestAllowlistProject` (project section → `ProjectAllowlist`, `Allowlist` nil when global absent); `TestAllowlistEmpty` (global and project variants, exact messages); `TestAllowlistBadEntry` (`https://x.eu`); `TestApplyPolicyStripsQualifications` (user provider + MCP server qualified, policy define qualified → only define keeps it; `PolicyStrippedQualifications` = sorted user IDs); orgpolicy `TestParseRejects` adds an unknown define qualification.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**; add a commented `[provider_allowlist]` and a `qualifications` example to `docs/config.example.toml` (it is loaded by `example_test.go`: keep it valid).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(config): provider allowlists and declared qualifications`

---

### Task 4: engine guard

**Files:**
- Modify: `internal/policy/outbound.go`, `internal/engine/engine.go`, `internal/engine/loop.go`
- Test: `internal/policy/outbound_test.go`, `internal/engine/*_test.go` (model: `internal/engine/orgpolicy_test.go`)

**Interfaces:**
- Consumes: `hostmatch.List`, `hostmatch.HostOf`.
- Produces:
  - `var ErrAllowlist = errors.New("allowlist violation")` in `internal/policy`.
  - `func (o *Outbound) SetAllowlists(lists []hostmatch.List)`; `Check` order: profile, policy host, allowlists (first list that refuses → `fmt.Errorf("%w: provider %q (host %q) not in provider_allowlist (%s)", ErrAllowlist, id, host, list.Name)`), jurisdiction.
  - `engine.Deps.Allowlists []hostmatch.List`; `engine.New` calls `SetAllowlists` next to `SetHostPolicy`.
  - `turn.outbound`: `errors.Is(err, policy.ErrAllowlist)` handled like `ErrPolicy` with `Code: "allowlist"`.

- [ ] **Step 1: Failing tests** — `TestOutboundAllowlist` (global list `api.mistral.ai` refuses `https://evil.example/v1` with `ErrAllowlist` and list name in the message, even with consent recorded; two lists where only the second refuses → message names the second; nil/empty slice → passes). Engine: host refused by allowlist → one `Error{code:"allowlist"}`, zero provider requests, turn reason `blocked`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(engine): provider allowlist guard before each send`

---

### Task 5: startup wiring

**Files:**
- Modify: `cmd/tenon/run.go`, `internal/i18n/catalog.go`
- Test: `cmd/tenon/allowlist_e2e_test.go` (new), `cmd/tenon/profile_e2e_test.go` / `policy_e2e_test.go` helpers reused

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces:
  - `providerInfo` copies `Qualifications`; `profileMCPSkip` passes `s.Qualifications` in `MCPTarget`.
  - `profileProviderErr` handles the `secnumcloud` messages (new i18n keys `err.profile_unqualified`, `profile.reason_unqualified`; the host message reuses `err.profile_host` with the profile name as an argument if it is currently hard-coded to strict-local — keep EN text per Global Constraints).
  - `func allowlists(cfg config.Config) []hostmatch.List` — global (`Name: "global"`) then project (`Name: "project"`), nil entries skipped.
  - `startEngine`: after `policyProviderErr`, before `detectLocal`, each list must allow `hostmatch.HostOf(info.BaseURL)` → `err.allowlist_provider`; `Deps.Allowlists: allowlists(cfg)`; notice `policy.qualifications_stripped` when `PolicyStrippedQualifications` non-empty.
  - `config check`: line `config.allowlist` when either list is present; the allowlist host check on the selected provider.
  - i18n keys: `err.allowlist_provider`, `err.profile_unqualified`, `profile.reason_unqualified`, `policy.qualifications_stripped`, `config.allowlist` (EN/FR).

- [ ] **Step 1: Failing e2e tests:**
  - `TestAllowlistGlobalRefusedE2E`: global `hosts = ["api.mistral.ai"]` → `-p salut` exit 1, stderr `not in provider_allowlist (global)`, zero requests.
  - `TestAllowlistLocalE2E`: global `local = true` → exit 0.
  - `TestAllowlistProjectOnlyE2E`: no global list, project `hosts = ["api.mistral.ai"]` → exit 1 `(project)` (Review Focus 1).
  - `TestAllowlistProjectNarrowsE2E`: global `local = true`, project `hosts = ["api.mistral.ai"]` → exit 1 `(project)`.
  - `TestAllowlistProviderFlagE2E`: global list allows the default provider host only; `--provider` naming a provider with another host → exit 1 (Review Focus 5).
  - `TestSecNumCloudUnqualifiedE2E`: env jurisdiction `eu`, global `profile = "secnumcloud"` → exit 1, stderr `is not declared secnumcloud-qualified`.
  - `TestSecNumCloudQualifiedE2E`: same with `qualifications = ["secnumcloud"]` on the test provider → exit 0, `SessionStarted.provider.qualifications == ["secnumcloud"]`.
  - `TestSecNumCloudLocalE2E`: env jurisdiction `local` on `127.0.0.1`, `profile = "secnumcloud"`, no qualification → exit 0 (Review Focus 3).
  - `TestSecNumCloudPolicySelfQualifiedE2E`: policy `profile = "secnumcloud"`, `[providers] allow_local = true`; user provider (jurisdiction `eu`, fake on 127.0.0.1) self-declares `qualifications = ["secnumcloud"]` → exit 1 and stderr notice `qualifications declared in config ignored`.
  - `TestSecNumCloudPolicyDefinedE2E`: policy `profile = "secnumcloud"`, `allow_local = true`, defines `souverain` (jurisdiction `eu`, fake URL, qualified); user `default_provider = "souverain"` → exit 0, `SessionStarted.provider.qualifications == ["secnumcloud"]` (Review Focus 4).
  - `TestConfigCheckAllowlistE2E`: prints `provider_allowlist: global 1 hosts + local, project - hosts` for global `hosts=[…1…], local=true` and no project list.
- [ ] **Step 2: Run** `go test ./cmd/tenon/ -run 'Allowlist|SecNumCloud'` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): provider allowlists and secnumcloud at startup`

---

### Task 6: compliance export and docs

**Files:**
- Modify: `internal/compliance/report.go`, `build.go`, `report.html.tmpl`, `testdata/report.golden.json`, `internal/i18n/catalog.go`
- Modify docs: `docs/security.md`, `docs/fr/securite.md` (profiles: add `secnumcloud`; allowlist section), `docs/usage.md`, `docs/fr/utilisation.md` (config keys, export schema `qualifications`), `docs/policy.md`, `docs/fr/politique.md` (`qualifications` in `define`; user qualifications ignored under a policy), `README.md` (profiles list)
- Test: `internal/compliance/build_test.go`, `html_test.go`

**Interfaces:**
- Produces: `ProviderUse.Qualifications []string \`json:"qualifications"\`` (never nil; from `SessionStarted.provider.qualifications`, union over the provider's sessions, sorted, cleaned); HTML column `report.col.qualification` (`Qualification déclarée` / `Declared qualification`) with footnote `report.qualification_note` (`déclarée, non vérifiée par tenon` / `declared, not verified by tenon`); `—` when empty.

- [ ] **Step 1: Failing tests** — `TestBuildProviderQualifications` (two sessions of `souverain` with `["secnumcloud"]` → `["secnumcloud"]`; a provider without → `[]`); HTML FR/EN shows the column header and the note; golden regenerated (`qualifications: []` per provider) and reviewed.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**; write the docs: `secnumcloud` rules (spec §4), that tenon does not verify qualifications, the allowlist (spec §3) with its messages quoted from the catalog, the policy interaction.
- [ ] **Step 4: Run** — PASS; full gates; `make ci` with go1.26.9 (`PATH=/opt/homebrew/opt/go@1.26/bin:$PATH`).
- [ ] **Step 5: Commit** `feat(compliance): declared qualifications in the export; docs: allowlist and secnumcloud (EN/FR)`
