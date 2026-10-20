# tenon compliance export — Implementation Plan (plan 10)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `tenon audit export` writes a self-contained HTML report and a structured JSON report from the hash-chained audit log, for a DPO, CISO or auditor: providers and jurisdictions, redactions, permissions, modified files, sub-agents/hooks/rewinds, incidents, chain integrity, regulatory mapping, and an opt-in conversation timeline.

**Architecture:** `internal/audit` gains a read-only line iterator. A new `internal/compliance` package loads the log (verify, scope, period, level), builds a pure `Report` model (all texts redacted and cleaned there), and renders it as JSON and as HTML (`html/template` + `embed`). `cmd/tenon` parses the options, writes both files atomically-or-not-at-all, and maps the result to exit codes 0/1/2.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), standard library only (`html/template`, `embed`, `encoding/json`, `crypto/sha256`).

**Spec:** `docs/superpowers/specs/2026-10-20-tenon-audit-export-design.md`

## Global Constraints

- No new dependency.
- The audit log is never modified, chmod'ed or locked by the export (do not call `audit.Open`).
- Dates: UTC, format `YYYY-MM-DD`, both bounds inclusive; default `--until` = today UTC, `--since` = `--until` − 29 days (30 days).
- Output files: `tenon-audit-<since>_<until>.html` and `.json` in `--out` (default: working directory), mode 0600, created with `O_EXCL`; if either exists, nothing is written.
- Exit codes: 0 success; 1 usage or write error; 2 report written but chain broken.
- `--project`, `--all-projects`, `--session` are mutually exclusive (usage error).
- Without `--details`: no prompt, no model response, no tool argument, no tool result content in either file (modified file paths and tool names stay).
- With `--details`: responses and tool results cut to the first 500 runes, AFTER redaction.
- Every text taken from the log goes through the current redactor (`loadSetup(...).redactor`, global + project config), then control and format characters (`unicode.IsControl`, `unicode.Cf`) become spaces.
- HTML: `html/template`, inline CSS, no `<script>`, no external resource, `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`.
- `report_id` = `"sha256:" + hex(sha256(json.Marshal(report)))`; HTML shows it; JSON holds `report_id` and `html_sha256` (`"sha256:" + hex` of the HTML bytes written).
- JSON: English keys, `schema_version: 1`, `generator: "tenon <version>"`, dates RFC 3339 UTC.
- Disclaimer (exact): FR « Ce rapport décrit l'activité enregistrée par tenon. Il ne constitue pas un avis juridique et ne certifie aucune conformité. » / EN "This report describes the activity recorded by tenon. It is not legal advice and does not certify compliance."
- Regulatory table: no article numbers.
- New user-visible strings through `internal/i18n` (EN and FR). Code comments in French.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- A secret that straddles the 500-rune cut in `--details`: redaction runs on the full text first, so no partial secret survives — test in Task 4.
- A session started before `--since` whose later events fall in the period: it belongs to its project (first `SessionStarted`, outside the period) and its provider is reported — test in Task 2.
- A log at `metadata` level with the default project scope: sessions are excluded (project unknown), the report says how many, no crash on undecodable payloads — test in Task 2 and Task 3.
- Writing fails on the second file (`.json` exists, or out dir becomes read-only after the HTML): the HTML just written is removed, exit 1 — test in Task 7.
- A path or hook reason containing `</style>` or `<script>` in the HTML: escaped, and the CSP stays first in `<head>` — test in Task 6.

---

### Task 1: `audit.ReadLines`

**Files:**
- Modify: `internal/audit/writer.go`, `internal/audit/verify.go`
- Create: `internal/audit/read.go`, `internal/audit/read_test.go`

**Interfaces:**
- Produces:
  - Rename the unexported `line` to exported `Line` (same fields, same order, same JSON tags — the hash depends on them); all existing uses updated.
  - `func ReadLines(dir string, fn func(file string, lineNo int, l Line, err error) error) error` — files in `listFiles` order, lines numbered from 1, empty lines skipped; `err` non-nil = unparsable line (`l` zero); a non-nil return from `fn` stops and is returned; missing dir → `nil`, no call; other dir/open/read errors returned. Read-only (`os.Open`).

