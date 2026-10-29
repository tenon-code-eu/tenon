# Changelog

## 0.10.0 — unreleased

- Home screen until the first message: logo, slogan and version, shortcuts, a tip, links to the docs, GitHub and the bug tracker (opened by the extension; the webview holds no URL).
- Answer badges: provider and jurisdiction of the turn's last call, non-EU or unknown in the alert colour.
- Session view under the chat: title, context gauge, tokens, modified files (a click opens the diff), calls per provider and jurisdiction (every call of the session, sub-agents included), sandbox, profile and policy.
- Palette (`tenon.palette`, Cmd+Shift+; / Ctrl+Shift+; while the chat has focus): tenon commands, skills, past sessions, themes, active provider.
- `tenon.theme` setting: `auto` (editor colours, default) or `tenon` (Europe blue and gold).

## 0.9.0 — 2026-10-28

- First release: tenon agent over ACP, binary resolution and version check.
- Chat panel: streamed answers, thoughts, tool calls, Stop, sessions list and resume, `/` skills.
- Permission cards (notification when the panel is hidden) and read-only diffs.
- Editor context: attach the selection or a file.
- Status bar: provider, jurisdiction and sandbox, following routing fallbacks.
- Terminal mode: the tenon terminal interface beside the editor, with `@<file>#L<a>-<b>` references.
- English and French interface.
- One package per platform (macOS and Linux, arm64 and x64), with the tenon binary bundled.
