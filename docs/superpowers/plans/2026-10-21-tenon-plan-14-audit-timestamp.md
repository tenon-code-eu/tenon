# tenon RFC 3161 audit timestamping — Implementation Plan (plan 14)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** tenon has the head hash of its audit chain timestamped by an RFC 3161 TSA at the end of each session and on demand (`tenon audit timestamp`), stores the tokens beside the log, and `tenon audit verify` / `tenon audit export` detect a chain rewritten after a timestamp.

**Architecture:** A new stdlib-only `internal/tsp` package builds requests, parses and verifies RFC 3161 tokens (CMS SignedData subset). `internal/audit/timestamp.go` reads the head, stores tokens under `audit/timestamps/` and verifies them against the chain. `internal/config` / `internal/orgpolicy` carry `[audit.timestamp]`; `cmd/tenon` wires the end-of-session anchor, the new command, `--tsa-ca`, the `strict-local` refusal and `config check`; `internal/compliance` reports the result.

**Tech Stack:** Go 1.26 (vendored deps, `GOFLAGS=-mod=vendor`), standard library (`encoding/asn1`, `crypto/x509`, `crypto/rsa`, `crypto/ecdsa`), `openssl ts` once to generate test vectors.

**Spec:** `docs/superpowers/specs/2026-10-21-tenon-audit-timestamp-design.md`

## Global Constraints

- No new module. `internal/tsp` imports only the standard library and `internal/profile` (for `IsLocalHost`).
- Imprint: `hashAlgorithm` SHA-256 (OID 2.16.840.1.101.3.4.2.1), `hashedMessage` = the 32 raw bytes of the head hash (`sha256:<hex>` decoded). Never the hash string's bytes.
- Request: version 1, 64-bit random nonce, `certReq true`; POST `Content-Type: application/timestamp-query`; 10 s timeout; body read up to 1 MiB (`1 << 20`); HTTP status other than 200 is an error.
- Response accepted only with PKIStatus 0 (granted) or 1 (grantedWithMods), nonce equal, imprint equal, token valid.
- Token checks (spec §4, all blocking): one SignerInfo; signer by issuer+serial or SKI, present in `certificates`; signed attributes with `contentType` = id-ct-TSTInfo (1.2.840.113549.1.9.16.1.4), `messageDigest`, and ESSCertID (1.2.840.113549.1.9.16.2.12, SHA-1) or ESSCertIDv2 (1.2.840.113549.1.9.16.2.47, default SHA-256) matching the signer cert; signature over signed attrs retagged `0x31`; RSA PKCS#1 v1.5 or ECDSA with SHA-256/384/512 only, else `unsupported algorithm <oid>`; signer EKU exactly `[timeStamping]` and EKU extension critical; chain with `x509.VerifyOptions{Roots, Intermediates, KeyUsages: [ExtKeyUsageTimeStamping], CurrentTime: genTime}`; TSTInfo version 1 and SHA-256 imprint of 32 bytes. Revocation not checked.
- Storage: `<datadir>/audit/timestamps/<genTime UTC 20060102T150405Z>-<first 12 hex of head>.tsr`, token DER only (the ContentInfo, not the TimeStampResp), file 0600, dir 0700, `O_EXCL`. Nothing is written into the chain while timestamping.
- Root selection: `--tsa-ca` > policy `ca` > `ca_file` > system roots (nil pool).
- Clock warning tolerance: 5 minutes.
- Exit codes: `audit timestamp` 0 done or already anchored, 1 otherwise; `audit verify` 0 all valid, 1 chain broken or token invalid/orphan; `audit export` 2 when the chain is broken or a token is invalid/orphan (report written). End-of-session failure never changes the exit code.
- Messages (exact, EN; FR equivalents in the i18n catalog except config errors, which stay EN like existing config errors):
  - `config: audit.timestamp.url must be https (http only for local hosts)`
  - `config: audit.timestamp.ca_file: <reason>` / `config: audit.timestamp.ca_file without url`
  - `policy <id>: audit timestamp imposed (<url>)` (notice)
  - `profile strict-local: audit timestamp host "<h>" is not local` (exit 1)
  - `audit timestamp: <reason>` (end-of-session notice, stderr)
  - `audit timestamp: <file> (<genTime RFC 3339>, <TSA>)` / `audit timestamp: already anchored` / `audit timestamp: not configured ([audit.timestamp] url)` / `audit timestamp: audit log is empty`
  - `timestamp <file>: anchored hash not in chain`
  - `timestamp <file>: <file>:<line> ts after genTime (local clock?)` (warning)
  - `timestamps: <n> valid, last <file>:<line> at <genTime RFC 3339> (<TSA>), <m> lines after` / `timestamps: none`
  - `audit.timestamp: <url> (roots: <ca_file|system|policy>)` (`config check`)
