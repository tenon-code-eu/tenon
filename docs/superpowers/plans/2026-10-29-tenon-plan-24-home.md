# tenon home screen, session view and visual identity — Implementation Plan (plan 24)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an opencode-style centered home screen, a session side panel, per-answer jurisdiction badges, themes, a command palette and a logo animation, in the tenon TUI and in the VS Code extension.

**Architecture:** TUI work stays in `internal/ui/tui` (bubbletea/lipgloss, already vendored): a theme layer every renderer reads, a home view replacing `welcome.go`, a side panel fed by engine events already received, a palette overlay and an intro animation. The extension adds a home view to the chat webview, a second webview view for session info, a provider badge on agent items, a QuickPick palette and a `tenon.theme` setting. No engine or protocol change.

**Tech Stack:** Go 1.26 (vendored, `GOFLAGS=-mod=vendor`), bubbletea v1.3.10, lipgloss v1.1.0, bubbles v1.0.0; TypeScript extension in `editors/vscode` (esbuild, vitest, `@vscode/test-electron`).

**Spec:** `docs/superpowers/specs/2026-10-29-tenon-home-design.md`

## Global Constraints

- No new Go module, no new npm runtime dependency.
- Slogan: EN `sovereign coding agent`, FR `agent de code souverain`, followed by the version (`welcomeVersion` rules: `dev` when empty, `v` prefix for a number).
- Theme `tenon`: structure `#003399`, accent `#FFCC00`, ok green, alert amber, muted gray. Themes: `tenon` (default), `tokyonight`, `catppuccin`, `gruvbox`, `light`.
- Config: `[ui] theme` (string, default `tenon`) and `[ui] animation` (bool, default `true`), global config only. Errors: `config: unknown ui.theme "<v>"`; project config → `config: ui not allowed in project configuration`.
- `NO_COLOR` disables every color whatever the theme.
- Links (constants): docs `https://tenon-code-eu.github.io/tenon/`, github `https://github.com/tenon-code-eu/tenon`, bug `https://github.com/tenon-code-eu/tenon/issues/new`. TUI: OSC 8 hyperlinks only when the terminal supports them, plain text otherwise. Extension: webview sends `openLink` with id `docs` | `github` | `bug`, never a URL.
- Home: shown until the first message; a resumed session shows the thread directly. Input box width `min(72, width − 8)`. Under 70 columns: compact `⬢ TENON`.
- Shortcuts line: `/help /skills /rewind /memory · Ctrl+P palette · Tab agents` (localised).
- TUI keys: `Ctrl+B` toggles the side panel, `Ctrl+P` opens the palette (both free in `keys.go`).
- Side panel: 26 columns, separator line, open by default at width ≥ 100, closed below; a manual `Ctrl+B` choice wins for the rest of the session. Order: title, context gauge (10 cells + %), session tokens in/out, modified files (max 8 then `+N`), sovereignty (sends per provider · country, non-EU in amber, sandbox, profile, policy).
- Badge: under each completed main-agent answer, `<provider name> · <country> · <EU|non-EU|local>` (FR `UE|hors UE|local`) of the turn's last call; non-EU and unknown jurisdiction in the alert color.
- Country: `Usage` carries no country. TUI resolves it from the configured providers (`Options.Providers map[string]llm.ProviderInfo`, keyed by provider id, passed by `cmd/tenon/run.go`); the extension shows the country only when known (`_meta.tenon` carries none), so its badge is `<provider name> · <EU|non-EU|local>`. No engine or protocol change.
- Palette entries: built-in commands, skills, past sessions of the folder (TUI: shows `tenon --resume <id>` to run, as the TUI has no in-session resume), themes, and an informative `Provider: <active> — change with tenon --provider <id>` entry. Fuzzy match: case-insensitive subsequence; contiguous and word-start matches rank first.
- Animation: 6–8 frames at ~80 ms, < 700 ms total; skipped without TTY, with `NO_COLOR`, with `[ui] animation = false`, or under 70 columns; any key interrupts it and is kept for the input.
- Extension: `tenon.theme` = `auto` (default, `--vscode-*` variables) | `tenon`; second view `tenon.session` under `tenon.chat`; command `tenon.palette` (Cmd+Shift+; / Ctrl+Shift+; when the chat has focus); animation off under `prefers-reduced-motion: reduce`; new webview messages `openLink`, `palette`, `openFileDiff` validated by `isFromWebview`; CSP and no-network rules unchanged.
- Every UI string EN + FR (`internal/i18n`; `vscode.l10n` + `package.nls*.json`), at least 8 tips.
- Gates: Go `gofmt -l cmd internal scripts` empty, `go vet ./...`, `GOOS=linux go vet ./...`, `go test -race ./...`, `make lint`; extension `npm run lint`, `npm test`, `npm run test:integration`.
- Commits: no `Co-Authored-By` or Claude/Anthropic mention; repo identity `tenon`.

