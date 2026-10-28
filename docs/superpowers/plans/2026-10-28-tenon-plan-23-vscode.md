# tenon for VS Code — Implementation Plan (plan 23)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a tenon-branded VS Code extension (Marketplace + Open VSX) that bundles the tenon binary, launches `tenon acp`, and gives a chat panel with permissions, native diffs, editor context, skills, session history, a sovereignty status bar, and an opencode-style terminal mode.

**Architecture:** `editors/vscode/` is a TypeScript extension using `@agentclientprotocol/sdk` as ACP client over the `tenon acp` subprocess. The extension host owns one connection per window and a conversation model; a framework-free webview renders that model and posts typed messages back. tenon gains `session/list` and `_meta.tenon` so the panel can list sessions and the status bar can show provider, jurisdiction and isolation.

**Tech Stack:** Go 1.26 (vendored, `GOFLAGS=-mod=vendor`) for the tenon side; Node 22, TypeScript 5, esbuild, `@agentclientprotocol/sdk` 2.x, vitest, `@vscode/test-electron`, `@vscode/vsce`, `ovsx`, eslint.

**Spec:** `docs/superpowers/specs/2026-10-28-tenon-vscode-design.md`

## Global Constraints

- Extension id `tenon-code-eu.tenon`; `displayName` "tenon"; license `EUPL-1.2`; icon from `docs/assets/site/mark.svg` (PNG 256×256 in `editors/vscode/media/icon.png`); `engines.vscode` `^1.95.0`.
- Platforms: `darwin-arm64`, `darwin-x64`, `linux-x64`, `linux-arm64` only (no Windows).
- npm dependencies pinned to exact versions (no `^`/`~`) with a committed `package-lock.json`; versions at least two weeks old. Runtime dependency: `@agentclientprotocol/sdk` only.
- No network call and no telemetry from the extension. Webview CSP: `default-src 'none'; script-src 'nonce-<n>'; style-src ${webview.cspSource} 'nonce-<n>'; img-src ${webview.cspSource} data:; font-src ${webview.cspSource}`; no remote resource; no `eval`.
- Markdown rendering: raw HTML escaped; only `https:` links kept (opened through VS Code); everything else rendered as text.
- ACP `clientInfo: { name: "tenon-vscode", version: <extension version> }`; no `fs` or `terminal` client capability; `mcpServers: []`.
- Binary resolution order: setting `tenon.path` → bundled `<extensionPath>/bin/tenon` → `tenon` on `PATH`. Minimum tenon version `0.9.0`; output of `tenon --version` is `tenon vX.Y.Z`; an unparsable version (dev build) is accepted with a line in the Output channel.
- Permission replies: `{ outcome: { outcome: "selected", optionId } }` for a click; `{ outcome: { outcome: "cancelled" } }` when the notification is dismissed, the turn is stopped or the process dies. No automatic answer, no extension-side "always allow".
- Diff documents: URI scheme `tenon-diff`, read-only; title `<file name> (tenon)`; old side empty for a new file.
- Status bar text: `tenon · <provider name> · <EU|non-EU|local> · <sandboxed|not sandboxed>` (FR: `UE|hors UE|local`, `isolé|non isolé`); non-EU uses `statusBarItem.warningBackground`; no session → `tenon`. "Sandboxed" when `sandbox.mechanism` is non-empty.
- Commands (`package.json` `contributes.commands`): `tenon.openChat`, `tenon.newSession`, `tenon.attachSelection`, `tenon.attachFile`, `tenon.openTerminal`, `tenon.insertReference`, `tenon.restart`, `tenon.showLogs`. Keybindings: `tenon.openTerminal` Cmd+Esc / Ctrl+Esc; Cmd+Alt+K / Ctrl+Alt+K → `tenon.insertReference` when `tenon.terminalVisible`, else `tenon.attachSelection` when `editorTextFocus`.
- UI strings EN + FR through `vscode.l10n` (`l10n/bundle.l10n.fr.json`) and `package.nls.json` / `package.nls.fr.json`; webview strings passed from the extension host (no l10n in the webview bundle).
- tenon side: `session/list` per spec §9 (result `{ sessions: [{ sessionId, cwd, title, updatedAt }] }`, most recent first, `title` = `session.Summary.FirstPrompt`, `updatedAt` RFC 3339 UTC, no `nextCursor`, relative `cwd` → `-32602`); `initialize` adds `agentCapabilities.sessionCapabilities: { list: {} }`; `_meta.tenon = { provider, model, jurisdiction, sandbox: { mode, mechanism, network }, profile, policy }` on `session/new` and `session/load` results; every `usage_update` carries `_meta.tenon.provider` (provider id of the call).
- Go gates per task touching Go: `gofmt -l cmd internal scripts` empty, `go vet ./...`, `GOOS=linux go vet ./...`, `go test -race ./...`, `make lint`. Extension gates per task touching `editors/vscode`: `npm run lint` (eslint + `tsc --noEmit`), `npm test` (vitest), `npm run test:integration` (from Task 2 on).
- Commits: no `Co-Authored-By` or Claude/Anthropic mention; repo identity `tenon`.

