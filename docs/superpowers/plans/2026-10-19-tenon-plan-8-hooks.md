# tenon hooks — Implementation Plan (plan 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Claude Code compatible hooks in tenon: user-level `hooks.json` (and optionally `~/.claude/settings.json`), 9 lifecycle events, `command`/`prompt`/`agent` hook types, run synchronously by the engine, audited, never able to widen permissions beyond a global `allow` rule.

**Architecture:** A new `internal/hooks` package loads and validates hook files, translates tool names and arguments between tenon and Claude Code, runs matching hooks in parallel and combines their outcomes; it knows neither the engine nor the LLM (an `Evaluator` interface runs `prompt`/`agent` hooks). The engine calls it at fixed points (`run`, `decideAndRun`, `runTool`, end of `loop`, `compact`, `ask`, `EndSession`), emits `HookStarted`/`HookCompleted`, and implements the `Evaluator`. `internal/policy` reports whether an `Ask` may be lifted by a hook. `cmd/tenon`, headless and the TUI consume the result.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), `regexp` (RE2), `encoding/json`, `os/exec`, Bubble Tea TUI, no new dependency.

**Spec:** `docs/superpowers/specs/2026-10-19-tenon-hooks-design.md`

## Global Constraints

- No new dependency.
- Hook sources (user only): `$XDG_CONFIG_HOME/tenon/hooks.json` (else `$HOME/.config/tenon/hooks.json`), source `tenon`, always; `$HOME/.claude/settings.json`, source `claude`, only with global `[hooks] claude_settings = true`. `[hooks] disabled = true` loads nothing. A relative or empty `HOME`/`XDG_CONFIG_HOME` drops that base (same rule as `skills.Dirs`). `"disableAllHooks": true` in a file empties that source.
- Project files `.tenon/hooks.json`, `.claude/settings.json`, `.claude/settings.local.json` are never run; notice `project hooks ignored: <path>` when `.tenon/hooks.json` exists or a Claude file has a `hooks` key. A broken project file is only a notice.
- Events: `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`, `SessionStart`, `SessionEnd`, `PreCompact`, `Notification`. Types: `command`, `prompt`, `agent`.
- Default timeouts: `command` 60 s, `prompt` 30 s, `agent` 60 s; `SessionEnd` capped at 5 s.
- Fail closed at load: invalid JSON, `hooks` not an object, invalid regex, empty `command`/`prompt`, `timeout` not a positive integer, file > 1 MiB (1 048 576 bytes), > 200 hooks → error naming the file and JSON path (e.g. `hooks.json: hooks.PreToolUse[0].hooks[1]: timeout must be a positive integer`). Unknown event or type → notice, that group/hook skipped.
- Matcher: RE2 anchored `^(?:m)$`; empty, absent or `*` matches all; tool events test the Claude name and the tenon name.
- Tool name map: `shell`↔`Bash`, `read_file`↔`Read`, `write_file`↔`Write`, `edit_file`↔`Edit`, `grep`↔`Grep`, `glob`↔`Glob`, `skill`↔`Skill`, `agent`↔`Task`; `mcp__*` unchanged.
- Argument map: `path`↔`file_path` (read/write/edit), `timeout_seconds`↔`timeout` (ms; s→ms ×1000, ms→s rounded up), skill `name`↔`skill`, agent `agent`↔`subagent_type`; other fields copied unchanged both ways.
- Command hooks: `bash -c`, cwd = project root, own process group killed at end/timeout/cancel, env = tenon's + `CLAUDE_PROJECT_DIR` + `TENON_PROJECT_DIR`, stdout/stderr capped at 65 536 bytes each.
- Combination: `deny`/`block` > `ask` > `allow`; reasons joined by `\n` in config order; contexts joined by `\n\n` in config order; two different `updatedInput` → `conflicting updatedInput from hooks`.
- Hook `allow` lifts only an `Ask` coming from a global `ask` rule or the risk default; never `Deny`, `ForceAsk`, outside/sensitive paths, or a project `ask` rule.
- Audit truncation: `command`/`prompt` in `HookStarted` and `stdout`/`stderr` in `HookCompleted` cut to 4096 bytes (whole runes).
- No recursion: requests of `prompt`/`agent` hooks and tool calls of an `agent` hook fire no hook.
- Events without the new fields serialize exactly as before (all new fields `omitempty`).
- New user-visible strings go through `internal/i18n` (EN and FR). Messages outside the catalog are English. Code comments in French, like the rest of the code.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...` and `GOFLAGS=-mod=vendor go test -race ./...` pass; `make lint` (staticcheck) clean.

## Review Focus

- A hook that never reads stdin and exits at once while tenon writes a large input: the broken pipe must not turn a success into an error — test in Task 4.
- A hook that leaves a background process holding stdout (`sleep 30 &`): the hook ends when the shell exits (`WaitDelay`), not at the timeout — test in Task 4.
- A missing or non-executable script (`exit 127`): non-blocking error, the tool still runs — test in Task 4 and Task 6.
- A `PreToolUse` `updatedInput` that moves a `write_file` path outside the project: the permission check runs on the new input and asks — test in Task 6.
- A `Stop` hook that always blocks: the turn ends at `max_iterations` with `stop_hook_active: true` from the second call — test in Task 7.

---

### Task 1: Config `[hooks]` and liftable `Ask`

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`, tests in `internal/config/`
- Modify: `internal/policy/permissions.go`, `internal/policy/permissions_test.go`