## Review Focus

- A terminal resized while the home or palette is open (e.g. 120 → 60 columns): layout recomputes, nothing wider than the terminal, panel auto-closes under 100 unless toggled — test in Task 3.
- A provider fallback mid-turn to a non-EU provider: the badge and the side panel show the provider that actually answered, in amber — test in Task 3.
- A session title or file path containing a secret or control characters: panel and badges go through `Model.text` (redact + sanitize) — test in Task 3.
- Typing during the intro animation: the first keystroke is not lost — test in Task 2.
- Webview `openLink` with an unknown id or a URL string: ignored, nothing opened — test in Task 5.

---

### Task 1: themes and `[ui]` config

**Files:**
- Create: `internal/ui/tui/theme.go`, `internal/ui/tui/theme_test.go`
- Modify: `internal/config/config.go`, `internal/config/load.go` (project refusal), `internal/config/*_test.go`, `docs/config.example.toml`, `internal/ui/tui/model.go` (Options gains `Theme string`, `Animation bool`), `cmd/tenon/run.go` (pass them), `internal/i18n/catalog.go`, existing renderers that hard-code colors (`welcome.go`, `view.go`, `permission.go`, `transcript.go`) switch to theme roles.

**Interfaces:**
- Produces: `type Theme struct{ Name string; Structure, Accent, OK, Alert, Muted, Text, Selection lipgloss.Color }`; `func ThemeByName(name string) (Theme, bool)`; `func ThemeNames() []string` (in the order of Global Constraints); `config.UI{ Theme string \`toml:"theme"\`; Animation *bool \`toml:"animation"\` }`, `Config.UI`; `Model` holds the active `Theme`; `/theme` lists, `/theme <name>` switches for the session (added to `skills.Builtins`).

- [ ] **Step 1: Failing tests:** `TestThemeNames` (5 names, order); `TestThemeTenonColors` (Structure `#003399`, Accent `#FFCC00`); config: unknown theme → exact error, project `[ui]` → exact error, default theme `tenon` and animation true; `TestThemeCommand` (`/theme gruvbox` switches, `/theme nope` shows an error line listing names); `TestNoColorIgnoresTheme` (no ANSI color in a rendered view with `NO_COLOR`); `TestConfigExampleLoads` with a commented `[ui]` block.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Pick the four non-tenon palettes from the themes' published colors (cite the source URL of each palette in a comment).
- [ ] **Step 4: Run** — PASS; Go gates.
- [ ] **Step 5: Commit** `feat(tui): themes and [ui] config`

---

### Task 2: home screen, status bar, links and intro animation

