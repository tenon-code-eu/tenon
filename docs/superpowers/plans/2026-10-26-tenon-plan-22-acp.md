# tenon ACP agent — Implementation Plan (plan 22)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `tenon acp` makes tenon an Agent Client Protocol agent: editors (Zed, JetBrains, Neovim, Emacs, VS Code) launch it over stdio and drive sessions, while every tenon guarantee (permissions, sandbox, masking, routing, checkpoints, audit) keeps applying because tenon's own tools do all the work.

**Architecture:** a new `internal/acp` package holds a line-delimited JSON-RPC 2.0 connection, the ACP types, and a server that drives sessions through a small `Session` interface (subscribe to engine events, submit input, answer permissions). A bridge maps engine events to `session/update` and permission requests to `session/request_permission`. `cmd/tenon` implements the backend with the existing `startEngine` path (one started engine per ACP session), adds the `acp` subcommand and the `[acp]` config/policy key.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), standard library only.

**Spec:** `docs/superpowers/specs/2026-10-26-tenon-acp-design.md`

## Global Constraints

- No new module. JSON-RPC 2.0 written on `encoding/json` + `bufio`, one JSON object per line on stdout; stderr only for technical logs; nothing else ever written to stdout.
- ACP: `protocolVersion: 1`; object keys `camelCase`; discriminators `snake_case`; paths absolute.
- `initialize` result: `agentCapabilities: { loadSession: true, promptCapabilities: { image: false, audio: false, embeddedContext: true }, mcpCapabilities: { http: true, sse: false } }`, `authMethods: []`, `agentInfo: { name: "tenon", version }`.
- Error codes: `-32700` parse error, `-32601` unknown method, `-32602` invalid params (relative `cwd`, `image`/`audio` block, protocol version ≠ 1), `-32000` tenon refusal (startup refusal message verbatim; `turn in progress`), `-32002` unknown session.
- Stop reasons: `completed` → `end_turn`; `max_iterations` → `max_turn_requests`; `incomplete` → `max_tokens`; `cancelled` → `cancelled`; hook stop or blocked turn → `end_turn` after a `notice`.
- Tool kinds: `read_file` → `read`; `write_file`, `edit_file` → `edit`; `glob`, `grep` → `search`; `shell` → `execute`; `agent` → `think`; `mcp__*` → `other`; anything else → `other`.
- Permission options: `allow_once` (optionId `allow`), `allow_always` (optionId `allow_session`), `reject_once` (optionId `reject`), and for a sandboxed shell `allow_once` with optionId `allow_network`. `allow_session` never remembers network. `cancelled` or a client error = refusal.
- `[acp] accept_editor_mcp` (bool, default `false`): global config only (project → `config: acp not allowed in project configuration`); a policy may set it to `false`, never to `true` (`policy: acp.accept_editor_mcp can only be false`).
- Editor MCP defaults: stdio → `jurisdiction = "local"`, `risk = ""`; http → `jurisdiction = "non-eu"` unless the host is local (`localhost`, `127.0.0.1`, `::1`) → `local`. Id clash with a config server: config wins, notice.
- Notice texts (i18n EN/FR) include `editor MCP servers ignored ([acp] accept_editor_mcp)`.
- `SessionStarted` gains `Client *ClientInfo \`json:"client,omitempty"\`` (`{name, version}`) and `Transport string \`json:"transport,omitempty"\`` (`"acp"`).
- Ruling vs spec §3 "un moteur par dossier": one `startEngine` per ACP session (same startup path, same guarantees; simpler lifecycle). Sessions of the same `cwd` share nothing but config.
- No `Co-Authored-By` or any Claude/Anthropic attribution in commits; commits authored by the repo's configured identity (`tenon`).
- Every task: `gofmt -l cmd internal scripts` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOOS=linux GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- An editor sends `session/prompt` for a session that is still starting, or two prompts back to back: second gets `-32000 turn in progress`, never two concurrent turns — test in Task 2.
- The editor closes stdin mid-turn (editor quit): the turn is cancelled, MCP servers and the audit log are closed (session `close()` runs), the process exits 0 — test in Task 5.
- A permission request arrives while the client is slow to answer, then `session/cancel`: the pending `session/request_permission` is abandoned, the engine gets a refusal, stop reason `cancelled` — test in Task 3.
- Output contains non-UTF-8 bytes or a line separator in tool output: every stdout line is still one valid JSON object (`json.Marshal` escapes), never a raw newline — test in Task 1.
- `cwd` is a symlink to the project: the session's project root and checkpoint paths resolve like `tenon` run from that directory — test in Task 5.

---

### Task 1: JSON-RPC 2.0 line transport

