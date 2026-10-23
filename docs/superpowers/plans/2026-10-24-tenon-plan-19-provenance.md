# tenon provenance attestations — Implementation Plan (plan 19)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** every commit containing agent-written code gets a signed in-toto attestation (DSSE, ed25519) in `refs/notes/tenon-provenance`, created by git hooks, verifiable offline with `tenon audit provenance`, recorded in the journal and the compliance export.

**Architecture:** a pure package `internal/provenance` (statement, DSSE, keys, build from `gitmark.AgentWrites`), git plumbing helpers for notes and commit contents, new `post-commit`/`post-rewrite` hooks installed by `tenon git install-hook`, a `cmd/tenon/provenance.go` command set, `[provenance]` in config and policy, and a Provenance section in the compliance export.

**Tech Stack:** Go 1.26 (vendored, `GOFLAGS=-mod=vendor`), standard library (`crypto/ed25519`, `crypto/sha256`, `crypto/x509` for PKCS#8, `encoding/pem`, `encoding/base64`, `encoding/json`), `git` CLI (already used by gitmark).

**Spec:** `docs/superpowers/specs/2026-10-24-tenon-provenance-design.md`

## Global Constraints

- No new module.
- Statement: `_type` `https://in-toto.io/Statement/v1`; `predicateType` `https://tenon-code.eu/provenance/ai-assisted/v1`; subjects: `{"name": "git+commit", "digest": {"gitCommit": "<sha>"}}` first, then one per attributed file `{"name": "<path>", "digest": {"sha256": "<content sha256>", "gitBlob": "<oid>"}}` sorted by path.
- Predicate fields exactly: `commit`, `tree`, `generator{name, version}`, `files[]{path, origin, sessions[], model, provider{id, jurisdiction, country}, written_at}`, `audit{head, timestamped}`, `profile`, `policy` (empty strings omitted).
- `origin`: `agent` (committed content hash equals a `HashAfter` of a successful agent write for that path) or `agent-modified` (path written by the agent in a project session, committed content matches none of its writes). Never prompts or file contents.
- DSSE: `payloadType` `application/vnd.in-toto+json`; envelope `{"payloadType","payload"(base64 std),"signatures":[{"keyid","sig"(base64 std)}]}`; signature ed25519 over `PAE = "DSSEv1" SP len(type) SP type SP len(body) SP body` (lengths as ASCII decimal).
- Keys: private `<config>/provenance/ed25519.key` (0600, PKCS#8 PEM, dir 0700); public `ed25519.pub` content `ed25519:<base64 std of the 32 raw bytes>`; `keyid` = first 16 hex chars of sha256(raw public key).
- Notes ref `refs/notes/tenon-provenance` (git `--ref tenon-provenance`); note content = compact JSON envelope; copy at `<data>/provenance/<sha>.dsse.json` (0600).
- Hooks: `post-commit` runs `tenon provenance attest HEAD --hook`; `post-rewrite` reads `<old> <new>` lines on stdin and runs `tenon provenance attest <new> --hook` for each; both scripts exit 0 always (same pattern as the existing commit-msg `HookScript`); 5 s timeout per attest (reuse `gitmark.Timeout`).
- Verification statuses: `verified`, `untrusted`, `invalid`, `stale`, `missing`, `none`; exit 0 iff all commits are `verified` or `none`, else 1.
- Exact messages:
  - `config: provenance not allowed in project configuration`
  - `provenance: <sha>: note exists (use --force)`
  - hook notice `provenance: <reason>` (stderr)
  - `provenance: no key (run tenon provenance keygen)`
  - `provenance: key exists (use --force)`
  - `config check`: `provenance: <enabled|disabled> (key <keyid|absent>)`
  - text verify line: `<sha7> <status> <n> files (<a> agent, <m> agent-modified) key <keyid>`
- Event `ProvenanceAttested {commit, files, agent, agent_modified, keyid}` in a short session `SessionStarted{kind:"provenance"}`; provenance sessions excluded from compliance conversation statistics.
- Config `[provenance] enabled` (default false), `key_file`; policy `[provenance] required` (forces enabled, hook failure journaled as an `Error{code:"provenance"}` event in the provenance session), `trusted_keys` (malformed → policy refused).
- Code comments French; user strings via `internal/i18n` (EN/FR) except `provenance:`/`config:` errors (EN); docs EN+FR.
- Every task: `gofmt -l cmd internal scripts` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOOS=linux GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- A repository using SHA-256 object format (`git init --object-format=sha256`): oids are 64 hex chars; `gitBlob`/`gitCommit` hold whatever git returns, and verification compares with git's own values — test in Task 3 (skip if the local git lacks sha256 support).
- A commit with a path containing spaces, non-ASCII or a newline-free unusual character: paths come from `git diff-tree -z` (NUL-separated), never parsed from quoted output — test in Task 3.
- A merge commit: attested files are those changed relative to the first parent; verification of a merge with a note works — test in Task 3.
- `git commit` run outside a tenon session with `tenon` missing from PATH: hook scripts exit 0 silently — test in Task 4 (script content check + run with empty PATH).
- An attestation whose payload was produced by a different key but whose `keyid` field is forged to a trusted key's id: verification checks the signature against the trusted key bytes, not the keyid → `invalid` (or `untrusted`), never `verified` — test in Task 1.

---

### Task 1: `internal/provenance` — statement, DSSE, keys

**Files:**
- Create: `internal/provenance/statement.go`, `internal/provenance/dsse.go`, `internal/provenance/keys.go`
- Test: `internal/provenance/dsse_test.go`, `internal/provenance/keys_test.go`

**Interfaces:**
- Produces:
  - `type Subject struct { Name string \`json:"name"\`; Digest map[string]string \`json:"digest"\` }`
  - `type Provider struct { ID, Jurisdiction, Country string }` (JSON `id`, `jurisdiction`, `country`, omitempty), `type File struct { Path, Origin string; Sessions []string; Model string; Provider Provider; WrittenAt time.Time }` (JSON `path`, `origin`, `sessions`, `model`, `provider`, `written_at`)
  - `type Predicate struct { Commit, Tree string; Generator Generator; Files []File; Audit Audit; Profile, Policy string }`, `type Generator struct{ Name, Version string }`, `type Audit struct { Head string; Timestamped bool }`
  - `type Statement struct { Type string \`json:"_type"\`; Subject []Subject \`json:"subject"\`; PredicateType string \`json:"predicateType"\`; Predicate Predicate \`json:"predicate"\` }`
  - `const StatementType`, `const PredicateType`, `const PayloadType`
  - `type Signature struct { KeyID, Sig string }` (JSON `keyid`, `sig`), `type Envelope struct { PayloadType, Payload string; Signatures []Signature }` (JSON `payloadType`, `payload`, `signatures`)
  - `func PAE(payloadType string, payload []byte) []byte`
  - `func Sign(st Statement, key ed25519.PrivateKey) (Envelope, error)`
  - `func Verify(env Envelope, trusted []ed25519.PublicKey) (st Statement, keyid string, err error)` — pure: tries every signature against every trusted key using the signature bytes only (the declared `keyid` never decides); none verifies → `ErrBadSignature`; wrong payloadType or invalid JSON → `ErrMalformed`; returned keyid = `KeyID` of the key that verified. `untrusted` is decided by the caller (Task 5).
  - `func KeyID(pub ed25519.PublicKey) string`, `func FormatPublicKey(pub ed25519.PublicKey) string` (`ed25519:<b64>`), `func ParsePublicKey(s string) (ed25519.PublicKey, error)`, `func GenerateKey(dir string, force bool) (pub ed25519.PublicKey, err error)` (writes `ed25519.key` 0600 PKCS#8 PEM and `ed25519.pub`; existing key without force → `ErrKeyExists`), `func LoadPrivateKey(path string) (ed25519.PrivateKey, error)`.

- [ ] **Step 1: Failing tests:**
  - `TestPAE`: `PAE("http://example.com/HelloWorld", []byte("hello world"))` equals `"DSSEv1 29 http://example.com/HelloWorld 11 hello world"` (DSSE spec vector).
  - `TestSignVerify`: sign a statement, verify with the key → same statement, keyid = `KeyID(pub)`.
  - `TestVerifyTampered`: change one byte of the decoded payload (re-encoded) → `ErrBadSignature`; change the sig → `ErrBadSignature`; wrong payloadType → `ErrMalformed`.
  - `TestVerifyForgedKeyID` (Review Focus 5): envelope signed by key B with `keyid` set to `KeyID(A)`; `Verify(env, [A])` → `ErrBadSignature`.
  - `TestKeyFormat`: `ParsePublicKey(FormatPublicKey(p))` round-trips; bad prefix / bad length → error.
  - `TestGenerateKey`: files created with modes 0600 / dir 0700; second call without force → `ErrKeyExists`; `LoadPrivateKey` loads it and its public part equals the returned pub.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/provenance/` — FAIL.
- [ ] **Step 3: Implement.** JSON of the statement: `encoding/json` with struct order as above (deterministic).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(provenance): in-toto statement, DSSE envelope and ed25519 keys`

---

### Task 2: building the statement from agent writes

**Files:**
- Create: `internal/provenance/build.go`
- Test: `internal/provenance/build_test.go`

**Interfaces:**
- Consumes: Task 1; `gitmark.Write{Path, HashAfter, SessionID, Model, Provider, Country string; TS time.Time}` (check the exact fields and the hash format of `HashAfter` in `internal/gitmark` / the tools package — the committed content hash must be computed the same way).
- Produces:
  - `type CommitFile struct { Path, Blob string; Content []byte }` (Path relative to repo root, slash-separated)
  - `type CommitInfo struct { SHA, Tree string; Files []CommitFile; Generator Generator; Audit Audit; Profile, Policy string; Jurisdiction func(provider string) string }`
  - `func Build(ci CommitInfo, writes []gitmark.Write) (Statement, bool)` — false when no file is attributed. Attribution per spec §3: match `writes` by path (writes' paths made relative to the repo root the same way gitmark does); `agent` when the content hash equals any `HashAfter` for that path — the retained write is the most recent matching one, and `sessions` lists every session whose writes have that hash (sorted, unique); `agent-modified` otherwise if the path has writes — retained write is the most recent; files without writes skipped; subjects sorted by path after the commit subject.

- [ ] **Step 1: Failing tests:** `TestBuildAgent` (content hash equals a write's HashAfter → origin agent, subject digests sha256/gitBlob, model/provider from the write); `TestBuildAgentModified`; `TestBuildSkipsHumanFiles`; `TestBuildSessions` (two sessions wrote the same final content → both listed, sorted); `TestBuildNone` (no attributed file → false); `TestBuildSorted`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/provenance/ -run Build` — FAIL.
- [ ] **Step 3: Implement.** Hash function: the same as gitmark's comparison (look at `gitmark.Trailers` for how it compares staged content with `HashAfter`, and reuse that helper — export it from gitmark if needed rather than duplicating).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(provenance): attribute committed files to agent writes`

---

### Task 3: git plumbing — commit contents and notes

**Files:**
- Create: `internal/provenance/git.go`
- Test: `internal/provenance/git_test.go` (temporary repositories; skip if `git` missing)

**Interfaces:**
- Produces:
  - `const NotesRef = "refs/notes/tenon-provenance"`
  - `func ResolveCommits(ctx context.Context, repo, spec string) ([]string, error)` — one rev (`git rev-parse --verify <rev>^{commit}`) or a range `A..B` (`git rev-list --reverse A..B`).
  - `func ReadCommit(ctx context.Context, repo, sha string) (CommitInfo, error)` — fills `SHA`, `Tree`, and `Files` for added/modified paths vs the first parent (`git diff-tree -z --no-commit-id -r --name-status --root -m --first-parent <sha>` or equivalent producing first-parent diffs for merges), each with `Blob` (`git ls-tree -z <sha> -- <path>` or `rev-parse <sha>:<path>`) and `Content` (`git cat-file blob`).
  - `func BlobAt(ctx context.Context, repo, sha, path string) (oid string, content []byte, err error)`
  - `func ReadNote(ctx context.Context, repo, sha string) ([]byte, bool, error)` (`git notes --ref tenon-provenance show <sha>`; absent → false, nil)
  - `func WriteNote(ctx context.Context, repo, sha string, data []byte, force bool) error` (`git notes --ref tenon-provenance add [-f] -F - <sha>`; exists without force → `ErrNoteExists`)
  - `func CommitMessage(ctx context.Context, repo, sha string) (string, error)`
  - All git calls with `-c core.quotepath=off` where relevant and the environment hardening gitmark uses (look at gitmark's git invocation helper and reuse it).

- [ ] **Step 1: Failing tests:** `TestReadCommitFiles` (add, modify, delete → only add+modify; contents and blobs correct); `TestReadCommitPathsUnusual` (Review Focus 2: path with spaces and `é`); `TestReadCommitMerge` (Review Focus 3: first-parent diff); `TestResolveRange`; `TestNotesRoundTrip` (write, read, exists without force → `ErrNoteExists`, with force replaced); `TestSHA256Repo` (Review Focus 1: `git init --object-format=sha256`, skip if unsupported; oids 64 hex).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/provenance/ -run 'ReadCommit|Resolve|Notes|SHA256'` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(provenance): git commit contents and notes plumbing`

---

### Task 4: config, policy and hooks

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`, `internal/config/policy.go`, `internal/orgpolicy/*.go`, `internal/gitmark/hook.go`, `docs/config.example.toml`
- Test: `internal/config/*_test.go`, `internal/orgpolicy/*_test.go`, `internal/gitmark/hook_test.go`

**Interfaces:**
- Produces:
  - `config.Provenance struct { Enabled bool \`toml:"enabled"\`; KeyFile string \`toml:"key_file"\` }`, `Config.Provenance`; `Config.ProvenanceRequired bool \`toml:"-"\`` (from policy); `Config.ProvenanceTrusted []string \`toml:"-"\`` (from policy).
  - `orgpolicy.Policy.Provenance struct { Required bool; TrustedKeys []string }` (TOML `required`, `trusted_keys`); malformed key → policy refused (`policy violation: provenance.trusted_keys: <reason>`, following the existing prefix).
  - `gitmark.InstallHook(repoRoot, force)` also installs `post-commit` and `post-rewrite` scripts (constants `PostCommitScript`, `PostRewriteScript`) with the same rules as commit-msg (refuse a different existing hook without force, `.bak` with force, `core.hooksPath` respected); scripts per Global Constraints, each starting with `command -v tenon >/dev/null 2>&1 || exit 0`.

- [ ] **Step 1: Failing tests:** `TestProvenanceProjectRefused`; `TestPolicyProvenance` (required forces enabled; trusted keys copied; malformed key refused); `TestInstallHookProvenance` (three hooks installed, executable, contents; existing different post-commit refused without force; `.bak` with force); `TestHookScriptsNoTenon` (Review Focus 4: run each script with `PATH=/nonexistent` via `sh` → exit 0, no output); `TestConfigExampleLoads` with a commented `[provenance]` block.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/config/ ./internal/orgpolicy/ ./internal/gitmark/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(provenance): [provenance] config and policy; post-commit and post-rewrite hooks`

---

### Task 5: commands — keygen, attest, audit provenance

**Files:**
- Create: `cmd/tenon/provenance.go`, `cmd/tenon/provenance_e2e_test.go`
- Modify: `cmd/tenon/run.go` (dispatch, `config check`), `internal/event/event.go`, `internal/event/payloads.go`, `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: Tasks 1–4; `gitmark.AgentWrites(sessionsDir, projectRoot)`; audit head (`audit.Head(dir)`) and timestamp coverage (`audit.Anchored` / timestamp helpers from plan 14) for `Audit{Head, Timestamped}`; current profile and policy id from the loaded config; version string.
- Produces:
  - `tenon provenance keygen [--force]`; `tenon provenance attest [<rev>|<A..B>] [--force] [--hook]` (default rev `HEAD`); `tenon audit provenance <rev>|<A..B> [--key <file|ed25519:…>] [--json] [--verbose]`.
  - `event.ProvenanceAttested` + `ProvenanceAttestedPayload{Commit string; Files, Agent, AgentModified int; KeyID string}` (JSON `commit`, `files`, `agent`, `agent_modified`, `keyid`); journal: `SessionStarted{kind:"provenance"}` then the event (and `Error{code:"provenance"}` on a failed hook run when policy requires provenance).
  - `--hook` mode: never exits non-zero; does nothing when provenance is disabled and not required; errors → `provenance: <reason>` on stderr; respects `gitmark.Timeout` via context.
  - Verification order per commit: no note → `missing` if the commit message has an `AI-Assisted-By:` trailer, else `none`; note unparsable or `ErrMalformed` → `invalid`; signature verifies with a trusted key (`--key`, else policy `trusted_keys`, else the local public key) → then check `git+commit` subject == sha (else `stale`) and every file subject against `BlobAt` (oid and sha256; mismatch or missing path → `invalid`) → `verified`; signature does not verify with trusted keys but verifies with the local public key or `--key` outside the trusted set → `untrusted`; else `invalid`.
  - `config check` line per Global Constraints.

- [ ] **Step 1: Failing e2e tests** (temporary git repo with `user.name/email` set, fake LLM writing two files via the existing e2e harness or by writing a session file directly in the sessions dir that `AgentWrites` reads; `tenon git install-hook`; `[provenance] enabled = true`; keygen in a temp config dir):
  - `TestProvenanceAttestVerify`: one file left as written, one edited by hand, commit (hooks run through real `git commit` with `PATH` containing the test binary as `tenon` via a symlink, or call `attest` directly) → note exists; `audit provenance HEAD` exit 0, line contains `verified 2 files (1 agent, 1 agent-modified)`.
  - `TestProvenanceUntrusted`: `--key` of another key and policy trusted keys excluding the local key → `untrusted`, exit 1.
  - `TestProvenanceStale`: copy the note to another commit → `stale`, exit 1.
  - `TestProvenanceMissing`: commit with an `AI-Assisted-By:` trailer and no note → `missing`, exit 1; plain commit → `none`, exit 0.
  - `TestProvenanceAmend`: amend → the new commit is attested (post-rewrite) and verifies.
  - `TestProvenanceHookNoKey`: enabled, no key → commit succeeds, stderr `provenance: no key (run tenon provenance keygen)`.
  - `TestProvenanceDisabled`: no `[provenance]` → no note after commit.
  - `TestProvenanceNoteExists`: second `attest HEAD` → `provenance: <sha>: note exists (use --force)`, exit 1; `--force` replaces.
  - `TestProvenanceJournal`: `ProvenanceAttested` event with counts and keyid.
  - `TestProvenanceConfigCheck`.
  - `TestProvenanceKeygen`: files and modes; second run → `provenance: key exists (use --force)`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./cmd/tenon/ -run Provenance` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): provenance keygen, attest and audit provenance`

---

### Task 6: compliance export and documentation

**Files:**
- Modify: `internal/compliance/*.go` (model, build, HTML, labels), `internal/i18n/catalog.go`, `docs/usage.md`, `docs/fr/utilisation.md`, `docs/policy.md`, `docs/fr/politique.md`, `README.md`
- Create: `docs/provenance.md`, `docs/fr/provenance.md`
- Test: `internal/compliance/*_test.go`

**Interfaces:**
- Consumes: `ProvenanceAttested` events, `SessionStarted.kind == "provenance"`.
- Produces: report field `Provenance struct { Commits, Agent, AgentModified int; KeyIDs []string }` (JSON `provenance`: `commits`, `agent`, `agent_modified`, `key_ids`; omitted without events); HTML row « Provenance » (FR/EN); provenance sessions excluded from conversation statistics (same mechanism as `cra` sessions).

- [ ] **Step 1: Failing tests:** `TestExportProvenance` (two events → counts summed, distinct key ids sorted, HTML row EN/FR); `TestExportProvenanceSessionsExcluded`; `TestExportProvenanceAbsent`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/compliance/` — FAIL.
- [ ] **Step 3: Implement**, then docs EN/FR: purpose (AI Act transparency, procurement, IP), what is attested and what is not (no prompts/code; deletions not attested; attribution by content hash — a human copying agent output verbatim is attributed `agent`, a reformatted file becomes `agent-modified`), statement and DSSE format with an example, keys and `keygen`, publishing the public key, hooks (`tenon git install-hook` installs three hooks), pushing notes (`git push origin refs/notes/tenon-provenance`, fetching with `git fetch origin refs/notes/tenon-provenance:refs/notes/tenon-provenance`), verification statuses and exit codes, verifying with standard tooling (the envelope is plain DSSE: `jq` + `base64` to read the statement), policy keys, limits (spec §10).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(compliance): provenance in the export; docs: provenance attestations (EN/FR)`