## Review Focus

- The editor window is reloaded or closed mid-turn: the `tenon acp` child gets stdin closed and exits; no orphan process remains — test in Task 2 (dispose kills/ends the child within 5 s).
- tenon prints a huge or very fast stream (thousands of chunks): the webview stays responsive because the host coalesces state posts (≤ 1 post per animation frame / 16 ms) — test in Task 3 (model coalescing unit test).
- Model output containing `<img src=x onerror=…>`, `javascript:` links or a fenced block with HTML: rendered as text — test in Task 3 (render unit tests).
- Two permission requests arrive before the user answers the first: both cards show, each answer goes to its own request id — test in Task 4.
- Workspace with no folder open (single file): the panel shows "Open a folder to use tenon" and no `session/new` is sent — test in Task 3.

---

### Task 1: tenon side — `session/list`, `_meta.tenon`, provider in `usage_update`

**Files:**
- Modify: `internal/acp/server.go`, `internal/acp/types.go`, `internal/acp/bridge.go`, `cmd/tenon/acp.go`
- Test: `internal/acp/server_test.go`, `internal/acp/bridge_test.go`, `cmd/tenon/acp_e2e_test.go`

**Interfaces:**
- Produces: `Backend.ListSessions(ctx context.Context, cwd string) ([]SessionInfo, error)`; `type SessionInfo struct{ SessionID, Cwd, Title string; UpdatedAt time.Time }`; `Session.Meta() map[string]any` (the `_meta.tenon` object); `initialize` result gains `sessionCapabilities`.

- [ ] **Step 1: Failing tests**
  - `TestInitializeSessionCapabilities`: `agentCapabilities.sessionCapabilities` equals `{"list":{}}` (existing exact-JSON test updated).
  - `TestListSessions` (fake backend): result JSON `{"sessions":[{"sessionId":"s2","cwd":"/p","title":"b","updatedAt":"2026-10-28T10:00:00Z"},{"sessionId":"s1",…}]}`; relative `cwd` → `-32602`; backend error → `-32603`.
  - `TestNewSessionMeta`: `session/new` result `{"sessionId":"s1","_meta":{"tenon":{…}}}` with the fake session's `Meta()`; same for `session/load` (result `{"_meta":{"tenon":{…}}}`).
  - `TestUsageProviderMeta` (bridge): `Usage{Provider:"mistral"}` → `usage_update._meta.tenon.provider == "mistral"`.
  - e2e `TestACPListSessions`: two prompts in two sessions of the same project plus one in another project → `session/list` for the first project returns exactly its two sessions, newest first, `title` masked (a `TEST_KEY` value typed in the prompt is absent) and ≤ 80 runes.
  - e2e `TestACPSessionMeta`: `_meta.tenon.provider == "test"`, `jurisdiction` equals the configured one, `sandbox.mode` present.