- Without `[audit.timestamp]` (config and policy): no network call, no new output except `timestamps: none` in `audit verify`, all existing tests pass untouched (except `audit verify` output assertions that gain that line).
- New user-visible strings through `internal/i18n` (EN and FR). Code comments in French.
- Every task: `gofmt -l cmd internal` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- TSA URL that answers HTTP 200 with an HTML page (wrong URL, captive portal): clean error `audit timestamp: ...invalid response...`, no file written, session exit unchanged — tests in Task 2 and Task 5.
- The whole audit log deleted while tokens remain: every token is orphan, `audit verify` exits 1 — test in Task 3.
- Junk in `audit/timestamps/` (a `README`, a truncated `.tsr`): non-`.tsr` files ignored, a truncated `.tsr` is an invalid-token error naming the file — test in Task 3.
- Two processes anchoring the same head in the same second: second `SaveToken` finds the file, returns its path without overwriting, no error — test in Task 3.
- Policy imposing a remote TSA while the effective profile is `strict-local`: startup refused, exit 1 (admin conflict, fail closed) — test in Task 5.

---

### Task 1: `internal/tsp` — token parsing and verification

**Files:**
- Create: `internal/tsp/asn1.go` (ASN.1 structures and OIDs), `internal/tsp/verify.go`, `internal/tsp/verify_test.go`
- Create: `internal/tsp/tsptest/tsptest.go` (token builder + fake TSA server)
- Create: `internal/tsp/testdata/gen.sh`, `internal/tsp/testdata/{rsa,ec}.tsr`, `{rsa,ec}-ca.pem`, `digest.hex`

**Interfaces:**
- Produces:
  - `type Info struct{ GenTime time.Time; Digest []byte; Serial *big.Int; Nonce *big.Int; TSA string }` — `TSA` = signer certificate Subject CommonName (Subject string if CN empty); `Nonce` nil if absent.
  - `func ParseToken(token []byte) (Info, error)` — structural decode only, no signature or chain check (used by `audit.Anchored`).
  - `func Verify(token []byte, roots *x509.CertPool) (Info, error)` — every check of the Global Constraints; `roots == nil` = system roots.
  - `var ErrToken = errors.New("invalid timestamp token")` — every Verify/ParseToken error wraps it.
  - `tsptest`:
    - `type Options struct{ Status int; GenTime time.Time; SignerNotBefore, SignerNotAfter time.Time; Tamper Tamper; RSA bool }` (zero values = valid ECDSA P-256 signer, genTime now, validity now±1y).
    - `type Tamper int` with `None, Signature, Imprint, MessageDigest, NoESSCertID, WrongESSCertID, NoEKU, NonCriticalEKU, SHA1, PSS, Nonce, HTMLBody`.
    - `type CA struct{ Pool *x509.CertPool; PEM []byte }`; `func NewCA(t testing.TB) *CA`.
    - `func (ca *CA) Token(t testing.TB, digest []byte, nonce *big.Int, o Options) []byte` (ContentInfo DER) and `func (ca *CA) Response(t testing.TB, digest []byte, nonce *big.Int, o Options) []byte` (TimeStampResp DER).
    - `type Server struct{ URL string; CA *CA; Requests atomic.Int64 }`; `func NewServer(t testing.TB, o Options) *Server` — `httptest` server decoding the request (via an exported `tsp` helper from Task 2 is not available yet: decode with `encoding/asn1` locally) and answering `Response`; `Tamper: Nonce` answers another nonce, `HTMLBody` answers `200 text/html`.

