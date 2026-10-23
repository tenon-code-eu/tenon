# tenon Cyber Resilience Act assistant — Implementation Plan (plan 18)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `tenon cra` keeps a verifiable CRA dossier in the repository (product, SBOM, VEX, reporting deadlines), matches SBOM components against an offline OSV database, exports a technical dossier, and ships four built-in `cra-*` skills.

**Architecture:** pure packages under `internal/cra/` (`version`, `sbom`, `osv`, `dossier`, `scan`, `report`), a `cmd/tenon/cra.go` command tree with exit codes and journal events, a CRA section in the compliance export, and built-in skills embedded in the binary with lowest priority in the skills catalog.

**Tech Stack:** Go 1.26 (vendored, `GOFLAGS=-mod=vendor`), standard library only (`archive/zip`, `encoding/json`, `embed`, `net/http`), BurntSushi/toml (already vendored).

**Spec:** `docs/superpowers/specs/2026-10-23-tenon-cra-design.md`

## Global Constraints

- No new module.
- Dossier layout: `cra/product.toml`, `cra/sbom*.cdx.json` (CycloneDX JSON 1.4–1.6), `cra/vex.cdx.json`, `cra/reports/<id>.toml` (`id` = `AAAA-MM-JJ-<n>`), optional `cra/docs/*.md`.
- `product.toml` required fields: `name`, `version`, `manufacturer`, `security_contact`, `support_end`, `category`; `category` ∈ `default`, `important-1`, `important-2`, `critical`; optional `sboms` (default glob `cra/sbom*.cdx.json`).
- VEX states: `not_affected` (requires `analysis.justification`), `affected`, `fixed`, `under_investigation`, `in_triage` (= `under_investigation`).
- A vulnerability is `open` when it has no VEX entry or is `affected`/`under_investigation`; `to_verify` when its ecosystem has no comparator and the version is not in OSV `versions`.
- Deadlines: early warning `aware_at + 24h`; notification `aware_at + 72h`; final `fix_available_at + 14 days` (vulnerability; unknown while absent) or `notification_at + 1 calendar month` (incident).
- OSV base URL default `https://storage.googleapis.com/osv-vulnerabilities`, archive `<base>/<ECO>/all.zip`; https only (http only to a local host); redirects https only; 1 GiB cap per archive; local store `<data>/cra/osv/<ECO>/all.zip` + `<data>/cra/osv/index.json` `{eco: {date, sha256, entries}}`; atomic replace; DB older than 7 days → notice.
- purl type → OSV ecosystem: `golang`→`Go`, `npm`→`npm`, `cargo`→`crates.io`, `pypi`→`PyPI`, `maven`→`Maven`, `nuget`→`NuGet`, `gem`→`RubyGems`, `composer`→`Packagist`. Comparators: semver (Go incl. `v` prefix and pseudo-versions as pre-releases, npm, crates.io), PEP 440 (epoch, release, pre/post/dev; no local). Others: explicit `versions` only.
- Exit codes: `scan` 0 / 7 (`--fail-on open` and ≥1 open) / 1 (error, no DB); `status` 0 / 7 (a deadline overdue) / 1 (invalid dossier); other commands 0 / 1.
- Exact messages:
  - `cra: product.toml: <field> required`
  - `cra: product.toml: category must be default, important-1, important-2 or critical`
  - `cra: vex: <id>: not_affected requires a justification`
  - `cra: no vulnerability database (run tenon cra db update)`
  - notice `cra: vulnerability database is <n> days old`
  - `cra: profile strict-local requires a local osv mirror`
  - `config check`: `cra: dossier <ok|absent|invalid>, osv db <date|absent>`
