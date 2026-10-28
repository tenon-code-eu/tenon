# tenon for VS Code

The [tenon](https://github.com/tenon-code-eu/tenon) coding agent in VS Code.
The extension is a thin ACP client: tools, permissions, sandboxing, masking,
routing and audit all stay in `tenon acp`. The extension makes no network call
and sends no telemetry.

## Install

Install `tenon-code-eu.tenon` from the VS Code Marketplace. For VSCodium,
Cursor and other editors that use Open VSX, download the `.vsix` of your
platform from the [GitHub release](https://github.com/tenon-code-eu/tenon/releases/latest)
and run **Extensions: Install from VSIX…**. Each platform package (macOS and
Linux, arm64 and x64) bundles the tenon binary, so you need nothing else.
Windows is not supported.

To use another build, set `tenon.path` to its absolute path in your user
settings. Otherwise the extension uses the bundled binary, then `tenon` on your
`PATH`. tenon 0.9.0 or later is required.

## Use

- **Chat.** Click the tenon icon in the activity bar. Open a folder first. Enter
  sends, Shift+Enter adds a line, Stop ends the turn. Type `/` for the project
  skills. The Sessions list resumes an earlier session.
- **Context.** Cmd+Alt+K (Ctrl+Alt+K) attaches the selection to your next
  message. Right-click a file, in the Explorer or the editor, to attach it.
- **Permissions.** tenon asks before it writes, runs a command or calls an MCP
  tool. Answer on the card in the chat, or in the notification when the panel is
  hidden. Closing the notification refuses. Stop refuses every pending request.
- **Diffs.** **Open diff** shows a change in the VS Code diff editor, read-only.
- **Status bar.** `tenon · <provider> · <EU|non-EU|local> · <sandboxed|not
  sandboxed>`. A provider outside the EU shows in the warning colour.
  "sandboxed" means tenon confines the shell commands. When tenon stops, the
  bar shows `tenon` alone.
- **Terminal mode.** Cmd+Esc (Ctrl+Esc) opens the tenon terminal interface beside
  the editor. While it is visible, Cmd+Alt+K (Ctrl+Alt+K) types a reference to
  the selection into it, such as `@src/a.go#L10-20`. The key works only while
  the tenon terminal is the active one. If you set
  `terminal.integrated.commandsToSkipShell` yourself, add
  `tenon.insertReference` to your list.
- **Logs.** **tenon: Show Logs** opens the output channel. **tenon: Restart**
  restarts tenon.

The interface is in English or French, following VS Code.

## Documentation

<https://tenon-code-eu.github.io/tenon/editors/>

## License

EUPL-1.2