- [ ] **Step 1: Generate the openssl vectors.** `gen.sh` (bash, `set -euo pipefail`, run from `testdata/`, documented in a header comment, NOT run by tests): self-signed CA (RSA 2048 for `rsa-ca.pem`, P-256 for `ec-ca.pem`, 3650 days), TSA signer cert with `extendedKeyUsage = critical,timeStamping`, an `openssl.cnf` `[tsa]` section (`digests = sha256`, `ess_cert_id_alg = sha256`, `signer_digest = sha256`), query `openssl ts -query -digest $(cat digest.hex) -sha256 -cert`, reply `openssl ts -reply -token_out -out {rsa,ec}.tsr`, then `openssl ts -verify -token_in -digest … -CAfile …` must succeed. `digest.hex` = 64 hex chars (fixed value, e.g. sha256 of `tenon`). Private keys are deleted at the end of the script; commit only `.tsr`, `-ca.pem`, `digest.hex`, `gen.sh`.
- [ ] **Step 2: Failing tests** in `verify_test.go`:
  - `TestVerifyOpenSSLVectors`: both `rsa.tsr`/`ec.tsr` verify with their CA; `Info.Digest` equals `digest.hex` decoded; `GenTime` non-zero; `TSA` non-empty; with the other vector's CA → error wrapping `ErrToken`.
  - `TestVerifyTampered`: table over every `tsptest.Tamper` except `Nonce`/`HTMLBody` → error wrapping `ErrToken`; `SHA1` and `PSS` errors contain `unsupported algorithm`.
  - `TestVerifyExpiredAfterGenTime`: signer validity `[now-2y, now-1y]`, genTime `now-18 months` → valid.
  - `TestVerifyGenTimeOutsideValidity`: genTime after `SignerNotAfter` → error.
  - `TestVerifyGarbage`: `nil`, `[]byte("x")`, a truncated `rsa.tsr` → `ErrToken`, no panic.
  - `TestParseTokenNoVerify`: tampered signature token still parses, digest returned.
- [ ] **Step 3: Run** `GOFLAGS=-mod=vendor go test ./internal/tsp/...` — FAIL (undefined).
- [ ] **Step 4: Implement** `asn1.go`, `verify.go`, `tsptest`. Notes: `SignedData.certificates` is `[0] IMPLICIT SET OF` → parse as `asn1.RawValue` and `x509.ParseCertificates` on its content bytes; `signedAttrs` `[0] IMPLICIT` → keep `FullBytes`, replace first byte with `0x31` before hashing; `eContent` is `[0] EXPLICIT OCTET STRING`; TSTInfo optional fields (`accuracy`, `ordering`, `nonce`, `tsa [0]`, `extensions [1]`) tagged `optional`. Signer SID: SEQUENCE → issuer+serial (compare `RawIssuer` bytes and serial), `[0]` → SKI. RSA: `rsa.VerifyPKCS1v15`; ECDSA: `ecdsa.VerifyASN1`. Accept signature algorithm OIDs rsaEncryption and sha{256,384,512}WithRSAEncryption, ecdsa-with-SHA{256,384,512}.
- [ ] **Step 5: Run** — PASS; full gates.
- [ ] **Step 6: Commit** `feat(tsp): RFC 3161 token parsing and verification (stdlib)`

---

### Task 2: `internal/tsp` — request

**Files:**
- Create: `internal/tsp/request.go`, `internal/tsp/request_test.go`

**Interfaces:**
- Consumes: Task 1 (`Verify`, `Info`, `ErrToken`, `tsptest`).
- Produces:
  - `func NewRequest(digest []byte) (der []byte, nonce *big.Int, err error)` — `len(digest) != 32` → error.
  - `func ParseResponse(der []byte) (status int, token []byte, err error)`.
  - `func Request(ctx context.Context, c *http.Client, url string, digest []byte, roots *x509.CertPool) (token []byte, info Info, err error)` — POST, status/nonce/imprint/Verify checks; errors: `HTTP <code>`, `invalid response: <detail>` (non-DER body), `rejected (status <n>)`, `nonce mismatch`, `imprint mismatch`, or the `Verify` error.
  - `func CheckURL(raw string) error` — `https` any host, `http` only if `profile.IsLocalHost(host)`; else returns the exact config message of the Global Constraints (callers do not re-wrap); empty host or other scheme → same message.
  - `func Host(raw string) string` — lowercase hostname.

