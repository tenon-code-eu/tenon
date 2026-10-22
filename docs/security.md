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
- With `[audit.timestamp]`, a timestamp authority signs the head hash (see [Timestamping](#timestamping-rfc-3161)).

### Permissions

- Each tool call goes through the permission engine before it runs.
- By default, only reads inside the project are allowed without a request. Reading `.env*`, `*.pem` or `id_rsa*`, or any access outside the project, always requests an approval.
- A `deny` rule always wins. On a path, it also applies to the form before symbolic links are resolved, and it ignores case.
- A repository cannot relax the rules: its configuration can contain no `allow` rule, no provider and no MCP server, and it cannot turn the sandbox off or grant network to a rule.
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

### Compliance export

- `tenon audit export` only reads the log: it neither modifies nor locks it.
- Content is redacted again when the report is built, and control characters are replaced by spaces.
- The HTML report has no script and loads no external resource; its Content-Security-Policy is `default-src 'none'; style-src 'unsafe-inline'`.
- The two files are created with mode `0600` and never replace an existing file.
- Without `--details`, the files still hold redacted texts from the log: modified file paths, session ids, error messages, MCP failure texts and hook reasons. Handle the default report with care too.
- With `--details`, the files contain conversation data (prompts and commands in full; responses, tool arguments and tool results cut to 500 characters). Store and share them like the log itself. The HTML shows a banner when they are included.
- `report_id` and `html_sha256` detect an accidental change; they do not stop someone who rewrites both files. See [usage.md](usage.md#check-the-integrity-of-a-report).

### System sandbox

- Shell commands and stdio MCP servers are confined by the system: writes limited to the project, temporary files and caches; a built-in list of secrets unreadable; network closed (Landlock and user namespaces on Linux, `sandbox-exec` on macOS).
- Modes `auto` (default), `required` and `off`. The effective mode is the strictest of global, project, policy and profile; a project or a policy can never produce `off`. `strict-local` implies `required`.
- Secret variables (`api_key_env` of the providers and of every preset, `bearer_env`, `env_from`) are not passed on to commands, in every mode (on macOS a command can still read them from another process, see below).
- How each command ran is in the audit log (`ToolResult.sandbox`) and the compliance export. See [The sandbox and its limits](#the-sandbox-and-its-limits) for what stays open.

### Compliance profiles

- With `profile = "eu-only"`, `"secnumcloud"` or `"strict-local"` (global configuration; a project can only tighten it), the selected provider and the MCP servers must meet the profile, and `--allow-non-eu` is refused.
- The profile is checked at startup and again before each send to the model: on a violation nothing is sent and the turn is blocked (`Error` event, `code: "profile"`).
- The profile is recorded in `SessionStarted` and listed in the compliance export.
- Under `strict-local`, the requests to the provider (local model detection included) and to the MCP `http` servers ignore the proxy environment variables (`HTTP_PROXY`, `HTTPS_PROXY`): a private address accepted by the profile never goes through a proxy. Under `secnumcloud`, the same holds for a local provider (local model detection included); the MCP manager ignores the proxy variables under `strict-local` and `secnumcloud`.
- `secnumcloud` accepts a local recipient, or an `eu` recipient **declared** `secnumcloud`-qualified. tenon never verifies a qualification: a SecNumCloud qualification is an ANSSI certification of a service, not a technical property. tenon records who declared it (`qualifications` in `SessionStarted.provider` and in the compliance export). Presets declare none.
- A profile bounds the data sent to the model and to MCP servers. It does **not** restrict what shell commands or stdio MCP programs send over the network (the [sandbox](#the-sandbox-and-its-limits) closes their network by default, and `strict-local` makes it mandatory), and it does not change permissions, audit level or hooks. See [usage.md](usage.md#compliance-profiles).

### Provider allowlist

- `[provider_allowlist]` (`hosts`, `local`) in the global configuration, and optionally in `.tenon/config.toml`, restricts the hosts a provider may use, for users without an organisation policy. Every list present (policy, global, project) must accept the provider host: a project can only restrict further.
- It is checked at startup, before anything is contacted, and again before each send (`Error` event, `code: "allowlist"`, turn `blocked`). `--allow-non-eu` does not lift it.
- Startup message: `provider "<id>" (host "<h>") not in provider_allowlist (<global|project>)` (exit code 1).
- It does not apply to MCP servers, and it does not restrict shell commands or stdio MCP programs (the [sandbox](#the-sandbox-and-its-limits) closes their network by default). See [usage.md](usage.md#provider-allowlist).

### Organisation policy

- An administrator can deploy a signed policy (minisign) in a directory owned by root. tenon verifies it at every startup and refuses to start if it is missing a file, badly signed, invalid, or if the directory or its files are not owned by root or are writable by the group or others (fail closed).
- The policy is a floor: it raises the profile, restricts providers by host, adds `deny`/`ask` permission rules and redaction rules, imposes the audit level and filters MCP servers. User and project configuration, and `--allow-non-eu`, cannot loosen it.
- Provider hosts are checked at startup and again before each send (`Error` event, `code: "policy"`).
- The policy (id, version, key id) is recorded in `SessionStarted` and listed in the compliance export.
- Qualifications declared in user configuration (providers and MCP servers) are ignored under a policy, with a notice; only those of `providers.define` count, so a user cannot self-qualify to get past a `secnumcloud` profile.
- `TENON_POLICY_DIR` is read only when no system policy exists: it cannot remove or replace one. A user who can write there can only add a policy signed by a key they provide, which restricts them further.
- Permission rules apply to shell commands, but the provider host allowlist does not restrict the network access made by shell commands or stdio MCP programs (the policy can require the sandbox, which closes it by default), and an administrator of the machine can remove it. See [policy.md](policy.md).

## Known limits

### The sandbox and its limits

Permissions decide whether a command runs. The system sandbox (see [usage.md](usage.md#system-sandbox)) limits what a shell command or a stdio MCP server can do once it runs. It is on by default (`mode = "auto"`).

**Threat model.** Without a sandbox, an approved command, or one covered by a broad `allow` rule (`tool = "shell"`, `match = "*"`), writes anywhere you can, reads your secrets and sends them over the network. The sandbox covers three things: writes outside the project, temporary files and caches; reads of the listed secrets (`~/.ssh`, `~/.aws`, the macOS Keychain, the tenon configuration and data directories, `deny_read`…); the network, closed unless a rule or your answer opens it. Variables that carry secrets known to tenon (`api_key_env`, `bearer_env`, `env_from`) are not passed on to commands, in every mode.

**What stays open.**

- Reads elsewhere: a command reads everything you can read, except the listed paths. A secret kept in a file that is not listed is readable.
- If the sandbox is unavailable and the mode is `auto`, commands run unconfined, with a notice. Use `required` to refuse instead; `strict-local` implies it.
- Linux, Landlock: names inside a denied directory can be listed (the contents cannot be read). A file cannot be created, removed or renamed directly in a directory that contains a denied entry, nor in its ancestors up to the writable root (example: `deny_read = ["<project>/secrets"]` blocks new files directly at the project root; a `--config` file under `/tmp` blocks new files directly in `/tmp`). Entries created after launch in non-writable directories are not readable.
- Linux, Unix sockets: confined only with Landlock ABI 9 or later. On an older kernel a socket reachable by path (for example `/var/run/docker.sock`) stays connectable; tenon prints `sandbox: Unix sockets not confined (Landlock ABI < 9): <path> reachable`. For a user of the `docker` group, that socket gives root on the host. The D-Bus session bus (`$XDG_RUNTIME_DIR/bus`, `/run/user/<uid>/bus`, same notice) lets a command start an unconfined process with `systemd-run --user`, or read secrets from the Secret Service (GNOME Keyring, KWallet), the provider keys stored by tenon included. `required` does not refuse to start for it.
- Linux, TCP fallback (user namespaces unavailable: Docker default seccomp profile, Ubuntu AppArmor restriction): UDP stays open, so DNS and other UDP traffic can leave. Abstract Unix sockets are scoped (ABI 6 or later). Full isolation needs `CLONE_NEWUSER|CLONE_NEWNET`.
- Linux, git: `.git/config` and `.git/hooks` of the project stay writable (a write-only exclusion would stop git creating its own files at the root of `.git`). A command can plant a hook or a `core.fsmonitor` that runs unconfined at your next git command. macOS protects these two paths in a plain `.git` directory; a `.git` file (worktree, submodule) points outside the project and is not handled.
- macOS: `sandbox-exec` is deprecated by Apple, but it works. `(deny network*)` also covers Unix sockets.
- macOS, environment: removing a variable means it is not passed on, not that it is unreadable. A confined command can read the environment of your other processes, tenon included, with `sysctl` `KERN_PROCARGS2`. Keep provider keys in the Keychain (denied to commands) rather than in the environment.
- Writable caches (`~/.cache`, `~/Library/Caches`, `~/.npm`, `$TMPDIR`, `write`) can be poisoned: content a confined command leaves there is used later by unconfined runs (hooks, your own commands).
- A project under `/tmp`, `$TMPDIR` or a cache directory stays writable for every stdio MCP server, `risk = "read"` or empty included: the default write list covers it.
- Hooks and git marking are not confined.
- A stdio MCP server is confined by its `risk`; its working directory `$HOME` is not writable. A server declared `risk = "network"` has the network; a server with no `risk` has no network and a read-only project.
- A command granted network (rule `network = true`, or "Allow with network") can send anything it can read. "Always for this session" never remembers the network of an answer; it keeps the network of an `ask` rule with `network = true`.
- An explicit `--config` file outside the project is denied to commands (read and write). Inside the project it is not: it is versioned.
- Seccomp, resource limits, PID and mount namespaces, a network host filter and Windows are not covered.

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

### Checkpoints keep the raw content of your files

Before `write_file` and `edit_file` overwrite a file, tenon keeps a copy of it so that `/rewind` can restore it. The copy is the **raw content**, secrets included (`.env`, keys, tokens): restoring needs the exact file.
The copies are in `~/.local/share/tenon/checkpoints/`, outside the project, so they cannot be committed by mistake. Directories are `0700` and files `0600`. They are never sent to the provider and never written to the session or the audit log, which hold only the hashes. Masking does not apply to them.
Anyone who can read your data directory can read these copies. They are kept `retention_days` days (30 by default). To delete them all now: `rm -rf ~/.local/share/tenon/checkpoints`. `retention_days = 0` stops saving them.
A rewind writes only paths that the tools wrote in the session. It never follows a symbolic link in the last component of a path: a path that became a link is a conflict, and it is not written. A file you changed after the agent is not overwritten without your explicit answer. Each copy is checked against its sha256 before it is restored. `write_file` and `edit_file` never overwrite an existing file they cannot read.
A rewind is an action of the user: no tool and no hook can trigger it. It does not undo `shell` commands or MCP tools (see [usage.md](usage.md#checkpoints-and-rewind)).

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

### Timestamping (RFC 3161)

The threat: someone who controls the machine rewrites the whole log and recomputes every hash. The chain alone cannot see it.

- With `[audit.timestamp]`, a timestamp authority (TSA) signs the head hash at the end of each session or on `tenon audit timestamp`. A token proves that the log up to the covered line existed at the signed time. A log rewritten afterwards no longer contains the hash a token covers: `tenon audit verify` and `tenon audit export` report it (`anchored hash not in chain`), provided the tokens are still there. Someone who controls the machine can also delete `audit/timestamps/`: `audit verify` then prints `timestamps: none` and exits 0. Copy the tokens (and the log) off the machine regularly, like the last hash (see below), and let auditors compare against those copies or the expected number of tokens.
- The TSA receives only a SHA-256 hash, never content. tenon ships no TSA preset: you choose the authority, and nothing is called without configuration.
- A token does not prove the lines after it (`<m> lines after` in the `audit verify` output), nor that the content was true. Deleting the last lines is detected only if a token covers a later line.
- Revocation (CRL, OCSP) is not checked: verification is offline. A TSA certificate revoked after signing still verifies.
- The TSA client does not follow redirects, so an `https` to `http` redirect cannot bypass the URL check.
- On macOS and Windows the system roots cannot verify TSA certificates: use `ca_file`, the policy `ca` or `--tsa-ca`. Otherwise the end-of-session anchoring fails with a notice (never an error).
- Verifying without tenon: `openssl ts -verify -digest <hex> -in <token>.tsr -token_in -CAfile <ca>`, with `<hex>` the head hash without the `sha256:` prefix.
- A failed anchoring never blocks a session and never changes the exit code.

Configuration, command and output: see [usage.md](usage.md#timestamping-rfc-3161).

### Log tail truncation

Deleting the **last** lines of the log does not break the chain: `tenon audit verify` cannot detect it.
Countermeasure: regularly write down the last hash shown by `tenon audit verify` outside the machine (ticket, separate repository, signed message).
Later, check that this hash is still in the log, for example with `grep`.
If it is gone, lines were removed.
A timestamp token also covers the log up to its line: see [Timestamping](#timestamping-rfc-3161).

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