**Interfaces:**
- Produces:
  - `type Hooks struct { ClaudeSettings bool `toml:"claude_settings"`; Disabled bool `toml:"disabled"` }`, field `Config.Hooks Hooks `toml:"hooks"``, copied from the global file in `Load`. Not in `projectFile`: a project `[hooks]` is refused by the existing `key not allowed` check.
  - `func (p *Permissions) Decide(c Call) (d Decision, liftable bool)`; `Check(c)` returns `Decide(c)`'s decision. `liftable` is true only when `d == Ask` and the `Ask` came from a non-project `ask` rule or from the risk default.

- [ ] **Step 1: Failing tests** — `TestHooksGlobalKey`: global `[hooks]\nclaude_settings = true\ndisabled = true` → both true. `TestHooksProjectKeyRefused`: project `[hooks]\ndisabled = true` → error containing `key not allowed`. `TestDecideLiftable` (table): default write → `(Ask, true)`; global `ask` rule → `(Ask, true)`; project `ask` rule → `(Ask, false)`; `ForceAsk` → `(Ask, false)`; path outside root → `(Ask, false)`; `.env` → `(Ask, false)`; `deny` rule → `(Deny, false)`; read in project → `(Allow, false)`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/config/ ./internal/policy/` — FAIL.
- [ ] **Step 3: Implement** as above; `Check` keeps its doc comment and becomes a one-line wrapper.
- [ ] **Step 4: Run** the same — PASS; `TestConfigExampleLoads` unchanged.
- [ ] **Step 5: Commit** `feat(config,policy): [hooks] section and liftable ask`.

### Task 2: Hook loading and matching

**Files:**
- Create: `internal/hooks/load.go`, `internal/hooks/load_test.go`

**Interfaces:**
- Consumes: `config.Hooks` (Task 1), `safefile.ReadRegular`.
- Produces:
  - `type Event string` with the 9 constants (`PreToolUse` … `Notification`, values = names); `type Type string`: `Command = "command"`, `Prompt = "prompt"`, `Agent = "agent"`; `type Source string`: `SourceTenon = "tenon"`, `SourceClaude = "claude"`; `type Notice string`.
  - `type Hook struct { ID string; Source Source; Event Event; Matcher string; Type Type; Command, Prompt, Model string; Timeout time.Duration }` (unexported compiled regex). `ID` = `<source>:<event>:<i>.<j>`.
  - `type Options struct { Getenv func(string) string; ProjectRoot string; Config config.Hooks }`.
  - `func Load(o Options) (*Set, []Notice, error)`.
  - `func (s *Set) Match(ev Event, subjects ...string) []Hook` — nil-safe; config order (tenon source first); no subject → matcher ignored; two `command` hooks with the same `Command` → first kept.
  - `func (s *Set) List() []Hook`, `func (s *Set) Count() (total, claude int)`.
  - `const MaxHooks = 200`, `MaxFileBytes = 1 << 20`.

- [ ] **Step 1: Failing tests** (helper writes files under a temp `HOME`):
  - `TestLoadTenonFile`: hooks.json with one `PreToolUse` group `matcher: "Bash"` and a `command` hook without timeout → one hook, `ID == "tenon:PreToolUse:0.0"`, `Timeout == 60*time.Second`.
  - `TestLoadClaudeOptIn`: `~/.claude/settings.json` `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"a"}]}]}}` → ignored when `ClaudeSettings` false; loaded with `Source == SourceClaude` when true; `Count() == (1, 1)`.
  - `TestLoadDisabled` (`Config.Disabled`) and `TestLoadDisableAllHooks` (`"disableAllHooks": true` empties only that source).
  - `TestLoadProjectIgnored`: project `.claude/settings.json` with `hooks` and `.tenon/hooks.json` → 0 hooks, 2 notices containing `project hooks ignored`; a project `.claude/settings.json` without `hooks` → no notice; a broken project file → notice, no error.
  - `TestLoadFailClosed` (table, each → error containing the file name and the path shown): invalid JSON; `"hooks": []`; matcher `"("`; empty command; `"timeout": 0`; `"timeout": 1.5`; file of `MaxFileBytes+1` bytes; 201 hooks.
  - `TestLoadUnknown`: event `PostToolUseFailure` and type `http` → notices, other hooks kept.
  - `TestLoadRelativeHome`: `HOME=rel` and `XDG_CONFIG_HOME=""` → no user file read, no error.
  - `TestMatch`: `Edit|Write` matches subjects `("Edit","edit_file")` and `("Write","write_file")`, not `("MultiEdit","x")` (anchored); matcher `edit_file` matches `("Edit","edit_file")`; `*` and `""` match anything; `SessionStart` matcher `resume` matches subject `resume` only; `Stop` with a matcher and no subject → matches; duplicated command → one result.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/hooks/` — FAIL.
