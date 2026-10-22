# tenon system sandbox — Implementation Plan (plan 16)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** shell commands and stdio MCP servers run confined: writes limited to the project, temp dirs and caches, known secrets unreadable, no network unless a rule or the approval opens it, secret environment variables never passed. Landlock + user/net namespaces on Linux, `sandbox-exec` on macOS.

**Architecture:** a new `internal/sandbox` package computes what a command may touch (`Spec`), probes the system once (`Probe`), and rewrites an `*exec.Cmd` before `Start` (`Wrap`). On Linux, `Wrap` re-executes tenon as `__sandbox`, which applies Landlock and `execve`s the command; on macOS it prefixes `sandbox-exec -p <SBPL>`. Config, policy and profile set the mode; the permission engine carries a per-call `network` flag to the shell tool through the context.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), `golang.org/x/sys/unix` (already vendored), standard library.

**Spec:** `docs/superpowers/specs/2026-10-22-tenon-sandbox-design.md`

## Global Constraints

- No new module. `x/sys/unix` only for Landlock and prctl constants and syscalls.
- Modes: `auto` (default) < `required`; `off`. Effective mode = strictest of global, project, policy, profile (`strict-local` → `required`). A project or a policy can never produce `off`.
- Internal helper argv: `<os.Executable()> __sandbox -- <argv>`; spec in env `TENON_SANDBOX_SPEC` (JSON of `Spec`), removed before `execve`. Helper failure: stderr `sandbox: <reason>`, exit `126`, command not run.
- macOS: `/usr/bin/sandbox-exec -p <profile> -- <argv>`. SBPL order: `(version 1)`, `(allow default)`, `(deny file-write*)`, `(allow file-write* …)`, `(deny file-read* file-write* …)`, `(deny network*)` when network is closed.
- Default writable: project; `$TMPDIR`, `/tmp` (macOS also `/private/tmp`, `/private/var/folders`); `/dev/null`, `/dev/tty`, `/dev/zero`, `/dev/random`, `/dev/urandom`; `$XDG_CACHE_HOME` or `~/.cache` (macOS `~/Library/Caches`); `[sandbox] write`.
- Built-in deny (read and write, wins everywhere, even inside the project): `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.azure`, `~/.config/gcloud`, `~/.kube`, `~/.docker`, `~/.netrc`, `~/.password-store`, tenon config dir, tenon data dir, the org policy file, `deny_read` (global, project, policy). Missing paths ignored.
- Environment of shell commands: tenon's minus every `api_key_env` (all providers, presets included), `bearer_env`, `env_from` (all MCP servers). Applies in every mode, including `off` and unavailable.
- Exact messages:
  - `config: sandbox.mode must be auto, required or off`
  - `config: sandbox.<key>: path must be absolute or start with ~/`
  - `config: sandbox.<key> not allowed in project configuration`
  - `config: network only with allow or ask`
  - `config: network only for tool shell`
  - `config: network not allowed in project configuration`
  - notice `sandbox: unavailable (<reason>), commands run unconfined` (mode `auto`)
  - notice `sandbox: network isolation TCP only (user namespaces unavailable)`
  - notice `sandbox: network not isolated (<reason>)` (mode `auto`)
  - startup refusal (mode `required`, exit 1): `sandbox: required but unavailable (<reason>)` / `sandbox: required but network not isolated (<reason>)`
  - `config check`: `sandbox: <mode> (<mechanism>, network <full|tcp|none>)` or `sandbox: <mode> (unavailable: <reason>)`
