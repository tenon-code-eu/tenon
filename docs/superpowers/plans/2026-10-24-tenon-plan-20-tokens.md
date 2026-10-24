# tenon token sobriety — Implementation Plan (plan 20)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** measure real token usage on every model call, send less with a projected context view (deduplication, masking under pressure, head-and-tail truncation, stable prefix), enforce a session budget, and let compaction and sub-agents use a smaller model.

**Architecture:** the provider asks for `usage` and returns it on the last chunk. The engine builds a pure `view(history)` before each send, emits `Usage`, `ContextShaped` and `BudgetWarning` events, enforces the budget, and picks a dedicated router for compaction and sub-agents. Config and policy carry `[budget]`, `[usage]`, `routing.compaction`, `routing.subagents` and `stream_usage`. The CLI wires them, and the TUI, headless output and export show the totals.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), standard library only.

**Spec:** `docs/superpowers/specs/2026-10-24-tenon-tokens-design.md`

## Global Constraints

- No new module. The history is never modified by the view; audit, `--resume`, `/rewind`, routing and the outbound scan keep using the full history.
- Request field: `stream_options: {"include_usage": true}`; usage fields read: `prompt_tokens`, `completion_tokens`, `prompt_tokens_details.cached_tokens` (absent → 0). Retry once without the option on a 400 whose body mentions `stream_options`, remembered for the session. `stream_usage = false` on a provider disables it.
- Events (new types): `Usage {provider, model, purpose, input, output, cached, estimated}` with `purpose` ∈ `turn`, `compaction`, `subagent`; `ContextShaped {masked: [call_id], deduplicated: [call_id], saved_tokens}` emitted only when the sets change; `BudgetWarning {used, max}`.
- View thresholds: masking starts above 50 % of the window and stops below 40 %; the tool results of the last 3 assistant messages carrying tool calls (the last 3 iterations) and every message after the earliest of them, the compaction summary, user and assistant messages are never masked; compaction threshold unchanged (0.8).
- Exact texts: mask stub `[output hidden to save context: <tool> <arguments abridged to 60 chars>, <n> lines, sha256 <12 hex>; run the tool again if you need it]`; dedup stub `[same content as the read at turn <n>]`; truncation marker `[… <n> lines omitted …]` (multi-line) or `[… <n> characters omitted …]` (single line); split 70 % head / 30 % tail of the limit; never split a UTF-8 character.
- Budget: `[budget] session_tokens` (> 0), `warn_at` (0 < x < 1, default 0.8); counter = sum of `input + output` of the session's `Usage` events, rebuilt on `--resume`; warning once: `budget: <used> of <max> tokens used (<pct> %)`; at 100 % the current turn ends normally and the next turn ends with reason `budget_exceeded`; under `--strict` that reason exits 4. Project can only lower; policy can impose; effective = lowest.
- Errors: `config: budget.session_tokens must be positive`, `config: budget.warn_at must be between 0 and 1`, `config: usage.<key> must be positive`, `config: routing.<key>: unknown provider "<id>"`.
- TUI status: `entrée 12,4 k · sortie 1,1 k · session 340 k` (FR) / `in 12.4k · out 1.1k · session 340k` (EN). Headless text output, end of turn, stderr: `tokens: in <n> · out <n> · cached <n> · session <n>`; nothing extra in JSON output.
- Energy: `[usage] wh_per_mtoken`, `gco2_per_kwh` (global only, optional, positive); export line only when declared: `≈ <Wh> Wh · ≈ <g> g CO₂e (facteurs déclarés)` / `(declared factors)`.
- Small models: `[routing] compaction`, `[routing] subagents` (provider ids, default: the session provider); same guards (profile, allowlists, policy, non-EU consent); refused → session provider with a notice; a sensitive session always uses `routing.local`.
- Code comments in French; new user-visible strings via `internal/i18n` (EN/FR); `config:` errors in English; docs EN + FR.
- Every task: `gofmt -l cmd internal scripts` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOOS=linux GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint`.

## Review Focus

- A provider that sends `usage` in a chunk that also carries text or tool deltas, or sends it twice: counted once, from the last chunk that has it — test in Task 1.
- A tool result that is identical in content but from different arguments (two reads of different files with the same bytes): not deduplicated — test in Task 3.
- A sub-agent or compaction call while the session is over budget: compaction still runs (it reduces the context), a new sub-agent call is refused like a turn — test in Task 5.
- `--resume` of a session whose log has `Usage` events from before the budget was configured: counter rebuilt, warning not repeated if already emitted in that log — test in Task 5.
- Masking with a window so small that the last 3 turns alone exceed 40 %: stops masking (nothing else eligible), no loop, compaction takes over — test in Task 3.

---

### Task 1: provider usage

**Files:**
- Modify: `internal/llm/types.go`, `internal/provider/openai.go` (request, SSE parsing, retry), `internal/config/config.go` (`Provider.StreamUsage *bool` → passed to the provider)
- Test: `internal/provider/openai_test.go`

**Interfaces:**
- Produces: `type Usage struct { Input, Output, Cached int }` in `llm`; `llm.Chunk.Usage *llm.Usage` (set on the `Done` chunk when the stream carried usage); provider option to disable the request field (`OpenAICompat.StreamUsage bool`, default true); `config.Provider.StreamUsage *bool` (TOML `stream_usage`).

- [ ] **Step 1: Failing tests:** `TestStreamUsageRequested` (request body has `stream_options.include_usage = true`); `TestStreamUsageParsed` (`usage` with `cached_tokens` → `Done` chunk `Usage{10, 5, 3}`; without `prompt_tokens_details` → `Cached 0`); `TestStreamUsageLastWins` (Review Focus 1: usage on a text chunk and again on the final empty chunk → the last value, once); `TestStreamUsageRetry400` (first answer 400 with body containing `stream_options` → second request without the field, success; later requests in the same provider value omit it); `TestStreamUsageDisabled` (`StreamUsage false` → field absent); `TestStreamNoUsage` (no `usage` → `Done` chunk `Usage == nil`).
- [ ] **Step 2: Run** `go test ./internal/provider/` — FAIL.
- [ ] **Step 3: Implement**; config field documented in `docs/config.example.toml` (commented).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(provider): read real token usage from the stream`