**Files:**
- Create: `internal/acp/rpc.go`
- Test: `internal/acp/rpc_test.go`

**Interfaces:**
- Produces:
  - `type Conn struct` — `func NewConn(r io.Reader, w io.Writer, h Handler) *Conn`; `func (c *Conn) Serve(ctx context.Context) error` (reads until EOF/ctx; returns `nil` on EOF); `func (c *Conn) Call(ctx context.Context, method string, params, result any) error` (agent → client request, waits for the response or ctx); `func (c *Conn) Notify(method string, params any) error`.
  - `type Handler interface { Handle(ctx context.Context, method string, params json.RawMessage) (result any, err error) }` — called for client → agent requests and notifications (notifications: result ignored, no response written). Requests are handled concurrently (one goroutine each); writes are serialized.
  - `type Error struct { Code int \`json:"code"\`; Message string \`json:"message"\`; Data any \`json:"data,omitempty"\` }` implementing `error`; constants `CodeParse = -32700`, `CodeMethodNotFound = -32601`, `CodeInvalidParams = -32602`, `CodeTenon = -32000`, `CodeUnknownSession = -32002`.

- [ ] **Step 1: Failing tests** (pipes, no network):
  - `TestRequestResponse`: client sends `{"jsonrpc":"2.0","id":1,"method":"m","params":{}}` → one line `{"jsonrpc":"2.0","id":1,"result":…}`.
  - `TestErrorResponse`: handler returns `&Error{Code:-32602,...}` → `error` object with that code; a plain `error` → `-32603` with its message.
  - `TestNotificationNoReply`: request without `id` → handler called, nothing written.
  - `TestParseError`: invalid JSON line → `{"id":null,"error":{"code":-32700,…}}`, the connection keeps serving.
  - `TestCallFromAgent`: `Call` writes a request with a fresh id; the client's response line resolves it; a response with an unknown id is ignored.
  - `TestCallCancelled`: ctx cancelled before the response → `Call` returns ctx error; a late response is dropped.
  - `TestOneJSONPerLine` (Review Focus 4): a result string containing `\n`, U+2028 and invalid UTF-8 → exactly one line, valid JSON.
  - `TestEOF`: closing the reader → `Serve` returns `nil`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/acp/` — FAIL.
- [ ] **Step 3: Implement.** Lines up to 16 MiB (`bufio.Scanner` buffer).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(acp): line-delimited JSON-RPC 2.0 connection`

---

### Task 2: ACP types and session server