- [ ] **Step 2: Run** `go test ./internal/acp/ ./cmd/tenon/ -run 'Session|Usage|ACP'` — FAIL.
- [ ] **Step 3: Implement.** Backend `ListSessions` in `cmd/tenon/acp.go` resolves `cwd` exactly like `NewSession` and calls `session.List(sessionsDir(getenv), root)`. `Meta()` reads the `SessionStartedPayload` of the started session (provider id, model, jurisdiction, `Sandbox`, `Profile`, `Policy.ID`); values are not secrets but pass through `Redact` for strings anyway.
- [ ] **Step 4: Run** — PASS; full Go gates. Update `docs/editors.md` / `docs/fr/editeurs.md` VS Code section: the tenon extension first (Marketplace / Open VSX name `tenon-code-eu.tenon`), the third-party client second.
- [ ] **Step 5: Commit** `feat(acp): session/list and session metadata for editors`

---

### Task 2: extension skeleton, binary resolution, ACP connection, test harness

**Files:**
- Create: `editors/vscode/{package.json,package-lock.json,tsconfig.json,esbuild.mjs,eslint.config.mjs,vitest.config.ts,.vscodeignore,LICENSE,README.md,CHANGELOG.md,package.nls.json,package.nls.fr.json,l10n/bundle.l10n.fr.json,media/icon.png}`, `editors/vscode/src/{extension.ts,binary.ts,agent.ts,log.ts}`, `editors/vscode/test/unit/binary.test.ts`, `editors/vscode/test/integration/{runTest.ts,suite/index.ts,suite/agent.test.ts,fakeLLM.ts,env.ts}`
- Modify: `.gitignore` (`editors/vscode/{node_modules,out,dist,*.vsix,.vscode-test}`)

**Interfaces:**
- Produces:
  - `resolveBinary(opts: { setting?: string; extensionPath: string; envPath: string; exists: (p: string) => boolean }): string | undefined`.
  - `parseVersion(output: string): [number, number, number] | undefined`; `meetsMinimum(v: [number,number,number], min = [0,9,0]): boolean`.
  - `class Agent` (`agent.ts`): `static start(bin: string, cwdForSpawn: string, log: Log, client: ClientHandlers): Promise<Agent>` (spawns `bin acp`, `ClientSideConnection` from the SDK over stdio, sends `initialize`, rejects on startup failure); `readonly connection: ClientSideConnection`; `readonly init: InitializeResponse`; `onExit: vscode.Event<{ code: number | null }>`; `dispose(): Promise<void>` (ends stdin, waits ≤ 5 s, then SIGKILL).
  - `interface ClientHandlers { sessionUpdate(n: SessionNotification): Promise<void>; requestPermission(r: RequestPermissionRequest): Promise<RequestPermissionResponse> }` — implemented in Tasks 3–4; in this task a stub that logs and answers `cancelled`.
  - `activate(ctx): Promise<TenonTestApi | undefined>` — returns the test API only when `process.env.TENON_VSCODE_TEST === "1"`; `TenonTestApi` grows in later tasks, starting with `{ agentReady(): Promise<InitializeResponse> }`.
  - Integration helpers: `startFakeLLM(script: string[][]): Promise<{ url: string; requests(): string[]; close(): Promise<void> }>` (OpenAI-compatible SSE, same wire format as `cmd/tenon/e2e_test.go` `sseText`/`sseCall`), `makeEnv(fake, opts?) → { env: Record<string,string>; projectDir: string }` writing a tenon `config.toml` under a temp `XDG_CONFIG_HOME` (provider `test`, `api_key_env = "TEST_KEY"`, `lang = "en"`).
  - Integration runner builds tenon once (`go build -o editors/vscode/.vscode-test/tenon ./cmd/tenon`) and sets `tenon.path` to it.