- [ ] **Step 3: Implement** — decode with `json.Decoder` into `map[string]json.RawMessage` (top level) then `map[string][]group`; validate `timeout` from `json.Number` (`UseNumber`); read with `safefile.ReadRegular(path, MaxFileBytes+1)` (truncated → too large); a user file that is a symlink is followed (resolve with `filepath.EvalSymlinks` before reading). Base-dir rule: reuse the `HOME`/`XDG_CONFIG_HOME` logic of `skills.Dirs`.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(hooks): load and match Claude Code compatible hook files`.

### Task 3: Tool name and argument translation

**Files:**
- Create: `internal/hooks/translate.go`, `internal/hooks/translate_test.go`

**Interfaces:**
- Produces:
  - `func ClaudeName(tenon string) string` (unknown → unchanged).
  - `func ToClaude(tenon string, args json.RawMessage) json.RawMessage`.
  - `func FromClaude(tenon string, in json.RawMessage) (json.RawMessage, error)` — `in` must be a JSON object, else error `updatedInput must be an object`.

- [ ] **Step 1: Failing tests** — `TestToClaude` (table, one row per mapped tool: `read_file {"path":"a","offset":2,"limit":3}` → `{"file_path":"a","offset":2,"limit":3}`; `shell {"command":"ls","timeout_seconds":2}` → `{"command":"ls","timeout":2000}`; `skill {"name":"x","file":"f"}` → `{"skill":"x","file":"f"}`; `agent {"agent":"r","description":"d","prompt":"p"}` → `{"subagent_type":"r","description":"d","prompt":"p"}`; `mcp__s__t {"q":1}` unchanged; invalid JSON args unchanged). `TestFromClaude`: inverse of each row; `Bash {"timeout":1500}` → `timeout_seconds: 2`; unknown field `"x":1` kept; `"[]"` → error. `TestRoundTrip` on every row.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** with `map[string]json.RawMessage` renames. **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(hooks): translate tool names and arguments to and from Claude Code`.

### Task 4: Running hooks

**Files:**
- Create: `internal/hooks/run.go`, `internal/hooks/run_test.go`

**Interfaces:**
- Consumes: `Hook`, `Event` (Task 2).
- Produces:
  - `type Env struct { SessionID, TranscriptPath, ProjectRoot string }`.
  - `type Evaluator interface { Evaluate(ctx context.Context, h Hook, prompt string) (text string, err error) }` (only for `prompt`/`agent`).
  - `type Status string`: `StatusOK = "ok"`, `StatusBlocked = "blocked"`, `StatusError = "error"`, `StatusTimeout = "timeout"`, `StatusCancelled = "cancelled"`.
  - `type Result struct { Hook Hook; Status Status; ExitCode int /* -1 if not command */; Decision string /* "", "allow", "ask", "deny", "block" */; Reason, Context, SystemMessage, StopReason, Stdout, Stderr, Err string; UpdatedInput json.RawMessage; Stop bool; Duration time.Duration }`.
  - `type Outcome struct { Results []Result; Block, Ask, Allow bool; Reason, Context, StopReason string; UpdatedInput json.RawMessage; Conflict, Stop bool }` — after combination at most one of `Block`/`Ask`/`Allow` is true.
  - `func Run(ctx context.Context, hs []Hook, ev Event, fields map[string]any, env Env, eval Evaluator) Outcome` — input = `fields` + `session_id`, `transcript_path`, `cwd`, `hook_event_name`; hooks run in parallel; `Results` in `hs` order.
  - `func BuildPrompt(tmpl string, input []byte) string` (`$ARGUMENTS` replaced; absent → appended after a blank line).
  - `func ParseVerdict(text string) (ok bool, reason string, err error)` — takes the text from the first `{` to the last `}`; requires boolean `ok`.

Interpretation (per hook, then combined):

| Outcome | PreToolUse | PostToolUse, UserPromptSubmit, Stop, SubagentStop | SessionStart, SessionEnd, PreCompact, Notification |
|---|---|---|---|
| exit 0, stdout JSON | `hookSpecificOutput.permissionDecision` (+`Reason`), legacy `decision: approve/block` → `allow/deny`; `hookSpecificOutput.updatedInput` | `decision: "block"` + `reason` → block; `hookSpecificOutput.additionalContext` → context (not Stop/SubagentStop) | `additionalContext` → context (SessionStart only) |
| exit 0, text stdout | none | context for UserPromptSubmit | context for SessionStart |
| exit 2 | deny, reason = stderr | block, reason = stderr | status `error`, `Err` = stderr |
| other exit, start failure, bad JSON | status `error` | status `error` | status `error` |
| timeout / ctx cancelled | `timeout` / `cancelled` | same | same |
| `prompt`/`agent`: `ok:false` | deny | block | no effect |

