# Usage guide

This guide describes the behavior of tenon as it is coded today.
For security guarantees and limits, see [security.md](security.md).
For a commented configuration example, see [config.example.toml](config.example.toml).

## Contents

- [Commands](#commands)
- [Interactive interface](#interactive-interface)
- [Non-interactive mode](#non-interactive-mode)
- [Sessions](#sessions)
- [Language](#language)
- [Configuration](#configuration)
- [Skills](#skills)
- [Sub-agents](#sub-agents)
- [Hooks](#hooks)
- [Checkpoints and rewind](#checkpoints-and-rewind)
- [Providers and jurisdictions](#providers-and-jurisdictions)
- [Compliance profiles](#compliance-profiles)
- [API keys and keyring](#api-keys-and-keyring)
- [MCP servers](#mcp-servers)
- [Git marking of generated code](#git-marking-of-generated-code)
- [Audit log](#audit-log)
- [Files and directories](#files-and-directories)

## Commands

```
tenon [--resume <id|last>] [--lang fr|en] [--allow-non-eu] [--provider <id>]
tenon -p "<prompt>" [--resume <id|last>] [--lang fr|en] [--output text|json] [--strict] [--allow-non-eu] [--provider <id>]
tenon sessions [--lang fr|en]
tenon skills [--lang fr|en]
tenon agents [--lang fr|en]
tenon hooks [--lang fr|en]
tenon rewind --list --resume <id|last> [--lang fr|en]
tenon key set|delete <provider>
tenon audit verify [--tsa-ca <pem>]
tenon audit timestamp
tenon audit export [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project <path> | --all-projects | --session <id>] [--details] [--tsa-ca <pem>] [--out <dir>] [--lang fr|en]
tenon config check
tenon git install-hook [--force]
tenon git mark <message-file>
tenon --version
tenon --help
```

In the synopsis, `<provider>` is the provider ID and `<message-file>` is the commit message file.

| Option | Effect |
|---|---|
| `-p "<prompt>"` | Runs one turn without the interactive interface. |
| `--output text\|json` | Output format of the non-interactive mode. Default: `text`. Refused without `-p`. |
| `--strict` | Exit code 4 when the turn ends with `max_iterations`, `incomplete`, `stopped_by_hook` or `cancelled`. Code 3 and code 1 take precedence. Refused without `-p`. |
| `--provider <id>` | Provider to use. Default: `default_provider` from the configuration. |
| `--allow-non-eu` | Accepts sending data outside the EU, for providers and for MCP servers. |
| `--resume <id\|last>` | Resumes a session of the current project. Refused with a subcommand other than `sessions`. |
| `--lang fr\|en` | Interface language for this run. |
| `--version` | Shows the version and exits. |

You can write options with one or two dashes (`-p` or `--p`).
Put them before the subcommand. Exception: `--lang` is also accepted after `tenon sessions`.

The **project** is the current directory when tenon starts.
It is not necessarily the root of the git repository.

## Interactive interface

Run `tenon` without `-p`.
Standard input and standard output must be a terminal. Otherwise tenon stops with code 1.

The status bar always shows:
`tenon · model · provider · country · jurisdiction · session · ~tokens/window`.
The token count is an estimate.

### Keys

| Key | Effect |
|---|---|
| Enter | Sends the message. |
| Alt+Enter or Ctrl+J | Inserts a line break. |
| Esc | Cancels the current turn. Twice in less than 1 s outside a turn: opens `/rewind`. |
| Ctrl+C | Cancels the current turn. Outside a turn, quits. Twice in less than 2 s during a turn: quits. |
| PgUp / PgDn | Scrolls the conversation by one page (on a Mac laptop: fn + ↑ / fn + ↓). |
| Shift + ↑ / Shift + ↓ | Scrolls by one line. |
| Mouse wheel or trackpad | Scrolls by 3 lines. To select text with the mouse, hold Option (iTerm2, Terminal) or Shift (most Linux terminals) while dragging. |
| Ctrl+U / Ctrl+D | Scrolls by half a page, when the input is empty (otherwise they edit the input). |
| Home / End | Goes to the top or the bottom of the conversation, when the input is empty. |
| `/help` | Shows the key help, one line per built-in command, and the available skills. |
| `/skills` | Lists the skills. |
| `/agents` | Lists the sub-agents (see [Sub-agents](#sub-agents)). |
| `/hooks` | Lists the hooks (see [Hooks](#hooks)). |
| `/rewind` | Goes back to an earlier prompt (see [Checkpoints and rewind](#checkpoints-and-rewind)). |
| `/<name> [arguments]` | Starts a skill (see [Skills](#skills)). |
| Tab | Completes a command or a skill name after `/`. |
| `/quit` | Quits. |

While you read higher up, new text does not move the view: the status bar starts with `↓ new text`. End, PgDn or the wheel down to the bottom brings back automatic following.

### Welcome screen

At startup, below the project line, the welcome screen shows:

```
skills: 5 · agents: 3 · hooks: 2
commands: /help · /quit · /skills · /agents · /hooks · /rewind
Enter to send · Esc twice to rewind · Ctrl+C to quit
```

- The counts are the number of entries listed by `/skills`, `/agents` (the built-in `general-purpose` included) and `/hooks`. They are shown even at 0.
- The command list is the list of built-in commands (the one Tab completes). Skills are not in it.
- Long lines wrap at the ` · ` separators instead of being cut.
- `/help` shows the same commands, one per line, with a short description.

When you quit, tenon waits at most 10 s for the current turn to end.
After that, it exits with code 1.

### Approve an action

When the agent wants to write, run a command or go outside the project, a panel replaces the input area.
It shows the tool, the full command or the paths, and a preview of the change for `write_file` and `edit_file`.

| Answer | English | French |
|---|---|---|
| Yes, once | `y` | `o` |
| Yes for the session | `a` | `s` |
| No | `n` | `n` |

Esc also denies the request.
Ctrl+C denies all pending requests, then acts as usual.

"Yes for the session" applies to the same tool with exactly the same command or the same paths.
It is not offered for sending to a provider outside the EU.
It is not kept when a session is resumed.

To prevent an accidental approval, tenon ignores the approval keys until all these conditions are met:

- **Quiet period of 500 ms.** 500 ms must pass without any key press since the request appeared and since the previous key. Any key, even an ignored one, restarts this period. A paste is never an answer.
- **Full command visible.** The title, the full command, all paths and the keys must fit on the screen. Only preview lines can be cut. Otherwise the panel shows "command too long to review: enlarge the terminal or deny" or "enlarge the terminal to approve".
- **Width of at least 40 columns.**
- **Preview ready.** While the preview is computed, only denial works.

A denial (`n`, Esc) always works immediately.

### Unambiguous display

The command and the paths are shown so that no character is hidden:

- `↪` starts the continuation of a line wrapped at the terminal width. This way, a wrap is distinct from a real line break.
- `␠` replaces a space at the end of a segment, which is otherwise invisible.
- `[U+XXXX]` replaces a control character, an invisible or zero-width character, a bidirectional character, a space other than the ASCII space, and the characters `↪`, `␠` and U+FFFD themselves.
- `[0xXX]` replaces an invalid byte.
- A `[` followed by `U+` or `0x` in the original text is written `[U+005B]`.
- A sequence of at least 3 empty lines becomes `⏎ × N` when the command does not fit on the screen. In that case, the request cannot be approved.

For a shell command, the panel reminds you that an approved command runs with your permissions, without a sandbox.
If the command contains a non-ASCII character, a warning appears.
For an MCP tool, the panel shows the server, its country and its jurisdiction.

## Non-interactive mode

```
tenon -p "fix the failing tests"
tenon -p "summarize this repository" --output json
```

Any action that would need an approval is **denied**.
Only reads inside the project and actions covered by an `allow` rule run.

### Outputs

- `--output text` (default): the final answer goes to standard output. The banner, warnings and denials go to standard error.
- `--output json`: one JSON event per line on standard output, with a redacted payload. Fields: `id`, `session`, `seq`, `ts`, `type`, `payload`. The first event, `SessionStarted`, contains the model, the provider, the country and the jurisdiction. When the model launches a sub-agent, the events `SubagentStarted` and `SubagentCompleted` appear, and the events of the sub-agent carry a `parent_call_id` field (see [Sub-agents](#sub-agents)).

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Turn completed. A turn stopped by the iteration limit or by an incomplete answer also gives 0, with a message. |
| `1` | Error: configuration, missing key, provider, context too large, invalid option. |
| `3` | An action was denied (approval not possible, `deny` rule) or sending to the provider was blocked (jurisdiction). |
| `4` | With `--strict`: the turn ended with `max_iterations`, `incomplete`, `stopped_by_hook` or `cancelled`. The line `strict: turn ended with <reason>` goes to standard error (text output only). Code 3 wins over 4, and code 1 wins over both. |
| `130` | Second Ctrl+C: immediate exit. The first one cancels the turn. |

Subcommands return 0 on success and 1 on error.
`tenon git mark` always returns 0.

## Sessions

Each conversation is saved, redacted, in `$XDG_DATA_HOME/tenon/sessions/<id>.jsonl`.
An identifier has the form `s_` followed by 12 hexadecimal characters.

```
tenon sessions                 # sessions of the current project, most recent first
tenon --resume last            # resumes the most recent one
tenon --resume s_0123456789ab  # resumes a specific session
tenon --resume last -p "continue"
```

For each session, `tenon sessions` shows: identifier, update date, number of turns, first message.

You can resume a session only from the project where it was opened.
A session open in another terminal is locked: tenon refuses to resume it.

If a session file is damaged, the resume continues up to the last readable line, with a warning.
The file is cut at the first unreadable line. The full original is kept in `<id>.jsonl.corrupt`.

## Language

The language of the interface and of the system prompt is, in order of priority:

1. the `--lang fr|en` option;
2. the `lang` key of the project configuration, then of the global configuration;
3. otherwise English (`en`).

The default is English.
For French, set `lang = "fr"` in the configuration or run with `--lang fr`.

The approval keys follow the language: `y`/`a`/`n` in English, `o`/`s`/`n` in French.

## Configuration

| File | Location |
|---|---|
| Global | `$XDG_CONFIG_HOME/tenon/config.toml`, otherwise `~/.config/tenon/config.toml` |
| Project | `.tenon/config.toml` in the current directory |

A missing file is ignored.
An unknown or misspelled key is refused.
`tenon config check` loads both files and shows `OK`, or the error with code 1.
It contacts no provider and no MCP server.

Default values:

| Key | Default |
|---|---|
| `lang` | `"en"` (see [Language](#language)) |
| `default_provider` | `"mistral"` |
| `max_iterations` | `50` |
| `shell_timeout` | `"2m"` |
| `output_limit` | `30000` characters |
| `compaction_threshold` | `0.8` |
| `audit.level` | `"full"` |
| `audit.timestamp` | absent (no timestamping; see [Timestamping](#timestamping-rfc-3161)) |
| `checkpoints.retention_days` | `30` |

### What the project configuration can do

A repository can be hostile. For this reason, its configuration is limited to:

- `lang`;
- `deny` and `ask` permission rules;
- `[[redaction.rules]]` redaction rules;
- `profile` and `provider_allowlist`: both restrict-only (the project can raise the profile or narrow the allowlist, never relax them).

Errors in the project file are reported with the prefix `project config:`.

It **cannot**:

- add an `allow` rule (loading fails);
- choose or declare a provider;
- declare an MCP server;
- change the audit level, the timeouts or the limits.

Project rules are added to global rules.
A project `deny` rule always blocks the call.
A project `ask` rule takes precedence over a global `allow` rule: the project can only restrict.
A global `ask` rule, however, is evaluated after the `allow` rules.

### Permission rules

```toml
[[permissions.rules]]
tool = "shell"          # or read_file, write_file, edit_file, grep, glob, mcp__<server>__<tool>, "*"
match = "go test *"     # * = any sequence of characters; empty = every call of the tool
decision = "allow"      # allow, ask or deny
```

Evaluation order:

1. a matching `deny` rule: denied;
2. a path outside the project, or a file `.env*`, `*.pem`, `id_rsa*`: approval requested;
3. a matching `ask` rule from the project configuration: approval requested;
4. a matching `allow` rule: allowed;
5. a matching global `ask` rule: approval requested;
6. otherwise: reads allowed, everything else needs approval.

The pattern is anchored: it must cover the whole compared text. `*` replaces any sequence of characters (`/` and spaces included).
The compared text depends on the tool:

| Tool | Text compared with the pattern |
|---|---|
| `read_file`, `write_file`, `edit_file`, `grep`, `glob` | the path, relative to the project root |
| `shell` | the command, as is |
| `mcp__<server>__<tool>` | `mcp <server>.<tool> <compact JSON arguments>`, for example `mcp gh.create_issue {"title":"x"}` |
| `agent` | the agent name (`general-purpose` when `agent` is omitted) |

So a path pattern does not apply to commands: `tool = "*"`, `match = "secrets/*"` blocks `read_file secrets/k`, but not the command `cat secrets/k`.
To protect a directory, combine a path rule and a shell rule:

```toml
[[permissions.rules]]
tool = "*"
match = "secrets/*"     # file tools
decision = "deny"

[[permissions.rules]]
tool = "shell"
match = "*secrets/*"    # any command that mentions secrets/
decision = "deny"
```

Even so, `cd secrets && cat k` or `cat secr*/k` get through: permissions filter calls, they are not a sandbox (see [security.md](security.md#permissions-are-not-a-sandbox)).

An `allow` rule on a command never covers a chained command: `go test *` does not allow `go test ./... ; rm x`.
`grep` and `glob` show only the files that `read_file` would read without a request.

### Project instructions

`AGENTS.md`, then `TENON.md`, at the project root, are added to the system prompt.
Each file is limited to 32,768 bytes.
A symbolic link is ignored.

## Skills

A skill is a folder that contains a `SKILL.md` file: instructions that the model, or you, load on demand.
tenon reads the open Agent Skills format. A skill written for Claude Code works without change.

### Folders and priority

A skill is a subfolder `<name>/` with a `SKILL.md` file in it.
tenon looks in these folders, from the highest priority to the lowest:

1. user: `$XDG_CONFIG_HOME/tenon/skills` (else `~/.config/tenon/skills`), `~/.claude/skills`, `~/.agents/skills`;
2. project, at the project root only: `.tenon/skills`, `.claude/skills`, `.agents/skills`.

When two skills have the same name, the first one found wins. The other is ignored and a notice reports it.
So a user skill always shadows a project skill of the same name.
A missing folder is ignored without a notice.
`~/.claude/skills` and `~/.agents/skills` are ignored when `HOME` is empty or is not an absolute path.
An empty or relative `XDG_CONFIG_HOME` is replaced by `$HOME/.config`. If neither is absolute, the user tenon folder is ignored too.

Reads follow the same rules as [project instructions](#project-instructions): only regular files are read, and a symbolic link or a FIFO is ignored.
A skill folder that is itself a symbolic link is skipped. So is a project skills directory (for example `.claude/skills` in the project) that is a symbolic link: a notice reports it. A user skills directory may be a symbolic link (for example `~/.claude/skills` managed with dotfiles).
`SKILL.md` is limited to 32,768 bytes: the rest is cut and marked as truncated.
tenon keeps at most 200 skills. The following ones are ignored, with a notice.
An unreadable folder or an invalid skill produces a notice and never prevents startup.

### Format of `SKILL.md`

The file starts with `---`, a YAML block, then `---`. The block is limited to 8 KiB and 200 lines.
Fields that tenon reads:

| Field | Rule |
|---|---|
| `name` | Required. Characters `[a-z0-9-]`, 1 to 64, equal to the folder name. |
| `description` | Required, not empty, 1024 characters at most. |
| `disable-model-invocation` | Boolean, default `false`. With `true`, the skill is not listed for the model: only `/<name>` starts it. |
| `allowed-tools` | String or list. Shown and logged, but it has no effect: it never changes permissions. |

Other fields (`license`, `metadata`, `compatibility`...) are ignored.
tenon uses its own minimal YAML parser: top-level keys, plain or quoted values, `>` and `|` blocks, `- item` and `[a, b]` lists, and booleans.
Nested tables are skipped.
A skill with an invalid header or name is ignored, with a notice.

### Use by the model

The system prompt gets a section after the project instructions:

```
## Available skills
Call the `skill` tool with a skill's name before acting when its description matches the request.
- <name>: <description>
```

Skills with `disable-model-invocation: true` are not in this list.
The section is limited to 8,000 bytes. Beyond that, descriptions are cut to 200 characters. If that is not enough, the last skills are removed and a notice gives their number.

The model loads a skill with the `skill` tool, with `{"name": "<name>"}`.
The result is a header `Skill <name> (<source>, <absolute folder>)`, the body of `SKILL.md` without the YAML header, then the list of files of the folder (3 levels, 100 entries at most).
With `{"name": "<name>", "file": "<relative path>"}`, the tool reads one file of the skill folder.
It refuses absolute paths, `..` components, symbolic links, files that are not regular, and sensitive files (`.env*`, `*.pem`, `id_rsa*`).
The content of a file is limited by `output_limit`, like `read_file`.
An error (unknown skill, refused or missing file) is returned to the model as an error result, and the turn continues.

The `skill` tool is a read tool: it is allowed without a request, even for a user skill outside the project.
`deny` and `ask` rules apply to `tool = "skill"`. The `match` pattern is the skill name, or `<name>/<file>` for a file.
A project can forbid a skill with `match = "<name>*"`.
The scripts of a skill run through the `shell` tool, with the usual approval.

### Use by you: `/<name>`

In the interface, or with `-p`, `/<name> [arguments]` starts a skill.
tenon sends one message to the model:

```
Use the skill "<name>".
<skill name="<name>" source="<source>" dir="<absolute folder>">
<body of SKILL.md>
</skill>
User request: <arguments>
```

The last line is omitted when there are no arguments.
This works also for a skill with `disable-model-invocation: true`.
The skill file is read again each time. If it was truncated, `[… truncated]` ends the body.

- `/help`, `/quit` and `/skills` are built-in commands and win over a skill of the same name. Such a skill is available to the model only, and tenon shows a notice at startup. In the interface, a built-in name followed by arguments (for example `/help foo`) is refused with `unknown skill or command`, whether or not a skill has that name.
- `/<unknown>` shows `unknown skill or command: /<unknown>` and sends nothing. In the interface, your text stays in the input to correct it.
- A text that starts with `/ ` (slash, then space) is sent as is. A text that starts with `//` is sent without the first slash.
- The substitutions of Claude Code (`$ARGUMENTS`, `` !command ``, `@file`) are not supported.

In the interface:

- `/skills` lists the skills: name, source and description.
- `Tab` completes a command or a skill name after a `/` at the start of the input: a single match is completed with a space, several matches are completed up to their common prefix.
- `/help` also lists the available skills.

In non-interactive mode, `-p "/<name> ..."` expands the skill the same way.
An unknown `/x` stops with exit code 1 before any session or audit entry is created.
`/help`, `/quit` and `/skills` are commands of the interactive interface: with `-p`, they are sent to the model as plain text, with or without arguments.

### List the skills

```
tenon skills [--lang fr|en]
```

One line per skill: name, source (`user` or `project`), absolute folder, description, and `allowed-tools: ...` when the skill has it.
Discovery notices follow. Without skills, tenon prints `no skills`.
At startup, the banner adds `skills: N (project: M)` when there is at least one skill.
Names, descriptions and notices are shown with control characters replaced by spaces, because project skills are not trusted.

### Session and audit

- Each load by the `skill` tool is an ordinary tool call in the [audit log](#audit-log). The `hash_after` field of the result holds the SHA-256 of the bytes read, in the form `sha256:<hex>`. `SKILL.md` is read up to 32,768 bytes; an annex file is read up to four times the `output_limit` plus one byte (1 MiB when there is no limit), so the hash covers that prefix when the file is longer. The log proves which version guided the agent.
- A message started with `/<name>` records the fields `skill` (the name) and `skill_sha256` in the `UserMessage` event.
- `tenon sessions` shows the typed `/<name> ...` as first message, not the body of the skill.
- `--resume` replays the text that was sent, not the skill read again.

## Sub-agents

A sub-agent is a helper that the main agent launches for a self-contained task.
It works in its own context and returns one final answer.
tenon reads the Claude Code format (`.claude/agents/*.md`). An agent written for Claude Code works without change.

### Folders and priority

A sub-agent is a file `<name>.md`: a YAML header, then the body, which is its system prompt.
tenon looks in these folders, from the highest priority to the lowest:

1. user: `$XDG_CONFIG_HOME/tenon/agents` (else `~/.config/tenon/agents`), `~/.claude/agents`, `~/.agents/agents`;
2. project, at the project root only: `.tenon/agents`, `.claude/agents`, `.agents/agents`.

The rules are the same as for skills. When two agents have the same name, the first one found wins and a notice reports the other.
A user folder is ignored when `HOME` is empty or is not an absolute path.
A project agents directory that is a symbolic link is skipped, with a notice. Only regular files are read.
A file is limited to 32,768 bytes: the rest is cut, with a notice, and `tenon agents` shows `(truncated)` after the path. The header of an agent file is limited to 16 KiB and 400 lines. tenon keeps at most 100 agents.
An unreadable folder or an invalid agent produces a notice and never prevents startup.

### Format of an agent file

```markdown
---
name: reviewer
description: Reviews a change for bugs and missing tests.
tools: Read, Grep
model: sonnet
---
You are a careful code reviewer. Read the files you are given and report problems.
```

| Field | Rule |
|---|---|
| `name` | Required. Characters `[a-z0-9-]`, 1 to 64. It does not have to match the file name: the agent is known by its `name`. |
| `description` | Required, not empty. Beyond 1024 characters it is cut to 1024, with a notice. |
| `tools` | Optional. Comma-separated string or list. Without it, the sub-agent gets all the tools of the session except `agent`. An empty `tools:` key means no tool at all (`tenon agents` shows `tools: none`). |
| `model` | Read and shown, but it has no effect: a sub-agent always uses the provider and the model of the session. |

Other fields (`color`...) are ignored.
The names of Claude Code tools are translated:

| Claude Code | tenon |
|---|---|
| `Read` | `read_file` |
| `Write` | `write_file` |
| `Edit`, `MultiEdit` | `edit_file` |
| `Bash` | `shell` |
| `Grep` | `grep` |
| `Glob` | `glob` |
| `Skill` | `skill` |

tenon names and `mcp__<server>__<tool>` names are accepted as they are.
A restriction in parentheses (`Bash(git:*)`) is removed: only the tool name counts.
An unknown name (for example `WebFetch`, `Task` or `agent`) is ignored, with a notice.
The sub-agent gets only the listed tools that exist in the session, and never `agent`.

### Built-in agent

`general-purpose` always exists: all the tools except `agent`, and no extra instructions.
Its description is `General-purpose agent for multi-step research, code search and tasks.`
A file named `general-purpose` replaces it.

### Use by the model

The system prompt gets a section after the skills:

```
## Available agents
Use the `agent` tool to delegate a self-contained task. The sub-agent does not see this conversation: give it everything it needs in `prompt`.
- <name>: <description>
```

The section is limited to 4,000 bytes. Beyond that, descriptions are cut to 200 characters. If that is not enough, the last agents are removed and a notice gives their number.

The model launches a sub-agent with the `agent` tool:

| Argument | Rule |
|---|---|
| `agent` | Optional. Name of the agent. Default: `general-purpose`. |
| `description` | Short label of the task (3 to 5 words), shown in the interface. Required by the tool schema, but not checked. |
| `prompt` | Required and checked. Complete instructions: the sub-agent does not see the conversation. |

The result is the last answer of the sub-agent, cut by `output_limit` and masked like any tool result.
An unknown agent (`unknown agent: <name>`), an empty `prompt` or invalid arguments are returned to the model as an error result, and the turn continues.

The sub-agent has its own history: the instructions of tenon and of the project, a short preamble, the body of the agent file, then your `prompt`.
It shares the provider, the model, the masking, the permission rules and the approvals "yes for the session" with the main agent.
`max_iterations` applies to the sub-agent. If it is reached, the result is an error that starts with `sub-agent stopped: iteration limit reached`, followed by the last partial answer.
A provider error also gives an error result. Esc or Ctrl+C cancels the whole turn.

### Permissions

The `agent` tool is a read tool: it is allowed without a request.
`deny` and `ask` rules apply to `tool = "agent"`. The `match` pattern is the agent name.

```toml
[[permissions.rules]]
tool = "agent"
match = "reviewer"
decision = "ask"
```

The actions of the sub-agent follow the usual rules, one by one. The `agent` rule only decides whether it can start.
Approvals ask for your confirmation as usual.

### Limits

- Sub-agents run one after the other: the main agent waits for the answer. Several `agent` calls in one message run in sequence.
- A sub-agent cannot launch a sub-agent.
- A sub-agent uses the model of the session. There is no other model or provider.

### In the interface

- `⏺ agent <name> · <description> [pending]` appears when the sub-agent starts. At the end, the status becomes `ok`, `error` or `cancelled`, followed by `· N iterations`.
- The tool calls of the sub-agent are shown under this line, indented: `  ⎿ <tool> <detail> [status]`.
- The messages of the sub-agent are not shown. Its text is not streamed.
- An approval panel for an action of a sub-agent adds the line `sub-agent <name> · <description>`.
- `/agents` lists the agents: name, source and description. `/help` mentions it.

### In non-interactive mode

The text output is unchanged: only the answer of the main agent.
With `--output json`, the events of the sub-agent are included. `SubagentStarted` (`call_id`, `agent`, `description`) and `SubagentCompleted` (`call_id`, `status`, `iterations`) frame them.
The events emitted during the sub-agent turn carry a `parent_call_id` field: the `call_id` of the `agent` call. The field is absent for the main agent.
Actions that need an approval are denied, as for the main agent.

### List the agents

```
tenon agents [--lang fr|en]
```

One line per agent: name, source (`builtin`, `user` or `project`), file (not for `builtin`), description, `tools: ...` (`tools: all` without a `tools` field) and `model: ...` when the file has it.
Discovery notices follow.
At startup, the banner adds `agents: N (project: M)` when there is at least one agent file.
Names, descriptions and notices are shown with control characters replaced by spaces, because project agents are not trusted.

### Session and audit

- The actions of a sub-agent are in the same [audit log](#audit-log), in the same chain. `tenon audit verify` is unchanged.
- Files written by a sub-agent count as written by the agent for [git marking](#git-marking-of-generated-code).
- `--resume` rebuilds the history of the main agent and ignores the events of sub-agents. A sub-agent interrupted by a crash is not restarted: its `agent` call gets `cancelled: session interrupted`.

## Hooks

A hook is a command (or a check by the model) that tenon runs at a precise moment: before and after a tool, when you send a prompt, at the end of a turn, at the start and end of a session.
A hook can block, ask for your approval, change the input of a tool, send feedback to the model or add context.
tenon reads the Claude Code format (the `hooks` key of `settings.json`). Hooks written for Claude Code work without change.

### Sources

Only your own files are read:

| File | Condition |
|---|---|
| `$XDG_CONFIG_HOME/tenon/hooks.json` (else `~/.config/tenon/hooks.json`) | always, when it exists |
| `~/.claude/settings.json` | only with `hooks.claude_settings = true` in the global configuration. Only the `hooks` key is read. `"disableAllHooks": true` in this file gives no hook |

Hooks of both files are added together. A user folder is ignored when `HOME` (or `XDG_CONFIG_HOME`) is empty or is not an absolute path.

```toml
# global configuration only (refused in .tenon/config.toml)
[hooks]
claude_settings = false   # true: also reads ~/.claude/settings.json
disabled = false          # true: no hook is loaded or run
```

**Project hooks never run.** `.tenon/hooks.json`, `.claude/settings.json` and `.claude/settings.local.json` are not executed. If they exist (and, for the two Claude Code files, contain a `hooks` key), tenon shows the notice `project hooks ignored: <file>`. An unreadable or invalid project file only gives a notice.

### Format

```json
{"hooks": {
  "PreToolUse": [
    {"matcher": "Bash|shell", "hooks": [
      {"type": "command", "command": "~/bin/check.sh", "timeout": 30},
      {"type": "prompt", "prompt": "Is this safe? $ARGUMENTS", "timeout": 30},
      {"type": "agent", "prompt": "Verify the change. $ARGUMENTS"}
    ]}
  ]
}}
```

| Field | Rule |
|---|---|
| event key | One of the 9 events below. An unknown event gives a notice and its groups are ignored. |
| `matcher` | RE2 regular expression, anchored (`^(?:...)$`). Absent, empty or `*`: matches everything. For tool events, tested against the Claude Code name **and** the tenon name (`Bash\|shell`). For `SessionStart`: `startup`, `resume`, `compact`. For `PreCompact`: `auto`, `manual`. For `Notification`: `permission_prompt`. Ignored for the other events. |
| `type` | `command`, `prompt` or `agent`. An unknown type gives a notice and the hook is ignored. |
| `command` | Required and not empty for `command`. |
| `prompt` | Required and not empty for `prompt` and `agent`. |
| `timeout` | Integer in seconds, from 1 to 86400. Default: 60 (`command`), 30 (`prompt`), 60 (`agent`). |
| `model` | Read and ignored: a hook always uses the model of the session. |

Other fields are ignored.

A broken file stops tenon from starting (a security hook that is skipped without notice is dangerous). The error names the file and the JSON path, for example `hooks.json: hooks.PreToolUse[0].hooks[1]: timeout must be a positive integer`. Causes: invalid JSON, `hooks` that is not an object, invalid regex, empty `command` or `prompt`, `timeout` that is not an integer, is 0 or negative, or is above 86400. A file is limited to 1 MiB and tenon loads at most 200 hooks, all sources together: beyond that, tenon does not start.
A user file that is a symbolic link is followed (dotfiles).

A hook has a stable ID `<source>:<event>:<i>.<j>` (group index, then index in the group), for example `tenon:PreToolUse:0.1`. The source is `tenon` or `claude`.

### Events

| Event | When | Input fields (besides the common ones) | Effect |
|---|---|---|---|
| `PreToolUse` | Before a tool call, after argument checks and before the permission check | `tool_name`, `tool_input`, `tenon_tool`, `tenon_input`, `tool_use_id` | `deny`: the tool does not run. `ask`: approval required. `allow`: see [Limits of `allow`](#limits-of-allow). `updatedInput`: changes the arguments. |
| `PostToolUse` | After each real execution of a tool (success or error), never after a refusal | Same as `PreToolUse`, plus `tool_response: {content, is_error}` | `block` or exit `2`: the line `hook feedback: <reason>` is added to the result sent to the model. `additionalContext` is added the same way. The action is not undone and `is_error` is unchanged. |
| `UserPromptSubmit` | When you send a prompt, before it goes to the model | `prompt` (the text typed, `/skill args` included) | `block` or exit `2`: the prompt is not sent. You see the reason and the turn ends with `blocked_by_hook`. Context (see below) is joined to your message. |
| `Stop` | When the main agent finishes with the reason `done` | `stop_hook_active` | `block` or exit `2`: the message `Stop hook feedback: <reason>` is added to the history and the loop goes on (the iterations count for `max_iterations`). |
| `SubagentStop` | Same, for a sub-agent | `stop_hook_active`, `agent`, `tool_use_id` (of the `agent` call) | Same as `Stop`, in the sub-agent. |
| `SessionStart` | At the first turn (`startup`, or `resume` with `--resume`) and after each compaction (`compact`) | `source` | Context (see below). |
| `SessionEnd` | When tenon closes, if a session started | `reason`: `prompt_input_exit` (quit from the interface) or `other` | No effect, including `continue: false`. Each hook is capped at 5 s whatever its `timeout`. tenon waits at most 10 s in all: a hook still running then is cancelled. |
| `PreCompact` | Before a compaction | `trigger`: `auto` | `continue: false` ends the turn, before the summary request. Nothing else. tenon has no manual compaction: `manual` is reserved. |
| `Notification` | When the approval panel opens (interface only) | `message`, `notification_type` = `permission_prompt` | `continue: false` ends the turn, after your answer, at the next check point. Nothing else. |

`Stop` and `SubagentStop` do not run after a cancel, an error, `incomplete`, `blocked` or `max_iterations`.
`Stop` does not run at the end of a sub-agent: that is `SubagentStop`.
`PreToolUse` and `PostToolUse` also run inside a sub-agent. Their events carry `parent_call_id`.
`Notification` is never sent in non-interactive mode. It runs while the panel is open: a slow hook delays the handling of your answer, for at most its `timeout`. Its `message` is `tenon needs your permission to use <tool>` (`... to send data to <provider id>` for a jurisdiction consent).

Common input fields: `session_id`, `transcript_path` (the tenon session file: its format is not the Claude Code one), `cwd`, `hook_event_name`.

**Context.** For `UserPromptSubmit` and `SessionStart`, `additionalContext` (or the text on stdout) is joined to your next message in a separate block, and saved with the session (`UserMessage.hook_context`) so that `--resume` restores it. After a compaction, the context of `SessionStart` is joined to the summary message. A prompt blocked by `UserPromptSubmit` is still written in the session and in the audit log (`UserMessage` with `blocked: true`) but is never sent to the model: the `SessionStart` context then waits for the next accepted prompt.

### Types of hook

| Type | What runs |
|---|---|
| `command` | `bash -c <command>` in the project root. Own process group, killed at the end, at the timeout or when you cancel the turn (same as the `shell` tool). The environment of tenon, plus `CLAUDE_PROJECT_DIR` and `TENON_PROJECT_DIR`. The input is JSON on stdin. stdout and stderr are limited to 64 KiB each (the rest is dropped without error). |
| `prompt` | One request to the provider and the model of the session, without tools. `$ARGUMENTS` is replaced by the input JSON; without it, the JSON is added at the end after an empty line. The model must answer `{"ok": true}` or `{"ok": false, "reason": "..."}`. `ok: false` blocks with the reason. The request is not part of the session history. |
| `agent` | A sub-agent with the tools `read_file`, `grep` and `glob` only. Same prompt as `prompt`. It is limited by `max_iterations` and by the timeout. Its tool calls follow the usual permissions and are in the audit log with `parent_call_id` set to the `run_id` of the hook. Its last message is read as for `prompt`. |

`prompt` and `agent` hooks run one at a time. `command` hooks of the same event run in parallel, and identical commands run once.
Requests of `prompt` and `agent` hooks, and tool calls of an `agent` hook, trigger no hook.

### Exit codes and JSON output

For a `command` hook:

| Exit code | Meaning |
|---|---|
| `0` | Success. If stdout (without leading blanks) starts with `{`, it is read as JSON (an unreadable JSON is a non-blocking error). Otherwise, for `UserPromptSubmit` and `SessionStart`, non-empty stdout becomes added context. Elsewhere it is only logged. |
| `2` | Blocking error. stderr is the reason. The effect depends on the event (table above). |
| other, start failure, timeout | Non-blocking error: the action goes on, a warning is shown and logged. |

JSON fields:

| Field | Effect |
|---|---|
| `continue: false` | Stops the turn after this hook. `stopReason` is shown to you. Turn reason: `stopped_by_hook`. For `PreToolUse`, the tool does not run. For `PreCompact` and `Notification`, see the table. Only `SessionEnd` ignores it. Inside a sub-agent, it stops only the sub-agent: the `agent` call returns an error (`sub-agent stopped: stopped_by_hook`) and the main turn goes on. |
| `systemMessage` | Shown to you as a warning, and logged. |
| `suppressOutput` | Accepted, no visible effect. |
| `decision`, `reason` | `block` + `reason` blocks, for the events that can block. |
| `hookSpecificOutput` | Must contain `hookEventName` equal to the event, otherwise it is ignored. For `PreToolUse`: `permissionDecision` (`allow`, `deny`, `ask`), `permissionDecisionReason`, `updatedInput`. The old format `decision: approve\|block` also works. `additionalContext` for `PostToolUse`, `UserPromptSubmit` and `SessionStart`. |

When several hooks match, they all run, and the decisions combine as `deny`/`block` over `ask` over `allow`. The reasons are joined with line breaks, in the order of the configuration. Added contexts are joined in the same order. One `continue: false` is enough. Several different `updatedInput` refuse the call (`conflicting updatedInput from hooks`).

### Effect on tool calls (`PreToolUse`)

- `deny` (or exit `2`): the tool does not run. The result is the error `blocked by hook: <reason>`, with `denied: "hook"`.
- `ask`: the approval is asked even if the rules allow the action. The panel shows `hook: <reason>` and does not offer "yes for the session". An approval given earlier for the session does not skip a request that comes from a hook. In non-interactive mode, the action is denied, as for any request.
- `updatedInput`: translated back to tenon names, then checked again like any call. If it is invalid, the call is refused (`invalid updatedInput from hook: <error>`, `denied: "hook"`). Permissions are evaluated on the new input, which is the one that runs. `PostToolUse` receives these arguments as `tool_input` and `tenon_input`. The event `ToolCallRequested` keeps the original input, and `HookCompleted.updated_input` records the new one.
- `continue: false`: the tool does not run and the turn ends (`stopped_by_hook`).

#### Limits of `allow`

`allow` is like a global `allow` rule. It lifts an `ask` that comes from the rules, and nothing else. It never lifts:

- a `deny` rule;
- a protected action (an approval that rules cannot waive);
- a path outside the project, or a sensitive file;
- an `ask` rule of the project.

A hook can only restrict, or lift what a global `allow` rule could lift.

### Tool names and arguments

The hook receives both the Claude Code names and the tenon names. Fields that tenon does not know are kept as they are, in both directions.

| tenon | Claude Code |
|---|---|
| `shell {command, timeout_seconds}` | `Bash {command, timeout}` (`timeout` in milliseconds) |
| `read_file {path, offset, limit}` | `Read {file_path, offset, limit}` |
| `write_file {path, content}` | `Write {file_path, content}` |
| `edit_file {path, old_string, new_string, replace_all}` | `Edit {file_path, old_string, new_string, replace_all}` |
| `grep {pattern, glob}` | `Grep {pattern, glob}` |
| `glob {pattern}` | `Glob {pattern}` |
| `skill {name, file}` | `Skill {skill, file}` |
| `agent {agent, description, prompt}` | `Task {subagent_type, description, prompt}` |

`mcp__<server>__<tool>` tools keep their name.

### Differences from Claude Code

- `tool_response` is simpler: `{content, is_error}`.
- `transcript_path` points to a tenon session file, whose format is different.
- Hooks never come from the project (see [Sources](#sources)). Those of `~/.claude/settings.json` are read only with `hooks.claude_settings = true`.
- `PreCompact` runs with `auto` only.
- `model` in a hook is ignored.
- Hook types are `command`, `prompt` and `agent`. There is no HTTP hook.
- Events that are not in the list above (such as `PostToolUseFailure`) do not exist.

### Failures

A failing hook never blocks: an error, a timeout, an unreadable JSON or a provider error for a `prompt` or `agent` hook is a non-blocking error. The action goes on, and a warning is shown and logged. Only a deliberate decision blocks (exit `2`, `deny`, `block`, `ok: false`).
If you cancel the turn during a hook, the hook is killed (`status: cancelled`) and the turn stops as for any cancel.
Test your hooks: a security hook that fails silently does not protect you.
Warnings of `SessionEnd` hooks are not shown: these hooks run after the interface has closed. They are only in the audit log.

### In the interface

- At startup, the banner adds `hooks: N (claude: M)` when at least one hook is loaded, then the notices (ignored project hooks).
- A successful hook with no effect is invisible. A line `⏺ hook <event> · <command or type> [blocked|asked|error|timeout] <reason>` appears for a block, a request, an error or a timeout. `systemMessage` and `stopReason` are shown as warnings.
- The approval panel adds the line `hook: <reason>` when the request comes from a hook (`(no reason given)` if the hook gave none). When a hook changed the tool input (`updatedInput`), the diff preview shows the input that will run.
- A turn blocked or stopped by a hook ends with a notice ("prompt blocked by a hook", "turn stopped by a hook").
- `/hooks` lists the hooks. `/help` mentions it.
- Text that comes from a hook (reasons, `systemMessage`, `stopReason`, stderr) is shown with control characters replaced by spaces and secrets masked.

### In non-interactive mode

The text output is unchanged. With `--output text`, warnings of hooks during the turn (errors, timeouts, `systemMessage`, blocks) go to stderr.
With `--output json`, these warnings are only in the JSON events.
Startup hook notices (the `hooks: N (claude: M)` banner, ignored project hooks) go to stderr in both modes.

### List the hooks

```
tenon hooks [--lang fr|en]
```

One line per hook: ID, `matcher` (`*` when empty), type, command or prompt (cut at 80 characters, control characters replaced), then the notices.

### Session and audit

Each hook run is in the [audit log](#audit-log), in the same chain, with two events:

| Event | Fields |
|---|---|
| `HookStarted` | `run_id` (the id of this event), `hook_id`, `event`, `type`, `command` or `prompt` (cut at 4 KiB), `call_id` for tool hooks, `parent_call_id` in a sub-agent |
| `HookCompleted` | `run_id`, `hook_id`, `event`, `status` (`ok`, `blocked`, `error`, `timeout`, `cancelled`), `exit_code`, `decision`, `reason`, `updated_input`, `system_message`, `stop`, `stop_reason`, `error`, `duration_ms`, `stdout` and `stderr` (cut at 4 KiB), `call_id`, `parent_call_id` |

`exit_code` is absent when the command did not exit normally (start failure, timeout, cancel). The tool calls of an `agent` hook carry its `run_id` as `parent_call_id`.
`ToolResult.denied` can be `"hook"`. `TurnCompleted.reason` can be `blocked_by_hook` or `stopped_by_hook`.
`--resume` ignores `HookStarted` and `HookCompleted`. It rebuilds the user messages and the compaction summary with their `hook_context`. The `Stop hook feedback` messages are restored as user messages.

## Checkpoints and rewind

Each prompt you send is a restore point. `/rewind` goes back to one of them: it restores the files, the conversation, or both.

### What is saved

- Before `write_file` and `edit_file` change an existing file, tenon saves its previous content. This covers the main agent, sub-agents and tool inputs rewritten by a hook.
- A file that did not exist is not saved. Rewinding it deletes the file.
- **Not undone:** `shell` commands and MCP tools that write or run something. `/rewind` shows how many such calls ran after the chosen point.
- A file larger than 10 MiB (10 485 760 bytes) is written without a copy. The tool result adds the line `checkpoint skipped (file too large)`, and rewind cannot restore that file (`unrestorable`).
- If the copy cannot be saved, the write is refused with `cannot save checkpoint: <error>`.
- An existing file that tenon cannot read is never overwritten: the write is refused with `cannot read existing file: <error>`, with or without copies.
- Non-interactive mode (`-p`) saves copies as usual, but has no rewind.

### Open `/rewind`

Type `/rewind`, or press Esc twice in less than 1 s when no turn is running. During a turn, Esc cancels the turn as before.

| Step | What you see | Keys |
|---|---|---|
| 1. Point | The list of your prompts, newest first: `#<n>  <first line, 80 characters>`. The list scrolls. `(code only)` marks a point whose conversation can no longer be rewound. | Up/Down, Enter, Esc closes |
| 2. Preview and mode | One line per file: `restore`, `delete`, `conflict (modified since)`, `unrestorable`, `unchanged`. At most 8 files, then `… and N more`. The warning `N shell or MCP calls are not undone` when there are some. Then the mode. | Up/Down, Enter, Esc goes back |
| 3. Conflicts | For each conflict: `overwrite <path>? y/N`. | `y` overwrites. `n` or Enter keeps. Esc goes back |

If there is no file to restore and the conversation cannot be rewound, tenon shows `nothing to rewind`. When some files are `unrestorable`, tenon shows the preview instead, so you can see them, with every mode greyed out.
Conflicts are only asked for the modes that restore code. They are asked one by one, and the default is to keep your version.
At the end, tenon shows `rewind: N restored · N deleted · N kept · N skipped · N failed`, then one line `failed: <path>: <error>` per failure.

### Modes

| Mode | Files | Conversation |
|---|---|---|
| `code and conversation` | restored | cut back to the chosen prompt |
| `conversation only` | untouched | cut back to the chosen prompt |
| `code only` | restored | unchanged |

Modes that are not possible are greyed out.
After a conversation rewind, the chosen prompt comes back in the input area, so you can edit and send it again.
The conversation can only be rewound to a prompt sent after the last compaction. Before that, the point is marked `(code only)`.
After a `conversation only` or `code and conversation` rewind, the later points can only rewind code.
With `code only`, the next message you send carries the note `[tenon] The user restored files to their state before: "<prompt excerpt>"`, so the model knows the files changed. The note is added even if no file changed.

### Conflicts

For each file, tenon compares the current content with the content the agent left. A file is a **conflict** when:

- you changed it after the agent wrote it, or you deleted it;
- the path is now a symbolic link. A symbolic link is always a conflict, and tenon never follows a symbolic link in the last component of the path.

A conflict is kept unless you answer `y`. A file can also change between the preview and the restore. tenon then does not overwrite it: the status is `failed` with `file changed during rewind`. A path that became a symbolic link fails with `path became a symbolic link`.
A file with no copy (missing, corrupted, or written without a copy) is `unrestorable` and skipped.
A failure on one file does not stop the others.

### List the points

```
tenon rewind --list --resume <id|last> [--lang fr|en]
```

One line per point, newest first: `#<n>  <seq>  <excerpt>`, with `(code only)` when the conversation cannot be rewound. `<n>` counts from the oldest prompt. Nothing is restored. `--resume` is required.

### Retention

```toml
[checkpoints]
retention_days = 30
```

| Value | Effect |
|---|---|
| `30` (default) | Copies are kept 30 days. |
| `0` | No copy is saved. Rewind cannot restore the files the agent overwrote, but it can still delete the files the agent created. A notice at startup says `checkpoints disabled (retention_days = 0): /rewind cannot restore files`. |

A negative value is refused. This key is global only: it is refused in `.tenon/config.toml`.
At startup, tenon deletes the copies older than `retention_days` days that no recent session uses. A session is recent when its file was modified within `retention_days` days, and it uses the copies named by the `hash_before` of its tool results. A cleanup error is a notice and never blocks startup (`checkpoint cleanup failed: <error>`).
If the store cannot be opened, tenon works as with `0` and shows `checkpoints unavailable: <error>: /rewind cannot restore files`.

### Store

Copies are in `~/.local/share/tenon/checkpoints/` (`$XDG_DATA_HOME/tenon/checkpoints`), outside the project. One file per content: `<first 2 characters of the sha256>/<full sha256>`. Directories are `0700`, files `0600`. A content already stored is not written again: its modification date is refreshed, so the cleanup keeps it.
Before a restore, tenon checks the copy against its sha256. A copy that does not match is refused (`corrupted checkpoint`).

### Session and audit

A rewind never rewrites the session or the [audit log](#audit-log). It adds one event, in the same chain:

| Event | Fields |
|---|---|
| `Rewound` | `to_seq` (the `seq` of the chosen prompt), `mode` (`code`, `conversation`, `both`), `files` (`path`, `action`, `status`, `error`), `untracked_calls`, `note` (mode `code`) |

`status` is `restored`, `deleted`, `kept`, `skipped`, `failed` or `unchanged`.
`ToolResult` gains two optional fields: `no_checkpoint` (written without a copy) and `untracked` (a `shell` or MCP call that cannot be undone).
`--resume` applies `Rewound`: the rebuilt conversation is cut as it was live, and a pending `code only` note is kept for the next message. `--output json` includes the `Rewound` events of a resumed session.

## Providers and jurisdictions

You can use any OpenAI-compatible endpoint.
Presets:

| id | Provider | Jurisdiction | Key |
|---|---|---|---|
| `mistral` (default) | Mistral AI, FR | `eu` | `MISTRAL_API_KEY` |
| `scaleway` | Scaleway, FR | `eu` | `SCW_SECRET_KEY` |
| `ovhcloud` | OVHcloud AI Endpoints, FR | `eu` | `OVH_AI_ENDPOINTS_ACCESS_TOKEN` |
| `albert` | Albert API (DINUM), FR | `eu` | `ALBERT_API_KEY` |
| `codestral` | Codestral (Mistral AI), FR | `eu` | `CODESTRAL_API_KEY` |
| `ollama` | Ollama, `http://localhost:11434/v1` | `local` | none |
| `vllm` | vLLM, `http://localhost:8000/v1` | `local` | none |

- `albert`: Albert API keys are issued to public-sector staff only. Model identifiers change over time: list them with `GET /v1/models` and set `model` in a `[[providers]]` block if the preset's model is gone.
- `codestral`: the `codestral.mistral.ai` domain needs its own key, distinct from a La Plateforme key. With a La Plateforme key, use the `mistral` preset or set `base_url = "https://api.mistral.ai/v1"`. Codestral is designed for code completion; for agent work with tools, Mistral recommends Mistral Medium 3.5 (`mistral` preset).

The jurisdiction means the **law that applies to the operator**, not the location of its servers.
A provider added without `jurisdiction` is treated as `non-eu`.
A `[[providers]]` block that reuses the `id` of a preset replaces the whole preset (except local model detection, see below).

Sending to a `non-eu` provider:

- **interactive interface**: before the first send, tenon asks "send the conversation to …?" (`y` or `n`; `o` or `n` in French). The consent is valid until the end of the run. After a yes, the outside-EU warning (`JurisdictionWarning`) is also shown and recorded in the log.
- **non-interactive mode**: sending is blocked (code 3) without `--allow-non-eu`.
- with `--allow-non-eu`: no question; a warning is shown and recorded in the log.

tenon never switches automatically to another provider.

### Local models (Ollama, vLLM)

For `ollama` and `vllm`, tenon reads the served model and the context window applied by the server at startup, from the configured address only:

- **Ollama**: without `model`, tenon picks the most recently installed model that supports tool calls (`ollama list`). It loads the model, then reads the real window (`/api/ps`).
- **vLLM**: tenon reads `GET /v1/models`. Without `model`, the server must serve exactly one model. The window is `max_model_len`.

The detected model and window are shown at startup.
A value set in the configuration (`model`, `context_window`) is never replaced; `detect = "off"` disables detection.

```
ollama pull qwen3:8b           # 5 GB model, supports tool calls
tenon --provider ollama
```

**Ollama context window.** By default, Ollama limits the window according to the machine's memory (4096 tokens below 24 GiB of VRAM), whatever the model supports.
4096 tokens are not enough for an agent: tenon says so at startup.
Increase the server window, then restart Ollama:

- macOS app: Ollama's "Context length" setting, or `launchctl setenv OLLAMA_CONTEXT_LENGTH 32768` then restart the app;
- Linux service: `Environment="OLLAMA_CONTEXT_LENGTH=32768"` in `systemctl edit ollama`, then `systemctl restart ollama`;
- command line: `OLLAMA_CONTEXT_LENGTH=32768 ollama serve`.

A larger window uses more memory (about 4.5 GB more for 32768 tokens with `qwen3:8b`).

Example for an Ollama server on another machine:

```toml
[[providers]]
id = "ollama"
name = "Ollama (GPU server)"
base_url = "http://gpu.lan:11434/v1"
model = "qwen3:8b"          # no context_window: read from the server
jurisdiction = "local"
```

## Compliance profiles

A profile restricts, for the whole run, what tenon may send and to whom.
It is set with the `profile` key of the global configuration:

```toml
profile = "eu-only"
```

| Profile | Allowed providers and MCP servers |
|---|---|
| `eu-only` | jurisdiction `eu` or `local` |
| `secnumcloud` | provider: jurisdiction `local` with a local host, or jurisdiction `eu` with `"secnumcloud"` in `qualifications`. MCP server: `stdio` of jurisdiction `local`; `http` local (jurisdiction `local` and local host) or `eu` with `"secnumcloud"` in `qualifications` |
| `strict-local` | jurisdiction `local` **and** a URL whose host is a local address |

Profiles have an order: none < `eu-only` < `secnumcloud` < `strict-local`.
A provider or MCP server of unknown jurisdiction (not set) is refused by both profiles.

**Local hosts.** For `strict-local`, the host of the URL must be exactly one of these: `localhost`, or an IP address literal in `127.0.0.0/8`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` or `fc00::/7` (an IPv6 zone such as `%eth0` is ignored). tenon does no DNS resolution: a host name other than `localhost` is refused, even if it points to a local address. A stdio MCP server has no URL, so only its jurisdiction is checked.

**Declared qualifications.** A provider (`[[providers]]`), an MCP server (`[[mcp.servers]]`) or a policy-defined provider can carry `qualifications = ["secnumcloud"]`. It is the only accepted value (exact, lowercase); another value is a configuration error: `config: unknown qualification "<x>" (secnumcloud)`. **tenon never verifies a qualification.** A SecNumCloud qualification is an ANSSI certification of a service, not a technical property: tenon only records who declared it. Presets declare none. Under an [organisation policy](policy.md), the qualifications from user configuration are ignored (notice `policy <id>: qualifications declared in config ignored (<ids>)`, ids sorted); only those of `providers.define` count.

Under `secnumcloud`, requests to a local provider (local model detection included) ignore the proxy environment variables (`HTTP_PROXY`, `HTTPS_PROXY`), and so does the MCP manager (under `strict-local` and `secnumcloud`). A refused provider stops tenon at startup with exit code 1: `profile secnumcloud: provider "<id>" is not declared secnumcloud-qualified`. An MCP server refused for this reason is skipped with the reason `not declared secnumcloud-qualified`. `default_provider` can name a provider defined by the policy.

**Who decides.** The profile is read from the global configuration and from the project configuration, and the stricter one wins: a project can tighten the profile, never loosen it. A project value weaker than the global one is ignored, and tenon shows a notice at startup. An unknown profile name is a configuration error: `config: unknown profile "<x>" (eu-only|secnumcloud|strict-local)`.

**What is checked.**

- At startup, only the **selected** provider (`--provider` or `default_provider`) is checked, before anything is contacted: local model detection (Ollama, vLLM) is not run for a refused provider. Other configured providers are not checked until you select them.
- `--allow-non-eu` is refused with a profile: tenon stops with an error at startup.
- An MCP server that the profile refuses is neither started nor contacted. A notice names it and the reason, and the log records `McpServerDown`.
- Defense in depth: before each send to the model, the profile is checked again. On a violation nothing is sent, the log records an `Error` event with `code: "profile"`, and the turn is blocked.

The profile in force is recorded in the `profile` field of `SessionStarted` (omitted without a profile).
The welcome screen shows a `profile: <name>` line.
`tenon config check` prints a `profile: <name>` line only when a profile is active, then checks the selected provider (`--provider` is honored) and exits with code 1 if it is refused.
The [compliance export](#compliance-export) lists the profiles of the sessions in the `profiles` key.
At metadata level the profile is unknown (not available) and `profiles` is `[]`. In a range with mixed levels, sessions logged at metadata level add nothing to `profiles`.

**What profiles do not cover.**

- Shell commands: an approved command, or a program started by a stdio MCP server, can still reach the network. A profile bounds the data sent to the model and the MCP servers, not what a command does.
- Permissions: approval rules are unchanged.
- The audit level (`audit.level`) and command hooks (prompt and agent hooks go through the model and are checked): a command hook is a command you wrote and is not filtered by the profile.

## Provider allowlist

Without an organisation policy, you can restrict the hosts providers may use:

```toml
[provider_allowlist]          # global configuration, and .tenon/config.toml
hosts = ["api.mistral.ai", "*.scaleway.ai"]
local = true                  # also admit local hosts
```

- The host of `base_url` (lowercase, port removed) must equal an entry, or be a strict subdomain covered by `*.x.eu` (never `x.eu` itself); `local = true` admits local hosts.
- Refused at load time: an empty entry, or one with a scheme, path, port, or a `*` other than a leading `*.` followed by at least two labels. A section with no `hosts` and no `local = true` is refused: `config: provider_allowlist is empty`.
- Every list present (policy, global, project) must accept the host: a project can only restrict further.
- At startup, after the policy check and before local model detection, tenon stops with exit code 1: `provider "<id>" (host "<h>") not in provider_allowlist (<global|project>)`. Before each send the check runs again: nothing is sent, the log records an `Error` event with `code: "allowlist"`, and the turn is `blocked`.
- The allowlist does not apply to MCP servers.
- `tenon config check` prints `provider_allowlist: global <n> hosts, project <m> hosts` when a list is present (` + local` is appended to a side with `local = true`; a missing side prints `-`).

## API keys and keyring

tenon never reads a key from a configuration file.
tenon looks for the provider key:

1. in the `api_key_env` environment variable;
2. otherwise in the system keyring (Keychain on macOS, Secret Service on Linux), service `tenon`, account = provider identifier.

```
tenon key set mistral      # asks for the key without showing it
echo "$KEY" | tenon key set mistral
tenon key delete mistral
```

The provider must exist in the configuration.
An empty value is refused.
An unreadable keyring does not prevent startup: the key is treated as missing, with a warning.

The keys of all configured providers, from the environment or from the keyring, are redacted in everything that tenon writes or sends.

## MCP servers

You declare MCP servers **only in the global configuration**.
See the two examples in [config.example.toml](config.example.toml).

Their tools are offered to the model under the name `mcp__<server>__<tool>`.
By default, each call requests an approval. `risk = "read"` allows them without a request.
The panel shows the arguments of a call in full.
A call whose arguments contain a redaction marker (`[SECRET_1]`…) is denied.

### Stdio server

- The command is an absolute path, or a bare name looked up in the absolute directories of `PATH`. A relative path is refused.
- The server starts **from your home directory (`HOME`)**, never from the project.
- It receives the project root in the `TENON_PROJECT_ROOT` variable.
- Its environment is reduced to `PATH`, `HOME`, `LANG`, `TENON_PROJECT_ROOT` and the variables listed in `env_from`.
- Without `jurisdiction`, it is `local`.
- On shutdown: SIGTERM to the process group, then SIGKILL after 2 s.

### HTTP server

- `https` is mandatory, except to `localhost`, `127.0.0.1` or `::1`.
- The URL contains no credentials, no query and no fragment. The token goes through `bearer_env`.
- `jurisdiction` is mandatory.
- Redirects are not followed.

### Server jurisdiction

A `non-eu` server is **neither started nor contacted** without `--allow-non-eu`.
tenon reports it as "MCP server … outside the EU skipped: rerun with --allow-non-eu".
In non-interactive mode, its tools do not exist and the exit code does not depend on it.

`--allow-non-eu` applies to the whole run, **for providers and for MCP servers**.
With this option, the tools of a `non-eu` server follow the usual permission rules.

### Failures

A server that cannot be reached at startup (timeout: 15 s) is reported and ignored.
If a server goes down during a session, its tools are disabled; the session continues.
The values of the `env_from` and `bearer_env` variables are redacted everywhere.

## Git marking of generated code

```
tenon git install-hook          # in the repository that contains the current directory
tenon git install-hook --force  # replaces an existing hook
```

The command installs a git **`commit-msg`** hook (executable, `core.hooksPath` respected).
If another `commit-msg` hook exists, it refuses without `--force`.
With `--force`, the old hook is renamed `commit-msg.bak`.
An old tenon `prepare-commit-msg` hook is removed.

At each commit, the hook runs `tenon git mark`.
This command compares the staged files with the files that the agent wrote successfully (`write_file`, `edit_file`), based on the sessions opened in this repository or in one of its subdirectories.
If it finds a match, it adds at the end of the message:

```
AI-Assisted-By: tenon (<model>, <provider>, <country>)
AI-Session: <session ID>
```

Several lines can appear: one `AI-Assisted-By` line per distinct (model, provider, country) triplet, and one `AI-Session` line per session concerned.
The trailers contain no secret and no file content.

The hook **never blocks a commit**:

- without `tenon` in `PATH`, it does nothing;
- any error leaves the message unchanged;
- marking stops after 5 s;
- `TENON_DEBUG=1` shows the error, if any.

Special cases:

- `git commit --no-verify` skips the hook: the commit is not marked.
- Merge commits are not marked.
- An empty message stays empty: the commit is aborted as usual.

## Audit log

Each event is appended to the log `$XDG_DATA_HOME/tenon/audit/YYYY-MM-DD.jsonl`.
Each line contains the hash of the previous line: the chain continues from one file to the next.

```
tenon audit verify [--tsa-ca <pem>]
```

The command shows `OK: <n> lines, last hash sha256:…`, or the first faulty line (`file:line`) with code 1.
It then checks the timestamp tokens, if any (see [Timestamping](#timestamping-rfc-3161)).
Write down the last hash somewhere else: see [security.md](security.md#log-tail-truncation).

If writing to the log fails, tenon runs no more actions.

### Timestamping (RFC 3161)

The hash chain detects an isolated change, not a complete rewrite by someone who controls the machine.
A timestamp authority (TSA, RFC 3161) signs the head hash of the chain: the token proves that the whole log up to that line existed at the time signed by the TSA.
An auditor checks it offline.

tenon ships no TSA preset and calls none by default: without `[audit.timestamp]`, nothing changes and no network call is made.
You choose the authority.

```toml
# global configuration only (refused in .tenon/config.toml)
[audit.timestamp]
url = "https://tsa.example.eu/tsr"
ca_file = "/etc/ssl/tsa-ca.pem"   # optional
```

- `url` must be `https`, or `http` for a local host only; otherwise `config: audit.timestamp.url must be https (http only for local hosts)`.
- `ca_file` holds the PEM trust roots of the authority. Unreadable or without a certificate: `config: audit.timestamp.ca_file: <reason>`. Without `url`: `config: audit.timestamp.ca_file without url`.
- An [organisation policy](policy.md#audit-timestamping) can impose the authority.
- Under `strict-local`, a TSA host that is not local stops tenon at startup (code 1): `profile strict-local: audit timestamp host "<h>" is not local`. Environment proxies are respected, except under `strict-local`.
- `tenon config check` prints `audit.timestamp: <url> (roots: <ca_file|system|policy>)`.

**Trust roots.** The order is: `--tsa-ca` (of `audit verify` and `audit export`), then the `ca` of the policy, then `ca_file`, then the system roots. A policy without `ca` keeps your `ca_file` (only the `url` is replaced).
On macOS and Windows the system roots cannot verify TSA certificates: there, set `ca_file`, the policy `ca` or `--tsa-ca`. Otherwise the anchoring at the end of a session fails with a notice.

**What is sent.** The TSA receives only a SHA-256 hash (the head hash of the chain), never content.
The request has a timeout of 10 seconds. The TSA client does not follow redirects: a redirect fails with `HTTP 30x`.

**When.** If `[audit.timestamp]` is set, tenon timestamps the head hash at the end of each session, after the log is closed.
A failure never changes the exit code: a notice `audit timestamp: <reason>` goes to stderr.
`tenon audit timestamp` does the same on demand; it prints `audit timestamp: <file> (<genTime RFC 3339>, <TSA>)`, or `audit timestamp: already anchored` when a token already covers the head.
Exit codes: 0 done or already done; 1 not configured (`audit timestamp: not configured ([audit.timestamp] url)`), empty log (`audit timestamp: audit log is empty`), network error or invalid response.

Tokens are stored apart from the chain, one DER file each, in `$XDG_DATA_HOME/tenon/audit/timestamps/<genTime>-<12 hex of the hash>.tsr` (mode `0600`).

**Verify.** `tenon audit verify` checks every token, then that the hash it covers is in the chain; a token whose hash is not in the chain means the chain was rewritten (`timestamp <file>: anchored hash not in chain`).
This holds only while the tokens exist: if `audit/timestamps/` is deleted, `audit verify` prints `timestamps: none` and exits 0. Copy the tokens (and the log) off the machine regularly, and compare against those copies or the expected number of tokens.
It prints, after the usual line:

```
timestamps: <n> valid, last <file>:<line> at <genTime> (<TSA>), <m> lines after
```

or `timestamps: none`, or `timestamps: 0 valid, <n> invalid` when tokens exist but none is valid.
The `<m>` lines after the last token are not covered by any timestamp.
A line covered by a token whose `ts` is more than 5 minutes after the token time gives a warning, not an error: `timestamp <file>: <file>:<line> ts after genTime (local clock?)`.
Exit code: 0 everything is valid; 1 broken chain, invalid or orphan token.
`--tsa-ca <pem>` sets the trust roots of the auditor.

**Without tenon.** A third party checks a token with OpenSSL, with `<hex>` the head hash without the `sha256:` prefix:

```
openssl ts -verify -digest <hex> -in <token>.tsr -token_in -CAfile <ca>
```

**Limits.** Revocation (CRL, OCSP) is not checked: verification is offline.
Only RSA PKCS#1 v1.5 and ECDSA signatures with SHA-256, SHA-384 or SHA-512 are accepted.
A token proves that the log up to the covered line existed at that time; it does not prove that the later lines are complete (see [security.md](security.md#timestamping-rfc-3161)).

To give the log to an auditor, see [Compliance export](#compliance-export).

## Compliance export

`tenon audit export` turns the audit log into a report for an auditor or a data protection officer: an HTML file to read or print, and a JSON file for tools.

```
tenon audit export [--since YYYY-MM-DD] [--until YYYY-MM-DD]
                   [--project <path> | --all-projects | --session <id>]
                   [--details] [--tsa-ca <pem>] [--out <dir>] [--lang fr|en]
```

| Option | Effect |
|---|---|
| `--since` | first day included, in UTC (default: `--until` minus 29 days) |
| `--until` | last day included, in UTC (default: today) |
| `--project <path>` | project to export (default: current directory) |
| `--all-projects` | all projects |
| `--session <id>` | a single session |
| `--details` | adds conversation data (see below) |
| `--tsa-ca <pem>` | PEM trust roots for the timestamp tokens (default: as for `audit verify`) |
| `--out <dir>` | output directory (default: current directory) |
| `--lang fr\|en` | language of the HTML report |

By default the report covers the last 30 days of the current project.
`--project`, `--all-projects` and `--session` cannot be combined.
Both dates are inclusive and `--since` cannot be after `--until`.

The command writes two files, with mode `0600`:

- `tenon-audit-<since>_<until>.html`;
- `tenon-audit-<since>_<until>.json`.

It never replaces a file: if one of them exists, nothing is written and the command stops with code 1.
It does not modify the log and does not lock it.

| Code | Meaning |
|---|---|
| 0 | report written, audit chain intact |
| 1 | usage or write error, nothing written |
| 2 | report written, but the audit chain is broken or a timestamp token is invalid or orphan: the report says so (banner or Timestamps row); stderr shows `invalid timestamps: <n> (report written)` for tokens |

### What the report contains

- **Organisation policy**: the policies (id, version, key id) of the sessions in the range, with the number of sessions under each; sessions without a policy are not listed. At metadata level the policy is unknown: the row shows "not available at metadata level". See [policy.md](policy.md).
- **Applied profiles**: the compliance profiles of the sessions in the range, in the report header. At metadata level the profile is unknown: the row shows "not available at metadata level".
- **Summary**: sessions, turns, tool calls, modified files, denials, jurisdictions of the providers.
- **Providers and transfers**: provider, model, country and jurisdiction, number of sessions, declared qualifications (`qualifications`, a list, `[]` when none; declared, not verified by tenon), first and last activity; jurisdiction consents granted and denied; sessions with a jurisdiction warning; MCP servers and their failures.
- **Redactions**: number of masked items per category.
- **Permissions**: per tool, requests, approvals (once or for the session) and denials (by the user, by policy, by a hook).
- **Modified files**: date, session, path, tool, hash before and after, author (`main` or the sub-agent), whether there was no checkpoint, and the state left by a later rewind.
- **Commands**: number of shell calls per session.
- **Sub-agents, hooks, rewinds**: sub-agent runs, hooks that blocked, failed, timed out or asked for confirmation, rewinds with the files restored, deleted, kept, skipped, failed or unchanged.
- **Incidents**: errors, turns that ended for a notable reason (blocked, stopped by a hook, context too large, iteration limit, incomplete, cancelled) and unreadable log lines.
- **Regulatory mapping**: which elements of the report help document GDPR, NIS2, DORA and AI Act requirements. It cites no article.
- **Integrity**: whether the whole chain is intact, the number of lines, the final hash, and the hashes of the first and last line of the exported range.
- **Timestamps** (`integrity.timestamps`, always present): `count`, `valid`, `last_file`, `last_line`, `last_time` (RFC 3339), `tsa`, `lines_after` and `errors` (a list, `[]` when none). The HTML row is `none` without tokens, or `<valid>/<count> valid, last <file>:<line> at <time> (<tsa>), <m> lines after` followed by the errors. Tokens are verified only when the chain is intact, with the roots chosen as for `audit verify`.

Git marking is not in the log: the report gives the command that finds marked code, `git log --grep "AI-Assisted-By"`.
Every report starts with a notice (the JSON `disclaimer` field is always in English): it describes the activity recorded by tenon, it is not legal advice and it does not certify compliance.

### `--details`

Without `--details`, the report holds no prompt, response, command text, tool argument or tool result.
It still holds texts taken from the log: modified file paths, session ids, tool and MCP server names, error messages (including MCP failures) and hook reasons.
These texts are redacted, but they can reveal project names or internal details: handle the default report with care too.
With `--details`, it adds:

- the text of the shell commands, per session;
- a timeline per session: prompts, responses, tool calls and results, permissions, hooks.

Responses, tool arguments and results are cut to 500 characters.
All texts are redacted again when the report is built.
The HTML then shows a banner: the report contains conversation data.
Store and share these files like the log itself.

### Metadata level

With `audit.level = "metadata"`, the log keeps only the hash and the size of each payload.
The report then:

- shows the unreadable lines and the regulatory mapping, which need no content;
- writes "not available at metadata level" in the other content sections;
- sets `content_available` to `false` and `integrity.level` to `metadata`.

If the log mixes both levels (`integrity.level` is `mixed`), the lines whose payload is at metadata level are skipped: they are not counted.
A session whose start is at metadata level has an unknown provider: it is missing from the providers and jurisdictions.
Their number is in `summary.unknown_provider_sessions`; the HTML shows it in the summary and, at `mixed` level, in a banner saying that counts and providers are incomplete.
Regulatory mapping appears at every level.

At metadata level, the project of a session is unknown. With `--project` (or the default), such sessions are left out of the report: their number is in `excluded_metadata_sessions`, and the HTML shows it in a banner.
`--all-projects` and `--session` leave nothing out.

### Check the integrity of a report

1. The log itself: `tenon audit verify` (see above). The report gives the final hash (`integrity.final_hash`) to compare with the hash you wrote down.
2. The JSON file: `report_id` is `sha256:` followed by the SHA-256 of the `report` field, serialised in compact form as Go's `encoding/json` does it:
   keys in file order, no whitespace, `<`, `>` and `&` written as `\u003c`, `\u003e` and `\u0026`, U+2028 and U+2029 written as `\u2028` and `\u2029`, other non-ASCII characters kept as UTF-8.
   With `python3`:

   ```python
   import hashlib, json, sys
   doc = json.load(open(sys.argv[1], encoding="utf-8"))
   raw = json.dumps(doc["report"], separators=(",", ":"), ensure_ascii=False)
   for ch, esc in (("<", "\\u003c"), (">", "\\u003e"), ("&", "\\u0026"),
                   ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
       raw = raw.replace(ch, esc)
   print("sha256:" + hashlib.sha256(raw.encode("utf-8")).hexdigest())
   print(doc["report_id"])
   ```

   Run it as `python3 check.py tenon-audit-<since>_<until>.json`: both lines must be identical.
3. The HTML file: `html_sha256` is the SHA-256 of the HTML file.
   ```
   shasum -a 256 tenon-audit-<since>_<until>.html     # macOS
   sha256sum tenon-audit-<since>_<until>.html         # Linux
   ```
   Compare the result with `html_sha256` (after the `sha256:` prefix).

These checks detect an accidental change of a file.
Someone who can rewrite both files can recompute both hashes: for proof against that, keep the final hash of the log somewhere else.

### JSON schema

The file is indented with two spaces and ends with a newline. Dates are RFC 3339 in UTC (`2026-10-20T12:00:00Z`); `period` dates are `YYYY-MM-DD`.
`schema_version` is `1`. A breaking change (key removed, renamed or with a different type) increases it; adding a key does not.
An empty list is written `[]`, never `null`.

Envelope:

| Key | Type | Meaning |
|---|---|---|
| `schema_version` | number | schema version (`1`) |
| `generator` | string | `tenon <version>` |
| `report_id` | string | `sha256:` + hash of `report` (see above) |
| `html_sha256` | string | `sha256:` + hash of the HTML file |
| `report` | object | the report |

`report`:

| Key | Type | Meaning |
|---|---|---|
| `generator` | string | `tenon <version>` |
| `generated_at` | string | generation date |
| `period` | object | `since`, `until` (`YYYY-MM-DD`, inclusive) |
| `scope` | object | `kind` (`project`, `all` or `session`), `project` or `session` when it applies |
| `details` | boolean | `--details` was used |
| `disclaimer` | string | the notice |
| `integrity` | object | see below |
| `content_available` | boolean | `false` at metadata level |
| `excluded_metadata_sessions` | number | sessions left out of the project scope (project unknown at metadata level) |
| `summary` | object | `sessions`, `turns`, `tool_calls`, `files_modified`, `denials` (numbers), `jurisdictions` (list of strings), `unknown_provider_sessions` (number: sessions in the range whose provider is unknown, 0 at metadata level) |
| `profiles` | list | strings: distinct compliance profiles of the sessions in the range (`[]` without profile; also `[]` at metadata level, where the profile is unknown, not absent; in a mixed-level range, sessions at metadata level add nothing) |
| `policies` | list | `id`, `key_id` (strings), `version`, `sessions` (numbers): one entry per distinct (id, version, key) among the sessions in the range, sorted by id then version; sessions without a policy are not listed (`[]` without policy; also `[]` at metadata level, where the policy is unknown, not absent) |
| `providers` | list | `id`, `name`, `model`, `country`, `jurisdiction` (strings), `sessions` (number), `first`, `last` (dates) |
| `jurisdiction_consents` | object | `granted`, `denied` (numbers) |
| `jurisdiction_warning_sessions` | list | session ids |
| `mcp_servers` | list | `id`, `jurisdiction`, `country` (strings), `sessions` (number) |
| `mcp_failures` | list | `session`, `server`, `error` (strings), `ts` (date) |
| `redactions` | object | category name to number of masked items |
| `permissions` | list | `tool` (string), `requested`, `allowed_once`, `allowed_session`, `denied_user`, `denied_policy`, `denied_hook` (numbers) |
| `files` | list | `ts`, `session`, `path`, `tool`, `hash_before`, `hash_after`, `author`, `no_checkpoint` (boolean), `rewind_status` (omitted when no rewind concerns the file) |
| `commands` | list | `session`, `count`; `commands` (list of strings) only with `--details` |
| `subagents` | list | `ts`, `session`, `call_id`, `agent`, `status` |
| `hooks` | list | `ts`, `session`, `hook_id`, `event`, `status`; `decision` and `reason` when not empty |
| `rewinds` | list | `ts`, `session`, `mode` (strings and date), `to_seq`, `restored`, `deleted`, `kept`, `skipped`, `failed`, `unchanged`, `untracked_calls` (numbers) |
| `incidents` | object | `errors` (`ts`, `session`, `code`, `message`), `turns` (`ts`, `session`, `reason`), `unreadable` (`file`, `line`, `error`) |
| `regulatory_mapping` | list | `element` (string), `frameworks` (list of strings) |
| `git_marking_hint` | string | command that finds marked code |
| `timeline` | list | only with `--details`: `session`, `entries` (`ts`, `kind`, `text`, and `tool` when it applies) |

`integrity`:

| Key | Type | Meaning |
|---|---|---|
| `intact` | boolean | the whole chain verifies |
| `error` | string | first fault, omitted when intact |
| `total_lines` | number | lines in the whole log |
| `final_hash` | string | hash of the last line of the log (omitted when broken) |
| `range_lines` | number | lines in the exported range |
| `first_hash`, `last_hash` | string | hashes of the first and last line of the range (omitted when the range is empty) |
| `level` | string | `full`, `metadata`, `mixed` or `none` |

`kind` in `timeline` is `prompt`, `response`, `tool_call`, `tool_result`, `permission` or `hook`.

## Files and directories

| Path | Content | Permissions |
|---|---|---|
| `~/.config/tenon/config.toml` | global configuration | — |
| `.tenon/config.toml` | project configuration | — |
| `~/.local/share/tenon/audit/` | audit log | directory `0700`, files `0600` |
| `~/.local/share/tenon/sessions/` | sessions | directory `0700`, files `0600` |
| `~/.local/share/tenon/checkpoints/` | file copies for `/rewind` | directory `0700`, files `0600` |

`$XDG_CONFIG_HOME` and `$XDG_DATA_HOME` replace `~/.config` and `~/.local/share` when they are set.