**Files:**
- Create: `internal/ui/tui/home.go`, `internal/ui/tui/home_test.go`, `internal/ui/tui/intro.go`, `internal/ui/tui/intro_test.go`, `internal/ui/tui/links.go`
- Delete: `internal/ui/tui/welcome.go`, `internal/ui/tui/welcome_test.go` (move reused helpers — `welcomeVersion`, `shortenHome`, `truncMiddle`, `Model.text` — into `home.go`)
- Modify: `internal/ui/tui/view.go`, `internal/ui/tui/model.go`, `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: Task 1 `Theme`, `Options.Animation`.
- Produces: `func (m Model) homeLines() []string` (centered block, never wider than `m.width`); `func (m Model) statusLine() string`; `func hyperlink(url, text string, enabled bool) string` (OSC 8 `\x1b]8;;URL\x1b\\TEXT\x1b]8;;\x1b\\` when enabled); `func supportsHyperlinks(getenv func(string) string) bool` (true for `TERM_PROGRAM` in `iTerm.app`, `WezTerm`, `vscode`, `ghostty`, or `KITTY_WINDOW_ID` set, or `VTE_VERSION` ≥ 5000; false when `TERM=dumb`); `func tipOfTheDay(lang i18n.Lang, rnd func(int) int) string`; intro as a bubbletea sub-state (`introFrame int`, tick message `introTickMsg`).

- [ ] **Step 1: Failing tests:**
  - `TestHomeWidths`: at 60, 80, 120 columns every line ≤ width; 60 → contains `⬢ TENON`, no art; 120 → art lines present, slogan + version line, the input box, the shortcuts line, the links line.
  - `TestHomeNoColor`: with `NO_COLOR`, no `\x1b[` color sequences.
  - `TestHomeHiddenAfterFirstMessage` and `TestHomeHiddenOnResume`.
  - `TestHyperlinks`: enabled → OSC 8 sequence around `docs`; disabled → plain `docs`.
  - `TestSupportsHyperlinks` table over the env rules.
  - `TestTipOfTheDay`: ≥ 8 tips per language, deterministic with an injected `rnd`.
  - `TestStatusLine`: project (shortened, middle-truncated), model, provider, jurisdiction and sandbox pills, profile when set, session tokens; fits the width.
  - `TestIntroSkipped`: no TTY / `NO_COLOR` / animation false / width < 70 → no intro frame.
  - `TestIntroKeyKept` (Review Focus 4): a key pressed during the intro ends it and the rune is in the input.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Logo frames: two halves of the shield sliding together over 6–8 frames, last frame = the home art.
- [ ] **Step 4: Run** — PASS; Go gates.
- [ ] **Step 5: Commit** `feat(tui): centered home screen, status bar, links and intro`

---

### Task 3: side panel and per-answer badges

**Files:**
- Create: `internal/ui/tui/sidebar.go`, `internal/ui/tui/sidebar_test.go`
- Modify: `internal/ui/tui/view.go`, `internal/ui/tui/model.go` (event handling feeds a `sessionStats`), `internal/ui/tui/transcript.go` (badge), `internal/ui/tui/keys.go` (`Sidebar` = `ctrl+b`), `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces: `type sessionStats struct{ Title string; LastInput, Window int; In, Out int64; Files []fileChange; Sends []sendCount; Sandbox, Profile, Policy string }`; `type fileChange struct{ Path, Op string; Add, Del int }` (`Op` `M|A|D`); `type sendCount struct{ Provider, Country, Jurisdiction string; N int }`; `func (s *sessionStats) apply(ev event.Event)` (from `UserMessage`, `Usage` (main agent only for the gauge), `ToolResult`); `func (m Model) sidebarLines(height int) []string`; transcript entries for agent answers gain the badge of the turn's last `Usage`.

- [ ] **Step 1: Failing tests:**
  - `TestSidebarAutoOpen`: width 99 → no panel; 100 → panel 26 columns + separator; `Ctrl+B` toggles and the choice survives a resize (Review Focus 1: 120 → 60 → 120 keeps the manual choice, every line ≤ width).
  - `TestSessionStats`: two `Usage` (Mistral FR eu ×2, then a fallback provider non-eu ×1) → sends `2 → Mistral · FR`, `1 → <name> · US` in alert color; gauge from the last main-agent `Usage` (`Input` / window) → `███░░░░░░░ 31 %`.
  - `TestSidebarFiles`: three `ToolResult` (edit, write new, edit same file) → 2 entries, ops `M`/`A`, max 8 then `+N`.
  - `TestBadgeFallback` (Review Focus 2): a turn whose last `Usage` is non-EU → badge in alert color with that provider's name.
  - `TestSidebarRedacts` (Review Focus 3): a title and a path containing a configured secret and `\x1b[31m` → masked and sanitized in panel and badge.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; Go gates.
- [ ] **Step 5: Commit** `feat(tui): session side panel and jurisdiction badges`

---

### Task 4: command palette (TUI)

**Files:**
- Create: `internal/ui/tui/palette.go`, `internal/ui/tui/palette_test.go`
- Modify: `internal/ui/tui/model.go`, `internal/ui/tui/view.go`, `internal/ui/tui/keys.go` (`Palette` = `ctrl+p`), `internal/i18n/catalog.go`, `cmd/tenon/run.go` (pass past sessions: `Options.Sessions []session.Summary` loaded with `session.List(sessionsDir(getenv), root)`), `docs/usage.md`, `docs/fr/utilisation.md` (home, panel, palette, themes, `[ui]`)

**Interfaces:**
- Consumes: Tasks 1–3, `skills.Builtins`, `Options.Skills`.
- Produces: `type paletteItem struct{ Kind, Label, Detail, Action string }` (`Kind`: `command|skill|session|theme|info`); `func fuzzyScore(query, s string) (int, bool)`; `func rankItems(query string, items []paletteItem) []paletteItem`; selecting `command`/`skill` puts `/<name> ` in the input, `theme` applies `/theme <name>`, `session` shows `tenon --resume <id>` as a notice line, `info` does nothing.

- [ ] **Step 1: Failing tests:** `TestFuzzyScore` (`"rw"` matches `rewind`, not `memory`; `"mem"` ranks `memory` above `remember`); `TestRankStable` (ties keep input order); `TestPaletteOpenClose` (`Ctrl+P` opens, Esc closes, Enter on `skills` puts `/skills ` in the input); `TestPaletteSessions` (two `session.Summary` → entries with masked titles; Enter → notice with `tenon --resume <id>`); `TestPaletteProviderInfo` (entry text with the active provider id); `TestPaletteFits` (60 columns: every line ≤ width).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; Go gates; `mkdocs build --strict` (docker: `docker run --rm -v "$PWD":/docs -w /docs python:3.12-slim sh -c "pip install -q -r docs/requirements.txt && mkdocs build --strict -d /tmp/site"`).
- [ ] **Step 5: Commit** `feat(tui): command palette; docs: home, panel, palette, themes`

---

### Task 5: extension home, badges, theme, links

**Files:**
- Create: `editors/vscode/webview/home.ts`, `editors/vscode/media/logo.svg` (bundled), `editors/vscode/test/unit/home.test.ts`
- Modify: `editors/vscode/src/model.ts` (agent item `provider?`), `editors/vscode/src/protocol.ts` (`openLink`), `editors/vscode/src/panel.ts`, `editors/vscode/src/extension.ts`, `editors/vscode/webview/{main.ts,render.ts,style.css}`, `editors/vscode/package.json` (`tenon.theme` setting), `package.nls*.json`, `l10n/bundle.l10n.fr.json`, `editors/vscode/test/integration/suite/chat.test.ts`

**Interfaces:**
- Produces: `Item` agent `provider?: { name: string; country?: string; jurisdiction?: string }`, set when an agent item is created/extended from the last `usage_update` `_meta.tenon` (`providerName` || `provider`, `jurisdiction`); `const LINKS = { docs, github, bug } as const` (src only); `FromWebview` `{ type: "openLink"; id: "docs" | "github" | "bug" }`; `renderHome(strings, version, tip): HTMLElement`; `TenonTestApi.openedLinks(): string[]`.

- [ ] **Step 1: Failing tests:**
  - `home.test.ts`: home shown when `items` is empty and no session loaded; contains slogan, version, shortcuts, the three link buttons; no `innerHTML` (spy); hidden once a user item exists.
  - model: `usage_update` with `providerName:"Mistral", jurisdiction:"eu"` then an agent chunk → agent item `provider.name === "Mistral"`; non-EU → badge element has the alert class; unknown jurisdiction → alert class and the localised "unknown jurisdiction" text.
  - protocol (Review Focus 5): `{type:"openLink", id:"docs"}` valid; `id:"evil"` or `id:"https://x"` or extra keys → invalid, nothing opened.
  - theme: `tenon.theme = "tenon"` → webview root gets the tenon class; `auto` → none.
  - integration: home visible on first open, gone after the first prompt; clicking docs records `https://tenon-code-eu.github.io/tenon/` in `openedLinks()` (openExternal stubbed in test mode).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** CSS animation of the two logo halves (< 700 ms) inside `@media (prefers-reduced-motion: no-preference)`.
- [ ] **Step 4: Run** — PASS; extension gates.
- [ ] **Step 5: Commit** `feat(vscode): home screen, jurisdiction badges, theme and links`

---

### Task 6: extension session view and palette

**Files:**
- Create: `editors/vscode/src/sessionView.ts`, `editors/vscode/src/palette.ts`, `editors/vscode/src/stats.ts`, `editors/vscode/test/unit/{stats.test.ts,palette.test.ts}`, `editors/vscode/test/integration/suite/session.test.ts`
- Modify: `editors/vscode/src/extension.ts`, `editors/vscode/src/protocol.ts` (`palette`, `openFileDiff`), `editors/vscode/package.json` (view `tenon.session` under `tenon.chat`, command `tenon.palette`, keybinding), `package.nls*.json`, `l10n/bundle.l10n.fr.json`, `editors/vscode/README.md`, `docs/editors.md`, `docs/fr/editeurs.md`, `editors/vscode/CHANGELOG.md` (`0.10.0 — unreleased`)

**Interfaces:**
- Consumes: Task 5, `Conversation`, `DiffProvider`/`toolDiff`.
- Produces: `computeStats(state: ConversationState): { title?: string; used?: number; size?: number; files: { path: string; toolCallId: string; op: "M" | "A" }[]; sends: { provider: string; country?: string; jurisdiction?: string; n: number }[] }` (the model records `used`/`size` from `usage_update` and one send per `usage_update`); `SessionViewProvider` (`tenon.session`) posting `computeStats` on each coalesced change; `paletteItems(state, skills, sessions): vscode.QuickPickItem[]` with kinds command/skill/session/theme/info; `openFileDiff` message → `DiffProvider.open(toolCallId)`.

- [ ] **Step 1: Failing tests:** `stats.test.ts` (sends grouped by provider+jurisdiction; non-EU flagged; files deduplicated by path, op `A` when the diff has no old text; gauge from the last `usage_update`); `palette.test.ts` (items order: commands, skills, sessions, themes, info; session pick calls load; theme pick updates `tenon.theme`); integration `session.test.ts` (after a write turn the session view lists the file; `openFileDiff` opens the diff with title `<file> (tenon)`).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; extension gates; `mkdocs build --strict`.
- [ ] **Step 5: Commit** `feat(vscode): session view and palette; docs`