- [ ] **Step 1: Failing tests:**
  - `TestNewRequestRoundTrip`: decode `der` with `encoding/asn1`, version 1, imprint = digest, `certReq` true, nonce equal and ≤ 64 bits; two calls give different nonces; 31-byte digest → error.
  - `TestRequestOK`: `tsptest.NewServer` → token verifies, `info.Digest` = digest, server got `Content-Type: application/timestamp-query`.
  - `TestRequestErrors`: status 2 → `rejected (status 2)`; `Tamper: Nonce` → `nonce mismatch`; `Tamper: HTMLBody` → contains `invalid response` (Review Focus 1); HTTP 500 → `HTTP 500`; wrong roots → `ErrToken`; body > 1 MiB → error, no hang.
  - `TestRequestTimeout`: context deadline from caller honoured (server sleeps longer than a 100 ms context).
  - `TestCheckURL`: `https://tsa.example.eu/tsr` ok; `http://127.0.0.1:8318/` ok; `http://localhost/` ok; `http://tsa.example.eu/` → exact message; `ftp://x` → message; `""` → message.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** The 10 s timeout belongs to callers (`context.WithTimeout`), `Request` reads with `io.LimitReader(body, 1<<20+1)` and fails if more.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(tsp): RFC 3161 request with nonce and response checks`

---

### Task 3: `internal/audit` — head, token storage, verification

**Files:**
- Create: `internal/audit/timestamp.go`, `internal/audit/timestamp_test.go`

**Interfaces:**
- Consumes: `tsp.ParseToken`, `tsp.Verify`, `tsptest.CA.Token`.
- Produces:
  - `func Head(dir string) (string, error)` — last chain hash via `readTail`; `""` when the log is empty or missing.
  - `func Digest(head string) ([]byte, error)` — `sha256:<64 hex>` → 32 bytes; anything else → error.
  - `const TimestampDir = "timestamps"`.
  - `func Anchored(dir, head string) (bool, error)` — true if some `*.tsr` whose name ends with `-<12 hex>.tsr` parses (`ParseToken`) with `Digest == Digest(head)`; missing dir → false.
  - `func SaveToken(dir string, info tsp.Info, token []byte) (string, error)` — creates the dir 0700, file 0600 with `O_EXCL`; if the name exists, returns its path and nil without writing (Review Focus 4).
  - `type TimestampSummary struct{ Count, Valid int; LastFile string; LastLine int; LastTime time.Time; TSA string; LinesAfter int; Errors, Warnings []string }` (`Errors`/`Warnings` never nil).
  - `func VerifyTimestamps(dir string, roots *x509.CertPool) (TimestampSummary, error)` — returns an error only when the chain itself is broken or unreadable (same error text as `Verify`); walks the chain recording, per line, hash → (file, line number, global index, ts) and a prefix max of ts; then each `*.tsr` sorted by name: `Count++`; `Verify` failure → `Errors += "timestamp <file>: <err>"`; digest not in chain → `Errors += "timestamp <file>: anchored hash not in chain"`; else `Valid++` and clock warning when prefix-max ts up to the anchor > genTime + 5 min (message names the line holding that max). `Last*` = valid token with the highest global anchor index (ties: latest genTime); `LinesAfter` = total lines − (that index + 1); no valid token → `LinesAfter` = total.

- [ ] **Step 1: Failing tests** (helpers: write events with `Open`/`Write` into `t.TempDir()`, tokens from `tsptest.NewCA(t).Token`):
  - `TestHeadEmptyAndFilled`; `TestDigest` (valid, `sha256:0`, bad hex, wrong length).
  - `TestSaveTokenAndAnchored`: name format `20261021T153000Z-<12hex>.tsr` from `GenTime` 2026-10-21 15:30:00 UTC; mode 0600, dir 0700; `Anchored` true for that head, false for another; second `SaveToken` same info → same path, nil, content unchanged.
  - `TestVerifyTimestampsValid`: 3 lines, token on line 2 → `Valid 1`, `LastLine 2`, `LinesAfter 1`, no errors.
  - `TestVerifyTimestampsRewrittenChain`: anchor line 2, then rewrite every line with a changed payload and recomputed hashes (helper using `computeHash`) → chain verifies but `Errors` has `anchored hash not in chain`.
  - `TestVerifyTimestampsLogDeleted`: tokens present, no `*.jsonl` → orphan error (Review Focus 2).
  - `TestVerifyTimestampsJunk`: `README` ignored; truncated `.tsr` → one error naming the file (Review Focus 3).
  - `TestVerifyTimestampsClockWarning`: line ts 10 min after the token genTime → one warning with the exact format; 4 min → none.
  - `TestVerifyTimestampsBrokenChain` → error returned.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(audit): store and verify RFC 3161 tokens against the chain`