- [ ] **Step 1: Failing tests**
  - `binary.test.ts`: setting wins over bundled and PATH; bundled wins over PATH; PATH lookup splits on `:`; nothing found → `undefined`; `parseVersion("tenon v0.9.1\n") → [0,9,1]`; `parseVersion("tenon dev") → undefined`; `meetsMinimum([0,8,9]) === false`, `[0,9,0]` true, `[1,0,0]` true.
  - `agent.test.ts` (integration): extension activates, `agentReady()` resolves with `agentCapabilities.loadSession === true` and `agentInfo.name === "tenon"`; with `tenon.path` set to a missing file → an error notification whose message contains "tenon" and actions "Install" and "Choose binary" (capture via a test hook on `window.showErrorMessage`); Review Focus 1: after `dispose`, the child pid no longer exists within 5 s.
- [ ] **Step 2: Run** `npm test` and `npm run test:integration` — FAIL.
- [ ] **Step 3: Implement.** stderr of the child → Output channel "tenon" (`log.ts`); child exit → `onExit`. Version check runs `bin --version` with a 5 s timeout before `acp`; below minimum → warning "tenon <v> is too old (0.9.0 required)" with "Choose binary". "Install" opens `https://tenon-code-eu.github.io/tenon/editors/` via `vscode.env.openExternal` (the only URL in the extension). "Choose binary" opens a file picker and writes `tenon.path` (global scope).
- [ ] **Step 4: Run** — PASS; extension gates.
- [ ] **Step 5: Commit** `feat(vscode): extension skeleton, tenon binary and ACP connection`

---

### Task 3: sessions, conversation model, chat webview

**Files:**
- Create: `editors/vscode/src/{session.ts,model.ts,protocol.ts,panel.ts}`, `editors/vscode/webview/{main.ts,render.ts,style.css}`
- Modify: `editors/vscode/src/extension.ts`, `editors/vscode/esbuild.mjs` (second bundle for the webview), `package.json` (view container `tenon` in the activity bar, webview view `tenon.chat`)
- Test: `editors/vscode/test/unit/{model.test.ts,render.test.ts}`, `editors/vscode/test/integration/suite/chat.test.ts`

**Interfaces:**
- Produces:
  - `model.ts`: `type Item = { kind: "user"; text: string } | { kind: "agent"; text: string } | { kind: "thought"; text: string } | { kind: "tool"; id: string; title: string; toolKind: string; status: "pending"|"in_progress"|"completed"|"failed"; output: string; diff?: { path: string; oldText?: string; newText: string } } | { kind: "permission"; requestId: string; title: string; options: { optionId: string; name: string; kind: string }[]; answered?: string }`; `interface ConversationState { sessionId?: string; cwd?: string; items: Item[]; busy: boolean; commands: { name: string; description: string }[]; sessions: { sessionId: string; title: string; updatedAt: string }[]; meta?: Record<string, unknown>; banner?: { kind: "stopped"|"error"|"noFolder"; text: string } }`; `class Conversation { state: ConversationState; apply(n: SessionNotification): void; onDidChange: vscode.Event<ConversationState> }` — consecutive `agent_message_chunk` / `agent_thought_chunk` append to the last item of the same kind; `onDidChange` fires at most once per 16 ms (Review Focus 2).
  - `protocol.ts`: `type ToWebview = { type: "state"; state: ConversationState; strings: Record<string,string> }`; `type FromWebview = { type: "send"; text: string } | { type: "cancel" } | { type: "permission"; requestId: string; optionId: string } | { type: "openDiff"; toolCallId: string } | { type: "load"; sessionId: string } | { type: "newSession" } | { type: "pickFolder" } | { type: "ready" }`; `isFromWebview(x: unknown): x is FromWebview` (strict validation; invalid messages ignored).
  - `session.ts`: `class SessionController { constructor(agent: Agent, conv: Conversation); newSession(cwd: string): Promise<void>; prompt(blocks: ContentBlock[]): Promise<StopReason>; cancel(): Promise<void>; load(sessionId: string, cwd: string): Promise<void>; list(cwd: string): Promise<void> }` — `prompt` sets `busy`, adds the user item, clears `busy` on response; an error response adds a `banner {kind:"error"}` with tenon's message verbatim.
  - `render.ts`: `renderMarkdown(src: string): DocumentFragment` (or HTML string built only from escaped text + a fixed tag set: `p, pre, code, em, strong, ul, ol, li, a, h1–h4, blockquote`) and `renderItem(item: Item, strings): HTMLElement`.
  - `TenonTestApi` gains `state(): ConversationState`, `send(text: string): Promise<string>` (returns the stop reason).