Common JSON fields on any event: `continue:false` → `Stop`, `StopReason`; `systemMessage`; `hookSpecificOutput` ignored unless `hookEventName == ev`. A deny/block sets `Status = StatusBlocked`.

- [ ] **Step 1: Failing tests** with real `sh` scripts in a temp dir (`exec` hooks via `Command: "sh <script>"`), a fake `Evaluator`:
  - `TestRunExit0JSONPreToolUse`: script prints `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"why"}}` → `Ask && Reason == "why"`.
  - `TestRunExit2`: `echo no >&2; exit 2` → PreToolUse `Block`, `Reason == "no"`; SessionStart → `Status == StatusError`, no block.
  - `TestRunPlainStdoutContext`: `echo ctx` for UserPromptSubmit → `Context == "ctx"`; for PreToolUse → no context, `Stdout == "ctx\n"`.
  - `TestRunInput`: script copies stdin to a file; `fields{"tool_name":"Bash"}` → file JSON has `tool_name`, `session_id`, `transcript_path`, `cwd`, `hook_event_name == "PreToolUse"`; env has `CLAUDE_PROJECT_DIR` and `TENON_PROJECT_DIR` = root; cwd = root.
  - `TestRunNonBlockingErrors`: `exit 1`, missing script (`exit 127`), stdout `{bad` → `Status == StatusError`, no block.
  - `TestRunTimeout`: `sleep 5`, `Timeout: 200ms` → `StatusTimeout` in < 2 s. `TestRunBackgroundChild`: `sleep 30 & echo ok` → returns in < 3 s with `StatusOK`. `TestRunCancel`: ctx cancelled → `StatusCancelled`.
  - `TestRunIgnoresStdin`: 1 MiB `fields` value, script `exit 0` without reading → `StatusOK`.
  - `TestRunCaps`: 200 000 bytes of stdout → `len(Stdout) == 65536`.
  - `TestRunParallel`: three `sleep 1` hooks finish in < 2.5 s.
  - `TestCombine`: deny + ask + allow → `Block`, reasons joined in order; ask + allow → `Ask`; two equal `updatedInput` → kept; two different → `Conflict`; `continue:false` from one → `Stop`.
  - `TestPromptHook`: fake evaluator returns ```` ```json\n{"ok":false,"reason":"r"}\n``` ```` → PreToolUse `Block`, `Reason == "r"`; returns `nonsense` → `StatusError`; `BuildPrompt("x $ARGUMENTS y", in)` and `BuildPrompt("x", in) == "x\n\n" + in`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — command runner mirrors `tools.Shell` (`exec.CommandContext(tctx, "bash", "-c", cmd)`, `Setpgid`, `Cancel` kills the group, `WaitDelay = time.Second`, `exec.ErrWaitDelay` = success); stdin from a `bytes.Reader` (an `EPIPE` on stdin is not an error); a capping writer for stdout/stderr; one goroutine per hook, `sync.WaitGroup`.
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test -race ./internal/hooks/` — PASS.
- [ ] **Step 5: Commit** `feat(hooks): run command, prompt and agent hooks and combine outcomes`.

### Task 5: Hook events

**Files:**
- Modify: `internal/event/event.go`, `internal/event/payloads.go`, `internal/event/event_test.go`

**Interfaces:**
- Produces:
  - Types `HookStarted`, `HookCompleted`.
  - `HookStartedPayload{ RunID `json:"run_id"`; HookID `json:"hook_id"`; Event `json:"event"`; Type `json:"type"`; Command `json:"command,omitempty"`; Prompt `json:"prompt,omitempty"`; CallID `json:"call_id,omitempty"`; ParentCallID `json:"parent_call_id,omitempty"` }`.
  - `HookCompletedPayload{ RunID, HookID, Event; Status `json:"status"`; ExitCode *int `json:"exit_code,omitempty"`; Decision, Reason `omitempty`; UpdatedInput json.RawMessage `json:"updated_input,omitempty"`; SystemMessage `json:"system_message,omitempty"`; Stop bool `json:"stop,omitempty"`; StopReason `json:"stop_reason,omitempty"`; Error `json:"error,omitempty"`; DurationMS int64 `json:"duration_ms"`; Stdout, Stderr `omitempty`; CallID, ParentCallID `omitempty` }`.
  - New fields: `UserMessagePayload.HookContext `json:"hook_context,omitempty"``, `.Blocked bool `json:"blocked,omitempty"``, `.Hook string `json:"hook,omitempty"`` (`"Stop"` / `"SubagentStop"` for feedback messages); `ContextCompactedPayload.HookContext `json:"hook_context,omitempty"``; `PermissionRequestedPayload.HookReason `json:"hook_reason,omitempty"``.
  - `RunID` = the `ID` of the `HookStarted` event (`<session>-<seq>`); `WithParent` covers both new payloads. `run_id` is an addition to spec §7.1: it pairs a `HookCompleted` with its `HookStarted` (a hook may run several times per tool call) and is the `parent_call_id` of an `agent` hook's child events.

- [ ] **Step 1: Failing tests** — JSON of each new payload; a `UserMessagePayload{Text:"x"}` serializes exactly as before (`{"text":"x"}`); `WithParent` sets `parent_call_id` on both hook payloads.
- [ ] **Step 2–4:** run (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(event): hook events and hook fields`.