- Config: `[cra] osv_mirror = "<url>"` (global only; refused in project config with `config: cra not allowed in project configuration`).
- Network: only `db update`; under `strict-local` the mirror must be a local host and env proxies are bypassed; elsewhere env proxies are respected. The org policy's provider allowlist does not apply (OSV is not a provider) — plan clarification of spec §4.
- Events: `CRAScan {sboms, components, open, to_verify, not_affected, fixed, db_date}`, `CRAReport {id, kind, step, at}` with `step` ∈ `open`, `early-warning`, `notification`, `final`, `fix-available`; written by `tenon cra scan|report …` in a short session (`SessionStarted` with new `kind: "cra"`, then the CRA event).
- Built-in skills `cra-sbom`, `cra-triage`, `cra-report`, `cra-docs`: source `builtin`, overridden silently by a user or project skill of the same name.
- Code comments French; user strings via `internal/i18n` (EN/FR) except `cra:`/`config:` errors (EN); docs EN+FR (`docs/cra.md`, `docs/fr/cra.md`, usage).
- Every task: `gofmt -l cmd internal scripts` empty, `GOFLAGS=-mod=vendor go vet ./...`, `GOOS=linux GOFLAGS=-mod=vendor go vet ./...`, `GOFLAGS=-mod=vendor go test -race ./...`, `make lint` clean.

## Review Focus

- A CycloneDX SBOM from `syft` (components nested under `components[].components`, purls with qualifiers `?type=jar`, subpaths) — nested components are collected and qualifiers/subpaths ignored for matching — test in Task 2.
- An OSV entry with several `affected[]` for the same package (different ranges per ecosystem variant, e.g. Go stdlib vs module) and an entry affecting several packages — each package matched on its own ranges, finding reported once per (vuln, component) — test in Task 3.
- A truncated or corrupt `all.zip` from a flaky mirror — `db update` fails without replacing the previous archive; `scan` with a corrupt archive reports the ecosystem as unreadable (error, code 1), not "no vulnerabilities" — test in Task 4.
- Timezones: `aware_at` written with an offset (`+02:00`) and `--aware-at` without zone — RFC 3339 required (zone mandatory), deadlines computed in UTC; DST change in between does not shift the 24 h/72 h deadlines — test in Task 5.
- A dossier with `support_end` in the past — `status` reports it as incomplete (support period over) but exit 1 is not used for this (it is a status line, exit 0 unless a deadline is overdue) — test in Task 5.

---

### Task 1: version comparators

**Files:**
- Create: `internal/cra/version/semver.go`, `internal/cra/version/pep440.go`, `internal/cra/version/version.go`
- Test: `internal/cra/version/semver_test.go`, `internal/cra/version/pep440_test.go`

**Interfaces:**
- Produces:
  - `type Comparator interface { Compare(a, b string) (int, error) }`
  - `var Semver Comparator` (accepts optional `v` prefix; Go pseudo-versions `vX.Y.Z-0.yyyymmddhhmmss-abcdefabcdef` and `vX.Y.(Z+1)-0.…` ordered as pre-releases per semver; build metadata ignored)
  - `var PEP440 Comparator` (epoch `N!`, release segments with trailing zeros equal, `a/b/rc` pre, `.postN`, `.devN`, normalisation of `alpha`/`beta`/`c`/`pre`/`preview`, `-`/`_`/`.` separators; local `+…` rejected with an error)
  - `func For(ecosystem string) (Comparator, bool)` — `Go`, `npm`, `crates.io` → Semver; `PyPI` → PEP440; others → false.

