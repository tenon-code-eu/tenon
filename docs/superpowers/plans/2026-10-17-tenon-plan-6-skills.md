# tenon skills — Implementation Plan (plan 6)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agent Skills support (`SKILL.md` folders) in tenon: discovery, model-invoked `skill` tool, user-invoked `/<name>`, with audit and no permission escalation.

**Architecture:** New package `internal/skills` (frontmatter parser, discovery/catalog, safe file reads, prompt section, slash expansion, and the `skill` tool implementing `tools.Tool`). `cmd/tenon` wires discovery into startup, the system prompt, the tool registry, `tenon skills` and `-p "/<name>"`. The engine gains `SubmitInput` so the `UserMessage` event can carry skill metadata. The TUI handles `/<name>`, `/skills`, Tab completion.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), Bubble Tea TUI, no new dependency.

**Spec:** `docs/superpowers/specs/2026-10-17-tenon-skills-design.md`

## Global Constraints

- No new dependency (YAML parsed by an internal minimal parser).
- Skill dirs, highest priority first: `$XDG_CONFIG_HOME/tenon/skills` (else `~/.config/tenon/skills`), `~/.claude/skills`, `~/.agents/skills`, then project root `.tenon/skills`, `.claude/skills`, `.agents/skills`.
- `name`: `^[a-z0-9-]{1,64}$`, equal to the folder name. `description`: non-empty, ≤ 1024 chars.
- `SKILL.md` ≤ 32768 bytes (truncate at last whole rune, mark truncated). Frontmatter ≤ 8 KiB and ≤ 200 lines. ≤ 200 skills.
- Prompt section budget: 8000 chars; descriptions cut to 200 chars first, then trailing skills dropped.
- Tool `skill`: risk `tools.Read`; file listing 3 levels, 100 entries max; `file` reads limited by `output_limit`.
- `allowed-tools` never changes permissions.
- Built-ins `/help`, `/quit`, `/skills` win over skills of the same name.
- New user-visible strings go through `internal/i18n` (EN and FR, same verbs); messages outside the catalog are English. Code comments in French, like the rest of the code.
- Reads: regular files only, `O_RDONLY|O_NOFOLLOW|O_NONBLOCK`, symlinks and FIFOs ignored — same as `internal/instructions`.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...` and `GOFLAGS=-mod=vendor go test -race ./...` pass.

## Review Focus

- A skill folder that is itself a symlink to outside the tree (e.g. `.claude/skills/x -> /etc`) must be skipped, not followed — test in Task 2.
- `file` argument `references/../../../.ssh/id_rsa`, `/etc/passwd`, `refs/link/secret` where `link` is a symlink — refused — test in Task 3.
- A project skill named like a user skill (shadowing attempt) — user wins, notice emitted — test in Task 2.
- Windows-style CRLF `SKILL.md` (common when edited on Windows) parses identically — test in Task 1.
- `-p "/unknown"` exits 1 without contacting the provider — test in Task 7.

---

### Task 1: Frontmatter parser

**Files:**
- Create: `internal/skills/frontmatter.go`
- Test: `internal/skills/frontmatter_test.go`

**Interfaces:**
- Produces: `type frontmatter struct { Name, Description string; DisableModelInvocation bool; AllowedTools []string }` and `func parseFrontmatter(data []byte) (fm frontmatter, body string, err error)`.

- [ ] **Step 1: Write failing tests** — table test `TestParseFrontmatter` with cases:
  - plain: `"---\nname: review\ndescription: Review code\n---\nBody\n"` → Name `review`, Description `Review code`, body `"Body\n"`.
  - quoted: `description: "a: b"` and `description: 'it''s'` → `a: b`, `it's`.
  - folded `description: >\n  line one\n  line two\n` → `line one line two`; literal `|` → `"line one\nline two"`.
  - `allowed-tools: Read, Grep` → `["Read","Grep"]`; `allowed-tools: [Read, "Bash(git:*)"]` → `["Read","Bash(git:*)"]`; block list `allowed-tools:\n  - Read\n  - Grep` → same.
  - `disable-model-invocation: true` → true.
  - nested `metadata:\n  author: x\n  tags: [a]\n` skipped, following keys still read.
  - unknown key `license: MIT` ignored.
  - CRLF version of the plain case → identical result.
  - errors: no leading `---`; unclosed frontmatter; frontmatter > 8192 bytes; > 200 lines; missing `name`; missing `description`.

- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/skills/ -run TestParseFrontmatter` — FAIL (undefined).
- [ ] **Step 3: Implement `parseFrontmatter`** — line-based: normalize `\r\n`; key lines `^[A-Za-z0-9_-]+:`; indented lines belong to the previous key (block scalar, block list, or skipped mapping). Validation of name/description format is NOT done here (Task 2).
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(skills): SKILL.md frontmatter parser`.

### Task 2: Discovery and catalog

**Files:**
- Create: `internal/skills/catalog.go`
- Test: `internal/skills/catalog_test.go`

**Interfaces:**
- Consumes: `parseFrontmatter` (Task 1).
- Produces:
  - `type Source string` with `User Source = "user"`, `Project Source = "project"`.
  - `type Dir struct { Path string; Source Source }`.
  - `func Dirs(getenv func(string) string, projectRoot string) []Dir` — the 6 dirs of Global Constraints, in order.
  - `type Skill struct { Name, Description string; Source Source; Dir string; AllowedTools []string; ModelInvocable bool }` (`Dir` = absolute skill folder).
  - `type Notice string` (English text, e.g. `skill "x" ignored: …`).
  - `type Catalog struct{…}`; `func Discover(dirs []Dir) (*Catalog, []Notice)`; `func (c *Catalog) Get(name string) (Skill, bool)`; `func (c *Catalog) List() []Skill` (priority order, then name); `func (c *Catalog) Load(name string) (body string, sha256hex string, truncated bool, err error)` (re-reads `SKILL.md`, no cache, sha256 of the file bytes read).
  - `const MaxSkills = 200`, `const MaxSkillBytes = 32768`.