### Task 6: Engine — hook runner, PreToolUse, PostToolUse

**Files:**
- Create: `internal/engine/hooks.go`, `internal/engine/hooks_test.go`
- Modify: `internal/engine/engine.go` (`Deps`), `internal/engine/loop.go` (`turn`, `decideAndRun`, `runTool`), `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
  - `Deps.Hooks *hooks.Set` (nil: no hooks), `Deps.TranscriptPath string`, `Deps.Notify bool` (true in the TUI: enables `Notification`).
  - `turn.noHooks bool` (true for an `agent` hook's child turn).
  - `func (t *turn) runHooks(ev hooks.Event, callID string, fields map[string]any, subjects ...string) (hooks.Outcome, error)` — no-op when `t.noHooks` or nothing matches; emits `HookStarted` per hook (in order, before running), runs `hooks.Run` with `hookEval{t, runIDs}`, emits `HookCompleted` per result (`parent_call_id` via the turn's parent); returns `t.ctx.Err()` if cancelled.
  - `hookEval.Evaluate`: `prompt` → `t.outbound` then `t.stream(…, false)` on a copy of the turn whose `ctx` is the hook ctx, messages `[system: i18n "hooks.prompt_system", user: prompt]`, no tools, not added to history; `agent` → child turn like `runSubagent` with `tools: d.Tools.Only([]string{"read_file","grep","glob"})`, `parent: <run id>`, `noHooks: true`, history `[system: base prompt + "\n\n" + i18n "hooks.agent_preamble", user: prompt]`, returns `res.Final`.
  - `decideAndRun` returns `(res tools.Result, denied string, ran bool, err error)`; new denial value `deniedHook = "hook"`.
  - i18n keys (EN / FR): `hooks.prompt_system`: `"You evaluate a hook for a coding agent. Reply with one JSON object only: {\"ok\": true} to let it proceed, or {\"ok\": false, \"reason\": \"...\"} to block it."` / `"Vous évaluez un hook pour un agent de code. Répondez par un seul objet JSON : {\"ok\": true} pour laisser faire, ou {\"ok\": false, \"reason\": \"...\"} pour bloquer."`; `hooks.agent_preamble`: `"You verify a condition for a coding agent with read-only tools. End with one JSON object only: {\"ok\": true} or {\"ok\": false, \"reason\": \"...\"}."` / `"Vous vérifiez une condition pour un agent de code avec des outils en lecture seule. Terminez par un seul objet JSON : {\"ok\": true} ou {\"ok\": false, \"reason\": \"...\"}."`.

PreToolUse in `decideAndRun`, after `Target` and the marker guards, before permissions:

```
out := runHooks(PreToolUse, c.ID, {tool_name, tool_input: ToClaude, tenon_tool, tenon_input, tool_use_id}, ClaudeName(c.Name), c.Name)
out.Stop        → result "stopped by hook: <StopReason>", denied hook, turn ends (t.stopReason)
out.Conflict    → "conflicting updatedInput from hooks", denied hook
out.Block       → "blocked by hook: <Reason>", denied hook
out.UpdatedInput → args = FromClaude; re-run Target and marker guards on args
                   (error → "invalid updatedInput from hook: <err>", denied hook)