- [ ] **Step 1: Failing tests**
  - `model.test.ts`: chunk merging; `tool_call` then two `tool_call_update` → one tool item with final status and output; replay of user/agent chunks rebuilds the same items; 1000 chunks applied synchronously → `onDidChange` listener called ≤ 2 times before the next timer tick.
  - `render.test.ts` (Review Focus 3): `<img src=x onerror=alert(1)>` → no `img` element, text kept; `[a](javascript:alert(1))` → no `a` element; `[a](https://x.eu)` → `a` with `href="https://x.eu"`; fenced block containing `<script>` → inside `pre > code` as text.
  - `chat.test.ts` (integration): fake LLM answers "hello" → `send("hi")` returns `end_turn`, state items `[user "hi", agent "hello"]`; Stop during a slow answer (fake LLM delays) → stop reason `cancelled`; tenon process killed → `banner.kind === "stopped"`, `tenon.restart` brings `agentReady` back; workspace without a folder (Review Focus 5) → `banner.kind === "noFolder"` and the fake LLM receives no request.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Webview = `WebviewViewProvider` for `tenon.chat`, CSP per Global Constraints with a fresh nonce per resolve; full-state posts (no diffing in v1). Style: opencode-like — monospace font (`var(--vscode-editor-font-family)`), dense layout, theme colors only (`--vscode-*` variables), no images except the icon. Multi-root: folder picker (`window.showWorkspaceFolderPick`) on `newSession` when > 1 folder. Enter sends, Shift+Enter newline, Stop button visible while `busy`; thought items collapsed by default; code blocks get a Copy button (`navigator.clipboard`).
- [ ] **Step 4: Run** — PASS; extension gates.
- [ ] **Step 5: Commit** `feat(vscode): chat panel, sessions and conversation model`

---

### Task 4: permissions and native diffs

**Files:**
- Create: `editors/vscode/src/{permissions.ts,diff.ts}`
- Modify: `editors/vscode/src/{extension.ts,session.ts,model.ts}`, `editors/vscode/webview/main.ts`
- Test: `editors/vscode/test/unit/permissions.test.ts`, `editors/vscode/test/integration/suite/permission.test.ts`

**Interfaces:**
- Consumes: Task 3 `Conversation`, `Item.kind === "permission"`, `FromWebview` `permission` / `openDiff`.
- Produces: `class Permissions { request(r: RequestPermissionRequest): Promise<RequestPermissionResponse>; answer(requestId: string, optionId: string): void; cancelAll(): void }` — request ids are generated by the extension (`p1`, `p2`, …), one pending promise per id; a notification (`window.showInformationMessage` with the option names, non-modal) is shown only when the chat view is not visible; the first answer (card or notification) resolves, the other is closed/marked answered; `cancelAll` on Stop and on process exit. `class DiffProvider implements TextDocumentContentProvider` (scheme `tenon-diff`) with `open(toolCallId: string)`. `TenonTestApi` gains `answer(optionId: string)` (answers the oldest pending request) and `lastDiff(): { left: string; right: string; title: string } | undefined`.

