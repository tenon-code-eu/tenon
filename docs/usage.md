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
- [Providers and jurisdictions](#providers-and-jurisdictions)
- [API keys and keyring](#api-keys-and-keyring)
- [MCP servers](#mcp-servers)
- [Git marking of generated code](#git-marking-of-generated-code)
- [Audit log](#audit-log)
- [Files and directories](#files-and-directories)

## Commands

```
tenon [--resume <id|last>] [--lang fr|en] [--allow-non-eu] [--provider <id>]
tenon -p "<prompt>" [--resume <id|last>] [--lang fr|en] [--output text|json] [--allow-non-eu] [--provider <id>]
tenon sessions [--lang fr|en]
tenon skills [--lang fr|en]
tenon key set|delete <provider>
tenon audit verify
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
| Esc | Cancels the current turn. |
| Ctrl+C | Cancels the current turn. Outside a turn, quits. Twice in less than 2 s during a turn: quits. |
| PgUp / PgDn | Scrolls the conversation. |
| `/help` | Shows the key help and the available skills. |
| `/skills` | Lists the skills. |
| `/<name> [arguments]` | Starts a skill (see [Skills](#skills)). |
| Tab | Completes a command or a skill name after `/`. |
| `/quit` | Quits. |

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
- `--output json`: one JSON event per line on standard output, with a redacted payload. Fields: `id`, `session`, `seq`, `ts`, `type`, `payload`. The first event, `SessionStarted`, contains the model, the provider, the country and the jurisdiction.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Turn completed. A turn stopped by the iteration limit or by an incomplete answer also gives 0, with a message. |
| `1` | Error: configuration, missing key, provider, context too large, invalid option. |
| `3` | An action was denied (approval not possible, `deny` rule) or sending to the provider was blocked (jurisdiction). |
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

### What the project configuration can do

A repository can be hostile. For this reason, its configuration is limited to:

- `lang`;
- `deny` and `ask` permission rules;
- `[[redaction.rules]]` redaction rules.

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
A skill folder that is itself a symbolic link is skipped. So is a skills directory (for example `.claude/skills`) that is a symbolic link: a notice reports it.
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

## Providers and jurisdictions

You can use any OpenAI-compatible endpoint.
Presets:

| id | Provider | Jurisdiction | Key |
|---|---|---|---|
| `mistral` (default) | Mistral AI, FR | `eu` | `MISTRAL_API_KEY` |
| `scaleway` | Scaleway, FR | `eu` | `SCW_SECRET_KEY` |
| `ovhcloud` | OVHcloud AI Endpoints, FR | `eu` | `OVH_AI_ENDPOINTS_ACCESS_TOKEN` |
| `ollama` | Ollama, `http://localhost:11434/v1` | `local` | none |
| `vllm` | vLLM, `http://localhost:8000/v1` | `local` | none |

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
tenon audit verify
```

The command shows `OK: <n> lines, last hash sha256:…`, or the first faulty line (`file:line`) with code 1.
Write down the last hash somewhere else: see [security.md](security.md#log-tail-truncation).

If writing to the log fails, tenon runs no more actions.

## Files and directories

| Path | Content | Permissions |
|---|---|---|
| `~/.config/tenon/config.toml` | global configuration | — |
| `.tenon/config.toml` | project configuration | — |
| `~/.local/share/tenon/audit/` | audit log | directory `0700`, files `0600` |
| `~/.local/share/tenon/sessions/` | sessions | directory `0700`, files `0600` |

`$XDG_CONFIG_HOME` and `$XDG_DATA_HOME` replace `~/.config` and `~/.local/share` when they are set.
