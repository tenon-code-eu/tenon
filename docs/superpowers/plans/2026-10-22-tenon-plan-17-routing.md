# tenon data-sensitivity routing — Implementation Plan (plan 17)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** classified content (path rules or markers) is only ever sent to a local model; once it enters a session, the whole session is routed locally (compaction and sub-agents included); without a local provider it never enters the history; other requests fall back along an ordered provider list on outage; the journal and the export prove it.

**Architecture:** `internal/classify` matches paths, markers and shell commands. `internal/routing` orders candidate providers and classifies retryable errors. The engine keeps a per-session sensitive flag, gates file/shell tools before they run, scans results and user messages, routes every model call through the router with the existing guards applied per candidate, and runs an outbound marker scan before any non-local send. Config/policy add `[classification]` and `[routing]`.

**Tech Stack:** Go 1.26 (vendored, `GOFLAGS=-mod=vendor`), standard library (`golang.org/x/text/unicode/norm` only if already vendored; otherwise a small accent-folding table).

**Spec:** `docs/superpowers/specs/2026-10-22-tenon-routing-design.md`

**Base branch:** `feat/routing` is created from `feat/sandbox` (bf3d079 or later) because both touch `internal/engine/loop.go` and `cmd/tenon/run.go`; merge `main` into it first to bring the spec and this plan.

## Global Constraints

- No new module. Check `vendor/golang.org/x/text/unicode/norm` before using it; if absent, fold accents with a fixed table for Latin-1/Latin Extended-A letters.
- Built-in markers, always active: `DIFFUSION RESTREINTE`, `SPÉCIAL FRANCE`, `SPECIAL FRANCE`, `RESTREINT UE`, `EU RESTRICTED`.
- Marker match: case- and accent-insensitive, runs of whitespace (incl. newlines) collapsed to one space, whole phrase with word boundaries at both ends (`DIFFUSION RESTREINTES` does not match).
- Path patterns: same wildcard syntax as permission rules (`*` within a segment, `**` across segments), matched against the path relative to the resolved project root; a path outside the project never matches a relative pattern; absolute patterns allowed.
- Spec §4 names a `search` tool; the real tools are `grep` and `glob`.
- Tools that can taint: `read_file`, `write_file`, `edit_file` (path or content), `grep` (any result path classified, or a marker in the output), `shell` (a classified path in the command words, or a marker in the output), any MCP tool (marker in the result). `glob` never taints (names only).
- Exact messages:
  - `config: routing.local must name a local provider`
  - `config: routing.fallback: unknown provider "<id>"`
  - `config: routing not allowed in project configuration`
  - notice `policy <id>: routing.local imposed (<id>)`
  - tool refusal content `classified content requires a local provider (routing.local)` (`ToolResult.denied = "classification"`)
  - withheld content `[classified content withheld: no local provider]`
  - turn block: `Error` `code: "classification"`, turn `blocked`
  - headless stderr `classification: session is sensitive (<reason>), routed to <local>` and `routing: <from> failed (<error>), using <to>`
  - `config check`: `classification: <n> paths, <m> markers` and `routing: local <id>, fallback <ids>` or `routing: none`
- Events: `SessionClassified {reason: "path"|"marker", source: "tool"|"shell"|"mcp"|"user"|"outbound", call_id, path, marker}` (empty omitted, once per session); `ProviderSelected {provider, reason: "default"|"fallback"|"sensitive", error}` (when the effective provider changes, incl. the first send); `SessionStarted.routing {local, fallback}` (omitted without `[routing]`).
- Fallback triggers: `provider.ErrUnavailable` (the provider already retried 429/5xx), connection errors (`net.Error`, `*url.Error`, `syscall.ECONNREFUSED`), timeout before the first chunk. Never after the first chunk; never on `ErrAuth`, `ErrContextLength`, other 4xx.
- Sensitive session → `routing.local` only, never a non-local fallback; failure ends the turn in error.
- Notices via `internal/i18n` (EN/FR); `config:` errors EN; code comments French; docs EN+FR.
- Every task: `gofmt -l cmd internal scripts` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOOS=linux GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- A classified file reached through a symlink inside the project, or through `../` in a tool path: the resolved path is matched, so it taints (or is refused) — test in Task 1 (`MatchPath` on resolved paths) and Task 4 (tool through a symlink).
- A marker split across two chunks of a streamed tool output, or across lines (`DIFFUSION\nRESTREINTE`): matched after whitespace collapsing on the full result text — test in Task 1.
- `--provider <local>` chosen explicitly with no `[routing]`: the session is already local, classified reads are allowed, no fallback — test in Task 6.
- Resume of a sensitive session when the local provider is now missing: the session cannot continue remotely; the first send is blocked (`classification`) — test in Task 4/5.
- Hook `updatedInput` rewriting a tool path to a classified one after the pre-check: the check runs on the final arguments — test in Task 4.

