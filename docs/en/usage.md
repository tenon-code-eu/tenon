# Usage guide

*English translation of [the French reference](../utilisation.md). If the two differ, the French version is authoritative.*

This guide describes the behavior of tenon as it is coded today.
For security guarantees and limits, see [security.md](security.md).
For a commented configuration example, see [config.example.toml](../config.example.toml).

## Contents

- [Commands](#commands)
- [Interactive interface](#interactive-interface)
- [Non-interactive mode](#non-interactive-mode)
- [Sessions](#sessions)
- [Language](#language)
- [Configuration](#configuration)
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
tenon key set|delete <fournisseur>
tenon audit verify
tenon config check
tenon git install-hook [--force]
tenon git mark <fichier-message>
tenon --version
tenon --help
```

In the synopsis, `<fournisseur>` is the provider id and `<fichier-message>` is the commit message file.

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
| `/help` | Shows the key help. |
| `/quit` | Quits. |

When you quit, tenon waits at most 10 s for the current turn to end.
After that, it exits with code 1.

### Approve an action

When the agent wants to write, run a command or go outside the project, a panel replaces the input area.
It shows the tool, the full command or the paths, and a preview of the change for `write_file` and `edit_file`.

| Answer | French | English |
|---|---|---|
| Yes, once | `o` | `y` |
| Yes for the session | `s` | `a` |
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
tenon -p "corrige les tests qui échouent"
tenon -p "résume ce dépôt" --output json
```

(The example prompts mean "fix the failing tests" and "summarize this repository".)

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
tenon sessions                 # sessions du projet courant, la plus récente d'abord
tenon --resume last            # reprend la plus récente
tenon --resume s_0123456789ab  # reprend une session précise
tenon --resume last -p "continue"
```

(The comments mean: sessions of the current project, most recent first; resumes the most recent one; resumes a specific session.)

For each session, `tenon sessions` shows: identifier, update date, number of turns, first message.

You can resume a session only from the project where it was opened.
A session open in another terminal is locked: tenon refuses to resume it.

If a session file is damaged, the resume continues up to the last readable line, with a warning.
The file is cut at the first unreadable line. The full original is kept in `<id>.jsonl.corrupt`.

## Language

The language of the interface and of the system prompt is, in order of priority:

1. the `--lang fr|en` option;
2. the `lang` key of the project configuration, then of the global configuration;
3. `en` if `$LANG` starts with `en`, otherwise `fr`.

The approval keys follow the language: `o`/`s`/`n` in French, `y`/`a`/`n` in English.

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
| `lang` | depends on `$LANG` (see [Language](#language)) |
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
tool = "shell"          # ou read_file, write_file, edit_file, grep, glob, mcp__<serveur>__<outil>, "*"
match = "go test *"     # * = toute suite de caractères ; vide = tout appel de l'outil
decision = "allow"      # allow, ask ou deny
```

(The comments mean: `tool` is `shell` or one of the other listed tools, or `"*"`; in `match`, `*` matches any sequence of characters and an empty value matches every call of the tool; `decision` is `allow`, `ask` or `deny`.)

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
| `mcp__<serveur>__<outil>` | `mcp <serveur>.<outil> <arguments JSON compacts>` (server, tool, compact JSON arguments), for example `mcp gh.create_issue {"title":"x"}` |

So a path pattern does not apply to commands: `tool = "*"`, `match = "secrets/*"` blocks `read_file secrets/k`, but not the command `cat secrets/k`.
To protect a directory, combine a path rule and a shell rule:

```toml
[[permissions.rules]]
tool = "*"
match = "secrets/*"     # outils de fichiers
decision = "deny"

[[permissions.rules]]
tool = "shell"
match = "*secrets/*"    # toute commande qui cite secrets/
decision = "deny"
```

(The comments mean: file tools; any command that mentions secrets/.)

Even so, `cd secrets && cat k` or `cat secr*/k` get through: permissions filter calls, they are not a sandbox (see [security.md](security.md#permissions-are-not-a-sandbox)).

An `allow` rule on a command never covers a chained command: `go test *` does not allow `go test ./... ; rm x`.
`grep` and `glob` show only the files that `read_file` would read without a request.

### Project instructions

`AGENTS.md`, then `TENON.md`, at the project root, are added to the system prompt.
Each file is limited to 32,768 bytes.
A symbolic link is ignored.

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

- **interactive interface**: before the first send, tenon asks "send the conversation to …?" (`o` or `n`). The consent is valid until the end of the run. After an `o`, the outside-EU warning (`JurisdictionWarning`) is also shown and recorded in the log.
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
tenon key set mistral      # demande la clé sans l'afficher
echo "$CLE" | tenon key set mistral
tenon key delete mistral
```

(The comment means: asks for the key without showing it.)

The provider must exist in the configuration.
An empty value is refused.
An unreadable keyring does not prevent startup: the key is treated as missing, with a warning.

The keys of all configured providers, from the environment or from the keyring, are redacted in everything that tenon writes or sends.

## MCP servers

You declare MCP servers **only in the global configuration**.
See the two examples in [config.example.toml](../config.example.toml).

Their tools are offered to the model under the name `mcp__<serveur>__<outil>` (server, tool).
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
tenon git install-hook          # dans le dépôt qui contient le dossier courant
tenon git install-hook --force  # remplace un hook existant
```

(The comments mean: in the repository that contains the current directory; replaces an existing hook.)

The command installs a git **`commit-msg`** hook (executable, `core.hooksPath` respected).
If another `commit-msg` hook exists, it refuses without `--force`.
With `--force`, the old hook is renamed `commit-msg.bak`.
An old tenon `prepare-commit-msg` hook is removed.

At each commit, the hook runs `tenon git mark`.
This command compares the staged files with the files that the agent wrote successfully (`write_file`, `edit_file`), based on the sessions opened in this repository or in one of its subdirectories.
If it finds a match, it adds at the end of the message:

```
AI-Assisted-By: tenon (<modèle>, <fournisseur>, <pays>)
AI-Session: <identifiant de session>
```

(The placeholders are the model, the provider, the country and the session identifier.)

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

Each event is appended to the log `$XDG_DATA_HOME/tenon/audit/AAAA-MM-JJ.jsonl` (`AAAA-MM-JJ` is the date, YYYY-MM-DD).
Each line contains the hash of the previous line: the chain continues from one file to the next.

```
tenon audit verify
```

The command shows `OK: <n> lines, last hash sha256:…`, or the first faulty line (`fichier:ligne`, that is file:line) with code 1.
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