dec, liftable := Permissions.Decide(call on args)
Deny → policy denial
Ask  → out.Allow && liftable && !out.Ask → run; else prompt
Allow → out.Ask → prompt
prompt: out.Ask → HookReason = out.Reason, remembered ignored, Remember not stored
run with args
```

PostToolUse in `runTool`, only when `ran`: `fields` = PreToolUse fields + `tool_response: {content, is_error}`; `Block` → append `"\n\nhook feedback: " + Reason`; `Context` → append `"\n\n" + Context`; before emitting `ToolResult`. `Stop` → turn ends after this tool.

`t.stopReason string`: set by any hook `Stop`; `loop` checks it after each tool and returns `res(iter, "stopped_by_hook")` (main turn: `TurnCompleted` reason `stopped_by_hook`; sub-agent: `sub-agent stopped: stopped_by_hook`).

- [ ] **Step 1: Failing tests** (fake hooks via real tiny scripts in a temp `hooks.json`, loaded with `hooks.Load`):
  - `TestPreToolUseDeny`: hook exit 2 on `Bash` → `ToolResult.denied == "hook"`, content `blocked by hook: …`, tool not run (no file created); events order `ToolCallRequested, HookStarted, HookCompleted, ToolResult`.
  - `TestPreToolUseAllowLiftsDefaultAsk`: `write_file` inside project + hook `allow` → no `PermissionRequested`, file written.
  - `TestPreToolUseAllowNeverLifts` (table): `deny` rule, MCP tool with `ForceAsk`, path outside project, `.env`, project `ask` rule → denied or `PermissionRequested`, never silent run.
  - `TestPreToolUseAskForces`: read inside project + hook `ask` → `PermissionRequested` with `hook_reason`; after a "yes for the session" reply, a second identical call asks again.
  - `TestPreToolUseUpdatedInput`: hook rewrites `Write.file_path` to `out.txt` → `out.txt` written, `HookCompleted.updated_input` set, `ToolCallRequested.args` unchanged; rewrite to a path outside the project → `PermissionRequested` (Review Focus); rewrite to `[]` → `invalid updatedInput from hook`.
  - `TestHookErrorDoesNotBlock`: missing script → `HookCompleted.status == "error"`, tool runs.
  - `TestPostToolUseFeedback`: block with reason `fix it` → `ToolResult.content` ends with `hook feedback: fix it`; not called when the PreToolUse hook denied.
  - `TestContinueFalseStopsTurn`: PostToolUse `{"continue":false,"stopReason":"enough"}` → `TurnCompleted.reason == "stopped_by_hook"`, `HookCompleted.stop_reason == "enough"`.
  - `TestPromptHookUsesOutbound`: `prompt` hook with a fake provider that records the request; a secret from the redactor in the tool input is masked in the provider request; reply `{"ok":false,"reason":"r"}` → denied.
  - `TestAgentHookNoRecursion`: `agent` hook whose fake provider asks `shell` then answers `{"ok":true}` → `unknown tool: shell` for the child, no `HookStarted` for the child's calls, `parent_call_id` of the child events = the run id.
  - `TestHooksInSubagent`: a `PreToolUse` hook fires for a sub-agent's call; its events carry `parent_call_id`.
  - `TestHookAuditFailure`: sink fails on `HookStarted` → `ErrAuditFailure`, tool not run.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/engine/` — FAIL.