- [ ] **Step 1: Failing tests** — `TestReadLinesOrder` (two files written by `Writer`, lines delivered in chain order with `file`, `lineNo`, `Hash`); `TestReadLinesUnreadable` (a garbage line → callback with `err != nil`, next lines still delivered); `TestReadLinesMissingDir` (nil, zero calls); `TestReadLinesStop` (fn returns sentinel → returned); `TestReadLinesNoWrite` (file mode and mtime unchanged after reading).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/audit/` — FAIL.
- [ ] **Step 3: Implement** — `bufio.Reader.ReadBytes('\n')` like `Verify` (no line-length limit).
- [ ] **Step 4: Run** — PASS (existing audit tests too). **Step 5: Commit** `feat(audit): read-only line iterator`.

### Task 2: `compliance.Load`

**Files:**
- Create: `internal/compliance/load.go`, `internal/compliance/load_test.go`, `internal/compliance/testlog_test.go` (helper writing a real chained log through `audit.Writer` from a list of `event.Event`)

**Interfaces:**
- Consumes: `audit.ReadLines`, `audit.Line`, `audit.Verify`, `session.SameRoot`, `event.SessionStartedPayload`.
- Produces:
  - `type Filter struct{ Since, Until time.Time; Project string; AllProjects bool; Session string }` — `Since`/`Until` are UTC midnights; lines with `Since <= ts < Until+24h` are in the period.
  - `type Chain struct{ Intact bool; Error string; Lines int; FinalHash string }` — from `audit.Verify` (`Error` = its error text; `FinalHash` empty when broken).
  - `type Unreadable struct{ File string `json:"file"`; Line int `json:"line"`; Error string `json:"error"` }`.
  - `type SessionInfo struct{ Started *event.SessionStartedPayload /* first SessionStarted, nil at metadata level or if absent */ }`.
  - `type Dataset struct{ Filter Filter; Chain Chain; Lines []audit.Line; Unreadable []Unreadable; Sessions map[string]SessionInfo; Level string; ExcludedMetadataSessions int }`.
  - `const LevelFull = "full"`, `LevelMetadata = "metadata"`, `LevelMixed = "mixed"`, `LevelNone = "none"` (no line in range).
  - `func IsMetadataPayload(p json.RawMessage) bool` — object with exactly the keys `sha256` (64 lowercase hex) and `size` (number).
  - `func Load(dir string, f Filter) (Dataset, error)` — error only when `ReadLines` fails.

Rules: every parsed line updates `Sessions` (first `SessionStarted` per session wins, even outside the period); lines in the period and matching `Session` (if set) are candidates; then scope: `Session` set → keep; `AllProjects` → keep; else keep sessions whose `Started.ProjectRoot` `SameRoot` `Project`; a candidate session with `Started == nil` because its first `SessionStarted` is a metadata payload is excluded and counted in `ExcludedMetadataSessions`. `Level` over kept lines. Unparsable lines (JSON, or `ts` not RFC 3339) go to `Unreadable` whatever the scope.

- [ ] **Step 1: Failing tests** — period bounds inclusive (events at `Since 00:00:00` and `Until 23:59:59.999` kept, `Until+1d 00:00:00` and `Since−1ns` dropped); project scope keeps only sessions of `Project` (`SameRoot` with a trailing slash variant); `AllProjects`; `Session`; session started before `Since` with events in period is kept and `Sessions[sid].Started` is set (Review Focus 2); metadata log + project scope → no line, `ExcludedMetadataSessions == 1` (Review Focus 3); metadata log + `AllProjects` → lines kept, `Level == "metadata"`; mixed → `"mixed"`; empty → `"none"`; garbage line → `Unreadable{File, Line}` and `Chain.Intact == false`; missing dir → empty dataset, `Chain{Intact: true, Lines: 0, FinalHash: "sha256:0"}`; `IsMetadataPayload` table (exact keys only; extra key → false).
- [ ] **Step 2–4:** run `GOFLAGS=-mod=vendor go test ./internal/compliance/` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(compliance): load and scope the audit log`.

