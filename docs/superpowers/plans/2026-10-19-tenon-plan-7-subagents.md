# tenon sub-agents — Implementation Plan (plan 7)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sub-agents in tenon: Claude Code agent files (`.claude/agents/*.md`) plus a built-in `general-purpose` agent, launched by the model with an `agent` tool, run as a nested turn inside the same engine, session, permissions and audit chain.

**Architecture:** The skills frontmatter parser moves to a shared `internal/frontmatter`. A new `internal/agents` package discovers agent files, translates Claude Code tool names and builds prompt texts. The engine's turn loop is parametrized (history, tool set, `parent_call_id`, streaming) so the same loop runs the main agent and a sub-agent; the engine owns the `agent` tool. Events gain `SubagentStarted`/`SubagentCompleted` and an optional `parent_call_id`. `cmd/tenon`, headless and the TUI consume them.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), Bubble Tea TUI, no new dependency.

**Spec:** `docs/superpowers/specs/2026-10-19-tenon-subagents-design.md`

## Global Constraints

- No new dependency.
- Agent dirs, highest priority first: `$XDG_CONFIG_HOME/tenon/agents` (else `~/.config/tenon/agents`), `~/.claude/agents`, `~/.agents/agents`, then project root `.tenon/agents`, `.claude/agents`, `.agents/agents`. Same base-dir rules as skills (empty/relative `HOME`/`XDG_CONFIG_HOME`, symlinked project base ignored with a notice, symlinked user base followed).
- Agent file `<name>.md`, regular file only (`safefile.ReadRegular`), ≤ 32768 bytes (truncated at last whole rune, marked truncated), ≤ 100 agents.
- `name`: `^[a-z0-9-]{1,64}$`, equal to the file name without `.md`. `description`: non-empty, ≤ 1024 chars. `model`: read, shown, no effect.
- Tool name translation: `Read`→`read_file`, `Write`→`write_file`, `Edit`/`MultiEdit`→`edit_file`, `Bash`→`shell`, `Grep`→`grep`, `Glob`→`glob`, `Skill`→`skill`; tenon names and `mcp__*` kept; `(...)` suffix stripped; anything else (incl. `agent`, `Task`, `WebFetch`) dropped with a notice.
- Prompt section "Available agents": budget 4000 bytes; descriptions cut to 200 chars first, then trailing agents dropped (notice).
- Tool `agent`: args `{"agent"?: string, "description": string, "prompt": string}`, `agent` defaults to `general-purpose`; risk `tools.Read`; `Target{Command: <name>}`.
- A sub-agent never gets `agent`; its tools are the intersection of its `tools` with the session's registry.
- Events without `parent_call_id` serialize exactly as before (field is `omitempty`).
- New user-visible strings go through `internal/i18n` (EN and FR). Messages outside the catalog are English. Code comments in French, like the rest of the code.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...` and `GOFLAGS=-mod=vendor go test -race ./...` pass.

## Review Focus

- Providers that reuse tool call IDs across requests (Ollama-style `call_0`): a sub-agent call and a main call with the same `call_id` must not be confused in the TUI (result, preview, permission match) — test in Task 8.
- Resuming a session whose sub-agent asked for permissions: the permission counter must include nested `PermissionRequested`, so the next ID does not collide — test in Task 6.
- A sub-agent declared with `tools: Bash` while a `deny` rule covers the command: still denied, error result to the sub-agent, main turn continues — test in Task 5.
- A model forcing an `agent` call inside a sub-agent: `unknown tool: agent`, no recursion — test in Task 5.
- `.claude/agents/x.md` in the project as a symlink to `/etc/passwd` or a FIFO: skipped, no hang — test in Task 2.

---

### Task 1: Shared frontmatter parser

**Files:**
- Create: `internal/frontmatter/frontmatter.go`, `internal/frontmatter/frontmatter_test.go` (move of `internal/skills/frontmatter*.go`)
- Modify: `internal/skills/catalog.go` (use the new package), delete `internal/skills/frontmatter.go` and its test

**Interfaces:**
- Produces:
  - `type Fields struct{…}` with `func (f Fields) String(key string) string`, `func (f Fields) List(key string) []string`, `func (f Fields) Bool(key string) bool`, `func (f Fields) Has(key string) bool`, and convenience `Name() string`, `Description() string`.
  - `func Parse(data []byte) (f Fields, body string, err error)` — same syntax, limits (8 KiB, 200 lines) and errors as today's `parseFrontmatter`, including "missing name"/"missing description".
  - Nested mappings (`metadata:` block) are skipped as today; `Has` is true for a key present with an empty value.

- [ ] **Step 1: Move the tests** — port `TestParseFrontmatter` and `TestParseFrontmatterErreurs` to `package frontmatter`, asserting through the accessors (`f.Name()`, `f.List("allowed-tools")`, `f.Bool("disable-model-invocation")`). Add `TestHas`: `"---\nname: a\ndescription: b\ntools:\n---\n"` → `Has("tools") == true`, `List("tools") == nil`; `Has("model") == false`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/frontmatter/` — FAIL (package missing).
- [ ] **Step 3: Implement** — move the parser; store every top-level key's scalar value and continuation lines so `String`/`List`/`Bool` decode on demand with the existing `scalarValue`/`listValue`. `skills.Discover` uses `frontmatter.Parse` and reads `disable-model-invocation`, `allowed-tools`.
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test ./internal/frontmatter/ ./internal/skills/` — PASS, skills tests unchanged.
- [ ] **Step 5: Commit** `refactor(frontmatter): extract the YAML frontmatter parser from skills`.

### Task 2: Agent discovery, catalog and prompts

**Files:**
- Create: `internal/agents/catalog.go`, `internal/agents/prompt.go`, tests `catalog_test.go`, `prompt_test.go`
- Modify: `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: `frontmatter.Parse` (Task 1), `skills.Dirs`, `safefile.ReadRegular`.
- Produces:
  - `type Source string`: `Builtin = "builtin"`, `User = "user"`, `Project = "project"`. `type Dir struct{ Path string; Source Source }`. `type Notice string`.
  - `func Dirs(getenv func(string) string, projectRoot string) []Dir` — derived from `skills.Dirs`: same bases, last element `skills` replaced by `agents`.
  - `type Agent struct { Name, Description string; Source Source; Path string; Tools []string; Model string; Body string; Truncated bool }` — `Tools == nil` means all session tools except `agent`; a present `tools:` key always yields non-nil (possibly empty).
  - `const GeneralPurpose = "general-purpose"`, `MaxAgents = 100`, `MaxAgentBytes = 32768`.
  - `func Discover(lang i18n.Lang, dirs []Dir) (*Catalog, []Notice)`; `(*Catalog).Get(name) (Agent, bool)`; `(*Catalog).List() []Agent` (built-in first unless replaced, then discovery order); `(*Catalog).FileCount() (total, project int)`.
  - `func TranslateTools(names []string) (tools []string, unknown []string)`.
  - `func (c *Catalog) PromptSection(lang i18n.Lang, budget int) (section string, dropped int)`.
  - `func SystemPrompt(lang i18n.Lang, base, skillsSection string, a Agent, withSkills bool) string` — `base`, then `skillsSection` if `withSkills` and non-empty, then the preamble, then `a.Body` if non-empty; parts joined by `"\n\n"`.
  - i18n keys (EN / FR):
    - `agents.prompt_header`: `"## Available agents\nUse the `agent` tool to delegate a self-contained task. The sub-agent does not see this conversation: give it everything it needs in `prompt`."` / `"## Agents disponibles\nUtilisez l'outil `agent` pour déléguer une tâche autonome. Le sous-agent ne voit pas cette conversation : donnez-lui tout ce qu'il faut dans `prompt`."`
    - `agents.preamble`: `"You are a sub-agent launched by the main agent. Your final answer is returned to it as a tool result: make it complete and concise. You cannot launch sub-agents."` / `"Vous êtes un sous-agent lancé par l'agent principal. Votre réponse finale lui est renvoyée comme résultat d'outil : rendez-la complète et concise. Vous ne pouvez pas lancer de sous-agent."`
    - `agents.general_purpose`: `"General-purpose agent for multi-step research, code search and tasks."` / `"Agent généraliste pour les recherches, la recherche dans le code et les tâches en plusieurs étapes."`
    - `agents.banner` `"agents: %d (project: %d)"` / `"agents : %d (projet : %d)"`; `agents.dropped` `"%d agents left out of the prompt (size limit)"` / `"%d agents omis du prompt (limite de taille)"`.