- [ ] **Step 3: Implement** as above.
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test -race ./internal/engine/` — PASS, existing tests unchanged.
- [ ] **Step 5: Commit** `feat(engine): PreToolUse and PostToolUse hooks`.

### Task 7: Engine — lifecycle hooks and resume

**Files:**
- Modify: `internal/engine/loop.go`, `internal/engine/compact.go`, `internal/engine/engine.go`, `internal/engine/restore.go`, tests `internal/engine/hooks_test.go`, `internal/engine/restore_test.go`

**Interfaces:**
- Consumes: Task 6.
- Produces:
  - `func withHookContext(text, ctx string) string` — `text` if `ctx == ""`, else `text + "\n\n[hook context]\n" + ctx`; used by `run`, `compact` and `Restore`.
  - `func (e *Engine) EndSession(ctx context.Context, sessionID, reason string) error` — no-op if the session never started; runs `SessionEnd` (`reason`), each hook's timeout capped at 5 s.
  - `s.stopActive` not needed: `stop_hook_active` is a local of `loop`.

Points:
- `run`: after `SessionStarted` on the first turn, `SessionStart` (`source` = `"resume"` if `t.s.resumed` else `"startup"`, subject = source); then `UserPromptSubmit` (`prompt` = `in.Typed` if set, else `in.Text`). Block → emit `UserMessage{…, Blocked: true}`, no history change, `TurnCompleted{0, "blocked_by_hook"}`. Otherwise `UserMessage.HookContext` = SessionStart context + `\n\n` + UserPromptSubmit context (non-empty parts), history content `withHookContext(in.Text, hc)`. `Stop` from either → `TurnCompleted{0, "stopped_by_hook"}`.
- `loop`, when `len(calls) == 0`: if `!t.noHooks`, run `Stop` (`t.parent == ""`) or `SubagentStop` (with `agent`, `tool_use_id` = parent call id) with `stop_hook_active`. Block → append user message `"Stop hook feedback: " + Reason` (same text for SubagentStop), emit `UserMessage{Text, Hook: "Stop"|"SubagentStop"}`, set `stop_hook_active = true`, continue the loop (iterations count).
- `compact`, main turn only (`t.parent == ""`): `PreCompact` (`trigger: "auto"`, subject `auto`) before the summary request; after a successful summary, `SessionStart` (`source: "compact"`); its context goes into `ContextCompactedPayload.HookContext` and the summary message content is `withHookContext(summaryPrefix+summary, hc)`.
- `ask`: after emitting `PermissionRequested`, if `d.Notify`, `Notification` (`message` = `tenon needs your permission to use <tool>`, or `tenon needs your permission to send data to <provider id>` for `kind: jurisdiction`; `notification_type: "permission_prompt"`, subject `permission_prompt`).
- `Restore`: `UserMessage` with `Blocked` skipped; content `withHookContext(p.Text, p.HookContext)`; `ContextCompacted` summary message built with `HookContext`; `HookStarted`/`HookCompleted` ignored (default branch).

- [ ] **Step 1: Failing tests**:
  - `TestUserPromptSubmitBlock`: exit 2 → no provider call, `UserMessage.blocked`, `TurnCompleted.reason == "blocked_by_hook"`, history unchanged (a second turn's request has no blocked text).
  - `TestUserPromptSubmitContext` and `TestSessionStartContext`: provider request's user message = `withHookContext(...)`; `UserMessage.hook_context` set; `SessionStart` runs only on the first turn with `source == "startup"`, and `"resume"` after `Restore`.
  - `TestStopHookContinues`: first `Stop` blocks with `more`, second passes → two provider calls, history has `Stop hook feedback: more`, second hook input `stop_hook_active == true`.
  - `TestStopHookAlwaysBlocks` (Review Focus): `MaxIterations: 3` → `TurnCompleted.reason == "max_iterations"`.
  - `TestStopNotOnCancelOrError`: provider error → no `Stop` hook run.
  - `TestSubagentStop`: block once inside a sub-agent → sub-agent gets feedback, main `Stop` runs once at the end.
  - `TestPreCompactAndSessionStartCompact`: small window forcing compaction → `PreCompact` then `SessionStart` (`compact`), `ContextCompacted.hook_context` set, summary message contains `[hook context]`.
  - `TestNotification`: `Notify: true` → hook input `message` for a write; `Notify: false` → no hook run.
  - `TestEndSession`: started session → `SessionEnd` hook with `reason`; `sleep 10` hook ends within 6 s (`timeout`); never-started session → no hook.
  - `TestRestoreHookContext`: events with `hook_context`, a blocked `UserMessage`, a `Stop` feedback message and `HookStarted`/`HookCompleted` → restored history equals the live one.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** `GOFLAGS=-mod=vendor go test -race ./internal/engine/` — PASS.
- [ ] **Step 5: Commit** `feat(engine): lifecycle hooks (prompt, stop, session, compaction, notification) and resume`.

### Task 8: CLI wiring, headless and `tenon hooks`

**Files:**
- Create: `cmd/tenon/hooks.go`, `cmd/tenon/hooks_e2e_test.go`
- Modify: `cmd/tenon/run.go`, `internal/ui/headless/headless.go`, `internal/ui/headless/headless_test.go`, `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: `hooks.Load` (Task 2), `Engine.EndSession` (Task 7), `config.Config.Hooks` (Task 1).
- Produces:
  - `func loadHooks(lang i18n.Lang, getenv func(string) string, wd string, cfg config.Hooks) (*hooks.Set, []string, error)` — notices translated and cleaned; adds `hooks.banner` when `total > 0`.
  - `startEngine`: `loadHooks` after `loadSetup`; an error aborts startup (`err.prefix`); `Deps.Hooks`, `Deps.TranscriptPath = filepath.Join(sdir, sid+".jsonl")`, `Deps.Notify = o.interactive`; notices appended to `started.notices`. `started.close` unchanged; callers run `eng.EndSession(context.Background(), sid, reason)` before `st.close()` — `"prompt_input_exit"` after the TUI returns, `"other"` after `headless.Run`.
  - `tenon config check` also loads hooks (an invalid hook file fails the check).
  - `tenon hooks [--lang fr|en]`: one line per hook `<id>  <matcher or *>  <type>  <command or prompt cut to 80 chars>`, then notices; `hooks.none` when empty; exit 1 with the load error.
  - Headless: `HookCompleted` → stderr lines (text mode) for `error`/`timeout` (`headless.hook_error`), `blocked` (`headless.hook_blocked`), `system_message` (`headless.hook_message`), `stop_reason` (`headless.hook_stopped`); `ToolResult.denied == "hook"` → `refused = true`; `TurnCompleted` `blocked_by_hook` → `refused = true` with `turn.blocked_by_hook`; `stopped_by_hook` → `turn.ended`.
  - i18n keys (EN / FR): `hooks.banner` `"hooks: %d (claude: %d)"` / `"hooks : %d (claude : %d)"`; `hooks.project_ignored` `"project hooks ignored: %s"` / `"hooks du projet ignorés : %s"`; `hooks.none` `"no hooks"` / `"aucun hook"`; `headless.hook_error` `"hook %s (%s): %s"` / `"hook %s (%s) : %s"`; `headless.hook_blocked` `"hook %s blocked: %s"` / `"hook %s a bloqué : %s"`; `headless.hook_message` `"hook: %s"` / `"hook : %s"`; `headless.hook_stopped` `"hook stopped the turn: %s"` / `"un hook a arrêté le tour : %s"`; `turn.blocked_by_hook` `"prompt blocked by a hook"` / `"prompt bloqué par un hook"`.