- [ ] **Step 1: Failing tests:** `TestSemverOrder` table: `1.0.0-alpha < 1.0.0-alpha.1 < 1.0.0-alpha.beta < 1.0.0-beta < 1.0.0-beta.2 < 1.0.0-beta.11 < 1.0.0-rc.1 < 1.0.0`; `v1.2.3 == 1.2.3`; `1.0.0+build == 1.0.0`; `v0.0.0-20210101000000-abcdefabcdef < v0.0.1`; `1.2` is accepted as `1.2.0`; `abc` → error. `TestPEP440Order`: `1.0.dev1 < 1.0a1 < 1.0a2.dev1 < 1.0a2 < 1.0b1 < 1.0rc1 < 1.0 == 1.0.0 < 1.0.post1 < 1.1`; `1!0.5 > 2.0`; `1.0alpha1 == 1.0a1`; `1.0+local` → error. `TestFor`.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/cra/version/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cra): semver and PEP 440 version comparators`

---

### Task 2: SBOM reader and purl

**Files:**
- Create: `internal/cra/sbom/sbom.go`, `internal/cra/sbom/purl.go`, `internal/cra/sbom/testdata/*.cdx.json`
- Test: `internal/cra/sbom/sbom_test.go`

**Interfaces:**
- Produces:
  - `type Component struct { Name, Version, PURL, BOMRef string }`
  - `type BOM struct { SpecVersion, Generator string; Timestamp time.Time; Components []Component }`
  - `func Read(path string) (BOM, error)` — JSON only; `bomFormat` must be `CycloneDX`, `specVersion` 1.4–1.6 (else `sbom: <path>: unsupported CycloneDX version <v>`); nested `components[].components` flattened; components without purl kept with empty PURL (reported by `status`).
  - `type PURL struct { Type, Namespace, Name, Version string }`, `func ParsePURL(s string) (PURL, error)` — percent-decoding, qualifiers and subpath dropped.
  - `func (p PURL) Ecosystem() (string, bool)` and `func (p PURL) OSVName() string` (Go: `namespace/name` joined as module path; npm: `@ns/name` when namespace; Maven: `group:artifact`; PyPI: PEP 503 normalised lowercase with runs of `-_.` → `-`).

- [ ] **Step 1: Failing tests:** `TestReadCycloneDX` (1.5 file from `cyclonedx-gomod` shape: metadata.tools generator, timestamp, 3 components); `TestReadNested` (syft shape with nested components and `?type=jar` qualifiers — Review Focus 1); `TestReadRejects` (1.3 version, SPDX file, invalid JSON); `TestParsePURL` table (`pkg:golang/github.com/foo/bar@v1.2.3`, `pkg:npm/%40scope/pkg@1.0.0`, `pkg:maven/org.apache/commons-text@1.9?type=jar`, `pkg:pypi/Django_REST.framework@3.0` → OSVName `django-rest-framework`, `pkg:cargo/serde@1.0.0`); `TestEcosystem` mapping per Global Constraints.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/cra/sbom/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cra): CycloneDX SBOM reader and purl parsing`

---

### Task 3: OSV database and matching

**Files:**
- Create: `internal/cra/osv/db.go`, `internal/cra/osv/match.go`
- Test: `internal/cra/osv/db_test.go` (archives built in memory with `archive/zip`)

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces:
  - `type DB struct` ; `func Open(dir string) (*DB, error)` — reads `index.json`; archives opened lazily per ecosystem; an unreadable/corrupt archive → error naming the ecosystem.
  - `func (db *DB) Date(eco string) (time.Time, bool)`; `func (db *DB) OldestDate() (time.Time, bool)`.
  - `type Finding struct { ID string; Aliases []string; Summary string; Component sbom.Component; State string }` with `State` ∈ `affected`, `to_verify`.
  - `func (db *DB) Match(c sbom.Component) ([]Finding, error)` — finds entries whose `affected[].package` (`ecosystem` exact, `name` normalised as `OSVName`) matches; evaluates each `affected` independently: `versions` list membership, then `ranges` of type `SEMVER` or `ECOSYSTEM` with the comparator from `version.For`; ranges of type `GIT` ignored; no comparator and not listed → `to_verify`; entries with `withdrawn` skipped; one finding per (ID, component).

- [ ] **Step 1: Failing tests:** `TestMatchSemverRange` (`introduced:0, fixed:1.2.0`: 1.1.9 affected, 1.2.0 not); `TestMatchLastAffected`; `TestMatchVersionsList`; `TestMatchMultipleAffected` (Review Focus 2: two `affected` for the same package with different ranges, and one entry affecting two packages → findings per component, deduplicated); `TestMatchWithdrawn`; `TestMatchToVerify` (Maven component, version not listed); `TestMatchPyPINormalised`; `TestOpenCorrupt` (garbage zip → error naming the ecosystem).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/cra/osv/` — FAIL.
- [ ] **Step 3: Implement.** Index per ecosystem: `map[osvName][]entry` built on first use, decoding only the fields needed.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cra): offline OSV database and component matching`

---

### Task 4: OSV download (`db update`) and `[cra]` config

**Files:**
- Create: `internal/cra/osv/update.go`
- Modify: `internal/config/config.go`, `internal/config/load.go`, `docs/config.example.toml`
- Test: `internal/cra/osv/update_test.go`, `internal/config/*_test.go`

**Interfaces:**
- Produces:
  - `const DefaultBase = "https://storage.googleapis.com/osv-vulnerabilities"`, `const MaxArchive = 1 << 30`
  - `func Update(ctx context.Context, c *http.Client, base string, ecos []string, dir string, now func() time.Time) error` — per ecosystem: GET `<base>/<eco>/all.zip`, stream to a temp file in `dir` with the size cap, verify it opens as a zip with at least one `.json` entry, compute sha256 and entry count, `rename` over `<dir>/<eco>/all.zip`, then rewrite `index.json` atomically; any failure leaves the previous archive and index untouched.
  - `func CheckBase(base string) error` — https, or http to a local host (reuse `profile.IsLocalHost`); else `cra: osv mirror must be https (http only for local hosts)`.
  - `func Client(noProxy bool) *http.Client` — `CheckRedirect` refuses non-https redirects (and any redirect off a local host when the base is http).
  - `config.CRA struct { OSVMirror string \`toml:"osv_mirror"\` }`, `Config.CRA CRA \`toml:"cra"\``; project config refuses `[cra]`.

- [ ] **Step 1: Failing tests:** `TestUpdateOK` (httptest TLS server serving two ecosystems → archives and index with sha256/entries/date from injected clock); `TestUpdateTruncated` (server closes mid-body / serves non-zip → error, previous archive and index unchanged — Review Focus 3); `TestUpdateTooLarge` (cap overridden in test); `TestUpdateRedirectHTTP` refused; `TestCheckBase` table; `TestCRAProjectRefused` (`config: cra not allowed in project configuration`); `TestConfigExampleLoads` with a commented `[cra]` block.
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/cra/osv/ ./internal/config/` — FAIL.
- [ ] **Step 3: Implement.** Tests use an unexported `maxArchive` variable to lower the cap.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cra): offline OSV database download with mirror support`

---

### Task 5: dossier, deadlines and scan

**Files:**
- Create: `internal/cra/dossier/product.go`, `internal/cra/dossier/vex.go`, `internal/cra/dossier/report.go`, `internal/cra/dossier/deadlines.go`, `internal/cra/scan/scan.go`
- Test: `internal/cra/dossier/*_test.go`, `internal/cra/scan/scan_test.go`

**Interfaces:**
- Consumes: Tasks 2–3.
- Produces:
  - `type Product struct { Name, Version, Manufacturer, SecurityContact, Category string; SupportEnd time.Time; SBOMs []string }`
  - `type VEXEntry struct { ID, Ref, State, Justification string }`, `func LoadVEX(path string) ([]VEXEntry, error)` (missing file → empty, no error)
  - `type Report struct { ID, Kind, Summary string; AwareAt, FixAvailableAt, EarlyWarningAt, NotificationAt, FinalAt *time.Time }`
  - `type Dossier struct { Root string; Product Product; VEX []VEXEntry; Reports []Report }`, `func Load(root string) (Dossier, error)` (errors per Global Constraints; `cra/` absent → `ErrNoDossier`)
  - `func NewReportID(root string, now time.Time) string` (`AAAA-MM-JJ-<n>`, n = next free), `func SaveReport(root string, r Report) error`
  - `type Deadline struct { Step string; Due *time.Time; Done *time.Time; Status string }` with `Status` ∈ `due`, `overdue`, `done`, `unknown`; `func Deadlines(r Report, now time.Time) []Deadline` (steps `early-warning`, `notification`, `final`; UTC arithmetic; 1 calendar month = `AddDate(0,1,0)` on the UTC date-time, clamping 31 Jan → last day of Feb)
  - `type Finding struct { osv.Finding; Status string }` with `Status` ∈ `open`, `not_affected`, `fixed`, `to_verify`; `type Result struct { SBOMs, Components int; Findings []Finding; Open, ToVerify, NotAffected, Fixed int; DBDate time.Time }`; `func scan.Run(d dossier.Dossier, boms []sbom.BOM, db *osv.DB) (Result, error)` — VEX entry matched by `ID` (or one of `Aliases`) and `Ref` equal to the component purl (version-insensitive purl comparison) or `BOMRef`.

- [ ] **Step 1: Failing tests:**
  - `TestLoadProduct` (each missing field → exact message; bad category → exact message; `sboms` default glob).
  - `TestLoadVEX` (`not_affected` without justification → exact message; `in_triage` mapped to `under_investigation`).
  - `TestDeadlinesVulnerability` (`aware_at` 2026-10-23T09:00:00+02:00 → early warning 2026-10-24T07:00:00Z, notification 2026-10-26T07:00:00Z; final unknown until `fix_available_at`, then +14 days); `TestDeadlinesIncident` (final = notification + 1 month; 2027-01-31 → 2027-02-28); `TestDeadlinesOverdueDone`; `TestDeadlinesDST` (Review Focus 4: aware_at just before the 2026-10-25 Europe/Paris DST change → +24h exact).
  - `TestNewReportID` (`2026-10-23-1`, then `-2`).
  - `TestScanVEX` (open without VEX; `not_affected` filtered; `affected` stays open; alias match; `to_verify` counted).
  - `TestSupportEndPast` (Review Focus 5: loading succeeds; a helper `d.Incomplete(now) []string` lists `support period ended on <date>`).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/cra/dossier/ ./internal/cra/scan/` — FAIL.
- [ ] **Step 3: Implement.** RFC 3339 with zone mandatory in TOML datetimes (offset datetime); a local datetime → `cra: reports/<id>.toml: <field> must include a time zone`.
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cra): dossier, reporting deadlines and VEX-aware scan`

---

### Task 6: `tenon cra` commands and journal

**Files:**
- Create: `cmd/tenon/cra.go`, `cmd/tenon/cra_e2e_test.go`
- Modify: `cmd/tenon/run.go` (dispatch, `config check` line), `internal/event/event.go`, `internal/event/payloads.go`, `internal/i18n/catalog.go`

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces:
  - Subcommands per spec §4: `init`, `db update [--mirror] [--ecosystem …]`, `scan [--fail-on open] [--json]`, `status`, `report open --kind --summary [--aware-at]`, `report mark <id> <step> [--at]`, `export --out [--lang]` (wired in Task 7).
  - `event.CRAScan` + `CRAScanPayload{SBOMs, Components, Open, ToVerify, NotAffected, Fixed int; DBDate string}`; `event.CRAReport` + `CRAReportPayload{ID, Kind, Step, At string}`; `SessionStartedPayload.Kind string \`json:"kind,omitempty"\`` (`"cra"`).
  - Journal: `scan` (on success) and `report open|mark` open the audit writer, append `SessionStarted{kind:"cra"}` then the CRA event, close.
  - `status` text output: one line per check (`ok`/`missing`/`invalid`), scan summary from the last `CRAScan` in the journal (or `never`), reports with each deadline status.

- [ ] **Step 1: Failing e2e tests:** `TestCRAInit` (creates files; second run refused); `TestCRAReportOpenMark` (id, file contents, journal `CRAReport` events); `TestCRAStatusOverdue` (report with `aware_at` 2 days ago, no early warning → exit 7, line `early-warning overdue`); `TestCRAScanNoDB` (exit 1, exact message); `TestCRAScanFailOn` (fake DB dir built with Task 3 helpers in testdata + SBOM → exit 7 with `--fail-on open`, 0 without; `--json` is valid JSON with the `Result` fields); `TestCRADBUpdateStrictLocal` (profile strict-local + default base → exit 1 exact message); `TestCRADBUpdateMirror` (httptest TLS mirror, env `SSL_CERT_FILE` or injected client); `TestCRADBOld` (DB index date 10 days ago → notice); `TestCRAConfigCheck` (`cra: dossier absent, osv db absent` then `cra: dossier ok, osv db 2026-10-20`).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./cmd/tenon/ -run CRA` — FAIL.
- [ ] **Step 3: Implement.** The OSV dir is `<data>/cra/osv`; ecosystems for `db update` default to those of the dossier SBOMs (error if no dossier and no `--ecosystem`).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cli): tenon cra commands with reporting deadlines and journal events`

---

### Task 7: technical dossier export and compliance section

**Files:**
- Create: `internal/cra/report/report.go`, `internal/cra/report/report.html.tmpl`, `internal/cra/report/testdata/golden.*`
- Modify: `cmd/tenon/cra.go` (`export`), `internal/compliance/*.go` (CRA section), `internal/i18n/catalog.go`
- Test: `internal/cra/report/report_test.go`, `internal/compliance/*_test.go`

**Interfaces:**
- Consumes: Tasks 5–6 (`dossier.Dossier`, `scan.Result`, `CRAScan`/`CRAReport` events).
- Produces:
  - `type Dossier struct { Product dossier.Product; BOMs []BOMSummary; Scan *scan.Result; Reports []ReportView; Docs []Doc; GeneratedAt time.Time }` (JSON field names snake_case), `func Write(dir string, d Dossier, lang i18n.Lang) error` → `cra-dossier.json`, `cra-dossier.html` (0600, dir created 0700), same CSS/structure as the compliance export; `cra/docs/*.md` rendered as escaped preformatted text.
  - Compliance export: report field `CRA struct { LastScan string; Open, ToVerify int; Reports []CRAReportStatus }` (JSON `cra`, omitted when no CRA events); HTML row « CRA »; overdue steps marked, no exit-code change.

- [ ] **Step 1: Failing tests:** `TestWriteGolden` (fixed clock; JSON and HTML match goldens in FR and EN; update with `-update`); `TestWriteEscapesDocs` (`<script>` in a doc rendered escaped); `TestExportCRASection` (journal with `CRAScan` + `CRAReport` events → section values; overdue marked; absent → omitted); e2e `TestCRAExport` (`tenon cra export --out <tmp>` writes both files).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/cra/report/ ./internal/compliance/ && GOFLAGS=-mod=vendor go test ./cmd/tenon/ -run CRAExport` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(cra): technical dossier export; CRA section in the compliance export`

---

### Task 8: built-in skills and documentation

**Files:**
- Create: `internal/skills/builtin/cra-sbom/SKILL.md`, `internal/skills/builtin/cra-triage/SKILL.md`, `internal/skills/builtin/cra-report/SKILL.md`, `internal/skills/builtin/cra-docs/SKILL.md`, `internal/skills/builtin.go`, `docs/cra.md`, `docs/fr/cra.md`
- Modify: `internal/skills/catalog.go` (`Builtin Source = "builtin"`, embedded FS, priority), `cmd/tenon/run.go` (catalog construction if needed), `docs/usage.md`, `docs/fr/utilisation.md`, `README.md`
- Test: `internal/skills/catalog_test.go`

**Interfaces:**
- Produces: `skills.Builtin Source = "builtin"`; `Discover` adds embedded skills last, skipped when a user/project skill has the same name (no notice); `Catalog.Load` reads embedded skills from the `embed.FS`; `/skills` shows the source.
- Skill content (English, like other skill files; frontmatter `name`, `description`, `allowed-tools`): each skill states its goal, the exact `tenon cra` commands to run, what to ask the user to confirm before writing files, and the facts from spec §1/§6 (article 14 steps and deadlines, ENISA single reporting platform, sending is manual; `cra-report` lists the expected content: product, vulnerability/incident nature, severity, corrective measures, affected Member States when known).

- [ ] **Step 1: Failing tests:** `TestBuiltinSkillsPresent` (four names, source `builtin`, `Load` returns non-empty bodies with a valid frontmatter); `TestBuiltinOverridden` (a user skill dir `cra-triage` → source `user`, builtin not listed twice, no notice).
- [ ] **Step 2: Run** `GOFLAGS=-mod=vendor go test ./internal/skills/` — FAIL.
- [ ] **Step 3: Implement** the catalog change and the four skill files, then docs EN/FR: purpose and legal context (with the "to confirm in the regulation" note for the incident final-report deadline), dossier layout, commands and exit codes, offline OSV database and mirrors, `strict-local`, CI example (`tenon cra db update && tenon cra scan --fail-on open`), built-in skills and how to override them, limits (§10 of the spec).
- [ ] **Step 4: Run** — PASS; full gates.
- [ ] **Step 5: Commit** `feat(skills): built-in CRA skills; docs: Cyber Resilience Act assistant (EN/FR)`