- [ ] **Step 1: Write failing tests** (helper `writeAgent(t, dir, name, frontmatter, body)`):
  - `TestDiscoverPriority`: `review` in `~/.claude/agents` and project `.claude/agents` → `Source == User`, one notice containing `review`.
  - `TestDiscoverClaudeCodeFile`: `"---\nname: reviewer\ndescription: Reviews code\ntools: Read, Grep, Bash(git:*), WebFetch\nmodel: sonnet\ncolor: red\n---\nYou review.\n"` → `Tools == ["read_file","grep","shell"]`, `Model == "sonnet"`, `Body == "You review.\n"`, one notice containing `WebFetch`.
  - `TestDiscoverToolsForms`: list form `tools: [Read, mcp__git__log]` → `["read_file","mcp__git__log"]`; absent `tools` → `nil`; `tools: Task` → non-nil empty + notice; `Edit, MultiEdit` → `["edit_file"]` (deduplicated).
  - `TestDiscoverInvalid`: name ≠ file name, bad name, missing description, description of 1025 chars, non-`.md` file ignored silently → notices for the first four.
  - `TestDiscoverUnsafe`: project file symlink → skipped; FIFO → skipped without blocking; project base `.claude/agents` symlink → notice, skipped; user base symlink → followed.
  - `TestDiscoverLimits`: 101 agents → 100 kept, notice; file of 40000 bytes → `Truncated`, body ends on a whole rune.
  - `TestBuiltin`: empty dirs → `List()` = one agent `general-purpose`, `Source == Builtin`, `Tools == nil`, description from `agents.general_purpose` in the given lang; a user file `general-purpose.md` replaces it (no notice).
  - `TestPromptSection`: header + `- general-purpose: …` lines; budget behaviour like `skills.TestPromptBudget` with 4000.
  - `TestSystemPrompt`: order base → skills → preamble → body; `withSkills=false` omits the skills section; FR preamble with `i18n.FR`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/agents/` — FAIL.
- [ ] **Step 3: Implement** — follow `skills.Discover` structure (Lstat, regular files, notices in English, e.g. `agent "x" in <path> ignored: …`). The built-in is appended to the catalog after discovery only if no file named `general-purpose` exists.
- [ ] **Step 4: Run** — PASS; `go test ./internal/i18n/` PASS (EN/FR key parity test).
- [ ] **Step 5: Commit** `feat(agents): discover Claude Code compatible agent files`.

### Task 3: Sub-agent events

**Files:**
- Modify: `internal/event/event.go`, `internal/event/payloads.go`, `internal/event/event_test.go`

**Interfaces:**
- Produces:
  - `SubagentStarted Type = "SubagentStarted"`, `SubagentCompleted Type = "SubagentCompleted"`.
  - `type SubagentStartedPayload struct { CallID string `json:"call_id"`; Agent string `json:"agent"`; Description string `json:"description"` }`.
  - `type SubagentCompletedPayload struct { CallID string `json:"call_id"`; Status string `json:"status"`; Iterations int `json:"iterations"` }` — status `"ok"`, `"error"`, `"cancelled"`.
  - `ParentCallID string `json:"parent_call_id,omitempty"`` on `AssistantMessagePayload`, `ToolCallRequestedPayload`, `ToolResultPayload`, `PermissionRequestedPayload`, `PermissionResolvedPayload`, `ContextCompactedPayload`, `RedactionAppliedPayload`, `JurisdictionWarningPayload`, `ErrorPayload`, `McpServerDownPayload`.
  - `func WithParent(payload any, parentCallID string) any` — returns a copy with `ParentCallID` set for the types above (value types), unchanged for others or when `parentCallID == ""`.

- [ ] **Step 1: Write failing tests** — `TestWithParent`: for each of the 10 payload types, `json.Marshal(WithParent(p, "c1"))` contains `"parent_call_id":"c1"`; `WithParent(p, "")` marshals byte-identical to `p` and contains no `parent_call_id`; `WithParent(event.TurnCompletedPayload{}, "c1")` unchanged.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/event/` — FAIL.
- [ ] **Step 3: Implement** with a type switch.
- [ ] **Step 4: Run** — PASS, and `go test ./internal/audit/ ./internal/session/` PASS (canonical form unchanged).
- [ ] **Step 5: Commit** `feat(event): sub-agent events and parent_call_id`.