---

### Task 1: `internal/classify`

**Files:**
- Create: `internal/classify/classify.go`, `internal/classify/fold.go`
- Test: `internal/classify/classify_test.go`

**Interfaces:**
- Produces:
  - `var BuiltinMarkers = []string{"DIFFUSION RESTREINTE", "SPÉCIAL FRANCE", "SPECIAL FRANCE", "RESTREINT UE", "EU RESTRICTED"}`
  - `type Rules struct { Paths, Markers []string }` — `Markers` holds only user/policy markers; built-ins are always added by the matcher.
  - `func (r Rules) MatchPath(root, path string) (pattern string, ok bool)` — `path` absolute or relative to root; resolved with `EvalSymlinks` when it exists (root too).
  - `func (r Rules) FindMarker(text string) (marker string, ok bool)` — returns the marker as configured (original spelling).
  - `func (r Rules) MatchCommand(root, cmd string) (pattern string, ok bool)` — splits the command into words (whitespace, quotes stripped, `=`/`>`/`<` treated as separators) and calls `MatchPath` on each word that looks like a path.
  - `func (r Rules) Empty() bool` — no user paths and no user markers (built-ins still apply).

- [ ] **Step 1: Failing tests:**
  - `TestMatchPath`: `rh/**` matches `<root>/rh/a/b.txt` and not `<root>/rhx/a`; `*.dr.md` matches `<root>/n.dr.md` and not `<root>/x/n.dr.md` (`*` stays within a segment); `**/*.dr.md` matches `<root>/x/n.dr.md`; absolute `/srv/secret/**` matches `/srv/secret/a`; relative pattern never matches `/etc/passwd`; a symlink `<root>/link -> <root>/rh` → `<root>/link/f` matches `rh/**` (Review Focus 1); `<root>/sub/../rh/f` matches.
  - `TestFindMarker`: `"… diffusion restreinte …"` → `DIFFUSION RESTREINTE`; `"Spécial  France"` and `"SPECIAL\nFRANCE"` match (Review Focus 2); `"DIFFUSION RESTREINTES"` no match; `"XDIFFUSION RESTREINTE"` no match; a user marker `C2 - Interne` matches `c2 - interne`; built-ins active with empty Rules.
  - `TestMatchCommand`: `cat rh/paie.csv` → `rh/**`; `grep -r x ./rh` → match; `echo rh` (not a path, no slash, not existing) → no match; `cp a "contrats/x y.pdf" b` → match.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/classify/` — FAIL.
- [ ] **Step 3: Implement.** Reuse the wildcard matcher of `internal/policy` (export it there as `policy.MatchWildcard(pattern, s string) bool` if it is unexported, without changing behaviour). "Looks like a path" in `MatchCommand`: contains `/`, or exists relative to root.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(classify): path, marker and command classification rules`

---