---

### Task 2: head-and-tail truncation

**Files:**
- Modify: `internal/tools/truncate.go`
- Test: `internal/tools/truncate_test.go` (adjust existing callers' expectations where they assert the old suffix)

**Interfaces:**
- Produces: `Truncate(s string, limit int) string` unchanged signature, new behaviour per Global Constraints.

- [ ] **Step 1: Failing tests:** `TestTruncateHeadTail` (200 lines, limit fitting ~20 → begins with line 1, ends with line 200, marker `[… <n> lines omitted …]` with the exact count); `TestTruncateSingleLine` (one long line → `[… <n> characters omitted …]`); `TestTruncateUTF8` (multi-byte runes at both cut points → valid UTF-8); `TestTruncateShort` (≤ limit → unchanged); limit ≤ 0 → unchanged.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** (count in runes like today). **Step 4: Run** — PASS; full gates (fix tests elsewhere that asserted `[… output truncated to`).
- [ ] **Step 5: Commit** `feat(tools): keep the head and the tail of long outputs`

---

### Task 3: projected context view

**Files:**
- Create: `internal/engine/view.go`
- Modify: `internal/engine/loop.go` (`send` uses the view), `internal/event/payloads.go` (`ContextShaped` type and payload)
- Test: `internal/engine/view_test.go`

**Interfaces:**
- Consumes: `estimate(msgs, defs)` (existing), the window (`t.window()`).
- Produces: `func view(history []llm.Message, defs []llm.ToolDef, window int) (out []llm.Message, shaped Shaped)`; `type Shaped struct { Masked, Deduplicated []string; SavedTokens int }`; `event.ContextShaped` with `ContextShapedPayload{Masked, Deduplicated []string; SavedTokens int}` (JSON `masked`, `deduplicated`, `saved_tokens`). The turn keeps the last emitted `Shaped` and emits only on change.

- [ ] **Step 1: Failing tests:** `TestViewDedupKeepsLatest` (two identical `read_file` results → older becomes the dedup stub with the later turn number); `TestViewDedupNeedsSameArgs` (Review Focus 2); `TestViewMaskOnlyAbove50` (below 50 % → unchanged); `TestViewMaskOldestUntil40` (oldest first, stops under 40 %, stub text exact with sha256 12 hex and line count); `TestViewSparesRecentAndSummary` (last 3 tool iterations and the summary message never masked; user and assistant text never touched); `TestViewSingleTurnIterations` (one user turn, 10 iterations over 50 % → oldest results masked, last 3 iterations kept); `TestViewSubagentHistory` (sub-agent history with one user message is masked too); `TestViewNothingEligible` (Review Focus 5: terminates, returns as much as possible); `TestViewPure` (input slice and messages unchanged; same input → same output); engine `TestContextShapedOnChange` (event emitted when the masked set changes, not on every send).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** Compaction keeps working on the history; the view is applied to every request (turn, compaction request, sub-agent requests on their own history).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(engine): projected context view with deduplication and masking under pressure`

---

### Task 4: configuration and policy

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`, `internal/config/policy.go`, `internal/orgpolicy/*.go`, `docs/config.example.toml`
- Test: `internal/config/*_test.go`, `internal/orgpolicy/*_test.go`

**Interfaces:**
- Produces: `config.Budget{SessionTokens int64 \`toml:"session_tokens"\`; WarnAt float64 \`toml:"warn_at"\`}` (`WarnAt` defaults to 0.8 when a budget is set); `config.Usage{WhPerMTok, GCO2PerKWh float64}` (TOML `wh_per_mtoken`, `gco2_per_kwh`, global only); `config.Routing.Compaction`, `config.Routing.Subagents string`; `orgpolicy.Policy.Budget.SessionTokens int64`. Effective `cfg.Budget.SessionTokens` = lowest of global, project, policy (0 = none).

- [ ] **Step 1: Failing tests:** invalid values → exact errors (Global Constraints); project raising the budget → ignored (lowest wins), project lowering → applied; `[usage]` in project config → `config: usage not allowed in project configuration`; policy budget imposed over a higher global one; `routing.compaction = "nope"` → `config: routing.compaction: unknown provider "nope"`; `TestConfigExampleLoads` still passes with commented blocks.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(config): [budget], [usage], routing.compaction and routing.subagents; policy budget`

---

### Task 5: usage events, budget, small-model routing, stable prefix

**Files:**
- Modify: `internal/engine/engine.go` (Deps), `internal/engine/loop.go` (`send`/`try` emit `Usage`; budget gate before a turn and before a sub-agent call), `internal/engine/compact.go`, `internal/engine/agent.go`, `internal/engine/restore.go` (rebuild the counter), `internal/event/payloads.go`
- Test: `internal/engine/*_test.go`

**Interfaces:**
- Consumes: `llm.Chunk.Usage` (Task 1), `view` (Task 3).
- Produces: `engine.Deps.Budget struct{ SessionTokens int64; WarnAt float64 }`; `engine.Deps.CompactionRouter, SubagentRouter *routing.Router` (nil → session router; a sensitive session always uses `Router.Local()`); events `Usage` (`UsagePayload{Provider, Model, Purpose string; Input, Output, Cached int; Estimated bool}`) and `BudgetWarning` (`BudgetWarningPayload{Used, Max int64}`); turn end reason `budget_exceeded`. Estimated usage when the chunk has none: `estimate` of the request for input, of the reply for output, `Estimated: true`.
- Stable prefix: tool definitions sorted by name before every request; system prompt built once per session (no time or per-turn value).

- [ ] **Step 1: Failing tests:** `TestUsagePerCall` (turn, compaction and sub-agent calls each emit `Usage` with the right `purpose`; real when the fake provider sends usage, `estimated` otherwise); `TestBudgetWarnOnce` (crossing 80 % → one `BudgetWarning`); `TestBudgetRefusesNextTurn` (crossing 100 % mid-turn → that turn completes; next `Submit` ends with `budget_exceeded` without contacting the provider); `TestBudgetSubagentAndCompaction` (Review Focus 3: over budget, compaction still runs, a new `agent` tool call is refused with a tool error naming the budget); `TestBudgetResume` (Review Focus 4: counter rebuilt from logged `Usage`; no second warning if the log already has one); `TestCompactionRouter` (compaction request goes to `CompactionRouter`; sensitive session → local); `TestSubagentRouter` (same for sub-agents); `TestStablePrefix` (two consecutive requests: identical system prompt and tool definitions byte for byte, tools sorted).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(engine): usage events, session budget, dedicated routers for compaction and sub-agents, stable prefix`

---

### Task 6: CLI, TUI and headless

**Files:**
- Modify: `cmd/tenon/run.go` (build `Deps.Budget`, `CompactionRouter`, `SubagentRouter` with the same guards as the session router; notice on refusal), `internal/ui/headless/headless.go` (stderr `tokens:` line in text output; `budget_exceeded` among strict reasons), `internal/ui/tui/*` (status line, budget notice), `internal/i18n/catalog.go`
- Test: `cmd/tenon/*_e2e_test.go` (new `tokens_e2e_test.go`), `internal/ui/headless/headless_test.go`, `internal/ui/tui/*_test.go`

**Interfaces:**
- Consumes: Tasks 4–5.

- [ ] **Step 1: Failing tests:** e2e `TestTokensLineText` (fake provider with usage → stderr `tokens: in … · out … · cached … · session …`; JSON output unchanged); e2e `TestBudgetStrictExit4` (`[budget] session_tokens` small + `--strict` → exit 4, reason `budget_exceeded`); e2e `TestCompactionProviderRefused` (`routing.compaction` naming a non-EU provider without consent → notice, compaction on the session provider); headless strict reason list includes `budget_exceeded`; TUI status line text EN/FR after a `Usage` event.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): token totals, session budget and small-model routing at startup; TUI status line`

---

### Task 7: compliance export and documentation

**Files:**
- Modify: `internal/compliance/*` (tokens section), `docs/usage.md`, `docs/fr/utilisation.md`, `docs/security.md`, `docs/fr/securite.md` (what masking does and does not change), `docs/policy.md`, `docs/fr/politique.md` (budget), `README.md`
- Test: `internal/compliance/*_test.go`

**Interfaces:**
- Produces: report field `Tokens` per session and per provider: `{input, output, cached, estimated_share, saved}`; energy line only when both factors are declared (Wh from `(input+output)/1e6 × wh_per_mtoken`, g from `Wh/1000 × gco2_per_kwh`).

- [ ] **Step 1: Failing tests:** `TestExportTokens` (sessions with `Usage` and `ContextShaped` → totals, cached, estimated share, saved); `TestExportEnergyOnlyDeclared` (no factors → no energy line; both → line with the exact computed values); older logs without `Usage` → section omitted.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement**, then docs (EN/FR): measurement, view (dedup, masking, truncation), budget, small models, energy factors (declared, never defaulted), what stays unchanged (audit, resume, rewind, routing).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(compliance): tokens in the export; docs: token sobriety (EN/FR)`
