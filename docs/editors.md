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
- tenon sends `notice` updates (startup notices, hook stops) and `usage_update` updates (tokens). Some clients ignore them.
- `session/load` replays messages and tool calls as text. Diffs are not replayed.
- No ACP modes (`session/set_mode`), no remote transport.
