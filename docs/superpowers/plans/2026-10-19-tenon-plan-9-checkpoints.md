# tenon checkpoints and welcome screen — Implementation Plan (plan 9)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Checkpoints and `/rewind` (restore code written by `write_file`/`edit_file`, the conversation, or both, to the state before a chosen prompt), plus a welcome screen showing skill/agent/hook counts and all `/` commands.

**Architecture:** A new `internal/checkpoint` package holds a content-addressed store of pre-write file contents and a pure planner that turns the session's recorded writes into a rewind plan. `write_file`/`edit_file` save the pre-image through a `WithSnapshot` option before writing. The engine keeps per-session rewind state (points, writes, untracked calls, past rewinds), exposes `RewindPlan`/`Rewind`, emits `Rewound`, and replays it in `Restore`. The TUI adds Esc×2 and a three-step `/rewind`, and a richer welcome screen and `/help`.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), Bubble Tea TUI, no new dependency.

**Spec:** `docs/superpowers/specs/2026-10-19-tenon-checkpoints-design.md`

## Global Constraints

- No new dependency.
- Store: `$XDG_DATA_HOME/tenon/checkpoints` (else `$HOME/.local/share/tenon/checkpoints`), path `<first 2 hex chars>/<full sha256 hex>`, dirs 0700, files 0600, atomic write (temp file in the same dir + rename), existing content not rewritten, content sha256 must equal the name on load (`corrupted checkpoint`).
- Max copy size: 10 MiB = 10 485 760 bytes; larger files are written without a copy, result line `checkpoint skipped (file too large)`, `no_checkpoint: true`.
- Save failure → write refused: `cannot save checkpoint: <error>`.
- Only `write_file`/`edit_file` are undoable; `shell` and `mcp__*` tools of risk `write`/`exec` are counted as untracked (`untracked: true`).
- Points: main-agent `UserMessage` that is not `blocked`, has no `hook`, has no `parent_call_id`; identified by its event `seq`.
- Plan statuses: `ok`, `conflict`, `unrestorable`, `noop`; actions `restore`, `delete`. Rewound file statuses: `restored`, `deleted`, `kept`, `skipped`, `failed`, `unchanged`.
- Modes: `code`, `conversation`, `both`. Conversation available only for points after the last main-agent `ContextCompacted`.
- Rewind refused while a turn runs: `a turn is in progress` (TryLock, no wait).
- Code-only note joined to the next accepted user message: `[tenon] The user restored files to their state before: "<excerpt>"`; excerpt = first line of the prompt, cut to 80 runes.
- Journal never rewritten; `Rewound{to_seq, mode, files, untracked_calls, note}` appended; new fields `omitempty`, old events serialize as before.
- Rewind writes only paths listed in the plan; never follows a symlink (a path that became a symlink is a `conflict`).
- `[checkpoints] retention_days` global-only, default 30; `0` disables copies (writes get `no_checkpoint`, startup notice). Startup prune: copies older than `retention_days` days not referenced by a `hash_before` of any session file modified within `retention_days` days; errors are notices.
- Esc×2: two Esc presses within 1 s while no turn runs opens `/rewind`; during a turn Esc cancels as today.
- Welcome screen lines (EN/FR via i18n), wrapped at ` · ` instead of truncated; counts shown even at 0; command list built from `skills.Builtins`.
- New user-visible strings through `internal/i18n` (EN and FR). Messages outside the catalog in English. Code comments in French.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- A file the agent created, then the user deleted, then rewind: target is "absent", current is absent → `noop`, never an error — test in Task 3.
- Two writes to the same file in one turn, then rewind before that turn: target is the `hash_before` of the FIRST write, not the second — test in Task 3.
- A rewind followed by a new turn that writes the same file, then a second rewind to an earlier point: expected hash comes from the latest event (the new write), and the target from the first write after the point — test in Task 3.
- `--resume` of a session whose last event is a code-only `Rewound`: the note is attached to the next prompt exactly once — test in Task 5.
- Prompt containing control characters or a secret shown in the `/rewind` list: cleaned and masked — test in Task 7.