### Task 4: Parametrized turn loop (no behaviour change)

**Files:**
- Modify: `internal/engine/loop.go`, `internal/engine/compact.go`, `internal/engine/engine.go`, `internal/tools/tool.go`
- Test: `internal/tools/tool_test.go` (or new `registry_test.go`), existing engine tests

**Interfaces:**
- Produces:
  - `func (r *Registry) With(ts ...Tool) *Registry` (copy plus `ts`; nil receiver = empty), `func (r *Registry) Only(names []string) *Registry` (copy restricted to `names`; nil receiver = empty), `func (r *Registry) Names() []string` (sorted).
  - `turn` gains `h *[]llm.Message` (main: `&s.history`), `tools *tools.Registry`, `parent string`, `deltas bool`. `turn.emit` wraps payloads with `event.WithParent(payload, t.parent)`. All `t.s.history` uses in `loop.go`/`compact.go` go through `*t.h`; tool lookups and defs through `t.tools`; `stream(req, …)` streams deltas only if `t.deltas` (summary requests still never stream).
  - `type loopResult struct { Iterations int; Reason string; Text string; ProvErr error }` — `Text` is the last non-empty assistant text; `Reason` is one of today's `TurnCompleted` reasons or `"error"` when `ProvErr != nil`.
  - `func (t *turn) loop() (loopResult, error)` — the iteration loop of today's `run`, after the `UserMessage`. Each place that today emits `TurnCompleted` inside the loop (directly, via `cancelled`, or via `compactOrStop`) returns the reason instead; the provider-error path still emits the `Error` event, then returns `ProvErr`. The returned `error` is fatal (audit, context).
  - `run(in)` = session start + `UserMessage` + `loop()`, then emits `TurnCompleted` from the result exactly as before (`cancelled` best-effort, emission error ignored) and returns `ProvErr` without `TurnCompleted` like today.

