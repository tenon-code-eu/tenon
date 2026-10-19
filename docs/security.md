# Security, sovereignty and limits

This document states what tenon guarantees, and above all what it does not guarantee.
It describes the behavior of the current code.
The detailed user manual is in [usage.md](usage.md).

## Sovereignty: the criteria

The sovereignty of tenon does not depend on the nationality of providers.
It depends on four verifiable criteria.
A service that is used **by default** to run, build or distribute tenon must meet all of them:

1. **Open**: open source software, or an open and documented protocol.
2. **Self-hostable**: you can replace it with your own instance.
3. **European applicable law**: the operator is subject to no extraterritorial law that would let a non-European authority access the data (for example the CLOUD Act, FISA section 702).
4. **Controlled data**: no data transfer outside the EU without your explicit consent, recorded in the audit log.

A service that does not meet these criteria stays usable.
It is never used by default, and its use is always explicit and recorded.

Consequences in tenon:

- The preconfigured providers are European (`eu`) or local (`local`). The default provider is Mistral AI.
- A `non-eu` provider or MCP server requires your consent: a question in the interactive interface for a provider, otherwise the `--allow-non-eu` option.
- The libraries are vendored: building from source needs no module proxy (`GOFLAGS=-mod=vendor`).

## What tenon guarantees

### Audit log

- Each event (message, tool call, permission, result, error) is written to `~/.local/share/tenon/audit/YYYY-MM-DD.jsonl`, with `fsync`.
- Each line contains the SHA-256 hash of the previous line. The first line has `prev = "sha256:0"`.
- `tenon audit verify` detects a line that was modified, inserted or deleted in the middle of the log.
- The content is redacted before it is written. With `audit.level = "metadata"`, only the hash and the size of each payload are kept.
- If writing fails, tenon runs no more actions: compliance comes before continuity.

### Permissions