### Task 3: Report model and `Build` — header to commands

**Files:**
- Create: `internal/compliance/report.go` (types), `internal/compliance/build.go`, `internal/compliance/build_test.go`

**Interfaces:**
- Consumes: Task 2.
- Produces (JSON tags = the stable schema; `time.Time` fields marshal as RFC 3339 UTC):
  - `type Options struct{ Redact func(string) string; Details bool; Generator string; Now time.Time }` — `Redact` nil = identity.
  - `type Report struct{ Generator string `json:"generator"`; GeneratedAt time.Time `json:"generated_at"`; Period Period `json:"period"`; Scope Scope `json:"scope"`; Details bool `json:"details"`; Disclaimer string `json:"disclaimer"`; Integrity Integrity `json:"integrity"`; ContentAvailable bool `json:"content_available"`; ExcludedMetadataSessions int `json:"excluded_metadata_sessions"`; Summary Summary `json:"summary"`; Providers []ProviderUse `json:"providers"`; Consents Consents `json:"jurisdiction_consents"`; WarningSessions []string `json:"jurisdiction_warning_sessions"`; MCPServers []MCPUse `json:"mcp_servers"`; MCPFailures []MCPFailure `json:"mcp_failures"`; Redactions map[string]int `json:"redactions"`; Permissions []ToolPermissions `json:"permissions"`; Files []FileChange `json:"files"`; Commands []SessionCommands `json:"commands"`; Subagents []SubagentRun `json:"subagents"`; Hooks []HookIssue `json:"hooks"`; Rewinds []RewindUse `json:"rewinds"`; Incidents Incidents `json:"incidents"`; Mapping []MappingRow `json:"regulatory_mapping"`; GitHint string `json:"git_marking_hint"`; Timeline []SessionTimeline `json:"timeline,omitempty"` }`.
  - `Period{Since, Until string}` (`since`, `until`, `YYYY-MM-DD`); `Scope{Kind, Project, Session string}` (`kind` = `project`|`all`|`session`, `project`/`session` omitempty).
  - `Integrity{Intact bool; Error string; TotalLines int; FinalHash string; RangeLines int; FirstHash, LastHash string; Level string}` (`intact`, `error,omitempty`, `total_lines`, `final_hash,omitempty`, `range_lines`, `first_hash,omitempty`, `last_hash,omitempty`, `level`).
  - `Summary{Sessions, Turns, ToolCalls, FilesModified, Denials int; Jurisdictions []string}` (`sessions`, `turns`, `tool_calls`, `files_modified`, `denials`, `jurisdictions` — distinct provider jurisdictions, sorted).
  - `ProviderUse{ID, Name, Model, Country, Jurisdiction string; Sessions int; First, Last time.Time}` (`id`, `name`, `model`, `country`, `jurisdiction`, `sessions`, `first`, `last`) — grouped by (ID, Model); First/Last = first/last in-range line of those sessions.
  - `Consents{Granted, Denied int}` (`granted`, `denied`).
  - `MCPUse{ID, Jurisdiction, Country string; Sessions int}`; `MCPFailure{Session, Server, Error string; TS time.Time}` (`session`, `server`, `error`, `ts`).
  - `ToolPermissions{Tool string; Requested, AllowedOnce, AllowedSession, DeniedUser, DeniedPolicy, DeniedHook int}` (`tool`, `requested`, `allowed_once`, `allowed_session`, `denied_user`, `denied_policy`, `denied_hook`), sorted by tool.
  - `FileChange{TS time.Time; Session, Path, Tool, HashBefore, HashAfter, Author string; NoCheckpoint bool; RewindStatus string}` (`ts`, `session`, `path`, `tool`, `hash_before`, `hash_after`, `author`, `no_checkpoint`, `rewind_status,omitempty`) — `Author` = `"main"` or the sub-agent's agent name (`SubagentStarted.agent` whose `call_id` = the result's `parent_call_id`; unknown → `"subagent"`); `RewindStatus` = status of the last later `Rewound` file entry with that path in that session.
  - `SessionCommands{Session string; Count int; Commands []string}` (`session`, `count`, `commands,omitempty`) — `shell` `ToolCallRequested`; `Commands` (args `command`) only with `Details`.
  - `const Disclaimer = "This report describes the activity recorded by tenon. It is not legal advice and does not certify compliance."`, `const GitHint = "git log --grep \"AI-Assisted-By\""`.
  - `func Build(ds Dataset, o Options) Report` (pure; slices never nil — empty `[]`; maps never nil).