- [ ] **Step 1: Write failing tests** — `TestRegistryWithOnly`: `NewRegistry(a, b).Only([]string{"b","zz"}).Names() == ["b"]`; `With(c)` does not modify the original; `(*Registry)(nil).With(c).Names() == ["c"]`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/tools/ -run TestRegistryWithOnly` — FAIL.
- [ ] **Step 3: Implement** the registry methods, then refactor the engine as above.
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test -race ./internal/engine/ ./internal/tools/` — PASS with no engine test modified (event sequences identical).
- [ ] **Step 5: Commit** `refactor(engine): parametrize the turn loop`.

### Task 5: The `agent` tool and nested turn

**Files:**
- Create: `internal/engine/agent.go`, `internal/engine/agent_test.go`
- Modify: `internal/engine/engine.go`, `internal/engine/loop.go`

**Interfaces:**
- Consumes: `agents.Catalog`, `agents.SystemPrompt` (Task 2); `event.SubagentStarted/Completed` (Task 3); `turn`, `loopResult`, `Registry.With/Only` (Task 4).
- Produces:
  - `Deps` gains `Agents *agents.Catalog` (nil: no `agent` tool), `BasePrompt string` (prompt without skills/agents sections; empty: `SystemPrompt`), `SkillsSection string`, `Lang i18n.Lang`, `OutputLimit int` (0: no truncation).
  - Main turn tool set = `d.Tools.With(agentTool)` when `d.Agents != nil`, else `d.Tools`; built once in `New`.
  - `agentTool` implements `tools.Tool`: name `agent`; English description `"Launch a sub-agent for a self-contained task. It does not see this conversation; it returns its final answer."`; schema with `agent` (string, optional), `description` (string, required), `prompt` (string, required); `Risk() == tools.Read`; `Target` → `{Command: name}`, error `prompt is required` on empty prompt, invalid JSON → error; `Run` returns an error result (never called by the engine).
  - `func (t *turn) runSubagent(c llm.ToolCall) (tools.Result, error)`, called from `decideAndRun` after the permission check when the tool is the `agentTool`.

  `runSubagent` behaviour:
  1. Unknown agent → error result `unknown agent: <name>`.
  2. Emit `SubagentStarted{CallID: c.ID, Agent, Description}` (no parent).
  3. Tool set: `d.Tools` (never the agent tool), restricted with `Only(a.Tools)` when `a.Tools != nil`.
  4. History: system = `agents.SystemPrompt(d.Lang, base, d.SkillsSection, a, toolset has "skill")`; user = `prompt`. No `UserMessage` event.
  5. Child turn: same `e`, `s`, `ctx`; `h` = local slice; `parent = c.ID`; `deltas = false`; run `loop()`.
  6. Map the result:

  | loop result | Status | Tool result |
  |---|---|---|
  | `done` | `ok` | `Text` |
  | `max_iterations` | `error` | `"sub-agent stopped: iteration limit reached"` + `"\n\n" + Text` if any |
  | `incomplete`, `blocked`, `context_too_large` | `error` | `"sub-agent stopped: " + reason` + partial text |
  | `ProvErr` | `error` | `"sub-agent stopped: " + ProvErr.Error()` + partial text |
  | fatal error / context cancelled | `cancelled` (best-effort emit when ctx is done) | return the error: the whole turn ends |

  7. Emit `SubagentCompleted{CallID, Status, Iterations}`; return `tools.Result{Content: tools.Truncate(content, d.OutputLimit), IsError: status != "ok"}`.