- [ ] **Step 1: Write failing tests** (helper `writeSkill(t, root, name, frontmatter, body)`):
  - `TestDiscoverPriority`: same name `review` in `~/.claude/skills` and project `.tenon/skills` → `Get("review").Source == User`, one notice containing `review`; same name in user `tenon` and user `.claude` → the tenon one wins.
  - `TestDiscoverInvalid`: name ≠ folder, name `Bad_Name`, description of 1025 chars, bad frontmatter → skipped, one notice each, others still loaded.
  - `TestDiscoverUnsafe`: skill folder is a symlink to a dir containing a valid `SKILL.md` → skipped; `SKILL.md` is a symlink → skipped; `SKILL.md` is a FIFO → skipped without blocking (3 s guard).
  - `TestDiscoverLimits`: 201 skills → 200 kept + notice; `SKILL.md` of 40000 bytes → `Load` returns 32768 bytes max, `truncated == true`, no split rune.
  - `TestLoadRereads`: modify `SKILL.md` after `Discover` → `Load` returns new body and new sha256; deleted → error.
  - `TestDirs`: with `XDG_CONFIG_HOME=/x`, `HOME=/h` → first dir `/x/tenon/skills`, then `/h/.claude/skills`, `/h/.agents/skills`, then `<root>/.tenon/skills`, `<root>/.claude/skills`, `<root>/.agents/skills`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — `os.Lstat` each skill folder (must be a real dir), open `SKILL.md` like `internal/instructions.loadOne`; absent dirs silent; unreadable dir → notice.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(skills): discovery and catalog`.

### Task 3: Safe file reads and listing

**Files:**
- Create: `internal/skills/files.go`
- Modify: `internal/tools/tool.go` (add `IsSensitive`), `internal/policy/permissions.go:272` (`isSensitive` calls `tools.IsSensitive`)
- Test: `internal/skills/files_test.go`

**Interfaces:**
- Consumes: `Catalog`, `Skill` (Task 2).
- Produces: `func IsSensitive(path string) bool` in `internal/tools` (same rule as today: base lowercased starts with `.env` or `id_rsa`, or ends with `.pem`); `func (c *Catalog) ReadFile(name, rel string, limit int) (content string, abs string, sha256hex string, err error)`; `func (c *Catalog) Files(name string) ([]string, bool /*truncated*/, error)` (relative paths, sorted, depth ≤ 3, ≤ 100 entries, symlinks excluded, `SKILL.md` included).

- [ ] **Step 1: Write failing tests**:
  - `TestReadFileOK`: `references/api.md` → content, `abs` inside skill dir, sha256 of bytes.
  - `TestReadFileRefused` (each must error, message names the reason): `""`, `/etc/passwd`, `../x`, `references/../../x`, `link/secret.md` with `link` a symlink dir, `alias.md` symlink file, `.env`, `keys/server.pem`, a FIFO, a directory, unknown skill name.
  - `TestReadFileLimit`: 100 KiB file with `limit=1000` → content truncated to the same marker convention as `read_file` (reuse `tools` truncation helper if exported; otherwise `[… truncated]`).
  - `TestFiles`: nested 5 levels → only depth ≤ 3; 150 files → 100 + truncated; symlink excluded.
  - `policy` existing tests still pass (sensitive behaviour unchanged).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** — reject absolute/`..` lexically, then walk components with `os.Lstat` from the skill dir, refuse any symlink, open the final file `O_NOFOLLOW|O_NONBLOCK`, require regular.
- [ ] **Step 4: Run** `go test ./internal/skills/ ./internal/policy/ ./internal/tools/` — PASS.
- [ ] **Step 5: Commit** `feat(skills): safe reads of skill files`.

### Task 4: Prompt section and slash expansion

**Files:**
- Create: `internal/skills/prompt.go`
- Modify: `internal/i18n/catalog.go` (keys below)
- Test: `internal/skills/prompt_test.go`

**Interfaces:**
- Consumes: `Catalog.List`, `Catalog.Load` (Task 2).
- Produces:
  - `func (c *Catalog) PromptSection(lang i18n.Lang, budget int) (section string, dropped int)` — `""` when no model-invocable skill.
  - `type Expansion struct { Text, Skill, SHA256 string }`.
  - `var ErrUnknownCommand = errors.New("unknown skill or command")`.
  - `func (c *Catalog) Expand(line string) (exp Expansion, ok bool, err error)` — `ok=false` when `line` is not a slash command (no leading `/`, or `/ ` prefix); `//text` → `ok=true`, `Text="/text"`, no skill; `/<name> args` → message of spec §5.2; unknown name → `ErrUnknownCommand`. Built-ins are NOT handled here (callers check them first).
  - `const Builtins` handled by callers: `/help`, `/quit`, `/skills`; `func IsBuiltin(name string) bool`.
  - i18n keys: `skills.prompt_header` (EN `## Available skills\nCall the `+"`skill`"+` tool with a skill's name before acting when its description matches the request.` / FR equivalent), `skills.unknown` (EN `unknown skill or command: %s`), `skills.banner` (EN `skills: %d (project: %d)`), `skills.dropped` (EN `%d skills left out of the prompt (size limit)`), `skills.builtin_conflict` (EN `skill %q shadowed by a built-in command: model use only`).
- [ ] **Step 1: Write failing tests**:
  - `TestPromptSection`: two skills, one `disable-model-invocation` → section starts with the header, lists only `- review: Review code`.
  - `TestPromptBudget`: 50 skills with 1000-char descriptions, budget 8000 → `len(section) <= 8000`, descriptions ≤ 200 chars, `dropped > 0`.
  - `TestExpand`: `/review main.go` → `Text == "Use the skill \"review\".\n<skill name=\"review\" source=\"user\" dir=\"<dir>\">\n<body>\n</skill>\nUser request: main.go"`, `Skill == "review"`, `SHA256` = Load's; without args → no `User request` line; works for a `disable-model-invocation` skill; `/nope` → `ErrUnknownCommand`; `hello` and `/ x` → `ok=false`; `//etc` → `Text == "/etc"`, `Skill == ""`.
  - `internal/i18n` key-parity test passes.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(skills): prompt section and slash expansion`.

### Task 5: The `skill` tool

**Files:**
- Create: `internal/skills/tool.go`
- Test: `internal/skills/tool_test.go`

**Interfaces:**
- Consumes: `Catalog.Get/Load/Files/ReadFile` (Tasks 2–3); `tools.Tool`, `tools.Target`, `tools.Result`, `tools.Read`.
- Produces: `func NewTool(c *Catalog, outputLimit int) tools.Tool` — `Name() == "skill"`, schema `{"type":"object","properties":{"name":{"type":"string"},"file":{"type":"string"}},"required":["name"]}`, `Risk() == tools.Read`, `Target` → `tools.Target{Command: name}` or `name + "/" + file`, no `Paths`.
- [ ] **Step 1: Write failing tests**:
  - `TestToolLoad`: `{"name":"review"}` → `Content` starts with `Skill review (user, <dir>)`, contains body, contains a `Files:` list with `SKILL.md`; `Result.Path` = abs `SKILL.md`, `Result.HashAfter` = sha256; `IsError == false`.
  - `TestToolFile`: `{"name":"review","file":"references/api.md"}` → content, `Path`, `HashAfter`.
  - `TestToolErrors`: unknown name, refused file → `IsError == true`, `Run` returns nil error.
  - `TestToolPermissions`: `policy.NewPermissions(root, rules)` with project rule `{Tool:"skill", Match:"deploy*", Decision:"deny", Project:true}` → `Check` on `deploy` = Deny, on `review` = Allow (skill dir outside project, still Allow).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(skills): skill tool`.