---

### Task 4: config and policy — `[audit.timestamp]`

**Files:**
- Modify: `internal/config/config.go`, `internal/config/policy.go`, `internal/orgpolicy/policy.go`, `docs/config.example.toml`
- Test: `internal/config/config_test.go` (or new `timestamp_test.go`), `internal/config/policy_test.go`, `internal/orgpolicy/policy_test.go`

**Interfaces:**
- Consumes: `tsp.CheckURL`.
- Produces:
  - `type AuditTimestamp struct{ URL string \`toml:"url"\`; CAFile string \`toml:"ca_file"\`; CAPEM []byte \`toml:"-"\` }`; `Audit.Timestamp *AuditTimestamp \`toml:"timestamp"\``. Global only: `projectFile` has no `audit` key, so the existing `project config: key not allowed` error covers it (test only).
  - Validation in `validate()`: `CheckURL` error returned as is; `ca_file` without url → `config: audit.timestamp.ca_file without url`; `ca_file` unreadable or containing no PEM certificate → `config: audit.timestamp.ca_file: <reason>` (`<reason>` = the read error, or `no certificate`).
  - `func (c Config) TimestampRoots() (*x509.CertPool, string, error)` — source `policy` (CAPEM set), `ca_file`, or `system` (nil pool, also when nothing is configured); error only when the PEM or `ca_file` cannot be read or parsed.
  - `orgpolicy`: `raw.Audit.Timestamp *struct{ URL string \`toml:"url"\`; CA string \`toml:"ca"\` }`; `Policy.Timestamp *AuditTimestamp` with `type AuditTimestamp struct{ URL string; CA []byte }`; validation: `tsp.CheckURL` (wrapped in `ErrPolicy`), `ca` present but with no parsable PEM certificate → `policyErr("audit.timestamp.ca: no certificate")`; url empty → `policyErr("audit.timestamp.url missing")`.
  - `ApplyPolicy`: when `pol.Timestamp != nil`, `cfg.Audit.Timestamp = &AuditTimestamp{URL: pol.Timestamp.URL, CAPEM: pol.Timestamp.CA}`; `cfg.PolicyTimestampImposed = true` when the user's URL differed (absent counts as different) — field `PolicyTimestampImposed bool \`toml:"-"\``.