### Task 2: configuration and policy

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`, `internal/config/policy.go`, `internal/orgpolicy/*.go`, `docs/config.example.toml`
- Test: `internal/config/*_test.go`, `internal/orgpolicy/*_test.go`

**Interfaces:**
- Consumes: `classify.Rules` (Task 1).
- Produces:
  - `config.Classification struct { Paths []string \`toml:"paths"\`; Markers []string \`toml:"markers"\` }`, `config.Routing struct { Local string \`toml:"local"\`; Fallback []string \`toml:"fallback"\` }`; fields `Config.Classification`, `Config.Routing`; `func (c Config) ClassifyRules() classify.Rules` (union global + project + policy).
  - `Config.PolicyRoutingLocal string \`toml:"-"\`` (set when the policy replaced `routing.local`, for the notice).
  - `orgpolicy.Policy.Classification{Paths, Markers}`, `orgpolicy.Policy.Routing{Local}`.

- [ ] **Step 1: Failing tests:**
  - `TestRoutingLocalNotLocal` (`local = "mistral"`) → `config: routing.local must name a local provider`; `local = "ollama"` accepted.
  - `TestRoutingFallbackUnknown` → `config: routing.fallback: unknown provider "nope"`.
  - `TestRoutingProject` → `config: routing not allowed in project configuration`.
  - `TestClassificationUnion`: global + project paths/markers concatenated; project cannot remove.
  - `TestPolicyRouting`: policy `local = "ollama"` replaces user `local = "vllm"`, `PolicyRoutingLocal == "ollama"`; policy `local` naming a non-local provider → policy refused; policy paths/markers appended.
  - `TestConfigExampleLoads` passes with commented `[classification]` and `[routing]` blocks.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/config/ ./internal/orgpolicy/` — FAIL.
- [ ] **Step 3: Implement.** Validation of `routing.local`/`fallback` happens after providers (presets + user + policy `define`) are known.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(config): [classification] and [routing]; policy can add rules and impose the local provider`

---

### Task 3: `internal/routing` and retryable errors

**Files:**
- Create: `internal/routing/routing.go`
- Modify: `internal/provider/openai.go` (only if needed for a typed "no first chunk" timeout)
- Test: `internal/routing/routing_test.go`

**Interfaces:**
- Produces:
  - `type Candidate struct { ID string; Provider provider.Provider }`
  - `func New(current Candidate, local *Candidate, fallback []Candidate) *Router` — `fallback` entries equal to `current.ID` are dropped; nil `local` = no local provider.
  - `func (r *Router) Candidates(sensitive bool) []Candidate` — sensitive: `[local]` or empty; normal: `current` then fallback, de-duplicated; if `current` is the local provider, normal also returns `[current]` first.
  - `func (r *Router) Local() (Candidate, bool)`
  - `func Retryable(err error) bool` — per Global Constraints.
  - `var ErrNoFirstChunk = errors.New("no response before timeout")` and `const FirstChunkTimeout = 60 * time.Second` — the engine wraps a send so that no chunk within the timeout cancels the call with this error (Retryable).

- [ ] **Step 1: Failing tests:** `TestCandidatesNormal` (order, de-dup, current not repeated), `TestCandidatesSensitive` (local only; none → empty), `TestCandidatesCurrentLocal`, `TestRetryable` table: `ErrUnavailable` true, `*url.Error` with `ECONNREFUSED` true, `ErrNoFirstChunk` true, `ErrAuth` false, `ErrContextLength` false, `fmt.Errorf("provider: HTTP status 400: x")` false, `context.Canceled` false.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/routing/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(routing): candidate order and retryable provider errors`

---

### Task 4: sensitive sessions in the engine (classification, no routing yet)

**Files:**
- Modify: `internal/engine/engine.go` (Deps), `internal/engine/loop.go`, `internal/engine/agent.go`, `internal/engine/restore.go` (resume), `internal/event/payloads.go`, `internal/event/event.go`
- Test: `internal/engine/classify_test.go`

**Interfaces:**
- Consumes: `classify.Rules` (Task 1).
- Produces:
  - `engine.Deps.Classify classify.Rules`, `engine.Deps.HasLocal bool` (Task 6 sets it from the router; Task 5 replaces it by the router).
  - `event.SessionClassified` type + `SessionClassifiedPayload{Reason, Source, CallID, Path, Marker}` (JSON `reason`, `source`, `call_id,omitempty`, `path,omitempty`, `marker,omitempty`).
  - session field `sensitive bool`; `func (t *turn) classify(reason, source, callID, path, marker string) error` (emits once).

- [ ] **Step 1: Failing tests** (fake provider, temp project with `rh/paie.csv` and `doc.md` containing `DIFFUSION RESTREINTE`):
  - `TestReadClassifiedPath` with `HasLocal: true` → `SessionClassified{reason:"path", source:"tool", path:"rh/paie.csv"}` once; with `HasLocal: false` → `ToolResult{denied:"classification"}`, exact content, no read, no `SessionClassified`.
  - `TestReadMarkedFile`: `HasLocal: true` → `SessionClassified{reason:"marker", marker:"DIFFUSION RESTREINTE"}`; `false` → tool result content replaced by `[classified content withheld: no local provider]`, not sensitive.
  - `TestShellClassified`: `cat rh/paie.csv` refused without local; shell output containing a marker withheld without local, taints with local.
  - `TestGrepClassified`, `TestGlobNeverTaints`, `TestMCPResultMarker` (fake MCP tool).
  - `TestUserMessageMarker`: with local → `source:"user"`; without → `Error{code:"classification"}`, turn `blocked`, provider not called.
  - `TestHookRewriteToClassified` (Review Focus 5): a PreToolUse hook `updatedInput` changes the path to `rh/paie.csv` → refused without local.
  - `TestSymlinkClassified` (Review Focus 1).
  - `TestResumeSensitive`: a journal with `SessionClassified` → resumed session sensitive; with `HasLocal: false` the first send is blocked (`classification`) (Review Focus 4).
  - `TestSubAgentTaintsParent`: a sub-agent reads a classified file → the parent session is sensitive.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/engine/ -run 'Classif|Marked|Shell|Grep|Glob|MCP|UserMessage|HookRewrite|Symlink|ResumeSensitive|SubAgentTaints'` — FAIL.
- [ ] **Step 3: Implement.** Pre-check after hooks and permission decision, on the final arguments; result scan on the full result text before it is recorded and appended to history.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(engine): sensitive sessions from classified paths and markers`

---

### Task 5: routed sends, fallback and outbound scan

**Files:**
- Modify: `internal/engine/engine.go`, `internal/engine/loop.go`, `internal/engine/compact.go`, `internal/engine/agent.go`, `internal/event/payloads.go`, `internal/event/event.go`
- Test: `internal/engine/routing_test.go`

**Interfaces:**
- Consumes: Tasks 3–4.
- Produces:
  - `engine.Deps.Router *routing.Router` (nil = today's single `Deps.Provider`, no fallback); `Deps.HasLocal` removed in favour of `Router.Local()` (keep `Deps.Provider` as the current provider for compatibility; when `Router` is nil, `New(Candidate{ID: Provider.Info().ID, Provider: Provider}, nil, nil)` is used internally).
  - `event.ProviderSelected` + `ProviderSelectedPayload{Provider llm.ProviderInfo; Reason, Error string}` (JSON `provider`, `reason`, `error,omitempty`).
  - `func (t *turn) send(req llm.Request, deltas bool) (text string, calls []llm.ToolCall, provErr, err error)` replacing direct `Provider.Stream` use in `stream`, compaction and sub-agents: iterate `Router.Candidates(sensitive)`, apply the existing per-provider guards (profile, allowlist, policy outbound, jurisdiction consent) to each candidate's `Info()`, skip refused candidates with a notice event (reuse `Error{code:"outbound"}` without failing the turn unless no candidate remains), emit `ProviderSelected` on change, fall back on `routing.Retryable` errors only while no chunk was received, enforce `routing.FirstChunkTimeout`.
  - Outbound scan: before sending to a candidate whose `Info().Jurisdiction != local`, run `Classify.FindMarker` over every message's text; found → `classify(…, "marker", "outbound", …)` and restart candidate selection as sensitive (or block without local).
  - Context window: the selected candidate's window drives compaction for that send.

- [ ] **Step 1: Failing tests** (fake providers `remote` (eu) and `local` (local)):
  - `TestSensitiveRoutesLocal`: after a classified read, every following request (incl. compaction forced by a small window, and a sub-agent call) goes to `local`; `remote` receives zero requests after `SessionClassified`; `ProviderSelected{reason:"sensitive"}` emitted once.
  - `TestSensitiveLocalFailsNoFallback`: local returns `ErrUnavailable` → turn error, remote not called.
  - `TestFallbackOn503`: `remote` returns `ErrUnavailable`, `remote2` answers → `ProviderSelected{provider:remote2, reason:"fallback", error:"provider unavailable"}`.
  - `TestNoFallbackAfterFirstChunk`: remote sends one chunk then errors → turn error, `remote2` not called.
  - `TestNoFallbackOnAuth`: `ErrAuth` → no fallback.
  - `TestFallbackGuardSkips`: `remote2` refused by the allowlist → skipped, `remote3` used; none left → blocked.
  - `TestOutboundScan`: a marker appears in history through a path not scanned before (inject an assistant message containing the marker) → next send goes to local with `SessionClassified{source:"outbound"}`; without local → blocked, remote not called.
  - `TestFirstChunkTimeout`: fake remote that never sends → after the (test-overridden) timeout, fallback to `remote2`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/engine/ -run 'Sensitive|Fallback|Outbound|FirstChunk'` — FAIL.
- [ ] **Step 3: Implement.** `FirstChunkTimeout` overridable in tests through an unexported engine field.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(engine): route model calls by sensitivity with ordered fallback and outbound marker scan`

---

### Task 6: CLI wiring, TUI indicator, headless notices

**Files:**
- Modify: `cmd/tenon/run.go`, `internal/ui/tui/*.go` (status bar), `internal/ui/headless/headless.go`, `internal/i18n/catalog.go`, `internal/event/payloads.go` (`SessionStarted.Routing`)
- Create: `cmd/tenon/routing.go`, `cmd/tenon/routing_e2e_test.go`

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces:
  - `event.RoutingInfo struct { Local string \`json:"local,omitempty"\`; Fallback []string \`json:"fallback,omitempty"\` }`, `SessionStartedPayload.Routing *RoutingInfo \`json:"routing,omitempty"\``.
  - `cmd/tenon/routing.go`: `func buildRouter(cfg config.Config, current config.Provider, …) (*routing.Router, *event.RoutingInfo, error)` — builds providers for `routing.local` (local model detection as for `ollama`/`vllm`) and each fallback; the local provider bypasses env proxies as today's local providers.
  - Notices: policy routing notice; headless strings per Global Constraints; TUI status bar shows `sensible → <local>` (FR) / `sensitive → <local>` (EN) once the session is sensitive, and a transient notice on fallback.
  - `config check` lines per Global Constraints.

- [ ] **Step 1: Failing e2e tests** (fake LLM servers for local and remote):
  - `TestRoutingE2ESensitive`: `[routing] local` + `[classification] paths = ["rh/**"]`; the script reads `rh/paie.csv` → later requests hit the local server only; stderr has the classification notice; journal has `SessionStarted.routing`, `SessionClassified`, `ProviderSelected`.
  - `TestRoutingE2ENoLocal`: no `[routing]` → read refused, exit 0, remote never receives the file content (assert on request bodies).
  - `TestRoutingE2EFallback`: remote 1 returns 503 (beyond retries) → remote 2 answers; stderr `routing: <r1> failed (provider unavailable), using <r2>`.
  - `TestRoutingE2EExplicitLocal` (Review Focus 3): `--provider ollama` (fake local), no `[routing]` → classified read allowed, no refusal.
  - `TestRoutingConfigCheck`: both lines, and `routing: none`.
  - `TestRoutingTUIIndicator`: TUI model receives `SessionClassified` → status bar contains `sensible → ollama` (FR).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./cmd/tenon/ -run Routing ./internal/ui/...` — FAIL.
- [ ] **Step 3: Implement.** With no `[routing]` and a current provider that is itself local, the router's local candidate is the current provider.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): data-sensitivity routing at startup; TUI and headless notices`

---

### Task 7: compliance export and documentation

**Files:**
- Modify: `internal/compliance/*.go` (model, build, HTML template, labels), `docs/usage.md`, `docs/fr/utilisation.md`, `docs/security.md`, `docs/fr/securite.md`, `docs/policy.md`, `docs/fr/politique.md`, `README.md`
- Test: `internal/compliance/*_test.go`

**Interfaces:**
- Consumes: `SessionClassified`, `ProviderSelected`, `SessionStarted.routing`.
- Produces: per-session report field `Routing struct { Sensitive bool; Reason string; Providers []string; Fallbacks int; Violation bool }` (JSON `routing`: `sensitive`, `reason,omitempty`, `providers`, `fallbacks`, `violation`); HTML row « Routage » / « Routing »; any `violation` → export exit code 2.

- [ ] **Step 1: Failing tests:**
  - `TestExportRouting`: a session with `SessionClassified` then `ProviderSelected` local → `sensitive: true`, `violation: false`; providers listed in order of first use; fallbacks counted from `ProviderSelected{reason:"fallback"}`.
  - `TestExportRoutingViolation`: a forged log with `ProviderSelected` to an `eu` provider after `SessionClassified` → `violation: true`, export exit code 2 (through the existing exit-code path).
  - `TestExportRoutingAbsent`: older log → field omitted, no row.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/compliance/` — FAIL.
- [ ] **Step 3: Implement**, then docs EN/FR: what classification protects and what it does not (content outside listed paths without markers; markers only detected in text; shell commands that read classified data without naming the path, e.g. via a variable, are caught only by the output marker scan); `[classification]`, `[routing]`, built-in markers; behaviour without a local provider; fallback triggers; journal events; export row and code 2; policy keys.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(compliance): routing in the export; docs: data-sensitivity routing (EN/FR)`