- [ ] **Step 1: Failing tests**
  - `permissions.test.ts`: options are kept in tenon's order with tenon's names; `answer` → `{outcome:{outcome:"selected",optionId}}`; notification dismissed → `cancelled`; `cancelAll` → every pending `cancelled`; Review Focus 4: two requests, answer the second first → each promise gets its own optionId.
  - `permission.test.ts` (integration): fake LLM calls `write_file` (`a.txt`, `v2`) on an existing `a.txt` = `v1` → a permission item appears; `answer("allow")` → file contains `v2`, tool item has a diff; `tenon-diff` documents give left `v1` (masked) and right `v2`, title `a.txt (tenon)`; second run: Stop while the permission is pending → file unchanged, stop reason `cancelled`; a sandboxed `shell` call offers `allow_network`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Card buttons post `{type:"permission"}`; answered cards stay in the thread, disabled, showing the chosen option name.
- [ ] **Step 4: Run** — PASS; extension gates.
- [ ] **Step 5: Commit** `feat(vscode): permission cards and native diffs`

---

### Task 5: editor context, skills, session history, status bar

**Files:**
- Create: `editors/vscode/src/{context.ts,status.ts}`
- Modify: `editors/vscode/src/{extension.ts,session.ts}`, `editors/vscode/webview/main.ts`, `package.json` (commands, menus `editor/context` for attach commands)
- Test: `editors/vscode/test/unit/{context.test.ts,status.test.ts}`, `editors/vscode/test/integration/suite/history.test.ts`

**Interfaces:**
- Produces: `selectionBlock(uri: string, text: string, startLine: number, endLine: number): ContentBlock` → `{ type: "resource", resource: { uri: "<uri>#L<start>-<end>", text, mimeType: "text/plain" } }` (1-based lines); `fileBlock(uri: string, name: string): ContentBlock` → `{ type: "resource_link", uri, name }`; `buildPrompt(text: string, attachments: ContentBlock[]): ContentBlock[]` (text block first, then attachments in order). `statusText(meta: Record<string, unknown> | undefined, l10n): { text: string; warning: boolean }`. Webview: attachments shown as removable chips above the input; `/` autocompletes from `state.commands`; a "Sessions" list from `state.sessions` (refreshed on panel open and after each turn).

- [ ] **Step 1: Failing tests**
  - `context.test.ts`: lines 10–20 → uri ends with `#L10-20`; `buildPrompt("fix", [file, sel])` order; empty text with attachments still sends attachments.
  - `status.test.ts`: `{provider:"Mistral",jurisdiction:"eu",sandbox:{mechanism:"seatbelt"}}` → `tenon · Mistral · EU · sandboxed`, warning false; `jurisdiction:"non-eu"`, no mechanism → `… · non-EU · not sandboxed`, warning true; undefined → `tenon`; FR strings via a FR l10n stub.
  - `history.test.ts` (integration): two turns in session A, new session B; `state().sessions` lists A and B newest first with titles; `load(A)` → items rebuilt with A's user and agent messages; a `/` skill defined in the project (`.tenon/skills/fix/SKILL.md`) appears in `state().commands` and `send("/fix it")` reaches the fake LLM with the skill body; status bar text contains `test` and the configured jurisdiction label.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Status bar reads `meta` from the `session/new`/`load` result (`_meta.tenon`) and updates the provider on `usage_update._meta.tenon.provider`; clicking it runs `tenon.openChat`. The jurisdiction labels map `eu`→EU, `non-eu`→non-EU, `local`→local (check the exact jurisdiction values in `internal/llm` and map every value; unknown → shown as is).
- [ ] **Step 4: Run** — PASS; extension gates.
- [ ] **Step 5: Commit** `feat(vscode): editor context, skills, session history and status bar`

---