- [ ] **Step 1: Failing tests:** `TestAuditTimestampValid` (https + ca_file with a PEM from `tsptest.NewCA`); `TestAuditTimestampHTTPRemote` (exact message); `TestAuditTimestampHTTPLocal` (`http://127.0.0.1:8318` ok); `TestAuditTimestampCAFileMissing`, `…NoCert` (file with garbage), `…WithoutURL` (exact messages); `TestAuditTimestampProjectRefused`; `TestTimestampRoots` (three sources); orgpolicy `TestParseRejects` adds http remote url, bad `ca`, missing url; `TestApplyPolicyTimestamp` (user url A + ca_file → policy url B with CA: user values gone, `CAPEM` set, `PolicyTimestampImposed`; same url → not imposed).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**; add a commented `[audit.timestamp]` block to `docs/config.example.toml` (kept valid: `example_test.go` loads it).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(config): [audit.timestamp] in config and organisation policy`

---

### Task 5: CLI wiring

**Files:**
- Modify: `cmd/tenon/run.go`, `internal/i18n/catalog.go`
- Create: `cmd/tenon/timestamp.go` (anchor helper, `audit timestamp`, verify output), `cmd/tenon/timestamp_e2e_test.go`

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces:
  - `func timestampClient(p profile.Name) *http.Client` — `Transport` cloned from `http.DefaultTransport` with `Proxy: nil` under `strict-local`, `http.ProxyFromEnvironment` otherwise.
  - `func anchor(ctx context.Context, cfg config.Config, p profile.Name, dataDir string) (path string, info tsp.Info, already bool, err error)` — `Head` (empty → error `audit log is empty`), `Anchored`, `tsp.Request` with `context.WithTimeout(ctx, 10*time.Second)` and `cfg.TimestampRoots()`, `SaveToken`.
  - `func timestampProfileErr(cfg config.Config, p profile.Name) error` — `strict-local` and `!profile.IsLocalHost(tsp.Host(url))` → `err.profile_timestamp_host`.
  - `startEngine`: `timestampProfileErr` after the provider checks (exit 1); notice `policy.timestamp_imposed` when `PolicyTimestampImposed`; the `close` func, after closing the audit writer, calls `anchor` when `cfg.Audit.Timestamp != nil` and prints `audit.timestamp_failed` (redacted) on error, nothing on success or `already`.
  - `tenon audit timestamp` (dispatch in `run`): `loadSetup`, `timestampProfileErr`, `anchor`; outputs per Global Constraints.
  - `tenon audit verify [--tsa-ca <pem>]`: after the chain line, `audit.VerifyTimestamps`; roots: `--tsa-ca` file (unreadable/no cert → exit 1), else when `audit/timestamps` holds a `.tsr`, `loadSetup` + `TimestampRoots` (a load error prints `err.prefix` as a notice and falls back to system roots), else nil; prints `Errors` and `Warnings` lines to stderr, the summary line to stdout; exit 1 when `Errors` non-empty.
  - `config check`: line `config.timestamp` when configured; `timestampProfileErr` refusal.
  - i18n keys (EN/FR): `err.profile_timestamp_host`, `policy.timestamp_imposed`, `audit.timestamp_failed`, `audit.timestamp_ok`, `audit.timestamp_already`, `audit.timestamp_unconfigured`, `audit.timestamp_empty`, `audit.timestamps_summary`, `audit.timestamps_none`, `config.timestamp`, `flag.tsa_ca`.

- [ ] **Step 1: Failing e2e tests** (fake TSA: `tsptest.NewServer`, its `CA.PEM` written to a temp `ca_file`; existing e2e helpers for config, fake provider and data dir):
  - `TestTimestampEndOfSessionE2E`: `-p salut` exit 0, one `.tsr` in `<data>/audit/timestamps`, `audit verify` exit 0 with `timestamps: 1 valid` and `0 lines after`.
  - `TestTimestampRewriteDetectedE2E`: after the session, rewrite the chain with recomputed hashes (helper shared with Task 3 is internal to `audit`: re-implement with `crypto/sha256` in the test) → `audit verify` exit 1, stderr `anchored hash not in chain`.
  - `TestTimestampTSADownE2E`: url `http://127.0.0.1:<closed port>/` → exit 0, stderr `audit timestamp:`, no `.tsr`; same with `Tamper: HTMLBody` (Review Focus 1).
  - `TestTimestampStrictLocalRemoteE2E`: `profile = "strict-local"`, url `https://tsa.example.eu/tsr` → exit 1, exact message, zero provider requests.
  - `TestTimestampPolicyImposedE2E`: signed policy (`orgpolicytest.Sign`) with `[audit.timestamp] url` = fake TSA and `ca` = its PEM; user config without timestamp → exit 0, notice `audit timestamp imposed`, one `.tsr`.
  - `TestTimestampPolicyStrictLocalE2E`: policy `profile = "strict-local"` + remote TSA url → exit 1 (Review Focus 5).
  - `TestAuditTimestampCommandE2E`: after a session with timestamp disabled then enabled in config: first `audit timestamp` exit 0 `audit timestamp: <file>`, second exit 0 `already anchored`; unconfigured → exit 1; empty log → exit 1.
  - `TestAuditVerifyTSACAE2E`: `--tsa-ca` with another CA → exit 1; with the right CA → exit 0.
  - `TestAuditVerifyNoTokensE2E`: no timestamp configured → output contains `timestamps: none`, exit 0.
  - `TestConfigCheckTimestampE2E`: `audit.timestamp: <url> (roots: ca_file)`.