### Task 6: Engine input with skill metadata

**Files:**
- Modify: `internal/event/payloads.go:29-31`, `internal/engine/engine.go:140`, `internal/engine/loop.go:34-54`, `internal/ui/tui/transcript.go:148-155`
- Test: `internal/engine/engine_test.go`, `internal/engine/restore_test.go`, `internal/ui/tui/transcript_test.go`

**Interfaces:**
- Produces:
  - `UserMessagePayload{ Text string "json:text"; Typed string "json:typed,omitempty"; Skill string "json:skill,omitempty"; SkillSHA256 string "json:skill_sha256,omitempty" }` (`Typed` = what the user typed, shown in UIs when set).
  - `type Input struct { Text, Typed, Skill, SkillSHA256 string }` in `internal/engine`; `func (e *Engine) SubmitInput(ctx context.Context, sessionID string, in Input) error`; `Submit(ctx, sid, text)` = `SubmitInput(ctx, sid, Input{Text: text})`.
- [ ] **Step 1: Write failing tests**:
  - `TestSubmitInputRecordsSkill` (engine): fake provider; `SubmitInput(…, Input{Text:"T", Typed:"/review", Skill:"review", SkillSHA256:"ab"})` → provider receives user content `"T"`; emitted `UserMessage` payload has all four fields.
  - `TestRestoreSkillMessage`: restoring events with such a payload → history user message `"T"`.
  - `TestTranscriptShowsTyped` (tui): `UserMessage` with `Typed:"/review main.go"` → item text `"/review main.go"`; without `Typed` → `Text`.
  - Existing session/audit fixtures without the new fields still load (existing tests).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/engine/ ./internal/ui/... ./internal/event/` — PASS.
- [ ] **Step 5: Commit** `feat(engine): user input carries skill metadata`.

### Task 7: CLI wiring (`run.go`, headless, `tenon skills`)

**Files:**
- Modify: `cmd/tenon/run.go` (startup ~490-640, subcommand dispatch, `-p` path ~160-180), `internal/ui/headless/headless.go:54-70`
- Test: `cmd/tenon/skills_e2e_test.go` (new)

**Interfaces:**
- Consumes: `skills.Dirs`, `skills.Discover`, `Catalog.PromptSection`, `Catalog.Expand`, `skills.IsBuiltin`, `skills.NewTool` (Tasks 2–5); `engine.Input`, `SubmitInput` (Task 6).
- Produces:
  - `started.skills *skills.Catalog` (for the TUI, Task 8).
  - System prompt = `instructions.Render(…)` + `"\n\n" + PromptSection(lang, 8000)` when non-empty.
  - Startup notices: discovery notices, `skills.banner` when N > 0, `skills.dropped` when > 0, `skills.builtin_conflict` per conflict.
  - `headless.Run(ctx, eng, sid, in engine.Input, opts, stdout, stderr)` (signature changes from `prompt string`; callers updated).
  - `tenon skills`: one line per skill `<name>  <source>  <dir>  <description>` (+ `  allowed-tools: a, b` when set), then notices; `skills.none` (EN `no skills`) when empty; exit 0.
- [ ] **Step 1: Write failing e2e tests** (reuse `newEnv`; skills written under `e.vars["HOME"]/.claude/skills` and `e.wd/.tenon/skills`):
  - `TestSkillsListCommand`: `tenon skills` lists both with sources `user`/`project`.
  - `TestSkillToolEndToEnd`: fake LLM first response = tool call `skill {"name":"review"}`, then text → second request contains a tool message with the body; audit file has a `ToolResult` with `hash_after` = sha256 of `SKILL.md`; `tenon audit verify` → code 0.
  - `TestSlashSkillHeadless`: `-p "/review main.go"` → first request's user message equals the §5.2 text; session file `UserMessage` has `skill:"review"`, `typed:"/review main.go"`.
  - `TestSlashUnknownHeadless`: `-p "/nope"` → code 1, stderr contains `unknown skill or command: /nope`, `len(f.requests()) == 0`.
  - `TestSkillsInSystemPrompt`: system message contains `## Available skills` and `- review:`.
  - `TestClaudeSkillCompat`: skill dir copied from `cmd/tenon/testdata/claude-skill/` (frontmatter with `allowed-tools: Bash(./scripts/check.sh)`, `scripts/check.sh`) → listed, loadable, and a model `shell` call to its script in `-p` mode is refused as non-interactive (no approval) — proving `allowed-tools` grants nothing.
