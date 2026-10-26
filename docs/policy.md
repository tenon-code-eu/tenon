# Organisation policy

An organisation can impose a floor on every workstation that the user cannot loosen: minimum profile, allowed providers, permission and redaction rules, audit level, allowed MCP servers.
The policy is a file deployed by the administrator (MDM, Ansible), signed with [minisign](https://jedisct1.github.io/minisign/) and verified at every startup.
It is recorded in the audit log and in the compliance export.

Without a policy, tenon behaves as described in [usage.md](usage.md). A policy only tightens: it never widens what the configuration allows.

## Where the policy lives

The system directory holds three files:

| Platform | Directory |
|---|---|
| Linux | `/etc/tenon/` |
| macOS | `/Library/Application Support/tenon/` |

| File | Content |
|---|---|
| `policy.toml` | the policy |
| `policy.toml.minisig` | minisign signature of `policy.toml` |
| `policy.pub` | the organisation public key (minisign `.pub` format) |

The directory and the three files must be owned by root (uid 0) and must not be writable by the group or by others (for example `chmod 755` on the directory and `chmod 644` on the files). Otherwise tenon refuses to start.

`TENON_POLICY_DIR` names another directory. It is read **only when the system directory has no `policy.toml`**: it can add a policy (personal or test policy), never remove or replace a system one. That directory is not permission-checked; the signature is still verified.

No `policy.toml` in either place: no policy.

## Sign a policy

```sh
minisign -G -p policy.pub -s org.key       # once: key pair
minisign -Sm policy.toml                   # writes policy.toml.minisig
# Linux
sudo install -d -o root -g root -m 755 /etc/tenon
sudo install -o root -m 644 policy.toml policy.toml.minisig policy.pub /etc/tenon/
# macOS
sudo install -d -o root -g wheel -m 755 "/Library/Application Support/tenon"
sudo install -o root -m 644 policy.toml policy.toml.minisig policy.pub "/Library/Application Support/tenon/"
```

Both minisign signature formats are accepted: prehashed (`ED`) and legacy (`Ed`).
The key id of the signature must match the one in `policy.pub`, and both the file signature and the global (trusted comment) signature must verify.
Any change to `policy.toml` after signing makes tenon refuse to start.

## Content

```toml
id = "acme-dev"                          # required, [a-z0-9_-]{1,64}
version = 3                              # required, integer >= 1
profile = "eu-only"                      # optional: floor

[providers]                              # optional: allowlist
allow_hosts = ["api.mistral.ai", "*.scaleway.ai"]
allow_local = true                       # local hosts (localhost, loopback and private IP literals) allowed

[[providers.define]]                     # optional: providers supplied by the organisation
id = "interne"
name = "vLLM interne"
base_url = "https://llm.acme.eu/v1"
api_key_env = "ACME_LLM_KEY"
model = "qwen3"
country = "FR"
jurisdiction = "eu"
context_window = 128000
qualifications = ["secnumcloud"]         # optional: declared, never verified

[[permissions.rules]]                    # deny / ask only
tool = "shell"
match = "curl *"
decision = "deny"

[[redaction.rules]]
name = "acme-token"
pattern = "ACME-[A-Z0-9]{32}"

[audit]
level = "full"                           # optional: full | metadata, imposed

[audit.timestamp]                        # optional: RFC 3161 timestamping
url = "https://tsa.acme.eu/tsr"
ca = """-----BEGIN CERTIFICATE-----
...
-----END CERTIFICATE-----"""      # optional trust roots

[sandbox]                                # optional: system sandbox floor
mode = "required"                        # auto | required ("off" refused)
deny_read = ["/srv/secrets"]             # added to the built-in list

[classification]                         # optional: added to user and project lists
paths = ["rh/**"]
markers = ["ACME CONFIDENTIAL"]

[routing]                                # optional: imposes the local provider
local = "interne-local"                  # a local preset or a providers.define provider

[budget]                                 # optional: imposes a session token cap
session_tokens = 1000000

[memory]                                 # optional: bounds the memory
enabled = false                          # false: no memory
project = false                          # false: .tenon/memory ignored
max_retention_days = 30                  # caps [memory] retention_days (1 to 3650)

[mcp]
mode = "allowlist"                       # any (default) | allowlist | off
allow_hosts = ["mcp.acme.eu"]
allow_commands = ["/usr/local/bin/mcp-git"]
```

Only `id` and `version` are required.

Values refused at load time: missing or invalid `id` or `version`; unknown profile; an `allow` rule; invalid decision or tool (same rules as the configuration); a redaction pattern that does not compile; an empty `allow_hosts` entry, or one with a scheme, path, port, or a wildcard anywhere but the start (`*.` followed by at least two labels); a relative `allow_commands` entry; unknown `mcp.mode`; `sandbox.mode = "off"` or another unknown mode; a relative `sandbox.deny_read` entry; a `network` key on a rule (a policy cannot open the network); unknown `audit.level`; an invalid `providers.define` entry (same rules as `[[providers]]`, `jurisdiction` required; `qualifications` accepts only `secnumcloud`); an `audit.timestamp.url` that is not `https` (`http` is allowed for local hosts only); an `audit.timestamp.url` whose host is not local under `profile = "strict-local"`; an `audit.timestamp.ca` without a valid PEM certificate; a `memory.max_retention_days` outside 1 to 3650; an unknown key.

### Memory

`[memory]` bounds [governed memory](usage.md#memory):

- `enabled = false`: no index, no memory tools, no `/remember`. `tenon memory list|show|forget` keep working, so an erasure request is always possible.
- `project = false`: `.tenon/memory` is ignored for reading and writing. `tenon memory forget` and `tenon memory list --scope project` still see it, so a project entry can always be erased.
- `max_retention_days`: upper bound of `retention_days` for `user` and `global` entries. The lowest value between the policy, the global configuration and the project configuration applies.

The effective value is the strictest of the policy, the global configuration and the project configuration. A refusal reads `memory: disabled by policy`.

## How the policy applies

Merge order: defaults, global configuration, project configuration, policy.

- **Profile**: the effective profile is the strictest of global, project and policy. If the configuration asks for a weaker one, tenon prints a notice and raises it: `profile "<c>" from config is weaker than policy profile "<p>" (policy <id>): raised`.
- **Qualifications**: under a policy, `qualifications` declared in the user configuration (providers and MCP servers) are removed, with a notice: `policy <id>: qualifications declared in config ignored (<ids>)`. Only the `qualifications` of `providers.define` count, so a user cannot self-qualify to satisfy a `secnumcloud` profile. tenon never verifies a qualification. `default_provider` may name a provider defined by the policy.
- **Defined providers**: merged after the user configuration. A user provider with the same `id` is ignored, with a warning: `policy <id>: provider "<pid>" from config ignored (defined by policy)`.
- **Provider allowlist** (only when `[providers]` is present): the host of the provider `base_url` (lowercase, port removed) must equal an `allow_hosts` entry, or be covered by a `*.x.eu` entry (any subdomain of `x.eu`, never `x.eu` itself), or be local with `allow_local = true`.
  - `[providers]` present with no `allow_hosts` and `allow_local = false` refuses every provider.
  - Providers defined by the policy (`define`) are subject to `allow_hosts` like the others: list their hosts.
  - A selected provider that is not allowed stops tenon at startup, before any network access, with exit code 1: `provider "<pid>" (host "<h>") not allowed by policy <id>`. `--allow-non-eu` does not lift it.
  - The same check runs before each send, after the profile check: nothing is sent and the turn is blocked (`Error` event, `code: "policy"`).
- **Permission rules**: placed first. `deny` is absolute; `ask` wins over the user's `allow` rules.
- **Sandbox**: the effective mode is the strictest of global, project, policy and profile (`off` < `auto` < `required`); `deny_read` of the policy is added to the others. `write` does not exist in a policy. See [usage.md](usage.md#system-sandbox).
- **Redaction**: policy rules are added to the others.
- **Classification**: `[classification] paths` and `markers` are added to the user and project lists; nobody can remove one. See [usage.md](usage.md#data-sensitivity-routing).
- **Routing**: `[routing] local` is imposed and replaces the user `routing.local`, with a notice: `policy <id>: routing.local imposed (<id>)`. It must name a provider whose jurisdiction is `local`: a local preset, one from `providers.define`, or a user `[[providers]]` entry. This is checked when the policy is applied to the configuration, not when the policy file is loaded; otherwise startup fails with `policy violation: routing.local must name a local provider`. Sessions of the policy are therefore routed to that provider when they become sensitive; the compliance export flags any session that reached a non-local provider after classification.
- **Budget**: `[budget] session_tokens` (positive integer) is imposed; the effective cap is the lowest of global, project and policy, so a user can lower it but never raise it. `warn_at` stays the user's. At the cap, the next turn ends with `budget_exceeded` (exit code 4 under `--strict`). See [usage.md](usage.md#token-sobriety).
- **Audit level**: `audit.level` of the policy replaces the configuration level, in both directions: an organisation can impose `metadata` (data minimisation) or `full` (traceability). When they differ, a notice is printed: `policy <id>: audit level "<p>" imposed (config: "<c>")`.
- **Audit timestamping** (<a id="audit-timestamping"></a>`[audit.timestamp]`): `url` replaces the user `url`; `ca` (PEM trust roots, covered by the signature), when present, replaces the user `ca_file`. Without `ca`, the user `ca_file` still applies. When they differ, a notice is printed: `policy <id>: audit timestamp imposed (<url>)`. The policy `ca` comes before `ca_file` in the choice of roots; `--tsa-ca` comes first. tenon ships no TSA preset; the organisation names its authority. On macOS and Windows, give a `ca`: the system roots cannot verify TSA certificates. Under `strict-local` a non-local host is refused. See [usage.md](usage.md#timestamping-rfc-3161).
- **MCP**: `off` removes every server; `allowlist` keeps a server only if its host (`http`) is in `allow_hosts` (same matching as providers, without `allow_local`) or if `command[0]` (`stdio`) is in `allow_commands` exactly. A removed server is dropped before it starts, with a notice (`policy <id>: MCP server "<sid>" skipped (not allowed)`) and a `McpServerDown` event. The profile MCP check still applies to the servers kept.

## Startup failures

tenon refuses to start (exit code 1) when a policy is present but not trustworthy. The message is:

```
tenon: policy: <file>: <reason>
```

In French: `tenon : politique : <fichier> : <raison>`.

Causes:

- `policy.toml.minisig` or `policy.pub` is missing;
- the key id of the signature differs from the one of `policy.pub`;
- the file signature or the global signature is invalid;
- system directory only: the directory or one of the three files is writable by the group or others, or is not owned by root;
- the TOML is invalid, contains an unknown key, or a value is refused (see above).

The policy is verified once, at startup, before the configuration is loaded. `tenon config check` fails the same way. An unauthorised provider is a separate startup error (see above).

## Check a policy: `tenon policy check`

```sh
tenon policy check [dir]
```

Verifies the signature and content of the policy in `dir` (default: the system directory) and prints a summary. It does not check ownership or permissions, so you can validate a policy before deploying it. It does not read the user or project configuration.

```
policy: acme-dev v3 (key 0123456789ABCDEF)
profile: eu-only
providers: 2 allowed hosts, local yes, 1 defined
rules: 1
redaction: 1
audit: full
mcp: allowlist
```

Without `[providers]` the line is `providers: no allowlist`; without a profile `profile: none`; without audit level `audit: not imposed`.
Exit code 0 when valid; 1 when invalid, or when no policy is found (`tenon: no policy in <dir>`).

## Traceability

- The welcome screen and `tenon config check` show `policy: acme-dev v3 (key <KeyID>)` when a policy is active. The key id is 16 uppercase hexadecimal characters, as printed by minisign.
- `SessionStarted` records `policy` (`id`, `version`, `key_id`). Sessions without a policy have no such key.
- The [compliance export](usage.md#compliance-export) lists the policies of the sessions in the range (`policies` key), with the number of sessions under each. At metadata level the policy is unknown: the report says "not available at metadata level".

## Limits

A policy bounds what tenon does. Permission rules apply to shell commands, and `[sandbox] mode = "required"` confines them, but the provider host allowlist does not restrict the network access of a command that was granted network or of stdio MCP programs, and an administrator of the machine can always remove it. Hooks imposed by the organisation, refresh by URL, expiry or revocation, several trusted keys and Windows are not supported.