### Task 6: terminal mode, French UI, packaging, CI, release docs

**Files:**
- Create: `editors/vscode/src/terminal.ts`, `scripts/vscode-package.sh`, `editors/vscode/test/unit/terminal.test.ts`
- Modify: `editors/vscode/{l10n/bundle.l10n.fr.json,package.nls.fr.json,README.md,CHANGELOG.md,package.json}`, `.github/workflows/ci.yml`, `.forgejo/workflows/ci.yml`, `.woodpecker.yml`, `RELEASE.md`, `README.md` (one line under Documentation), `docs/editors.md`, `docs/fr/editeurs.md`

**Interfaces:**
- Produces: `referenceFor(relPath: string, startLine: number, endLine: number): string` → `@<relPath>#L<a>-<b>` (single line → `#L<a>`, no selection → `@<relPath>`); `openTenonTerminal(bin: string, cwd: string): vscode.Terminal` (name "tenon", `location: { viewColumn: ViewColumn.Beside }`, reused while open, sets context key `tenon.terminalVisible`); `scripts/vscode-package.sh <version>` → `dist/tenon-vscode-<target>-<version>.vsix` for the four targets.

- [ ] **Step 1: Failing tests**
  - `terminal.test.ts`: `referenceFor("src/a.go", 10, 20) === "@src/a.go#L10-20"`, `(…, 5, 5) === "@src/a.go#L5"`.
  - `scripts/vscode-package.sh` check (shell, run in CI): with fake binaries in a temp `dist/`, produces four `.vsix`; `unzip -l` of each lists `extension/bin/tenon`; the binary inside is executable (`unzip -Z` mode `-rwx`), and the `.vsix` `targetPlatform` matches.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
  - Terminal per spec §8; `tenon.insertReference` uses `terminal.sendText(ref, false)`.
  - Every UI string has its FR entry; a unit test asserts every key of `package.nls.json` exists in `package.nls.fr.json` and every `l10n.t` literal in `src/` exists in `bundle.l10n.fr.json`.
  - `vscode-package.sh`: `npm ci && npm run build`, copies `dist/tenon-<os>-<arch>` (match the names `scripts/release.sh` produces) to `bin/tenon`, `vsce package --target <t> --out dist/…`; maps `darwin/arm64→darwin-arm64`, `darwin/amd64→darwin-x64`, `linux/amd64→linux-x64`, `linux/arm64→linux-arm64`. Appends the `.vsix` to the release file list so `SHA256SUMS` covers them (follow how `release.sh` builds the sums; do not sign — the maintainer signs).
  - CI job `vscode` in `.github/workflows/ci.yml` following the file's rules (no third-party `uses:`): container `node:22-bookworm`; installs Go 1.26.10 from `https://go.dev/dl/go1.26.10.linux-amd64.tar.gz` checked against its pinned sha256; `apt-get install -y xvfb libnss3 libatk-bridge2.0-0 libgtk-3-0 libgbm1 libasound2 unzip`; `npm ci`, `npm run lint`, `npm test`, `xvfb-run -a npm run test:integration`, packaging check. Same job in `.forgejo/workflows/ci.yml` and `.woodpecker.yml`.
  - `RELEASE.md`: after signing, `npx vsce publish --packagePath dist/tenon-vscode-*.vsix` (token `VSCE_PAT`) and `npx ovsx publish dist/tenon-vscode-*.vsix -p $OVSX_PAT`; creating the `tenon-code-eu` publisher on both registries is a manual maintainer step.
  - Docs EN/FR: install from the Marketplace / Open VSX, the panel, permissions, diffs, status bar, terminal mode (Cmd+Esc, Cmd+Alt+K), `tenon.path`.
- [ ] **Step 4: Run** — PASS; extension gates; Go gates; `mkdocs build --strict`.
- [ ] **Step 5: Commit** `feat(vscode): terminal mode, French UI, packaging and CI`