Counting rules: `Turns` = `TurnCompleted`; `ToolCalls` = `ToolCallRequested`; `FilesModified` = distinct (session, path) in `Files`; `Denials` = `ToolResult` with `denied` ∈ {`user`, `policy`, `hook`}. Permissions: `PermissionRequested{kind:"tool"}` per tool → `requested`; its `PermissionResolved` (same `permission_id`) → `allowed && remember` = session, `allowed` = once; `denied_*` from `ToolResult.denied`. Consents: `PermissionResolved` of a `kind:"jurisdiction"` request. `ContentAvailable` = `Level != metadata`; when false every content slice stays empty and `Summary` keeps only `Sessions`. At `mixed`, metadata payloads are skipped. All strings from payloads pass through `clean(s) = spaceClean(o.Redact(s))`.

- [ ] **Step 1: Failing tests** (logs built with the Task 2 helper, `AllProjects`):
  - integrity copies `Chain`, `RangeLines`, first/last hash of `ds.Lines`, `Level`;
  - summary counts on a session with 2 turns, 3 tool calls, 1 policy denial, 1 user denial;
  - provider grouped across 2 sessions with First/Last; jurisdictions `["eu","non-eu"]`; consent granted and denied; warning session listed; MCP server and failure;
  - redactions summed across two `RedactionApplied`;
  - permissions: once, session, user/policy/hook denials per tool;
  - files: main author, sub-agent author with its agent name, `no_checkpoint`, `rewind_status` `restored` after a later `Rewound`;
  - commands: count without details, texts with details;
  - redaction at build: a raw `sk-ant-...` key in a file path and in an `Error` message is masked (use a real `policy.NewRedactor(nil)`); `\x1b[31m` in a path → spaces;
  - metadata dataset → `ContentAvailable == false`, content slices empty, JSON of the report has `"files":[]` not `null`.
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test ./internal/compliance/` (PASS).
- [ ] **Step 5: Commit** `feat(compliance): report model, summary, providers, permissions, files`.

### Task 4: `Build` — sub-agents, hooks, rewinds, incidents, mapping, timeline

**Files:**
- Modify: `internal/compliance/report.go`, `internal/compliance/build.go`, `internal/compliance/build_test.go`

**Interfaces:**
- Consumes: Task 3.
- Produces:
  - `SubagentRun{TS time.Time; Session, CallID, Agent, Status string}` (`ts`, `session`, `call_id`, `agent`, `status` — from `SubagentCompleted`, `""` while not completed).
  - `HookIssue{TS time.Time; Session, HookID, Event, Status, Decision, Reason string}` — `HookCompleted` with status `blocked`, `error`, `timeout`, or decision `ask` (`reason`, `decision` omitempty).
  - `RewindUse{TS time.Time; Session string; ToSeq uint64; Mode string; Restored, Deleted, Kept, Skipped, Failed, Unchanged, UntrackedCalls int}` (snake_case keys).
  - `Incidents{Errors []ErrorIncident; Turns []TurnIncident; Unreadable []Unreadable}` (`errors`, `turns`, `unreadable`); `ErrorIncident{TS; Session, Code, Message string}`; `TurnIncident{TS; Session, Reason string}` for reasons `blocked`, `blocked_by_hook`, `stopped_by_hook`, `context_too_large`, `max_iterations`, `incomplete`, `cancelled`. `Unreadable` = `ds.Unreadable` (also at metadata level).
  - `MappingRow{Element string; Frameworks []string}` (`element`, `frameworks`), fixed rows in this order: `providers_jurisdictions_consents` → `["GDPR"]`; `redactions_permissions` → `["GDPR","NIS2"]`; `audit_chain_incidents` → `["NIS2","DORA"]`; `ai_usage_transparency` → `["AI Act"]`.
  - `SessionTimeline{Session string; Entries []TimelineEntry}`; `TimelineEntry{TS time.Time; Kind, Tool, Text string}` (`ts`, `kind`, `tool,omitempty`, `text`); kinds `prompt` (`UserMessage.text`, full), `response` (`AssistantMessage.text`), `tool_call` (tool + compact args), `tool_result` (tool + content), `permission` (`PermissionRequested` tool/command + resolution `allowed`/`denied`), `hook` (`HookCompleted` hook_id + status). `response`, `tool_call` and `tool_result` texts cut to 500 runes after `clean`, `…` appended when cut. Only with `Details`; sessions in first-line order, entries in log order.

- [ ] **Step 1: Failing tests** — sub-agent run with status; hook issues filter (an `ok` hook absent, `ask` present); rewind counts by status; error and turn incidents (a `completed`/empty reason turn absent); unreadable lines copied; mapping rows exact; timeline absent without details (and `json.Marshal` has no `"timeline"` key); with details: all kinds in order, a 600-rune response cut to 500 runes + `…`; a secret starting at rune 495 of a tool result is fully masked, no prefix of it left (Review Focus 1).
- [ ] **Step 2–4:** run (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(compliance): sub-agents, hooks, rewinds, incidents, regulatory mapping, timeline`.

