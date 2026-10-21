# tenon in CI

tenon runs in a pipeline in non-interactive mode (`-p`), with no terminal and no approval: any action that needs one is denied. This page gives ready-to-copy templates for GitLab CI, Forgejo/Gitea Actions and GitHub Actions, and explains how to keep the evidence of each run.

## Contents

- [Templates](#templates)
- [What the job does](#what-the-job-does)
- [Exit codes](#exit-codes)
- [Configuration of the job](#configuration-of-the-job)
- [Install from a mirror](#install-from-a-mirror)
- [Artifacts](#artifacts)
- [Security](#security)

## Templates

| Forge | Template |
|---|---|
| GitLab CI | [docs/ci/gitlab-ci.yml](ci/gitlab-ci.yml) |
| Forgejo and Gitea Actions | [docs/ci/forgejo.yml](ci/forgejo.yml) |
| GitHub Actions | [docs/ci/github.yml](ci/github.yml) |
| Configuration of the job | [docs/ci/tenon.toml](ci/tenon.toml) |

Copy the template of your forge and the configuration to `ci/tenon.toml` in your repository. Then:

1. Define the provider key (`MISTRAL_API_KEY` in the example) as a masked and protected variable (GitLab) or as a secret (Forgejo, Gitea, GitHub).
2. Pin the version: the templates install `v0.7.0`. Raise it on purpose, never use a floating version.
3. Run the job by hand with the task (`TENON_TASK` in GitLab, the `task` input elsewhere), or on merge and pull requests of your own repository.

## What the job does

1. **Install.** `TENON_VERSION=<vX.Y.Z> TENON_REQUIRE_SIGNATURE=1 sh install.sh`. The image has `minisign` (the templates install it with `apt-get`), so the signature of the release is verified, not only its SHA-256 hash.
2. **Run.** `XDG_DATA_HOME="$PWD/.tenon-data"` keeps the audit log inside the workspace. Then `tenon --config ci/tenon.toml -p "$TENON_TASK" --strict --output json > tenon-events.jsonl`. The exit code of the job is the exit code of tenon.
3. **Always, even on failure.** `tenon audit verify` (integrity of the hash chain) and `tenon audit export --all-projects --out tenon-audit` (compliance report), then the upload of the artifacts.

## Exit codes

| Code | Meaning in CI |
|---|---|
| `0` | Turn completed. |
| `1` | Error: configuration, missing key, provider, invalid option. |
| `3` | An action was denied (no approval possible in CI, or a `deny` rule) or sending to the provider was blocked by the jurisdiction. |
| `4` | With `--strict`: the turn ended with `max_iterations`, `incomplete`, `stopped_by_hook` or `cancelled`. Without `--strict` these cases give 0. |
| `130` | Second interruption (Ctrl+C or a forced stop): immediate exit. |

Code 3 wins over 4, and code 1 wins over both. See [Exit codes](usage.md#exit-codes). Use `--strict` in CI: a job that stops because the iteration limit was reached must not look like a success.

## Configuration of the job

`--config <file>` replaces the global configuration file (`$XDG_CONFIG_HOME/tenon/config.toml`) with a file of the repository, so the job does not depend on the home of the runner. The project file `.tenon/config.toml` and the organisation policy still apply: a policy can only tighten what `ci/tenon.toml` says. The file must exist, otherwise tenon exits with code 1.

[docs/ci/tenon.toml](ci/tenon.toml) declares the provider (`api_key_env`, the name of the variable, never the key), the `eu-only` profile, minimal `allow` rules (reads, `go test ./...`), `[audit] level = "full"` and, commented, `[audit.timestamp]`. Check it without network:

```
MISTRAL_API_KEY=x tenon --config ci/tenon.toml config check
```

## Install from a mirror

`TENON_DOWNLOAD_BASE` replaces GitHub as the source of the release (sovereign forge, air-gapped mirror). It requires `TENON_VERSION`, accepts `https://` only (and `http://localhost` or `http://127.0.0.1` for tests), and redirects must be `https://`. The mirror holds `<base>/<version>/<archive>`, `SHA256SUMS` and `SHA256SUMS.minisig`.

Each template has the line commented. The minisign signature is still verified with `TENON_REQUIRE_SIGNATURE=1`, so a compromised mirror cannot serve a forged binary.

## Artifacts

| Artifact | Content |
|---|---|
| `tenon-events.jsonl` | One JSON event per line (`--output json`), payload redacted. |
| `tenon-audit/` | Compliance export: HTML and JSON report. |
| `.tenon-data/tenon/audit/` | The audit log itself (hash chain) and, if `[audit.timestamp]` is set, the RFC 3161 tokens. |

See [Audit log](usage.md#audit-log), [Timestamping](usage.md#timestamping-rfc-3161) and [Compliance export](usage.md#compliance-export) for `audit verify`, `audit export` and `audit timestamp`. The artifacts are kept even when the job fails.

## Security

**A merge request can edit `ci/tenon.toml` like it edits the pipeline.** Whoever can change the configuration changes the provider, the profile and the permissions of the job. Either run the job only on protected branches with protected variables, or take the configuration from a separate repository that merge requests cannot modify (check it out and pass its path to `--config`).

**Keep the `allow` rules minimal.** In CI nobody approves anything, so each `allow` rule is a permanent authorisation for the model. Allow the exact commands the task needs (`go test ./...`), nothing broader. An `allow` rule on a command does not cover a chained command, but permissions are not a sandbox (see [Security and limits](security.md)): run the job on a runner that holds nothing you cannot lose.

**Never run a job with secrets on merge requests from forks.** The author of a fork controls the code, `ci/tenon.toml` and the task. The templates skip fork pipelines; keep it that way.

**An ephemeral runner keeps nothing.** The audit log, the compliance export and the timestamp tokens disappear with the runner. Archive the artifacts, timestamp tokens included, off the runner (artifact storage with a retention policy, or a copy to a store you control). A log that was not archived cannot be verified later.