**Files:**
- Create: `internal/acp/types.go`, `internal/acp/server.go`
- Test: `internal/acp/server_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces:
  - ACP types used (camelCase JSON): `InitializeRequest{ProtocolVersion int; ClientCapabilities json.RawMessage; ClientInfo *Implementation}`, `Implementation{Name, Version string}`, `NewSessionRequest{Cwd string; MCPServers []MCPServer}`, `LoadSessionRequest{SessionID, Cwd string; MCPServers []MCPServer}`, `PromptRequest{SessionID string; Prompt []ContentBlock}`, `ContentBlock{Type, Text, URI, Name string; Resource *EmbeddedResource}` (`type`: `text`, `resource_link`, `resource`, `image`, `audio`), `EmbeddedResource{URI, Text, MimeType string}`, `MCPServer{Name, Command string; Args []string; Env []EnvVariable; Type, URL string; Headers []HTTPHeader}`, `CancelNotification{SessionID string}`.
  - `type Session interface { ID() string; ProjectRoot() string; Subscribe() <-chan event.Event; Submit(ctx context.Context, in engine.Input) error; Respond(permissionID string, r engine.Reply) error; Notices() []string; Commands() []Command; OldText(hash string) (string, bool); Close() }`; `type Command struct{ Name, Description string }`.
  - `type Backend interface { NewSession(ctx context.Context, cwd string, mcp []MCPServer, client *Implementation) (Session, error); LoadSession(ctx context.Context, id, cwd string, mcp []MCPServer, client *Implementation) (Session, []event.Event, error) }` — a returned error of type `*Error` is passed through; any other error becomes `CodeTenon` with its message.
  - `type Server struct` — `func NewServer(b Backend, version string, lang i18n.Lang) *Server`; `func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error` (creates the `Conn`, handles `initialize`, `session/new`, `session/load`, `session/prompt`, `session/cancel`; on return closes every session).
  - Prompt text assembly: `text` blocks joined by `"\n\n"`; `resource_link` → `@<path-from-uri>` (file URI → path); `resource` → `"\n\n<resource uri=…>\n<text>\n</resource>"`; `image`/`audio` → `-32602`. A prompt starting with `/name` where `name` is a known command → `engine.Input{Skill: name, Text: rest, Typed: whole}`.

- [ ] **Step 1: Failing tests** with a fake `Backend`/`Session` (scripted event channel):
  - `TestInitialize`: exact `agentCapabilities`, `authMethods: []`, `agentInfo.name == "tenon"`; `protocolVersion: 2` → `-32602`.
  - `TestNewSessionRelativeCwd` → `-32602`; backend error `errors.New("profile strict-local: …")` → `-32000` with that message.
  - `TestPromptText`: two `text` blocks + a `resource_link` (`file:///p/a.go`) → `Submit` receives `"a\n\nb\n\n@/p/a.go"`.
  - `TestPromptImageRefused` → `-32602`.
  - `TestPromptSkill`: commands `[{fix, …}]`, prompt `/fix the test` → `Input{Skill:"fix", Text:"the test", Typed:"/fix the test"}`.
  - `TestUnknownSession` → `-32002`.
  - `TestTurnInProgress` (Review Focus 1): second prompt while the first blocks → `-32000` `turn in progress`.
  - `TestServeClosesSessions`: EOF → every session's `Close` called once.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** (the bridge of Task 3 produces updates; here `session/prompt` waits for `Submit` and returns `stopReason` from Task 3's mapper — stub it in this task as `end_turn` and replace in Task 3).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(acp): ACP types and session server`

---

### Task 3: bridge (events → session/update, permissions, stop reason)

**Files:**
- Create: `internal/acp/bridge.go`
- Modify: `internal/acp/server.go`, `internal/i18n/catalog.go`
- Test: `internal/acp/bridge_test.go`

**Interfaces:**
- Consumes: Tasks 1–2; engine event payloads (`AssistantDelta`, `ToolCallRequested`, `ToolResult`, `PermissionRequested` incl. `Kind`, `Provider`, `Sandboxed`, `RuleNetwork`, `TurnCompleted` reason, hook/notice events, token usage).
- Produces: `func toolKind(name string) string`; `func stopReason(turnReason string) string`; bridge goroutine per prompt translating events and answering permissions via `Conn.Call("session/request_permission", …)`.

- [ ] **Step 1: Failing tests** (fake session emitting scripted events; fake client answering permissions):
  - `TestMessageChunk`: `AssistantDelta "hi"` → `session/update` `{sessionUpdate:"agent_message_chunk", content:{type:"text", text:"hi"}}`.
  - `TestToolCallLifecycle`: `ToolCallRequested write_file a.go` → `tool_call {toolCallId, title:"write_file · a.go", kind:"edit", status:"pending", locations:[{path:"<root>/a.go"}]}`; `ToolResult` → `tool_call_update {status:"completed", content:[{type:"diff", path, oldText:"v1", newText:"v2"}]}` (`OldText` from the fake session, `newText` read from disk); failed result → `status:"failed"` with text content.
  - `TestToolKinds`: table over the Global Constraints mapping.
  - `TestPermissionRoundTrip`: `PermissionRequested{Kind:"tool"}` → client gets options `allow`, `allow_session`, `reject`; answering `allow_session` → `Respond(id, Reply{Allow:true, Remember:true})`.
  - `TestPermissionNetworkOption`: `Sandboxed:true` → extra `allow_network`; choosing it → `Reply{Allow:true, Network:true}`; `RuleNetwork:true` → no extra option, and the `allow` name says network is granted by the rule (i18n key).
  - `TestJurisdictionPermission`: `Kind:"jurisdiction"` → title names provider and jurisdiction; `reject` → `Reply{Allow:false}`.
  - `TestCancelDuringPermission` (Review Focus 3): `session/cancel` while the client has not answered → `Respond(id, Reply{Allow:false})`, prompt result `cancelled`.
  - `TestStopReasons`: table over the Global Constraints mapping; hook stop → a `notice` update before `end_turn`.
  - `TestNoticesAndCommands`: on `session/new`, `Notices()` → `notice` updates; `Commands()` → one `available_commands_update`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Masked text only (payloads are already redacted by the engine); tool output truncated as in headless (`OneLine`/limit helpers reused, not duplicated: export them from `internal/ui/headless` if needed).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(acp): bridge engine events and permissions to ACP`

---

### Task 4: `[acp]` config/policy, editor MCP conversion, audit fields

**Files:**
- Modify: `internal/config/*.go`, `internal/orgpolicy/*.go`, `internal/event/payloads.go`, `internal/compliance/*.go`, `docs/config.example.toml`
- Create: `internal/acp/mcp.go`
- Test: `internal/config/*_test.go`, `internal/orgpolicy/*_test.go`, `internal/acp/mcp_test.go`, `internal/compliance/*_test.go`