---

### Task 1: Checkpoint store and config

**Files:**
- Create: `internal/checkpoint/store.go`, `internal/checkpoint/store_test.go`
- Modify: `internal/config/config.go`, `internal/config/load.go`, tests in `internal/config/`

**Interfaces:**
- Produces:
  - `const MaxBytes = 10 << 20`.
  - `type Store struct{…}`; `func Open(dir string) (*Store, error)` (creates `dir` 0700); `func Dir(getenv func(string) string) string` (= `filepath.Join(config.DataDir(getenv), "checkpoints")`).
  - `func (s *Store) Save(data []byte) (hash string, err error)` — hex sha256; error `file too large` above `MaxBytes`.
  - `func (s *Store) Load(hash string) ([]byte, error)`; `func (s *Store) Has(hash string) bool` (exists and intact).
  - `var ErrCorrupted = errors.New("corrupted checkpoint")`, `ErrMissing = errors.New("missing checkpoint")`.
  - `func (s *Store) Prune(maxAge time.Duration, keep map[string]bool, now time.Time) (removed int, err error)`.
  - `config.Checkpoints{ RetentionDays int `toml:"retention_days"` }`, `Config.Checkpoints`, default 30, negative refused (`config: retention_days must be >= 0`), absent from `projectFile`.

- [ ] **Step 1: Failing tests** — `TestSaveLoad` (round trip, file mode 0600, dir 0700, path `<2>/<64>`); `TestSaveDedup` (second Save of same bytes does not change the file's mtime); `TestSaveTooLarge` (`MaxBytes+1` → error, nothing written; `MaxBytes` ok); `TestLoadCorrupted` (file content altered → `ErrCorrupted`; `Has` false); `TestLoadMissing` (`ErrMissing`); `TestSaveAtomic` (no temp file left after Save); `TestPrune` (old unreferenced removed, old referenced kept, recent kept); config: `TestCheckpointsDefault` (30), `TestCheckpointsNegative`, `TestCheckpointsProjectRefused` (`key not allowed`).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/checkpoint/ ./internal/config/` — FAIL.
- [ ] **Step 3: Implement** — `os.CreateTemp(dir2, ".tmp-*")`, `Chmod 0600`, write, `Sync`, `Rename`; Prune walks with `filepath.WalkDir`, uses file mtime.
- [ ] **Step 4: Run** — PASS. **Step 5: Commit** `feat(checkpoint): content-addressed store and [checkpoints] config`.

### Task 2: Snapshot in write/edit tools

**Files:**
- Modify: `internal/tools/fs.go`, `internal/tools/tool.go`, `internal/tools/fs_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 (the tool only calls a function).
- Produces:
  - `func WithSnapshot(save func(path string, old []byte) error) Option`.
  - `Result.NoCheckpoint bool`.
  - `const SnapshotMaxBytes = 10 << 20` in tools (same value as `checkpoint.MaxBytes`; tools must not import checkpoint).

Behaviour: `write_file` and `edit_file`, when the file exists, call `save(p, old)` before writing if `len(old) <= SnapshotMaxBytes`; `save` error → `fail("cannot save checkpoint: %v", err)`, file untouched; larger file → no call, `NoCheckpoint = true`, content gets `"\ncheckpoint skipped (file too large)"`. New file → no call. Without the option: behaviour unchanged, `NoCheckpoint` false.

- [ ] **Step 1: Failing tests** — `TestSnapshotWrite` (save receives the exact old bytes, before the file changes); `TestSnapshotEdit` (same for edit); `TestSnapshotNewFile` (no call); `TestSnapshotError` (save returns error → result error `cannot save checkpoint: boom`, file unchanged); `TestSnapshotTooLarge` (file of `SnapshotMaxBytes+1` → no call, written, `NoCheckpoint`, content line present); `TestNoSnapshotOption` (unchanged behaviour).
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test ./internal/tools/` (PASS).
- [ ] **Step 5: Commit** `feat(tools): save the pre-write content before write_file and edit_file`.

### Task 3: Rewind planner

**Files:**
- Create: `internal/checkpoint/plan.go`, `internal/checkpoint/plan_test.go`

**Interfaces:**
- Produces (pure, no engine import):
  - `type Point struct{ Seq uint64; Excerpt string; HistoryLen int; Conversation bool }`.
  - `type Write struct{ Seq uint64; Path, Before, After string; NoCheckpoint bool }` — `After == ""` means the file is absent after the event (used by recorded rewind deletions).
  - `type Untracked struct{ Seq uint64; Tool string }`.
  - `type State struct{ Points []Point; Writes []Write; Untracked []Untracked }` (append order = seq order).
  - `type FileState struct{ Exists, Symlink bool; Hash string }`.
  - `type FilePlan struct{ Path, Action, Status, Target, Expected string }` (`Action` `restore`|`delete`; `Status` `ok`|`conflict`|`unrestorable`|`noop`).
  - `type Plan struct{ Point Point; Files []FilePlan; UntrackedCalls int; ConversationAvailable bool }`.
  - `var ErrUnknownPoint = errors.New("unknown checkpoint")`.
  - `func BuildPlan(st State, toSeq uint64, current func(path string) FileState, has func(hash string) bool) (Plan, error)` — files sorted by path.

Rules: for each path with a write of `Seq > toSeq`: `Target` = `Before` of its first such write; `Action` = `delete` if `Target == ""` else `restore`; `Expected` = `After` of its last write overall. Status, in order: `noop` if current already equals target (absent for delete, same hash for restore); `conflict` if `current.Symlink`, or current ≠ expected (absent when `Expected == ""`); `unrestorable` if `Action == restore` and (`!has(Target)` or any write of `Seq > toSeq` on that path has `NoCheckpoint`); else `ok`.

- [ ] **Step 1: Failing tests** (table, fake `current`/`has`):
  - created after point → `delete`/`ok`; created then user-deleted → `noop` (Review Focus 1);
  - modified once → `restore` to `Before`; modified twice after point → target = first `Before` (Review Focus 2);
  - user changed the file → `conflict`; replaced by symlink → `conflict`; user deleted a modified file → `conflict`;
  - missing copy → `unrestorable`; `NoCheckpoint` → `unrestorable`;
  - writes before or at `toSeq` ignored; untracked counted only after `toSeq`;
  - rewind recorded as writes then new write then second plan → expected from the new write, target from the first write after the new point (Review Focus 3);
  - `ConversationAvailable` copied from the point; unknown `toSeq` → `ErrUnknownPoint`.
- [ ] **Step 2–4:** run (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(checkpoint): rewind planner`.

### Task 4: Events

**Files:**
- Modify: `internal/event/event.go`, `internal/event/payloads.go`, `internal/event/event_test.go`

**Interfaces:**
- Produces:
  - Type `Rewound`; `RewoundPayload{ ToSeq uint64 `json:"to_seq"`; Mode string `json:"mode"`; Files []RewoundFile `json:"files,omitempty"`; UntrackedCalls int `json:"untracked_calls,omitempty"`; Note string `json:"note,omitempty"` }`; `RewoundFile{ Path `json:"path"`; Action `json:"action"`; Status `json:"status"`; Hash string `json:"hash,omitempty"` /* content hash after the rewind, "" if deleted or unchanged */; Error string `json:"error,omitempty"` }`.
  - `ToolResultPayload.NoCheckpoint bool `json:"no_checkpoint,omitempty"``, `.Untracked bool `json:"untracked,omitempty"``.

- [ ] **Step 1–4:** JSON tests for the new payload, omitempty of the new ToolResult fields (old payload JSON unchanged); implement; PASS.
- [ ] **Step 5: Commit** `feat(event): Rewound event and checkpoint fields`.

### Task 5: Engine — rewind state, plan, rewind, resume

**Files:**
- Create: `internal/engine/rewind.go`, `internal/engine/rewind_test.go`
- Modify: `internal/engine/engine.go` (`Deps`, `session`), `internal/engine/loop.go` (record points/writes/untracked, pending note), `internal/engine/restore.go`, `internal/engine/restore_test.go`

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces:
  - `Deps.Checkpoints *checkpoint.Store` (nil: no rewind of code — plan files all `unrestorable` for `restore`).
  - `type RewindMode string`: `RewindCode = "code"`, `RewindConversation = "conversation"`, `RewindBoth = "both"`.
  - `func (e *Engine) RewindPoints(sessionID string) []checkpoint.Point` (most recent first).
  - `func (e *Engine) RewindPlan(sessionID string, toSeq uint64) (checkpoint.Plan, error)`.
  - `type RewindResult struct{ Files []event.RewoundFile; Prompt string /* full text, conversation modes */; Note string }`.
  - `func (e *Engine) Rewind(ctx context.Context, sessionID string, toSeq uint64, mode RewindMode, overwrite []string) (RewindResult, error)`; errors `ErrTurnInProgress = errors.New("a turn is in progress")`, `ErrConversationUnavailable = errors.New("conversation cannot be rewound past a compaction")`.
  - `ToolResult` events carry `NoCheckpoint` (from `tools.Result`) and `Untracked` (tool is `shell`, or name has prefix `mcp__` and risk is `tools.Write` or `tools.Exec`, and the tool ran).

Recording: at each accepted main `UserMessage` emit, append a Point (`HistoryLen` = len(history) before appending the message, `Conversation = true`; on main `ContextCompacted`, set `Conversation = false` on all earlier points). At each `ToolResult` with `Path != "" && HashAfter != ""` append a Write; untracked → append Untracked. On `Rewound`, append one Write per file with status `restored` (`Before` = previous expected, `After` = `Hash`) or `deleted` (`After = ""`).

Execution (`Rewind`): `s.turn.TryLock()` else `ErrTurnInProgress`; plan recomputed with `current` = Lstat + sha256 (`Symlink` from mode), `has` = `Store.Has`; for `code`/`both`: each `ok` file, and `conflict` files whose path is in `overwrite`: `restore` → `Store.Load` then temp file in the target dir + rename, mode of the existing file else 0644, `MkdirAll` parents 0755; `delete` → `os.Remove` (not-exist ok); statuses `restored`/`deleted`/`failed`(+error); `conflict` not chosen → `kept`; `unrestorable` → `skipped`; `noop` → `unchanged`. For `conversation`/`both`: refuse if `!ConversationAvailable`; history truncated to `HistoryLen`; `Prompt` = the point's full user text. For `code` only: `Note` = the code-only note, stored as pending on the session (joined into the next accepted user message through the same mechanism as the pending SessionStart context: `withHookContext`). Emit `Rewound` last.

Restore: replay points/writes/untracked/rewinds exactly as live; a `Rewound` with conversation modes truncates the rebuilt history to the point's `HistoryLen`; a code-only `Rewound` sets the pending note, consumed (and recorded in `hook_context`) by the next accepted `UserMessage`.

- [ ] **Step 1: Failing tests**:
  - `TestRewindCodeRestoresAndDeletes`: fake provider writes `a.txt` (modify) and creates `b.txt`; rewind `code` to the first prompt → `a.txt` original, `b.txt` absent, `Rewound` statuses `restored`/`deleted`, history unchanged, next request contains the note.
  - `TestRewindConflictKeptOrOverwritten`: user edits `a.txt` after the turn → plan `conflict`; rewind without overwrite → `kept`, file untouched; with overwrite → `restored`.
  - `TestRewindConversation`: two prompts; rewind `conversation` to the second → history = before the second prompt, `Prompt` returned, files untouched.
  - `TestRewindBoth`, `TestRewindConversationAfterCompaction` (`ErrConversationUnavailable`), `TestRewindTurnInProgress` (`ErrTurnInProgress`), `TestRewindPartialFailure` (a read-only dir makes one restore fail → `failed`, others restored).
  - `TestWriteRefusedWhenCheckpointFails` (store dir made read-only → write tool error `cannot save checkpoint`).
  - `TestUntrackedCounted` (a `shell` call after the point → `UntrackedCalls == 1`, `ToolResult.untracked`).
  - `TestSubagentWritesRewound` (a sub-agent write is restored).
  - `TestRestoreAfterRewound`: for each mode, restored history and pending note equal the live ones; code-only note attached once to the next prompt after `--resume` (Review Focus 4); restored rewind state lets a second rewind compute the same plan as live.
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test -race ./internal/engine/` (PASS).
- [ ] **Step 5: Commit** `feat(engine): checkpoints, rewind plan and rewind`.

### Task 6: CLI wiring and `tenon rewind --list`

**Files:**
- Create: `cmd/tenon/rewind.go`, `cmd/tenon/rewind_e2e_test.go`
- Modify: `cmd/tenon/run.go`, `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: Task 1 (`checkpoint.Open`, `Dir`, `Prune`), Task 2 (`tools.WithSnapshot`), Task 5 (`Deps.Checkpoints`, `RewindPoints`).
- Produces:
  - `startEngine`: when `cfg.Checkpoints.RetentionDays > 0`, open the store, prune (keep = all `hash_before` of session files with mtime within retention; errors → notice `checkpoints.prune_failed`), pass `tools.WithSnapshot(func(_ string, old []byte) error { _, err := store.Save(old); return err })` to `tools.FS`, and `Deps.Checkpoints`; `0` → notice `checkpoints.disabled`, no option.
  - `tenon rewind --list --resume <id|last> [--lang fr|en]`: restores the session read-only (no lock taken for writing — use `session.Load` and a throwaway `engine.Engine` with `Restore`), prints `#<n>  <seq>  <excerpt>` plus ` (code only)` / ` (code seul)` when the conversation is not available, most recent first; `--list` required (else `err.unexpected_args`); no session → existing errors.
  - `rewind` added to the subcommand `--lang` handling and the `--resume` guard like `sessions`.
  - i18n EN/FR: `checkpoints.disabled` `"checkpoints disabled (retention_days = 0): /rewind cannot restore files"` / `"points de restauration désactivés (retention_days = 0) : /rewind ne peut pas restaurer les fichiers"`; `checkpoints.prune_failed` `"checkpoint cleanup failed: %v"` / `"nettoyage des points de restauration en échec : %v"`; `rewind.code_only` `"(code only)"` / `"(code seul)"`; `usage` gains `tenon rewind --list --resume <id|last>`.

- [ ] **Step 1: Failing e2e tests** — `TestRewindListE2E` (session with two prompts → two lines, most recent first); `TestCheckpointSavedE2E` (`-p` run whose fake provider edits a file → a file exists in `checkpoints/` with the old content); `TestCheckpointsDisabledE2E` (`retention_days = 0` → notice on stderr, no store dir).
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test -race ./cmd/...` (PASS).
- [ ] **Step 5: Commit** `feat(cli): checkpoint store, pruning and tenon rewind --list`.

### Task 7: TUI — `/rewind` and Esc×2

**Files:**
- Create: `internal/ui/tui/rewind.go`, `internal/ui/tui/rewind_test.go`
- Modify: `internal/ui/tui/model.go`, `internal/ui/tui/keys.go`, `internal/ui/tui/view.go`, `internal/skills/prompt.go` (`Builtins` gains `rewind`), `internal/skills/prompt_test.go`, `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: `Engine.RewindPoints`, `RewindPlan`, `Rewind` (Task 5) — add them to the TUI `Engine` interface.
- Produces: rewind overlay with three steps (points → preview+mode → conflicts), result notice, prompt restored into the input on conversation modes.

Details: Esc while idle records the time; a second Esc within 1 s opens the overlay (a single Esc keeps today's idle behaviour). In the overlay: ↑/↓ move, Enter selects, Esc closes (or goes back one step). Preview lines: `restore <path>`, `delete <path>`, `conflict: modified since <path>`, `unrestorable <path>`, `unchanged <path>`; warning `N shell or MCP calls are not undone`; modes listed in the order `code and conversation`, `conversation only`, `code only`, unavailable ones dimmed and not selectable; no file to act on and conversation unavailable → notice `nothing to rewind`. Conflicts: one question per file `overwrite <path>? y/N`, default N. Result notice: `rewind: %d restored · %d deleted · %d kept · %d failed`, then one line per failure. Excerpts, paths and errors through `spaceClean` and `Redact`. i18n keys EN/FR for every string above (prefix `tui.rewind.`).

- [ ] **Step 1: Failing tests** — Esc×2 within 1 s opens, Esc + 2 s + Esc does not, Esc during a turn cancels and does not open; `/rewind` opens; preview renders each status; unavailable mode not selectable; conflict `y` adds the path to `overwrite`, Enter/`n` keeps; conversation mode puts the prompt in the input; result notice counts; prompt with `\x1b[31m` and a redacted secret in the list is cleaned and masked (Review Focus 5); `/rewind` completes with Tab.
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test -race ./internal/ui/tui/ ./internal/skills/` (PASS).
- [ ] **Step 5: Commit** `feat(tui): /rewind and Esc twice`.

### Task 8: Welcome screen and `/help`

**Files:**
- Modify: `internal/ui/tui/welcome.go`, `internal/ui/tui/model.go` (help), tests `internal/ui/tui/welcome_test.go` (create if absent), `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: `Options.Skills`, `Options.Agents`, `Options.Hooks` (existing), `skills.Builtins`.
- Produces: three lines under the project line, and a new `/help` body.

Lines (i18n EN/FR):
- `tui.welcome.counts` `"skills: %d · agents: %d · hooks: %d"` / `"skills : %d · agents : %d · hooks : %d"` — skills = number of skills `/skills` lists, agents = `len(Agents.List())` (built-in included), hooks = `len(Hooks.List())`; nil → 0.
- `tui.welcome.commands` `"commands: %s"` / `"commandes : %s"` — `/` + each of `skills.Builtins`, joined by ` · `.
- `tui.welcome.hints` becomes `"Enter to send · Esc twice to rewind · Ctrl+C to quit"` / `"Entrée pour envoyer · Échap ×2 pour revenir en arrière · Ctrl+C pour quitter"`.
- These three lines wrap at ` · ` boundaries to the frame width (continuation lines indented to align after the label) instead of being truncated; other lines keep today's truncation.
- `/help`: keys line kept (`tui.help` updated to mention Esc twice and `/rewind`), then one line per builtin: `/<name>  <description>` with keys `tui.help.cmd.<name>` (EN/FR) for help, quit, skills, agents, hooks, rewind.

- [ ] **Step 1: Failing tests** — counts with 2 skills, nil agents (0) and 1 hook; commands line lists every `skills.Builtins` entry (adding a fake entry in a test copy appears); width 50 → commands line wraps, no `…` on those lines; `/help` lists every builtin with a description, EN and FR.
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test -race ./internal/ui/tui/` (PASS).
- [ ] **Step 5: Commit** `feat(tui): welcome screen counts and commands, /help command list`.

### Task 9: Documentation

**Files:**
- Modify: `docs/usage.md`, `docs/fr/utilisation.md`, `docs/security.md`, `docs/fr/securite.md`, `README.md`, `docs/config.example.toml`

- [ ] **Step 1:** usage (EN/FR): section "Checkpoints and rewind" / "Points de restauration" after "Hooks": what is saved (write/edit only; shell/MCP not undone), when (each prompt), `/rewind` and Esc×2 (three steps, statuses, conflicts default keep), modes and the compaction limit, code-only note, `tenon rewind --list`, `[checkpoints] retention_days`, store location and size limit, `Rewound` event. Interface section: the new welcome lines and `/help`.
- [ ] **Step 2:** security (EN/FR): store holds raw file contents (secrets included), 0700/0600, never sent to the provider nor written to the logs, how to delete it; rewind writes only paths the tools wrote and never follows symlinks; rewind is user-only.
- [ ] **Step 3:** README feature list; `docs/config.example.toml` commented `[checkpoints]` (`TestConfigExampleLoads` passes).
- [ ] **Step 4: Run** `GOFLAGS=-mod=vendor go test ./internal/config/` — PASS. **Step 5: Commit** `docs: checkpoints, rewind and welcome screen`.