- [ ] **Step 1: Failing tests** (e2e, existing `newEnv` helpers):
  - `TestHooksBlockShell`: `hooks.json` with a `Bash` PreToolUse hook `exit 2`; fake provider asks `shell` → refusal exit code, stderr contains `blocked`.
  - `TestHooksClaudeSettingsOptIn`: `~/.claude/settings.json` hook absent from `tenon hooks` without `claude_settings`, present with it.
  - `TestHooksProjectNotice`: project `.claude/settings.json` with `hooks` → banner notice `project hooks ignored`, hook not run.
  - `TestHooksInvalidFileRefusesStart`: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":""}]}]}}` → `tenon -p x` and `tenon config check` exit 1 with the JSON path.
  - `TestHooksSessionEnd`: `SessionEnd` hook writes a file → present after `tenon -p` with `reason == "other"`.
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test -race ./cmd/... ./internal/ui/headless/` (PASS).
- [ ] **Step 5: Commit** `feat(cli): load hooks, tenon hooks, headless hook output`.

### Task 9: TUI

**Files:**
- Modify: `internal/ui/tui/transcript.go`, `internal/ui/tui/permission.go`, `internal/ui/tui/model.go`, `internal/ui/tui/skills.go`, `internal/i18n/catalog.go`, tests in `internal/ui/tui/`

**Interfaces:**
- Consumes: hook events (Task 5), `Options.Hooks *hooks.Set` (new, set by `runInteractive`).
- Produces:
  - Transcript: `HookStarted` remembered by `run_id` (event, label = `filepath.Base` of the first word of `command`, or the type); `HookCompleted` with status `blocked`/`error`/`timeout` or decision `ask` → item `⏺ hook <event> · <label> [blocked|asked|error|timeout] <reason or error>`; `system_message` and `stop_reason` → notices; success without effect → nothing. `UserMessage` with `hook` → notice item with its text; with `blocked` → user item as usual.
  - Permission panel: line `hook: <reason>` (`tui.perm.hook`) when `hook_reason` is set, truncated like the sub-agent line; the "yes for the session" choice is not offered for such a request.
  - `/hooks` command lists `Options.Hooks` like `tenon hooks`; `/help` mentions it (`tui.help` EN/FR gains `· /hooks lists hooks` / `· /hooks liste les hooks` before `/quit`); `/hooks` added to the Tab completion built-ins (`skills.Builtins`).
  - Text from hooks shown through `spaceClean` and `Redact`.
  - i18n keys: `tui.hook.line` `"hook %s · %s [%s] %s"`; `tui.hook.status.blocked|asked|error|timeout` EN `blocked`/`asked`/`error`/`timeout`, FR `bloqué`/`demandé`/`erreur`/`délai dépassé`; `tui.perm.hook` `"hook: %s"` / `"hook : %s"`.

- [ ] **Step 1: Failing tests** — transcript: a silent `HookCompleted{status: ok}` adds no item; `blocked` with reason `no` → item text contains `hook PreToolUse · check.sh [blocked] no`; an ANSI escape in the reason is replaced by a space; `UserMessage{hook:"Stop"}` → notice. Permission: `hook_reason` line present and the session choice absent. `/hooks` with an empty set → `aucun hook` / `no hooks`.
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test -race ./internal/ui/tui/` (PASS).
- [ ] **Step 5: Commit** `feat(tui): show hooks, hook reason in the permission panel, /hooks`.

### Task 10: Documentation

**Files:**
- Modify: `docs/usage.md`, `docs/fr/utilisation.md`, `docs/security.md`, `docs/fr/securite.md`, `README.md`, `docs/config.example.toml`

- [ ] **Step 1:** `docs/usage.md` and `docs/fr/utilisation.md`: section "Hooks" / "Hooks" after "Sub-agents" (contents entry too): sources and the `[hooks]` keys; format with the example of the spec §3.2; events table (moment, input fields, effect) from spec §5; exit codes and JSON fields; types `prompt` and `agent`; tool name and argument map; `allow` limits; differences from Claude Code (`tool_response`, `transcript_path` format, no project hooks, `PreCompact` `auto` only, `model` ignored); `tenon hooks`, `/hooks`, banner; audit events.
- [ ] **Step 2:** `docs/security.md` and `docs/fr/securite.md`: hooks run user code only; project hooks never run; `allow` limits; `command` hooks get unmasked input and may send it anywhere (example: a notification hook posting to a non-EU service is the user's responsibility); `prompt`/`agent` go through masking and the jurisdiction check; failures are non-blocking (a broken security hook does not block — test your hooks).
- [ ] **Step 3:** `README.md` feature list gains hooks; `docs/config.example.toml` gains a commented `[hooks]` section with both keys (`TestConfigExampleLoads` passes).
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test ./internal/config/` — PASS.
- [ ] **Step 5: Commit** `docs: hooks`.