- Each tool call goes through the permission engine before it runs.
- By default, only reads inside the project are allowed without a request. Reading `.env*`, `*.pem` or `id_rsa*`, or any access outside the project, always requests an approval.
- A `deny` rule always wins. On a path, it also applies to the form before symbolic links are resolved, and it ignores case.
- A repository cannot relax the rules: its configuration can contain no `allow` rule, no provider and no MCP server.
- In non-interactive mode, every approval request becomes a denial.
- In the interactive interface, approval requires a quiet period of 500 ms, a fully visible command and at least 40 columns (see [usage.md](usage.md#approve-an-action)).

### Redaction

- Secrets (API keys, tokens, private keys, `.env`-style values) and personal data (IBAN, NIR, e-mail, French phone number) are replaced with markers: `[SECRET_1]`, `[EMAIL_1]`…
- Redaction applies to everything sent to the provider, to the audit log, to sessions and to displayed outputs.
- The API keys of configured providers and the `env_from` / `bearer_env` values of MCP servers are redacted exactly, even when they have no recognizable form.
- Markers are never restored. An MCP call whose arguments contain a marker is denied.

### Transparency

- The model, the provider, the country and the jurisdiction are shown at startup, in the status bar and in the `SessionStarted` event of the JSON output.
- Commits that contain files written by the agent get the `AI-Assisted-By` and `AI-Session` trailers (see [usage.md](usage.md#git-marking-of-generated-code)).

## Known limits

### Permissions are not a sandbox

An approved shell command runs **with your permissions**, in the project directory.
It can read, modify or send everything that you can read, modify or send.
A broad `allow` rule (`tool = "shell"`, `match = "*"`) gives this power to the model without approval.
The same applies to a stdio MCP server: it is a program that runs with your permissions.
This version has no system isolation.

### Project skills are untrusted content

A skill in `.tenon/skills`, `.claude/skills` or `.agents/skills` of a repository is content written by the repository authors, like `AGENTS.md`.
Its text can contain instructions meant to mislead the model (instruction injection), and the model can load it on its own when the description matches.
tenon does not filter this text.

What a skill cannot do:

- It cannot raise permissions. `allowed-tools` is read, shown and logged, but never changes a decision.
- Its scripts are not run by the skill system: they run through the `shell` tool, with the usual validation, and every command that no `allow` rule covers needs your approval.
- It cannot shadow a user skill: when names are equal, the user skill wins and a notice reports the project skill as ignored.
- Its files are read with the same protections as other files: no symbolic link, no path outside the skill folder, no sensitive file.

What you should do: read a skill from an unknown repository before you use the project, as you would read `AGENTS.md`.
`tenon skills` lists the skills that tenon found, with their source. A `deny` rule on `tool = "skill"` forbids one.
Each load is in the audit log with the SHA-256 of the file read (see [usage.md](usage.md#skills)).

### Sub-agents run with the rights of the session

A sub-agent runs with the same permissions, masking, jurisdiction consent and audit as the main agent.
Each of its actions goes through the same rules and the same approvals. Its actions are in the same audit log, in the same chain.
It never gets more rights than the session, and it cannot launch a sub-agent.
The `model` field of an agent file is ignored: a sub-agent uses the provider and the model of the session, so it adds no new jurisdiction.

A file in `.tenon/agents`, `.claude/agents` or `.agents/agents` of a repository is untrusted content, like `AGENTS.md` and project skills.
Its text becomes the system prompt of a sub-agent, and can contain instructions meant to mislead the model. tenon does not filter it.
A project agent cannot shadow a user agent, and `tools` can only restrict the tools of the session.
What you should do: read the agents of an unknown repository before you use the project.
A project file named `general-purpose` replaces the built-in agent, which is the default target of the `agent` tool: a repository can therefore change the instructions and tools of every delegation that does not name an agent.
The approvals "yes for the session" are shared between the main agent and its sub-agents: an approval given for a sub-agent action also covers the main agent, and the other way round.
`tenon agents` lists the agents that tenon found, with their source. A `deny` rule on `tool = "agent"` forbids one (see [usage.md](usage.md#sub-agents)).

### Hooks run your code, with your rights

A hook is a program you wrote or installed. It runs as you, outside the permission rules.
Only your own files are read (`~/.config/tenon/hooks.json`, and `~/.claude/settings.json` if you set `hooks.claude_settings = true`). **Hooks of a project never run**: tenon only shows a notice. A repository cannot make you run code through a hook of its own. But a hook of yours that runs a file of the project (`$CLAUDE_PROJECT_DIR/.claude/hooks/x.sh`, `./scripts/...`, `npm test`) runs code of the repository, with your rights: write such hooks only for repositories you trust.

A hook cannot widen the permissions of the session. `allow` lifts only an `ask` that comes from the rules, as a global `allow` rule would. It never lifts a `deny` rule, a protected action, a path outside the project, a sensitive file or an `ask` rule of the project. A hook can always restrict: `deny`, `ask`, or a change of the tool input that is checked again.

`command` hooks receive the input of the tool **unmasked**: they run on your machine, and tenon does not control what they do with it. A hook that sends this input to a service (a notification hook that posts to a service outside the EU, for example) is your responsibility, as for a stdio MCP server. The jurisdiction check does not cover it.
`prompt` and `agent` hooks, and everything a hook sends back to the model (`additionalContext`, reasons, stderr), go through the same masking and jurisdiction check as any request. A `prompt` or `agent` hook uses the provider and the model of the session (`model` is ignored), so it adds no new jurisdiction.

A hook failure does not block. A crash, a timeout, an unreadable output or a provider error is a non-blocking error: the action goes on, with a warning in the interface and in the audit log. A broken security hook therefore does not protect you. Test your hooks, and watch for warnings. Only an invalid hooks file stops tenon from starting.
Every hook run is in the audit log (see [usage.md](usage.md#hooks)). `tenon hooks` lists the hooks that tenon loaded.

### Jurisdiction is declarative

tenon does not verify the jurisdiction of a provider or of a server.
It applies the value written in the configuration.
A wrong declaration (`eu` for an operator subject to the CLOUD Act) removes the protection.
As a precaution, a provider added without `jurisdiction` is treated as `non-eu`.

### `local` stdio server

A stdio MCP server is `local` by default.
This describes where the program runs, not where it then sends your data.
If the program relays a service outside the EU, declare it `jurisdiction = "non-eu"`.

### Redaction is approximate

- Detection uses regular expressions. It misses secrets with an unknown format and sometimes redacts ordinary text.
- Detection of personal data is partial. tenon does not guarantee GDPR compliance.
- The model answer arrives in fragments (`AssistantDelta`). Each fragment is redacted separately: a secret split between two fragments can appear in clear text in the fragments of the audit log and of the `--output json` output. The complete final message (`AssistantMessage`) is redacted as one piece.
- An API key or an `env_from` / `bearer_env` value shorter than 8 characters is not redacted as such, to avoid redacting ordinary text.

Add your own rules with `[[redaction.rules]]`.

### Log tail truncation

Deleting the **last** lines of the log does not break the chain: `tenon audit verify` cannot detect it.
Countermeasure: regularly write down the last hash shown by `tenon audit verify` outside the machine (ticket, separate repository, signed message).
Later, check that this hash is still in the log, for example with `grep`.
If it is gone, lines were removed.

### Look-alike characters and the terminal

The approval panel makes invisible, zero-width and bidirectional characters, and unusual spaces, visible (`[U+XXXX]`).
It cannot show everything:

- look-alike letters (Cyrillic `а` instead of Latin `a`, fake quotes, fake dashes) are shown as they are. A warning only signals that a shell command contains a non-ASCII character;
- a look-alike domain in a URL is not detected;
- some terminals reorder bidirectional text or draw combining marks badly: the rendering also depends on your terminal and its font.

Read the command. If in doubt, deny.

### Git marking

- Marking relies on the session files. A deleted session is no longer taken into account.
- Matching is done by path: a file written once by the agent is marked at each commit where it is staged, even if a human has fully rewritten it since.
- `git commit --no-verify`, a missing hook or `tenon` missing from `PATH` give an unmarked commit. Merge commits are not marked.

Marking helps traceability. It is not a proof.

### Build tools and network

Building tenon from source contacts no service: the dependencies are vendored.
The development toolchain (`make lint`, `make vuln`, `make release`, CI) is an exception:

- the pinned build tools (staticcheck and govulncheck, installed in `.tools/` by `scripts/go-tool.sh`; cyclonedx-gomod and nfpm, run by `go run <module>@<version>`) are downloaded from their origin hosts (`GOPROXY=direct`): `honnef.co` (which redirects to `github.com`), `golang.org` and `go.googlesource.com` (operated by Google), `github.com` (operated by Microsoft);
- their checksums are verified against `sum.golang.org`, operated by Google;
- `govulncheck` queries the vulnerability database `vuln.go.dev`, operated by Google;
- the repository and the GitHub Actions CI are hosted by GitHub (Microsoft, United States); the `.forgejo/` and `.woodpecker.yml` files allow CI on a European or self-hosted forge;
- the CI (`.github/workflows/ci.yml`, `.forgejo/workflows/ci.yml`, `.woodpecker.yml`) downloads its images from Docker Hub (Docker Inc., United States): `golang` and, for Woodpecker, `woodpeckerci/plugin-git`. These images are pinned to a version; a European registry mirror can replace them.

These services receive no data from your sessions.
With the default Go configuration (without `GOPROXY=direct`), modules also go through `proxy.golang.org`, operated by Google.

## Recovery

### Corrupted audit log

Symptoms: `tenon audit verify` reports `file:line`, or tenon refuses to start with "last log line unreadable, chain not resumed".

Do not modify the log in place: it is evidence.

1. Quit all tenon instances.
2. Move the log aside, without modifying it:
   ```
   mkdir -p ~/tenon-audit-2026-10-15/tenon
   mv ~/.local/share/tenon/audit ~/tenon-audit-2026-10-15/tenon/audit
   ```
3. Verify the copy to record the exact location of the fault:
   ```
   XDG_DATA_HOME=~/tenon-audit-2026-10-15 tenon audit verify
   ```
4. Start tenon again: a new log starts with `prev = "sha256:0"`.
5. Record the incident (date, reported fault, last known hash) next to the copy.

A cut last line (abrupt stop during a write) gives the same symptom.
The procedure is the same.

### Corrupted session

On resume (`tenon --resume`), tenon reads the session up to the last readable line and shows a warning.
The file is cut at the first unreadable line.
The full original is kept in `~/.local/share/tenon/sessions/<id>.jsonl.corrupt`.

If the resume still fails, start a new session.
The content of the old session stays readable in its JSONL file and in the audit log.

A session that is "already open in another terminal" is locked by another tenon process.
Close that process before you resume the session.