**Interfaces:**
- Produces:
  - `config.ACP struct{ AcceptEditorMCP bool \`toml:"accept_editor_mcp"\` }`, `Config.ACP ACP \`toml:"acp"\``; policy `[acp] accept_editor_mcp` (only `false` accepted) forcing it off.
  - `func EditorMCP(in []MCPServer, existing []config.MCPServer) (out []config.MCPServer, notices []string)` — defaults per Global Constraints; clashes dropped with a notice.
  - `event.ClientInfo{Name, Version string}`; `SessionStartedPayload.Client`, `.Transport`; compliance report shows the editor per session (JSON `client`, HTML row "Éditeur"/"Editor").

- [ ] **Step 1: Failing tests:** project `[acp]` refused (exact message); policy `true` refused, policy `false` overrides global `true`; `EditorMCP` stdio/http/local-host defaults and clash notice; export shows `client` when present and nothing for older logs; `TestConfigExampleLoads` with a commented `[acp]` block.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(config): [acp] accept_editor_mcp; editor MCP servers; editor in the audit log and export`

---

### Task 5: `tenon acp` command, backend, end-to-end, docs

**Files:**
- Create: `cmd/tenon/acp.go`, `cmd/tenon/acp_e2e_test.go`, `docs/editors.md`, `docs/fr/editeurs.md`
- Modify: `cmd/tenon/run.go` (dispatch `acp`; `engineOptions` gains `extraMCP []config.MCPServer`, `client *event.ClientInfo`, `transport string`), `mkdocs.yml` (nav), `README.md` (one line under Documentation)

**Interfaces:**
- Consumes: Tasks 1–4; `startEngine(o engineOptions) (started, error)` with `interactive: true` (the editor answers permissions).
- Produces: `func runACP(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, globalPath string) int`; backend type implementing `acp.Backend` (`NewSession` → `startEngine` with `wd = cwd` resolved like the CLI; `LoadSession` → `resume: id` plus the session's events for replay; `OldText(hash)` from the checkpoint store; `Close` → `started.close()`).

- [ ] **Step 1: Failing e2e tests** (fake ACP client over `io.Pipe`, existing `newFakeLLM`):
  - `TestACPInitializeAndText`: `initialize`, `session/new`, prompt → `agent_message_chunk` lines, `stopReason:"end_turn"`; audit log `SessionStarted.client.name` = the client's name, `transport:"acp"`.
  - `TestACPWritePermissionDiff`: fake LLM calls `write_file` → `session/request_permission` received, client answers `allow` → `tool_call_update` with a `diff`; the file is written; checkpoint exists.
  - `TestACPCancel`: cancel during a permission → `cancelled`, file unchanged.
  - `TestACPLoad`: second process `session/load` of the first session → replayed updates, then a new prompt continues the conversation.
  - `TestACPEditorMCP`: `mcpServers` with one stdio server → ignored + notice; with `[acp] accept_editor_mcp = true` → listed in `SessionStarted.mcp`.
  - `TestACPStartupRefusal`: `[sandbox] mode = "required"` with the probe stubbed unavailable → `-32000` whose message is the CLI's refusal.
  - `TestACPStdinClosed` (Review Focus 2): close stdin mid-turn → process returns 0, session closed (audit chain verifies with `tenon audit verify`).
  - `TestACPSymlinkCwd` (Review Focus 5): `cwd` symlink → `SessionStarted.project_root` equals the CLI's for the same directory.
  - `TestACPStdoutOnlyJSON`: every stdout line of a whole run decodes as JSON-RPC.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./cmd/tenon/ -run ACP` — FAIL.
- [ ] **Step 3: Implement.** Startup notices that `run` prints to stderr go to `Session.Notices()` instead (nothing on stdout). Docs EN/FR: what ACP is; Zed (`agent_servers` setting with `command: "tenon", args: ["acp"]`), JetBrains AI Assistant (ACP agent: command `tenon acp`), Neovim (CodeCompanion / avante.nvim ACP adapter), VS Code (an ACP client extension); what stays enforced (permissions, sandbox, routing, audit); `[acp] accept_editor_mcp`; limits (no images, editor buffers not read, `fs/*`/`terminal/*` unused). Verify each editor's configuration key against its current documentation before writing it, and cite the source URL in the page.
- [ ] **Step 4: Run** — PASS; full gates; `mkdocs build --strict`.
- [ ] **Step 5: Commit** `feat(cli): tenon acp, an Agent Client Protocol agent for editors; docs: editors (EN/FR)`