- [ ] **Step 2: Run** `go test ./cmd/tenon/ -run 'Skill|Slash'` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** full `go test -race ./...` — PASS.
- [ ] **Step 5: Commit** `feat(cli): skills at startup, tenon skills, /<name> in -p`.

### Task 8: TUI commands and completion

**Files:**
- Modify: `internal/ui/tui/model.go:54-55,358-388`, `internal/ui/tui/run.go` (Options), `internal/i18n/catalog.go` (`tui.help` mentions `/skills`)
- Test: `internal/ui/tui/skills_test.go` (new)

**Interfaces:**
- Consumes: `Catalog.List`, `Catalog.Expand`, `skills.IsBuiltin` (Tasks 2, 4); `engine.Input`/`SubmitInput` (Task 6); `started.skills` passed as `Options.Skills *skills.Catalog` (Task 7).
- Produces: `skillsCommand = "/skills"`.
- [ ] **Step 1: Write failing tests**:
  - `TestSubmitSkill`: input `/review main.go` → fake engine receives `Input{Skill:"review", Typed:"/review main.go"}`.
  - `TestSubmitUnknown`: `/nope` → notice `unknown skill or command: /nope` (FR catalog in FR tests), nothing submitted, input kept.
  - `TestSkillsCommand`: `/skills` → notice listing `review (user)` with description.
  - `TestBuiltinsWin`: skill named `help` exists → `/help` shows help, nothing submitted.
  - `TestTabCompletion`: input `/rev` + Tab → `/review `; `/x` with no match → unchanged; Tab not at a leading `/` → existing behaviour unchanged.
  - `TestHelpListsSkills`: `/help` notice contains `/review`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/ui/...` — PASS.
- [ ] **Step 5: Commit** `feat(tui): /<skill>, /skills, Tab completion`.

### Task 9: Documentation

**Files:**
- Modify: `docs/usage.md`, `docs/fr/utilisation.md` (new section "Skills" / "Skills" after "Project instructions"), `docs/security.md`, `docs/fr/securite.md`, `README.md` (feature bullet), `RELEASE.md` (nothing unless a checklist item applies)

- [ ] **Step 1: Write** the "Skills" section: folders and priority, frontmatter fields, model use (`skill` tool, prompt list), `/<name>`, `/skills`, `tenon skills`, `allowed-tools` has no effect, audit hash; FR mirror with identical facts.
- [ ] **Step 2: Write** security text: project skills = untrusted repository content like `AGENTS.md` (instruction injection possible), no permission escalation, user skills shadow project skills, scripts always need approval.
- [ ] **Step 3: Verify** links: every relative link in the edited files resolves (`grep -o '](…)'` check) and `GOFLAGS=-mod=vendor go test ./...` still passes (`TestConfigExampleLoads`).
- [ ] **Step 4: Commit** `docs: skills`.