- [ ] **Step 2: Run** `go test ./cmd/tenon/ -run 'Timestamp'` — FAIL.
- [ ] **Step 3: Implement**; update existing `audit verify` output assertions for the extra `timestamps: none` line.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): RFC 3161 anchoring at session end, audit timestamp, verify --tsa-ca`

---

### Task 6: compliance export and docs

**Files:**
- Modify: `internal/compliance/load.go`, `report.go`, `build.go`, `report.html.tmpl`, `testdata/report.golden.json`, `cmd/tenon/audit_export.go`, `internal/i18n/catalog.go`
- Modify docs: `docs/security.md`, `docs/fr/securite.md` (threat model, what a token proves, revocation not checked, `openssl ts -verify` recipe), `docs/usage.md`, `docs/fr/utilisation.md` (config keys, commands, `--tsa-ca`, export schema), `docs/policy.md`, `docs/fr/politique.md` (`[audit.timestamp]`), `README.md` (feature line)
- Test: `internal/compliance/load_test.go`, `build_test.go`, `html_test.go`, `cmd/tenon/audit_export_e2e_test.go`

**Interfaces:**
- Consumes: `audit.VerifyTimestamps`, `audit.TimestampSummary`, `tsptest`.
- Produces:
  - `compliance.Load(dir string, f Filter, roots *x509.CertPool) (Dataset, error)`; `Dataset.Timestamps audit.TimestampSummary` (computed only when the chain is intact).
  - `type Timestamps struct{ Count int \`json:"count"\`; Valid int \`json:"valid"\`; LastFile string \`json:"last_file,omitempty"\`; LastLine int \`json:"last_line,omitempty"\`; LastTime string \`json:"last_time,omitempty"\` (RFC 3339); TSA string \`json:"tsa,omitempty"\`; LinesAfter int \`json:"lines_after"\`; Errors []string \`json:"errors"\` }` (never nil); `Integrity.Timestamps Timestamps \`json:"timestamps"\``.
  - HTML row `report.timestamps` (`Horodatage` / `Timestamps`): `aucun` / `none`, or `<valid>/<count> valides, dernier <file>:<line> le <time> (<tsa>), <m> lignes après` and the errors.
  - `audit export --tsa-ca <pem>`; roots as in `audit verify`; exit 2 when `!Integrity.Intact || len(Integrity.Timestamps.Errors) > 0`, with `audit.export.timestamps_invalid` on stderr.

- [ ] **Step 1: Failing tests:** `TestLoadTimestamps` (valid token → `Valid 1`; orphan → error listed); `TestBuildTimestamps` (fields copied, `errors: []` when none); HTML FR/EN row; golden regenerated (`"timestamps": {"count": 0, "valid": 0, "lines_after": N, "errors": []}`) and reviewed; e2e `TestExportTimestampOrphanE2E` → exit 2, report written.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement**; write the docs (spec §1, §3–§6; messages quoted from the catalog; say plainly that tenon ships no TSA preset and does not check revocation).
- [ ] **Step 4: Run** — PASS; full gates; `make ci` with `PATH=/opt/homebrew/opt/go@1.26/bin:$PATH` (known: GO-2026-6809 fails govulncheck on go1.26.9 — report, do not fix here).
- [ ] **Step 5: Commit** `feat(compliance): timestamps in the export; docs: RFC 3161 timestamping (EN/FR)`
