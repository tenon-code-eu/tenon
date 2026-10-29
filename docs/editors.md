# tenon in your editor

`tenon acp` makes tenon an [Agent Client Protocol](https://agentclientprotocol.com) (ACP) agent. The editor starts `tenon acp` as a subprocess and talks to it over stdin and stdout (JSON-RPC 2.0, one message per line). You chat with tenon in the editor panel; tenon runs with its own tools and rules.

## Contents

- [Before you start](#before-you-start)
- [Zed](#zed)
- [JetBrains IDEs](#jetbrains-ides)
- [Neovim](#neovim)
- [VS Code](#vs-code)
- [What stays enforced](#what-stays-enforced)
- [Editor MCP servers](#editor-mcp-servers)
- [Limits](#limits)

## Before you start

1. Install tenon and check that `tenon config check` passes in your project.
2. Store your provider key with `tenon key set <provider>` or in the environment variable of the provider. The editor never sees the key: tenon announces no authentication method (`authMethods: []`).
3. Use the full path of the binary if the editor does not see your shell `PATH` (for example `/opt/homebrew/bin/tenon`). `which tenon` prints it.

The editor sends the project folder as `cwd`. tenon then starts exactly as `tenon` started in that folder: global and project configuration, signed policy, profile, allowlists, sandbox probe, routing, MCP servers. A refusal at startup (for example `[sandbox] mode = "required"` without a working sandbox) is shown by the editor as an error with tenon's message.

## Zed

Add tenon under `agent_servers` in your Zed `settings.json`:

```json
{
  "agent_servers": {
    "tenon": {
      "type": "custom",
      "command": "tenon",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

Then open the Agent panel and start a new thread with tenon.

Source: [Zed, External Agents](https://zed.dev/docs/ai/external-agents) (section "Custom Agents").

## JetBrains IDEs

The AI Assistant reads custom agents from `~/.jetbrains/acp.json`. In the AI Chat tool window, click the more button (⋯), then **Add Custom Agent**: the IDE creates and opens the file. Add tenon under `agent_servers`:

```json
{
  "agent_servers": {
    "tenon": {
      "command": "/full/path/to/tenon",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

The key (`tenon`) is the name shown in AI Chat. Restart the IDE if the agent does not appear.

Source: [JetBrains AI Assistant, Agent Client Protocol](https://www.jetbrains.com/help/ai-assistant/acp.html).

## Neovim

Two plugins act as ACP clients.

**avante.nvim** reads ACP agents from `acp_providers`:

```lua
vim.g.avante = {
  acp_providers = {
    ["tenon"] = {
      command = "tenon",
      args = { "acp" },
      env = {},
    },
  },
}
```

Select it with `:AvanteSwitchProvider`.

Source: [avante.nvim README](https://github.com/yetone/avante.nvim) (section ACP) and the `acp_providers` example in [lua/avante/config.lua](https://github.com/yetone/avante.nvim/blob/main/lua/avante/config.lua).

**CodeCompanion.nvim** defines ACP adapters under `adapters.acp`. A custom adapter has `type = "acp"`, the command in `commands.default` and `defaults.mcpServers`; it also needs the `handlers` block of the documented template, which we do not copy here. Start from the "custom adapter" example of the page below, with:

```lua
-- extract; see the full template in the CodeCompanion docs
name = "tenon",
formatted_name = "tenon",
type = "acp",
commands = {
  default = { "tenon", "acp" },
},
defaults = {
  mcpServers = {},
},
```

Then select it with `interactions = { chat = { adapter = "tenon" } }`.

Source: [CodeCompanion, Configuring ACP adapters](https://codecompanion.olimorris.dev/configuration/adapters-acp).

## VS Code

### The tenon extension

**Install.** Search for **tenon** in the Extensions view, or install `tenon-code-eu.tenon` from the [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=tenon-code-eu.tenon). VSCodium, Cursor and other editors that use Open VSX: download the `.vsix` of your platform from the [GitHub release](https://github.com/tenon-code-eu/tenon/releases/latest), check it against the signed `SHA256SUMS`, then run **Extensions: Install from VSIX…**. There is one package per platform: macOS and Linux, arm64 and x64. Each package bundles the tenon binary, so you need nothing else. Windows is not supported.

**The binary.** The extension looks for tenon in this order:

1. the `tenon.path` setting (an absolute path, set in your user settings);
2. the binary bundled with the extension;
3. `tenon` on your `PATH`.

tenon 0.9.0 or later is required. If tenon is missing or too old, a notification offers to install it or to choose the binary.

**The panel.** Click the tenon icon in the activity bar, or run **tenon: Open Chat**. Open a folder first: tenon works in the folder. Type your request and press Enter (Shift+Enter for a new line). Stop ends the turn. Type `/` to see the project skills and commands. The Sessions list resumes an earlier session of the folder. The `+` button starts a new session.

**Home.** Until the first message of a session, the chat shows the tenon home: the logo, `sovereign coding agent` and the tenon version, the input in the middle, the shortcuts (`/ skills · Shift+Enter new line · Tab complete · Cmd+Shift+; palette`, Ctrl+Shift+; on Linux), a tip and three links: docs, github, report a bug. The links open in your browser; the chat page itself holds no URL and makes no request. A resumed session shows its thread at once. The logo animation is off when the system asks for reduced motion.

**Answer badges.** Under each finished answer, a badge names the provider of the turn's last call and its jurisdiction: `<provider> · <EU|non-EU|local>`. A provider outside the EU, or an unknown jurisdiction, shows in the alert colour. After a routing fallback, the badge shows the provider that answered. tenon sends no country to the editor, so the badge has none.

**Session view.** The **Session** view, under the chat in the tenon side bar, shows the open session like the side panel of the terminal interface:

- the title (the session's title, else its first message);
- the context gauge (input tokens of the last call against its provider's window) and the input and output tokens of the session;
- the modified files (`A` created, `M` modified). Click a file to open its last diff, as with **Open diff**;
- sovereignty: the number of calls per provider and jurisdiction, non-EU and unknown in the alert colour, then the sandbox, the profile and the policy.

The view counts what `tenon acp` sends: the calls of the main agent (one `usage_update` per call), not those of sub-agents. For a resumed session, `session/load` replays each call's `usage_update`, so the calls and tokens include the history. Diffs are not replayed, so a resumed session lists no modified files until its next write.

**Palette.** **tenon: Palette** (Cmd+Shift+;, Ctrl+Shift+; on Linux, while the chat has focus, or the `⋯` button next to Send) opens a quick pick with:

- the commands: new session, attach selection, attach file, open in terminal, restart, logs;
- the skills of the session: a pick puts `/<skill> ` in the input;
- the past sessions of the folder: a pick resumes the session;
- the themes, `auto` and `tenon`: a pick sets `tenon.theme`;
- the active provider. The extension cannot change it: set `default_provider` in the tenon configuration, then restart tenon.

**Theme.** The `tenon.theme` setting colours the chat and the Session view. `auto` (default) takes the colours of your VS Code theme. `tenon` applies the tenon colours: Europe blue `#003399`, gold accent `#FFCC00`, green for EU and local, amber for non-EU. On a dark theme the blue text is lighter, for contrast.

**Context.** Select code and press Cmd+Alt+K (Ctrl+Alt+K on Linux), or right-click and choose **Attach Selection to Chat**. The selection goes with your next message, with its lines. **Attach File to Chat**, in the editor or on a file in the Explorer, sends a link to a file; tenon reads it with its tools, under permission.

**Permissions.** When tenon wants to write a file, run a command or call an MCP tool, a card appears in the chat. Click one of tenon's options: Allow, Always for this session, Reject, and Allow with network for a sandboxed command. When the panel is hidden, a notification asks instead. Closing the notification refuses. Stop refuses every pending request. The extension never answers on its own.

**Diffs.** A tool call that changes a file shows **Open diff**. The diff opens in the VS Code diff editor, titled `<file> (tenon)`, read-only. The text is tenon's, with secrets masked.

**Status bar.** The status bar shows `tenon · <provider> · <EU|non-EU|local> · <sandboxed|not sandboxed>`. A provider outside the EU, or an unknown jurisdiction, shows in the warning colour. After a routing fallback, the bar shows the provider that answered. "sandboxed" means tenon confines the shell commands; with `[sandbox] mode = "off"`, or no sandbox on the machine, the bar says "not sandboxed". When tenon stops, the bar shows `tenon` alone. Click it to open the chat.

**Terminal mode.** Run **tenon: Open in Terminal** (Cmd+Esc, Ctrl+Esc on Linux). The tenon terminal interface opens in a terminal named "tenon", beside the editor, in the folder of the workspace. The same command shows it again while it is open. While this terminal is visible, Cmd+Alt+K (Ctrl+Alt+K) types a reference to the selection into it: `@src/a.go#L10-20`, `@src/a.go#L5` for one line, `@src/a.go` with no selection. The key works only while the tenon terminal is the active terminal. The extension adds `tenon.insertReference` to `terminal.integrated.commandsToSkipShell`, so the terminal does not eat the key. If you set `terminal.integrated.commandsToSkipShell` in your own settings, add `tenon.insertReference` to your list.

**Language.** The extension follows the language of VS Code: English or French.

**Logs.** **tenon: Show Logs** opens the "tenon" output channel. **tenon: Restart** restarts `tenon acp`.

**How it works.** To list and resume the sessions of the open folder, the extension uses `session/list` and the `_meta.tenon` metadata (provider, provider name, model, jurisdiction, sandbox with its `confined` flag, profile, policy) that `tenon acp` returns for `session/new` and `session/load`. Each `usage_update` carries the provider, name and jurisdiction of the call, so a fallback shows at once. The extension makes no network call of its own and sends no telemetry. Its webviews post only fixed messages to the extension (a link id, never a URL; a tool call id for a diff), and the extension checks each one before it acts.

### Third-party client

VS Code has no built-in ACP client. The third-party extension [ACP Client for VS Code](https://github.com/formulahendry/vscode-acp) (`formulahendry/vscode-acp`) is one. It reads agents from the `acp.agents` setting: each key is the agent name, with `command`, `args` and `env`. In `settings.json`:

```json
{
  "acp.agents": {
    "tenon": {
      "command": "tenon",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

Setting `acp.agents` replaces the default list of the extension. Copy the defaults first if you want to keep them.

Source: the `acp.agents` setting in the extension [package.json](https://github.com/formulahendry/vscode-acp/blob/main/package.json). We did not test other VS Code extensions.

## What stays enforced

The editor is only a display. tenon keeps all of its guarantees:

- **Permissions.** Writes, commands and MCP calls that need approval are asked in the editor: Allow, Always for this session, Reject, and Allow with network for a sandboxed command. "Always for this session" never grants the network. A cancelled request or a client error is a refusal.
- **Sandbox.** Commands run in tenon's system sandbox, as in the terminal.
- **Masking.** Secrets are masked before they reach the model, the audit log and the editor (tool input, results, diffs).
- **Routing.** Classification, local routing and fallbacks apply. Sending to a provider outside the EU asks for consent in the editor; the default is refusal.
- **Audit.** Each session writes the same hash-chained audit log. `SessionStarted` records the editor (`client: {name, version}`) and `transport: "acp"`. The compliance export shows the editor of each session.

tenon uses its own tools only. It never calls the editor's `fs/*` or `terminal/*` methods.

## Editor MCP servers

Some editors send their own MCP servers when they open a session. tenon ignores them by default and shows the notice `editor MCP servers ignored ([acp] accept_editor_mcp)`.

To accept them, set this in the **global** configuration (it is refused in a project configuration, and an organisation policy can force it to `false`):

```toml
[acp]
accept_editor_mcp = true
```

Accepted servers go through the same filters as the servers of your configuration: profile, policy, jurisdiction consent, sandbox, audit. Defaults:

- stdio server: `jurisdiction = "local"`, no declared `risk` (no network, project read-only in the sandbox);
- http server: `jurisdiction = "non-eu"` (consent asked), or `local` for `localhost`, `127.0.0.1` and `::1`;
- sse servers are not supported;
- a server with the same id as one of your configuration is skipped: your configuration wins.

Environment variables and HTTP headers sent by the editor are dropped, with a notice. tenon never stores or logs their values. A server that needs a secret must be declared in tenon's configuration (`env_from`, `bearer_env`).

## Limits

- No images or audio in prompts: the editor gets an "invalid params" error.
- tenon does not read editor buffers. It reads files from disk with its own tools. Save your files before you ask.
- Attached files (`resource` blocks) are added to the message, so they are masked and classified like typed text. File links (`resource_link`) are mentioned by path; tenon reads them with its tools, under permission.
- One turn at a time per session. A second prompt during a turn gets `turn in progress`.
- tenon sends its notices (startup notices, hook stops) as agent thoughts (`agent_thought_chunk`, prefixed `tenon:`), and tokens as `usage_update` (`used`: input tokens of the last call, `size`: context window of its provider). Some clients ignore `usage_update`.
- `session/load` replays messages, tool calls (as text) and the `usage_update` of each call, then replies. A resumed session's token count (`sessionTokens`) therefore includes its history. Diffs are not replayed.
- No ACP modes (`session/set_mode`), no remote transport.