- [ ] **Step 1: Write failing tests** (fake provider scripts; helper `newAgentEngine(t, script, agentsFiles...)` building a catalog from a temp dir):
  - `TestSubagentDelegation`: main calls `agent{"description":"look","prompt":"read a.txt"}`; sub calls `read_file`; sub answers `"a.txt says hi"`; main answers `"done"`. Assert event order `ToolCallRequested(agent)`, `SubagentStarted`, `AssistantMessage{parent}`, `ToolCallRequested(read_file, parent)`, `ToolResult(read_file, parent)`, `AssistantMessage{parent}`, `SubagentCompleted{ok, 2}`, `ToolResult(agent, content "a.txt says hi")`, `AssistantMessage` (no parent), `TurnCompleted{done}`. No `AssistantDelta` carries a parent.
  - `TestSubagentIsolation`: request 2 (the sub's first) has exactly 2 messages: system containing the preamble and not the main user text, user = prompt; its tool defs do not include `agent`; request 1 and 4 (main) include `agent`.
  - `TestSubagentToolsRestricted`: agent file `tools: Read` → sub request defs = `[read_file]`; sub calling `shell` → `ToolResult{parent, IsError, "unknown tool: shell"}`.
  - `TestSubagentNoNesting`: sub calls `agent` → `unknown tool: agent`.
  - `TestSubagentPermissionDenied`: shell `ask`, Respond deny → `ToolResult{parent, Denied:"user"}`; main turn continues to `done`. With a `deny` rule on the command → `Denied:"policy"`.
  - `TestSubagentSessionApprovalShared`: shell approved with `Remember` in the main turn; same command in the sub → no `PermissionRequested`.
  - `TestSubagentMaxIterations`: `MaxIterations: 2`, sub always calls `read_file` → agent result is an error starting with `sub-agent stopped: iteration limit reached`; `SubagentCompleted{error, 2}`; main continues.
  - `TestSubagentProviderError`: sub stream ends with an error → `Error{parent}` event, agent result error containing the message, main continues.
  - `TestSubagentCancel`: context cancelled while the sub waits on a permission → `SubagentCompleted{cancelled}`, `TurnCompleted{cancelled}`, `SubmitInput` returns `context.Canceled`; next Submit works (history valid: agent call closed with `cancelled: …`).
  - `TestSubagentUnknownAndInvalid`: `agent:"nope"` → `unknown agent: nope`; empty prompt → `invalid arguments: prompt is required`; no `SubagentStarted` in both cases.
  - `TestAgentDenyRule`: rule `{tool="agent", match="general-purpose", action="deny"}` → `Denied:"policy"`, no `SubagentStarted`.
  - `TestNoAgentsNoTool`: `Deps.Agents == nil` → no `agent` in defs.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/engine/ -run 'Subagent|Agent'` — FAIL.
- [ ] **Step 3: Implement** as specified.
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test -race ./internal/engine/` — PASS.
- [ ] **Step 5: Commit** `feat(engine): agent tool runs sub-agents as nested turns`.

### Task 6: Resume ignores sub-agent events

**Files:**
- Modify: `internal/engine/restore.go`, `internal/engine/restore_test.go`

**Interfaces:**
- Consumes: `ParentCallID` fields (Task 3).
- Produces: `Restore` skips `UserMessage`, `AssistantMessage`, `ToolResult` and `ContextCompacted` events whose payload has a non-empty `parent_call_id`; `PermissionRequested` is still counted for `permN` whatever its parent; `seq` still advances.

- [ ] **Step 1: Write failing tests**:
  - `TestRestoreWithSubagent`: record the events of `TestSubagentDelegation`, restore into a new engine, submit `"next"` → the provider request contains system, user, assistant(agent call), tool(agent result), assistant(`done`), user(`next`), and none of the sub-agent messages.
  - `TestRestoreInterruptedSubagent`: events truncated after the sub's `ToolCallRequested(read_file)` → history ends with the agent call answered by `cancelled: session interrupted`.
  - `TestRestorePermCounterIncludesNested`: a nested `PermissionRequested` with ID `<sid>-p3` → after restore, the next permission ID is `<sid>-p4`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/engine/ -run TestRestore` — FAIL.
- [ ] **Step 3: Implement** — decode `struct{ ParentCallID string `json:"parent_call_id"` }` once per event before the switch.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(engine): resume ignores sub-agent events`.

### Task 7: CLI wiring, headless and `tenon agents`

**Files:**
- Create: `cmd/tenon/agents.go`, `cmd/tenon/agents_e2e_test.go`
- Modify: `cmd/tenon/run.go`, `cmd/tenon/skills.go` (`systemPrompt`), `internal/ui/headless/headless.go`, `internal/ui/headless/headless_test.go`
- Test: `internal/gitmark/gitmark_test.go`

**Interfaces:**
- Consumes: `agents.Dirs/Discover/PromptSection` (Task 2), `engine.Deps` fields (Task 5).
- Produces:
  - `func discoverAgents(lang i18n.Lang, getenv func(string) string, wd string) discoveredAgents` with `discoveredAgents{cat *agents.Catalog; section string; notices []string}`: discovery notices (`clean`ed), banner `agents.banner` only when `FileCount().total > 0`, `agents.dropped` when the 4000-byte budget drops agents.
  - `func listAgents(lang i18n.Lang, stdout io.Writer, getenv func(string) string, wd string) int` — one line per agent: `name  source  path  description  tools: a, b  model: x` (path omitted for `builtin`, `tools: all` when nil, `model:` only if set), then notices; all text through `clean`.
  - `systemPrompt(base string, sections ...string) string` — appends each non-empty section with `"\n\n"`.
  - `startEngine` discovers agents (passed from the headless path like skills, to avoid double discovery), sets `SystemPrompt: systemPrompt(base, skillsSection, agentsSection)`, `BasePrompt: base`, `SkillsSection`, `Agents`, `Lang`, `OutputLimit: cfg.OutputLimit`; `started` gains `agents *agents.Catalog`; agent notices join the startup notices after skill notices.
  - `tenon agents [--lang]` handled exactly like `tenon skills` (sub-flag parsing, `--resume` refusal, extra args).
  - Headless text output: `AssistantMessage` with a parent does not update `final`. JSON output unchanged (nested events printed).

- [ ] **Step 1: Write failing tests**:
  - headless `TestTextIgnoresSubagentMessages`: events with a nested `AssistantMessage{"sub text", parent}` then main `AssistantMessage{"main"}` → stdout `main\n`; with only a nested message → no `sub text` on stdout.
  - gitmark `TestAgentWritesIncludeSubagent`: a `ToolResult{write_file, parent_call_id}` counts as an agent write.
  - e2e `TestAgentsListCommand`: project `.claude/agents/reviewer.md` with `tools: Read, Grep` and `model: sonnet` → output contains `general-purpose  builtin`, `reviewer  project  `, `tools: read_file, grep`, `model: sonnet`; `--lang fr` works.
  - e2e `TestSubagentEndToEnd`: fake LLM script: `sseCall("agent", `{"agent":"reviewer","description":"review","prompt":"read go.mod"}`)`, `sseCall("read_file", `{"path":"a.txt"}`)`, `sseText("sub answer")`, `sseText("final answer")` → exit 0, stdout `final answer\n`; request 2 body contains the reviewer body and not the `-p` text; `tenon audit verify` exits 0; with `--output json`, decoded events contain `SubagentStarted`, a `ToolResult` with `parent_call_id` and `SubagentCompleted` status `ok`.
  - e2e `TestSubagentBanner`: `-p` stderr contains `agents: 1 (project: 1)` with `--lang en`; no agents line without agent files.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./cmd/tenon/ ./internal/ui/headless/ -run 'Agent|Subagent'` — FAIL.
- [ ] **Step 3: Implement** as specified.
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test -race ./cmd/tenon/ ./internal/ui/headless/` — PASS.
- [ ] **Step 5: Commit** `feat(cli): wire sub-agents, tenon agents`.

### Task 8: TUI

**Files:**
- Modify: `internal/ui/tui/transcript.go`, `internal/ui/tui/view.go`, `internal/ui/tui/permission.go`, `internal/ui/tui/model.go`, `internal/ui/tui/skills.go` (or new `agents.go`), `internal/skills/prompt.go` (`Builtins`), `internal/i18n/catalog.go`, `cmd/tenon/run.go` (pass `Agents` to TUI options)
- Test: `internal/ui/tui/transcript_test.go`, `permission_test.go`, `model_test.go`, `internal/skills/prompt_test.go`

**Interfaces:**
- Consumes: events of Task 3, `agents.Catalog` (Task 2).
- Produces:
  - `Item` gains `Parent string` and `Iterations int`; tool status `"cancelled"` (key `tui.tool.cancelled`: `cancelled` / `annulé`).
  - Transcript: `SubagentStarted` sets the main `agent` tool item's `Detail`/`reqDetail` to `<agent> · <description>`; `SubagentCompleted` sets its `Status` (`ok`/`error`/`cancelled`) and `Iterations`; the agent's `ToolResult` keeps that detail (no result line appended) unless denied. Nested `ToolCallRequested` adds an `ItemTool` with `Parent`; nested `ToolResult` matches on `(Parent, callID)`; nested `AssistantMessage` is not shown; main matches require `Parent == ""`.
  - View: nested tool items render `"  ⎿ <tool> <detail> [status]"`; the agent item appends `" · " + i18n.T(lang, "tui.agent.iterations", n)` (`%d iterations` / `%d itérations`) once completed.
  - Permission: `pendingCall` gains `parent`; `matchArgs` only considers calls with the request's `ParentCallID`; the model keeps `subagents map[string]subagentInfo` from `SubagentStarted` (cleared at `TurnCompleted`); a request with a parent shows a first panel line `i18n.T(lang, "tui.perm.subagent", agent, description)` (`sub-agent %s · %s` / `sous-agent %s · %s`), text `clean`ed and truncated like other panel lines.
  - `/agents`: `Options.Agents *agents.Catalog`; lists `name  source  description` as notices; `skills.Builtins` = `help, quit, skills, agents`; `tui.help` strings mention `/agents lists agents` / `/agents liste les agents`.

- [ ] **Step 1: Write failing tests**:
  - `TestTranscriptSubagent`: apply the delegation event sequence → items: user, `agent` tool with detail `general-purpose · look` status `ok`, `Iterations 2`; one nested `read_file` item with `Parent`; no item for nested assistant text; final assistant `done`.
  - `TestTranscriptCallIDCollision`: main `call_0` (shell) pending, nested `call_0` (read_file) result → the main item stays `pending`, the nested one is `ok`.
  - `TestViewNestedTool`: rendered line starts with `"  ⎿ read_file"`.
  - `TestPermissionSubagentContext`: `SubagentStarted` then nested `PermissionRequested` → panel contains `sub-agent general-purpose · look` (EN); preview uses the nested call's args, not a main call with the same ID and tool.
  - `TestAgentsCommand`: `/agents` adds a notice containing `general-purpose  builtin`.
  - `TestIsBuiltin` updated: `IsBuiltin("agents") == true`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/ui/tui/ ./internal/skills/` — FAIL.
- [ ] **Step 3: Implement** as specified.
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test -race ./internal/ui/tui/ ./internal/skills/ ./cmd/tenon/` — PASS.
- [ ] **Step 5: Commit** `feat(tui): show sub-agents, /agents`.

### Task 9: Documentation

**Files:**
- Modify: `docs/usage.md`, `docs/fr/utilisation.md` (section "Sub-agents" / "Sous-agents" after "Skills"; events list if present: `SubagentStarted`, `SubagentCompleted`, `parent_call_id`; `tenon agents` in the commands list; `/agents` in the keys), `docs/security.md`, `docs/fr/securite.md` (sub-agent = same permissions, masking, audit; never more rights than the session; project agent files are untrusted content like `AGENTS.md`; `model` ignored), `README.md` (features list, EN and FR sections).

- [ ] **Step 1: Write** the sections: dirs and priority, file format with a Claude Code example (`tools: Read, Grep`, `model: sonnet`), tool translation table, `general-purpose`, `agent` tool arguments, permission rule example `tool = "agent"`, `match = "reviewer"`, limits (sequential, no nesting, same model), interface display.
- [ ] **Step 2: Check** links: `grep -n "](#" docs/usage.md docs/fr/utilisation.md` anchors exist; `make ci` passes.
- [ ] **Step 3: Commit** `docs: sub-agents`.