- Mechanism names: `landlock`, `sandbox-exec`, `none`. Network isolation: `full`, `tcp`, `none`.
- Notices and refusals via `internal/i18n` (EN/FR); `config:` errors stay EN. Code comments in French; docs EN + FR.
- Hooks and gitmark stay unconfined.
- Linux tests run in Docker: `docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-mod=vendor golang:1.26.10 go test -race ./internal/sandbox/ ./internal/tools/` (default seccomp: TCP fallback path) and the same with `--security-opt seccomp=unconfined` (namespace path).
- Every task: `gofmt -l cmd internal scripts` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOOS=linux GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- Existing e2e tests that run shell commands now run confined on the dev machine (in-process `run`): a test writing outside its temp project or reaching `httptest` must keep passing — Task 6 sets `probeSandbox` to "unavailable" by default in `cmd/tenon` tests and enables it only in sandbox e2e tests.
- Project root that is a symlink, or contains a symlink to `~/.ssh`: canonical paths are used for rules, so the link does not open the target — test in Task 1 (split on resolved paths) and Task 2/3 integration.
- `$TMPDIR` unset or equal to a path inside a denied dir: no crash, default `/tmp` kept, deny still wins — test in Task 1.
- A command killed on timeout while confined (Linux: the helper `execve`s, so the PID is the command's): process group kill still reaches it — test in Task 3 (`sleep 30` with 1 s timeout returns timed out, no survivor).
- `--output json` headless run with an `ask` rule carrying `network = true`: headless denies the ask as today (exit 3), network never opens without an answer — test in Task 5.

---

### Task 1: `internal/sandbox` paths and SBPL profile

**Files:**
- Create: `internal/sandbox/sandbox.go`, `internal/sandbox/paths.go`, `internal/sandbox/seatbelt.go`
- Test: `internal/sandbox/paths_test.go`, `internal/sandbox/seatbelt_test.go`

**Interfaces:**
- Produces:
  - `type Spec struct { Dir string; Write, DenyRead []string; Network bool }` (JSON tags `dir`, `write`, `deny_read`, `network`)
  - `type Status struct { Available bool; Mechanism, Network, Reason string }`
  - `const SpecEnv = "TENON_SANDBOX_SPEC"`
  - `func DefaultWrite(project, home string, getenv func(string) string, goos string) []string`
  - `func DefaultDenyRead(home, configDir, dataDir, policyPath string) []string`
  - `func Canonical(paths []string) []string` — `EvalSymlinks` when the path exists, `filepath.Clean` otherwise; drops duplicates; keeps order.
  - `func splitAround(root string, deny []string) (allow []string, err error)` — unexported, used by Task 3: the entries to grant under `root` so that everything except `deny` is covered (walk from `root` down to each denied path, add every sibling at each level, never the denied component; a denied path outside `root` is ignored; a missing denied path is ignored).
  - `func seatbeltProfile(s Spec) string` — unexported, used by Task 2.

- [ ] **Step 1: Failing tests:**
  - `TestDefaultWrite`: linux with `TMPDIR=/x/tmp`, `XDG_CACHE_HOME` unset → contains project, `/x/tmp`, `/tmp`, `/dev/null`, `/dev/tty`, `/dev/zero`, `/dev/random`, `/dev/urandom`, `<home>/.cache`; darwin → also `/private/tmp`, `/private/var/folders`, `<home>/Library/Caches`, no `<home>/.cache`; `TMPDIR` unset → no empty entry (Review Focus 3).
  - `TestDefaultDenyRead`: contains the 9 home entries joined to `home`, plus configDir, dataDir, policyPath; empty arguments skipped.
  - `TestSplitAround` on a temp tree `r/{a,b/{c,secret,d},e}` with deny `r/b/secret` → allow = `r/a`, `r/b/c`, `r/b/d`, `r/e` (sorted); deny missing → allow = `[r]`; deny outside root → `[r]`; deny = root → empty.
  - `TestCanonicalSymlink`: a symlink `p → q` resolves to `q`; missing path kept cleaned.
  - `TestSeatbeltProfile`: lines appear in the Global Constraints order; each write path is `(subpath "<p>")` (or `(literal "<p>")` for `/dev/*` files); a path containing `"` and `\` is escaped; `Network: true` → no `(deny network*)`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/sandbox/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(sandbox): confinement spec, default paths, deny split and SBPL profile`

---

### Task 2: macOS mechanism and `Wrap`/`Probe` entry points

**Files:**
- Create: `internal/sandbox/wrap_darwin.go`, `internal/sandbox/wrap_other.go` (`//go:build !linux && !darwin`), `internal/sandbox/main.go`
- Test: `internal/sandbox/darwin_test.go` (`//go:build darwin`), `internal/sandbox/main_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces:
  - `func Probe() Status` — cached with `sync.Once`; darwin: run `/usr/bin/sandbox-exec -p '(version 1)(allow default)' /usr/bin/true` with a 2 s timeout → `{true, "sandbox-exec", "full", ""}`, else `{false, "none", "none", <reason>}`; other OS: `{false, "none", "none", "unsupported OS"}`.
  - `func Wrap(cmd *exec.Cmd, s Spec) error` — darwin: `cmd.Path = "/usr/bin/sandbox-exec"`, `cmd.Args = ["sandbox-exec", "-p", seatbeltProfile(canonical s), "--", <original Path>, <original Args[1:]...>]`; keeps `Dir`, `Env`, `SysProcAttr`, `Cancel`, `WaitDelay`. Other OS: returns an error `sandbox: unsupported OS`.
  - `func Main(args []string) int` — the `__sandbox` helper (Linux body in Task 3; on other OS prints `sandbox: unsupported OS`, returns 126).
  - `func MaybeMain()` — if `len(os.Args) > 1 && os.Args[1] == "__sandbox"`, `os.Exit(Main(os.Args[2:]))`; otherwise returns.

- [ ] **Step 1: Failing tests** (darwin, skip if `Probe().Available` is false):
  - `TestDarwinWriteOutsideRefused`: `Spec{Dir: proj, Write: DefaultWrite(proj, …)}` + `sh -c 'echo x > <other tempdir>/f'` → non-zero exit, file absent; same inside `proj` → exit 0, file present.
  - `TestDarwinDenyRead`: fake home `h` with `h/.ssh/id`; spec with `DenyRead: DefaultDenyRead(h, …)` → `cat h/.ssh/id` fails; a denied dir inside the project is unreadable too.
  - `TestDarwinNetwork`: `httptest` server; `curl -s <url>` (skip without curl) fails with network closed, succeeds with `Network: true`.
  - `TestWrapKeepsCmd`: `Dir`, `Env`, `SysProcAttr`, `WaitDelay` unchanged after `Wrap`.
  - `TestMaybeMainIgnoresOtherArgs` (all OS): `os.Args = ["x", "y"]` → returns.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/sandbox/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(sandbox): macOS sandbox-exec mechanism and probe`

---

### Task 3: Linux mechanism (`__sandbox`, Landlock, namespaces)

**Files:**
- Create: `internal/sandbox/wrap_linux.go`, `internal/sandbox/landlock_linux.go`
- Modify: `internal/sandbox/main.go`, `cmd/tenon/main.go` (first line of `main`: `sandbox.MaybeMain()`)
- Test: `internal/sandbox/linux_test.go` (`//go:build linux`, `TestMain` calls `MaybeMain()` first)

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces (Linux):
  - `Wrap`: `cmd.Path = os.Executable()`, `cmd.Args = [exe, "__sandbox", "--", <original Path>, <Args[1:]>]`, `cmd.Env` = original env (or `os.Environ()` when nil) plus `TENON_SANDBOX_SPEC=<json>`; when `!s.Network` and `Probe().Network == "full"`: merge `Cloneflags |= CLONE_NEWUSER|CLONE_NEWNET`, `UidMappings`/`GidMappings` `{ContainerID: os.Getuid()/Getgid(), HostID: same, Size: 1}`, `GidMappingsEnableSetgroups: false` into the existing `SysProcAttr` (keep `Setpgid`, `Pdeathsig`). When `!s.Network` and `Probe().Network == "tcp"`, the helper adds the TCP rules. `Spec` gains `TCP bool` (JSON `tcp,omitempty`), set only by `Wrap`, ignored on input from callers.
  - `Main(args)`: expects `-- <argv>`; reads and unsets `TENON_SANDBOX_SPEC`; `Main(["--probe"])` returns 0 without Landlock (used by `Probe` for the namespace test). Order: Landlock ABI (`unix.LANDLOCK_CREATE_RULESET_VERSION`), `prctl(PR_SET_NO_NEW_PRIVS)`, ruleset with every filesystem right of the ABI (minus `REFER` below ABI 2, `TRUNCATE` below 3, `IOCTL_DEV` below 5) and, if `tcp`, `ACCESS_NET_BIND_TCP|CONNECT_TCP` (ABI ≥ 4); read rules = `splitAround("/", DenyRead)`; write rules = for each `Write` entry, `splitAround(entry, DenyRead)`; `landlock_restrict_self`; `exec.LookPath` + `syscall.Exec`. Any failure → stderr `sandbox: <reason>`, return 126.
  - `Probe()`: ABI < 1 → unavailable (`landlock not supported by the kernel`); then start `<exe> __sandbox --probe` with the namespace flags (2 s): success → `network: "full"`; else ABI ≥ 4 → `"tcp"`; else `"none"` with reason.

- [ ] **Step 1: Failing tests** (Linux, in Docker per Global Constraints; skip if unavailable):
  - `TestLinuxWriteOutsideRefused`, `TestLinuxDenyRead` (same assertions as Task 2's darwin tests, also a denied dir inside the writable project).
  - `TestLinuxNetwork`: `httptest` on `127.0.0.1` outside the namespace; Go-free client via `bash -c 'exec 3<>/dev/tcp/127.0.0.1/<port>'` → fails closed (namespace or TCP rule), succeeds with `Network: true`.
  - `TestLinuxBadSpec`: `TENON_SANDBOX_SPEC=not-json` → exit 126, stderr starts with `sandbox: `.
  - `TestLinuxSpecNotLeaked`: `env` inside the sandbox has no `TENON_SANDBOX_SPEC`.
  - `TestLinuxTimeoutKillsGroup` (Review Focus 4): wrapped `bash -c 'sleep 30 & sleep 30'` with `Setpgid` and a 1 s context → returns within 3 s, `pgrep -g <pgid>` empty.
  - `TestProbeLinux`: `Mechanism == "landlock"`, `Network` in `full|tcp|none`.
- [ ] **Step 2: Run** both Docker commands — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** both Docker commands — PASS (namespace path with `seccomp=unconfined`, TCP path with default seccomp); full gates on the host.
- [ ] **Step 5: Commit** `feat(sandbox): Linux confinement with Landlock and user/net namespaces`

---

### Task 4: configuration, policy, profile

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`, `internal/config/policy.go`, `internal/orgpolicy/*.go` (policy struct + validation), `internal/profile/*.go`, `docs/config.example.toml`
- Test: `internal/config/*_test.go`, `internal/orgpolicy/*_test.go`, `internal/profile/*_test.go`

**Interfaces:**
- Produces:
  - `config.Sandbox struct { Mode string \`toml:"mode"\`; Write []string \`toml:"write"\`; DenyRead []string \`toml:"deny_read"\` }`, field `Config.Sandbox Sandbox \`toml:"sandbox"\``; after `Load`/`ApplyPolicy`, `Mode` is the effective mode (`"auto"` when empty), `DenyRead` is the union (global, project, policy), paths with `~/` expanded against `HOME`.
  - `config.Rule.Network bool \`toml:"network"\``.
  - `orgpolicy.Policy.Sandbox struct { Mode string; DenyRead []string }` (TOML `[sandbox] mode`, `deny_read`); `mode = "off"` or unknown → policy refused with `policy: sandbox.mode must be auto or required`.
  - `func profile.SandboxMode(n profile.Name) string` — `"required"` for `StrictLocal`, `""` otherwise.

- [ ] **Step 1: Failing tests:**
  - `TestSandboxModeInvalid` → `config: sandbox.mode must be auto, required or off`.
  - `TestSandboxPathInvalid` (`write = ["rel"]`) → `config: sandbox.write: path must be absolute or start with ~/`.
  - `TestSandboxProjectOff`, `TestSandboxProjectWrite` → `config: sandbox.mode not allowed in project configuration` / `config: sandbox.write not allowed in project configuration`; project `mode = "required"` raises `auto`; project `deny_read` appended.
  - `TestRuleNetworkDeny` → `config: network only with allow or ask`; `TestRuleNetworkTool` (`tool = "read_file"`) → `config: network only for tool shell`; `TestRuleNetworkProject` → `config: network not allowed in project configuration`.
  - `TestPolicySandbox`: `mode = "required"` over global `auto` → `required`; policy `off` refused; policy `deny_read` merged; global `off` + policy `auto` → `auto`.
  - `TestStrictLocalSandbox`: profile `strict-local` + global `off` → `required`.
  - `TestConfigExampleLoads` still passes with a commented `[sandbox]` block and a commented `network = true` rule example.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/config/ ./internal/orgpolicy/ ./internal/profile/` — FAIL.
- [ ] **Step 3: Implement.** Policy rules never carry `network` (the policy rule struct has no such field).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(config): [sandbox] mode, write, deny_read; network on shell rules; policy and strict-local floor`

---

### Task 5: per-call network, shell confinement, approval option

**Files:**
- Modify: `internal/policy/permissions.go`, `internal/engine/engine.go`, `internal/engine/loop.go`, `internal/event/payloads.go`, `internal/tools/shell.go`, `internal/tools/tool.go`, `internal/ui/tui/permission.go`, `internal/i18n/catalog.go`
- Test: `internal/policy/permissions_test.go`, `internal/engine/*_test.go`, `internal/tools/shell_test.go` (+ `TestMain` calling `sandbox.MaybeMain()`), `internal/ui/tui/*_test.go`, `internal/ui/headless/headless_test.go`

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces:
  - `policy.Rule.Network bool`; `func (p *Permissions) DecideNet(c Call) (d Decision, liftable, network bool)` — `network` is the matched `allow` or `ask` rule's flag (false for defaults); `Decide` keeps its signature and calls `DecideNet`.
  - `engine.Reply{Allow, Remember, Network bool}`; `Remember` never stores network.
  - `event.PermissionResolvedPayload.Network bool \`json:"network,omitempty"\``; `event.SandboxUse struct { Confined bool \`json:"confined"\`; Network bool \`json:"network"\` }`; `ToolResultPayload.Sandbox *SandboxUse \`json:"sandbox,omitempty"\``.
  - `tools.WithNetwork(ctx context.Context, on bool) context.Context`, `tools.NetworkFrom(ctx) bool`; `tools.Result.Sandbox *event.SandboxUse` copied by the engine into `ToolResultPayload`.
  - `tools.ShellOptions struct { Sandbox *sandbox.Spec; Env []string }`; `func Shell(root string, timeout time.Duration, limit int, opts ShellOptions) Tool` — `Env` is the filtered environment (always set, from Task 6); `Sandbox` nil = unconfined; non-nil = `Wrap` with a copy whose `Network = NetworkFrom(ctx)`.
  - TUI: when the shell tool is confined (engine passes `PermissionRequestedPayload.Sandboxed bool \`json:"sandboxed,omitempty"\``), the prompt adds "Allow with network" (i18n key `perm.allow_network`, FR « Autoriser avec réseau ») between Allow and Always.

- [ ] **Step 1: Failing tests:**
  - `TestDecideNet`: allow rule with `Network` → `(Allow, _, true)`; chained command beyond the allow → not matched, network false; ask rule with network → `(Ask, _, true)`; default ask → false.
  - engine `TestNetworkFromRule`: allow rule with network → tool receives `NetworkFrom(ctx) == true`; `TestNetworkFromReply`: ask + `Reply{Allow: true, Network: true}` → true and `PermissionResolved.network == true`; `TestRememberNoNetwork`: `Reply{Allow, Remember, Network}` then same command auto-allowed → network false.
  - tools `TestShellEnvFiltered`: `ShellOptions{Env: ["A=1"]}` + `env` command → output is exactly the given env (plus what bash adds), no parent `MISTRAL_API_KEY` set in the test process.
  - tools `TestShellConfined` (skip if `sandbox.Probe()` unavailable): write outside project fails; `Result.Sandbox == &SandboxUse{Confined: true, Network: false}`; with `WithNetwork(ctx, true)` → `Network: true`.
  - TUI `TestPermissionNetworkOption`: `Sandboxed: true` → the option is shown and answers `Reply{Allow: true, Network: true}`; `Sandboxed: false` → absent.
  - headless `TestHeadlessAskNetworkRule` (Review Focus 5): ask rule with `network = true` → denied, exit 3, no tool run.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/policy/ ./internal/engine/ ./internal/tools/ ./internal/ui/...` — FAIL.
- [ ] **Step 3: Implement.** Map `config.Rule.Network` to `policy.Rule.Network` where rules are converted today.
- [ ] **Step 4: Run** — PASS; full gates (update existing `tools.Shell` callers).
- [ ] **Step 5: Commit** `feat(engine): per-call network for confined shell commands; approval with network`

---

### Task 6: stdio MCP confinement and CLI wiring

**Files:**
- Modify: `internal/mcp/manager.go` (+ options), `cmd/tenon/run.go`, `internal/event/payloads.go`, `internal/i18n/catalog.go`
- Create: `cmd/tenon/sandbox.go`, `cmd/tenon/sandbox_e2e_test.go`
- Test: `internal/mcp/*_test.go`, `cmd/tenon/sandbox_e2e_test.go`, `cmd/tenon/e2e_test.go` (`TestMain`: `sandbox.MaybeMain()` first)

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
  - `mcp.Options.Sandbox *sandbox.Spec` (base spec; nil = unconfined); per server: `risk` `network` → `Network: true`; `write`/`exec` → project appended to `Write`; `read`/empty → project not writable. `Dir` stays `$HOME`, never added to `Write`.
  - `event.SessionStartedPayload.Sandbox *SandboxInfo \`json:"sandbox,omitempty"\``, `event.SandboxInfo struct { Mode, Mechanism, Network, Reason string }` (JSON `mode`, `mechanism`, `network`, `reason,omitempty`); MCP server start events carry `Sandbox *SandboxUse`.
  - `cmd/tenon/sandbox.go`: `var probeSandbox = sandbox.Probe`; `func sandboxSetup(lang i18n.Lang, cfg config.Config, wd, home, configDir, dataDir, policyPath string, getenv func(string) string) (spec *sandbox.Spec, info event.SandboxInfo, notices []string, err error)`; `func secretEnv(cfg config.Config, environ []string) []string` (drops every `api_key_env`, `bearer_env`, `env_from` name).
  - `config check` line per Global Constraints.

- [ ] **Step 1: Failing tests:**
  - e2e `TestSandboxRequiredUnavailable`: `probeSandbox` returns `{Available: false, Reason: "x"}`, `mode = "required"` → exit 1, stderr `sandbox: required but unavailable (x)`.
  - e2e `TestSandboxRequiredNetworkNone`: `{Available: true, Network: "none", Reason: "y"}` + `required` → exit 1, `sandbox: required but network not isolated (y)`.
  - e2e `TestSandboxAutoUnavailable`: `auto` → notice `sandbox: unavailable (x), commands run unconfined`, the shell command runs, exit 0, `ToolResult.sandbox.confined == false`.
  - e2e `TestSandboxTCPNotice`: `Network: "tcp"` → notice `sandbox: network isolation TCP only (user namespaces unavailable)`.
  - e2e `TestSandboxSessionStarted`: journal `SessionStarted.sandbox == {mode, mechanism, network}` from the probe.
  - e2e `TestSandboxSecretEnv`: provider `api_key_env = "K1"`, MCP `bearer_env = "K2"`, env has both; shell `env` output contains neither, in modes `auto` and `off`.
  - e2e `TestSandboxConfigCheck`: `config check` prints `sandbox: auto (sandbox-exec, network full)` with a stubbed probe, and `sandbox: required (unavailable: x)`.
  - e2e `TestSandboxRealDenyRead` (skip if real `sandbox.Probe()` unavailable; restores real probe): fake `HOME` with `.ssh/id`; fake LLM asks `cat ~/.ssh/id` under an allow rule → `ToolResult.is_error`, content lacks the file text.
  - mcp `TestStdioSandboxRisk`: `risk = "read"` → wrapped spec has no project in `Write`, `Network` false; `risk = "network"` → `Network` true; `risk = "write"` → project in `Write`.
  - Default in `cmd/tenon` tests (Review Focus 1): `TestMain` sets `probeSandbox` to `{Available: false, Reason: "test"}`; sandbox tests opt in.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./cmd/tenon/ ./internal/mcp/` — FAIL.
- [ ] **Step 3: Implement.** Probe once in `run` after config load; `required` refusals before any provider or MCP contact; notices printed with the existing startup notices.
- [ ] **Step 4: Run** — PASS; full gates; Docker run of `./cmd/tenon/ -run Sandbox`.
- [ ] **Step 5: Commit** `feat(cli): sandbox at startup, stdio MCP confinement, secret env removed from commands`

---

### Task 7: compliance export and documentation

**Files:**
- Modify: `internal/compliance/*.go` (model, build, HTML template), `docs/security.md`, `docs/fr/securite.md`, `docs/usage.md`, `docs/fr/utilisation.md`, `docs/policy.md`, `docs/fr/politique.md`, `README.md`
- Test: `internal/compliance/*_test.go`

**Interfaces:**
- Consumes: `SessionStarted.sandbox`, `ToolResult.sandbox` (Tasks 5–6).
- Produces: report field `Sandbox struct { Mode, Mechanism, Network string; Unconfined, WithNetwork int }` per session (JSON `sandbox`: `mode`, `mechanism`, `network`, `unconfined`, `with_network`); HTML row « Isolation » (FR) / « Sandbox » (EN).

- [ ] **Step 1: Failing tests:**
  - `TestExportSandbox`: a session with `SessionStarted.sandbox` + 3 shell results (`confined/network`: `true/false`, `true/true`, `false/false`) → `unconfined == 1`, `with_network == 1`, HTML contains the row in both languages.
  - `TestExportSandboxAbsent`: an older log without `sandbox` → field omitted, no row, no error.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/compliance/` — FAIL.
- [ ] **Step 3: Implement**, then docs (EN/FR): threat model (writes, reads of listed secrets, network); what stays open (reads elsewhere, UDP under TCP fallback, hooks, entries created after launch in non-writable dirs, `sandbox-exec` deprecation); modes and notices; `network = true` rules and "Allow with network"; MCP `risk` mapping; secret env removal; policy `[sandbox]`; `strict-local` → `required`; the Docker/userns note for CI runners. Replace the old "permissions are not a sandbox" warning in `docs/security.md` with the new limits.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(compliance): sandbox in the export; docs: system sandbox (EN/FR)`