### Task 5: `ReportID` and `RenderJSON`

**Files:**
- Create: `internal/compliance/json.go`, `internal/compliance/json_test.go`, `internal/compliance/testdata/report.golden.json`

**Interfaces:**
- Produces:
  - `const SchemaVersion = 1`.
  - `func ReportID(r Report) (string, error)` — `"sha256:" + hex(sha256(json.Marshal(r)))`.
  - `func RenderJSON(r Report, reportID, htmlSHA256 string) ([]byte, error)` — `json.MarshalIndent` (two spaces) of `{schema_version, generator, report_id, html_sha256, report}` in that key order, trailing newline.

- [ ] **Step 1: Failing tests** — golden file of a fixed report (fixed `Now`, `-update` flag regenerates); `ReportID` stable across two calls and changes when one count changes; `RenderJSON` output's `report` re-marshalled gives the same `ReportID`; `schema_version` is `1`.
- [ ] **Step 2–4:** run (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(compliance): JSON report and report id`.

### Task 6: `RenderHTML` and i18n

**Files:**
- Create: `internal/compliance/html.go`, `internal/compliance/report.html.tmpl`, `internal/compliance/html_test.go`
- Modify: `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: Tasks 3–5, `i18n.T`.
- Produces: `func RenderHTML(r Report, reportID string, lang i18n.Lang) ([]byte, error)` — template parsed once from `embed`; funcs `t` (i18n key, args…), `date` (`2006-01-02 15:04 UTC`), `day`; `<html lang="fr|en">`, `<meta charset="utf-8">` then the CSP meta as the first two `<head>` children.

Layout (sections in spec §5 order): header (title, period, scope, generated at, generator, level, `report_id`, integrity line, disclaimer); banners at the top when they apply: broken chain (red), metadata level, `--details`, excluded metadata sessions (`N sessions écartées : projet inconnu au niveau metadata`); no line in range → `aucune activité sur la période` and the content sections are not rendered; `ContentAvailable == false` → each content section shows `non disponible au niveau metadata`; empty section → `aucun`. Print CSS (`@media print`) keeps tables unbroken per row. Git paragraph with `GitHint` in `<code>`.

i18n keys (prefix `report.`), EN/FR, exact for these:
- `report.title` "tenon activity report" / "Rapport d'activité tenon"; `report.disclaimer` = Global Constraints disclaimer.
- `report.chain.ok` "audit chain intact: %d lines, final hash %s" / "chaîne intacte : %d lignes, hash final %s"; `report.chain.broken` "audit log tampered: %s" / "journal altéré : %s".
- `report.banner.metadata` "audit log at metadata level: content not available" / "journal au niveau metadata : contenu non disponible"; `report.banner.details` "This report contains conversation data." / "Ce rapport contient des données de conversation."; `report.banner.excluded` "%d sessions excluded: project unknown at metadata level" / "%d sessions écartées : projet inconnu au niveau metadata".
- `report.empty` "no activity in this period" / "aucune activité sur la période"; `report.unavailable` "not available at metadata level" / "non disponible au niveau metadata"; `report.none` "none" / "aucun".
- `report.map.<element>` for the four mapping rows: FR « Fournisseurs, juridictions, consentements → registre des traitements et transferts de données (RGPD) », « Masquages, permissions → mesures techniques de protection (RGPD, NIS2) », « Chaîne d'audit vérifiée, incidents → traçabilité et gestion des incidents (NIS2, DORA) », « Fournisseurs et modèles utilisés, fichiers modifiés par l'agent → transparence sur l'usage de l'IA (AI Act) »; EN equivalents ("helps document …").
- `report.git` "Git marking is not recorded in the audit log. To list commits marked by tenon: %s" / "Le marquage git n'est pas dans le journal d'audit. Pour lister les commits marqués par tenon : %s".
- Section titles and column headers: free wording, one key each (`report.sec.*`, `report.col.*`).

- [ ] **Step 1: Failing tests** — FR and EN render (title in each language); output contains the CSP meta exactly and no `<script`, no `<link`, no `src=` or `href=` attribute; a prompt `<script>alert(1)</script>` with details is escaped (`&lt;script&gt;`); a file path `</style><script>x</script>` is escaped (Review Focus 5); details banner present only with `Details`; no prompt text without it; broken chain banner and error text; metadata report shows `non disponible au niveau metadata`; empty report shows `aucune activité sur la période`; `report_id` present; every `report.` key used by the template exists in both languages (`i18n.T` never returns `!report.`).
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test ./internal/compliance/ ./internal/i18n/` (PASS).
- [ ] **Step 5: Commit** `feat(compliance): HTML report (FR/EN)`.

### Task 7: `tenon audit export`

**Files:**
- Create: `cmd/tenon/audit_export.go`, `cmd/tenon/audit_export_e2e_test.go`
- Modify: `cmd/tenon/run.go` (dispatch), `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: `compliance.Load`, `Build`, `ReportID`, `RenderHTML`, `RenderJSON`, `loadSetup(getenv, wd).redactor`, `redactText`, `uiLang`, `version`.
- Produces:
  - `run`: `case len(rest) >= 2 && rest[0] == "audit" && rest[1] == "export": return auditExport(rest[2:], *langFlag, stdout, stderr, getenv, wd)`; `audit verify` unchanged.
  - `func auditExport(args []string, langFlag string, stdout, stderr io.Writer, getenv func(string) string, wd string) int` — own `flag.FlagSet` `tenon audit export`: `--since`, `--until`, `--project`, `--all-projects`, `--session`, `--details`, `--out`, `--lang` (default `langFlag`); extra args → `err.unexpected_args`.
  - `var exportNow = time.Now` (test seam).
  - Flow: parse/validate → `loadSetup` (error → code 1) → `Load(filepath.Join(config.DataDir(getenv), "audit"), f)` → `Build` (`Redact: redactText(red)`, `Generator: "tenon " + version`, `Now: exportNow().UTC()`) → `ReportID` → `RenderHTML` → sha256 → `RenderJSON` → write HTML then JSON with `O_WRONLY|O_CREATE|O_EXCL`, 0600 (check both absent first; JSON failure removes the HTML) → stdout `audit.export.ok` → code 2 if `!Chain.Intact` (also stderr `audit.export.broken`), else 0.
  - `--project` relative paths resolved against `wd`; default project = `wd`.
  - i18n EN/FR: `audit.export.ok` "report written: %s, %s" / "rapport écrit : %s, %s"; `audit.export.broken` "audit log tampered: %s (report written)" / "journal altéré : %s (rapport écrit)"; `err.bad_date` "invalid date %q (expected YYYY-MM-DD)" / "date invalide %q (attendu AAAA-MM-JJ)"; `err.since_after_until` "--since is after --until" / "--since est postérieur à --until"; `err.scope_exclusive` "--project, --all-projects and --session cannot be combined" / "--project, --all-projects et --session ne se combinent pas"; `err.file_exists` "%s already exists" / "%s existe déjà"; flag help keys `flag.export_*`; `usage` gains `tenon audit export [--since AAAA-MM-JJ] [--until AAAA-MM-JJ] [--project <chemin> | --all-projects | --session <id>] [--details] [--out <dossier>] [--lang fr|en]` (EN: `YYYY-MM-DD`, `<path>`, `<dir>`).

- [ ] **Step 1: Failing e2e tests** (existing `env` helpers, fake LLM):
  - `TestAuditExportE2E`: headless run that reads and edits a file, then `audit export --out <tmp>` → code 0, both files exist, mode 0600, names use today's dates, HTML contains the provider name and the edited path, JSON `report_id` equals `ReportID` of its `report`, `html_sha256` equals sha256 of the HTML file, no prompt text in either file;
  - `TestAuditExportDetailsE2E`: `--details` → the prompt appears in both, banner present;
  - `TestAuditExportOtherProjectE2E`: default scope from another `wd` → zero sessions; `--all-projects` → one;
  - `TestAuditExportBrokenChainE2E`: one audit line altered → code 2, files written, HTML has `audit log tampered`;
  - `TestAuditExportExistingFileE2E`: `.json` already present → code 1, `already exists`, no `.html` created (Review Focus 4); out dir read-only → code 1, no file left;
  - `TestAuditExportUsageE2E`: `--since 2026-13-01` → `invalid date`; `--since 2026-10-10 --until 2026-10-01` → code 1; `--project x --all-projects` → code 1; extra arg → code 1;
  - `TestAuditExportNoAuditDirE2E`: fresh data dir → code 0, `no activity in this period`;
  - `TestAuditExportRedactsE2E`: audit line containing a secret written in clear (append a valid chained line with `audit.Writer` opened with a nil redactor) → masked in both files.
- [ ] **Step 2–4:** run (FAIL), implement, run `GOFLAGS=-mod=vendor go test -race ./cmd/...` (PASS).
- [ ] **Step 5: Commit** `feat(cli): tenon audit export`.

### Task 8: Documentation

**Files:**
- Modify: `docs/usage.md`, `docs/fr/utilisation.md`, `docs/security.md`, `docs/fr/securite.md`, `README.md`

- [ ] **Step 1:** usage (EN/FR): section "Compliance export" / "Export de conformité" after the audit section: command and options, defaults (30 days, current project), files and exit codes, what each section contains, `--details` and what it adds, metadata level and excluded sessions, how to check integrity (`tenon audit verify`, `report_id`, `html_sha256` with `shasum -a 256`), git marking command, the disclaimer.
- [ ] **Step 2:** JSON schema subsection (EN/FR): every key of the envelope and `report` with type and meaning, dates RFC 3339 UTC, `schema_version` 1 and the rule that a breaking change bumps it.
- [ ] **Step 3:** security (EN/FR): the report is read-only on the log, redacts again at render, no script/external resource, CSP; `--details` files contain conversation data — store and share them like the log itself.
- [ ] **Step 4:** README feature list line. Check every key named in the docs exists in `report.go` (`grep`).
- [ ] **Step 5: Commit** `docs: compliance export (EN/FR)`.
