# Cyber Resilience Act assistant

`tenon cra` helps a manufacturer of products with digital elements keep, in the repository, a verifiable dossier for Regulation (EU) 2024/2847 (the Cyber Resilience Act, "CRA"): SBOM, status of each vulnerability (VEX), reporting deadlines and an exportable technical dossier.
The commands are deterministic: no model, no network except `tenon cra db update`. They work in CI. Built-in skills guide the agent when it has to write.

## Contents

- [Legal context](#legal-context)
- [Dossier layout](#dossier-layout)
- [Commands and exit codes](#commands-and-exit-codes)
- [Scan](#scan)
- [Offline OSV database](#offline-osv-database)
- [Version ordering](#version-ordering)
- [Reports and deadlines](#reports-and-deadlines)
- [Export](#export)
- [CI](#ci)
- [Built-in skills](#built-in-skills)
- [Limits](#limits)

## Legal context

Sources are secondary and must be checked against the regulation text before you rely on them.

- Article 14 (reporting of actively exploited vulnerabilities and severe incidents) applies from 11 September 2026. The rest of the regulation applies from 11 December 2027.
- Reports go through the ENISA single reporting platform.
- Deadlines for a vulnerability: early warning 24 h after becoming aware, notification 72 h after becoming aware, final report 14 days after a corrective measure is available.
- Deadline for a severe incident: final report one calendar month after the notification (to confirm against the regulation text).

tenon sends nothing to ENISA or to any authority. It tracks the deadlines and helps draft the text; sending is manual.

## Dossier layout

```
cra/
  product.toml
  sbom*.cdx.json          # CycloneDX JSON 1.4 to 1.6
  vex.cdx.json            # CycloneDX VEX
  reports/<id>.toml       # one report per file, id = YYYY-MM-DD-<n>
  docs/*.md               # optional narrative parts
```

`tenon cra init` creates `cra/product.toml` (commented template) and `cra/reports/`. It refuses to run when `product.toml` exists.

### product.toml

```toml
name = "My product"
version = "2.3.0"
manufacturer = "Example Ltd"
security_contact = "security@example.eu"
support_end = 2031-12-31          # end of the support period
category = "default"              # default | important-1 | important-2 | critical
sboms = ["cra/sbom.cdx.json"]     # default: cra/sbom*.cdx.json
```

All fields except `sboms` are required: `cra: product.toml: <field> required`. An unknown category gives `cra: product.toml: category must be default, important-1, important-2 or critical`.

### SBOM

tenon reads CycloneDX JSON 1.4 to 1.6 and does not generate SBOMs: use the tool of your ecosystem (`cyclonedx-gomod`, `@cyclonedx/cyclonedx-npm`, `cyclonedx-py`, `cargo cyclonedx`, `syft` as a fallback). Components nested under `components[].components` are collected. Purl qualifiers (`?type=jar`) and subpaths are ignored for matching.

### VEX

`vulnerabilities[]` of `cra/vex.cdx.json` carries `id`, `affects[].ref` (purl or bom-ref of the component) and `analysis.state`:

| State | Effect |
|---|---|
| `not_affected` | Not counted as open. Requires `analysis.justification`, else `cra: vex: <id>: not_affected requires a justification`. |
| `affected` | Open. |
| `under_investigation` | Open. |
| `in_triage` | Read as `under_investigation`. |
| `fixed` | Resolved. |

CycloneDX values that appear in exports of other tools are mapped: `in_triage` to `under_investigation`, `false_positive` to `not_affected` (with a justification), `resolved_with_pedigree` to `fixed`.

### Reports

`cra/reports/<id>.toml`:

```toml
kind = "vulnerability"            # vulnerability | incident
summary = "..."
aware_at = 2026-10-23T09:12:00Z
fix_available_at = 2026-10-25T00:00:00Z
early_warning_at = 2026-10-23T15:00:00Z   # set by `report mark`
notification_at = 2026-10-24T08:00:00Z
final_at = ...
```

Times are RFC 3339 and the time zone is mandatory (`2026-10-23T09:12:00+02:00` or `Z`). A time without zone is refused. Deadlines are computed in UTC, so a daylight saving change does not shift the 24 h and 72 h limits.

## Commands and exit codes

| Command | Purpose | Exit codes |
|---|---|---|
| `tenon cra init` | Create the dossier skeleton. | 0, 1 |
| `tenon cra db update [--mirror <url>] [--ecosystem <eco>...]` | Download the OSV database (the only network use). | 0, 1 |
| `tenon cra scan [--fail-on open] [--json]` | Match SBOM components against the database and apply the VEX. | 0; 7 with `--fail-on open` and at least one open vulnerability; 1 on error |
| `tenon cra status` | Dossier completeness, last scan, reports and deadlines. | 0; 7 if a deadline is overdue; 1 if the dossier is invalid |
| `tenon cra report open --kind vulnerability\|incident --summary <text> [--aware-at <RFC 3339>]` | Create `cra/reports/<id>.toml` (`aware_at` defaults to now) and print the deadlines. | 0, 1 |
| `tenon cra report mark <id> early-warning\|notification\|final\|fix-available [--at <RFC 3339>]` | Record a step. | 0, 1 |
| `tenon cra export --out <dir> [--lang fr\|en]` | Technical dossier, HTML and JSON. | 0, 1 |

`status` also reports an incomplete dossier (missing SBOM, support period over) as lines of text. That does not change the exit code: only an overdue deadline gives 7, and only an invalid dossier gives 1.

`tenon config check` prints `cra: dossier <ok|absent|invalid>, osv db <date|absent>`.

Commands that scan or change a report write an event to the audit log (`CRAScan`, `CRAReport`). Outside a session, tenon opens a short session of kind `cra`, writes the event and closes it.

## Scan

`tenon cra scan` reads the SBOMs, matches each component (purl `pkg:<type>/<namespace>/<name>@<version>`) against the OSV entries of its ecosystem, then applies the VEX. The summary shows:

- open: no VEX entry, or `affected` or `under_investigation`;
- to verify: the ecosystem has no version comparator and the version is not in the explicit OSV `versions` list;
- not affected and fixed: from the VEX;
- conflicts: the VEX says `fixed` while OSV lists the exact version as affected. The entry stays resolved, because of backports, and a warning is printed. Check that the build really contains the fix;
- unchecked: components with an empty or unparsable purl, or a type tenon cannot map (such as `deb` or `apk`). They are never counted as clean.

Errors that exit with code 1:

- no SBOM found;
- no database: `cra: no vulnerability database (run tenon cra db update)`;
- an ecosystem of the SBOMs is missing from the database (the scan does not claim "no vulnerability" for it);
- a corrupt archive (reported as unreadable, never as "no vulnerability").

A database older than 7 days produces the notice `cra: vulnerability database is <n> days old`. A finding is reported once per (vulnerability, component), and an entry with `withdrawn` is ignored.

## Offline OSV database

Vulnerabilities come from [OSV](https://osv.dev) archives, matched locally: the list of your dependencies never leaves the machine.

- Default base: `https://storage.googleapis.com/osv-vulnerabilities`. Archive: `<base>/<ECO>/all.zip`.
- Another base (mirror): `--mirror <url>` or, in the global configuration only, `[cra] osv_mirror = "<url>"`. The key is refused in a project configuration: `config: cra not allowed in project configuration`.
- https only. http is accepted for a local host only. Redirects must be https. Each archive is limited to 1 GiB.
- Update per ecosystem: without `--ecosystem`, tenon updates the ecosystems of the SBOMs.
- Storage: `<data>/cra/osv/<ECO>/all.zip` and `<data>/cra/osv/index.json` (`{eco: {date, sha256, entries}}`). The replacement is atomic: a failed or truncated download keeps the previous archive.
- A corrupt `index.json` is moved aside and rebuilt.
- Purl type to OSV ecosystem: `golang` to `Go`, `npm`, `cargo` to `crates.io`, `pypi` to `PyPI`, `maven`, `nuget`, `gem` to `RubyGems`, `composer` to `Packagist`.

Under the `strict-local` profile:

- a local mirror is mandatory, otherwise `cra: profile strict-local requires a local osv mirror` (exit 1);
- redirects must stay on local hosts;
- proxies of the environment are bypassed. Elsewhere they are respected.

The provider allowlist of the organisation policy does not apply to OSV, which is not a provider.

## Version ordering

- semver for Go (including pseudo-versions, treated as pre-releases), npm and crates.io;
- PEP 440 for PyPI (epoch, release, pre, post and dev; local labels are ignored);
- Go `stdlib` and toolchain versions of the form `go1.x` are handled;
- NuGet names are compared case-insensitively;
- other ecosystems (Maven, RubyGems, Packagist...) use the explicit OSV `versions` lists. A complete list means that an unlisted version is not affected. Otherwise the finding is "to verify";
- OSV ranges (`introduced`, `fixed`, `last_affected`) are evaluated after sorting the events; `limit` is ignored.

An OSV entry may have several `affected[]` blocks for the same package (for example Go stdlib and a module) or affect several packages: each package is matched on its own ranges.

## Reports and deadlines

| Step | Vulnerability | Incident |
|---|---|---|
| Early warning | `aware_at` + 24 h | `aware_at` + 24 h |
| Notification | `aware_at` + 72 h | `aware_at` + 72 h |
| Final report | `fix_available_at` + 14 days | `notification_at` + 1 calendar month (to confirm against the regulation text) |

For a vulnerability, the final deadline is unknown while `fix_available_at` is absent: `status` shows "deadline unknown" and does not hide it. Each step is `due`, `overdue` or `done`.

## Export

`tenon cra export --out <dir> [--lang fr|en]` writes `cra-dossier.json` and `cra-dossier.html`, with the same template and style as the compliance export: product, SBOMs (components, generator, date), last scan with the VEX, reports and respect of the deadlines, support period, and the narrative parts `cra/docs/*.md` (rendered as escaped text).
Without a database or an SBOM the export still works, without scan results, and notices say what is missing.

`tenon audit export` adds a "CRA" section: last scan (date, open, to verify) and reports with respect of the deadlines. Deadlines are computed from the whole journal whatever `--since` says. Reports are listed while their final step is not done, and "deadlines unknown" stays visible. An overdue deadline marks the line, with no exit code 2.

## CI

```
tenon cra db update && tenon cra scan --fail-on open
```

Exit code 7 fails the job while a vulnerability is open. Add `tenon cra status` to fail on an overdue reporting deadline. In an air-gapped pipeline, set `[cra] osv_mirror` to an internal mirror in the global configuration.

## Built-in skills

Four skills are embedded in the binary, with the source `builtin` (the lowest priority). `/skills` and `tenon skills` show the source.

| Skill | Purpose |
|---|---|
| `cra-sbom` | Generate the SBOMs with the tool of the ecosystem, place them in `cra/`, run `tenon cra status`. |
| `cra-triage` | For each open vulnerability, analyse the real use of the component, propose the VEX entry, write it after confirmation. |
| `cra-report` | Draft the early warning, notification or final report from `cra/reports/<id>.toml`, then mark the step after confirmation. |
| `cra-docs` | Write `cra/docs/*.md` (risk analysis, security by default, updates and end of support). |

The descriptions of the built-in skills are added to the system prompt only when the project root contains a `cra/` directory. They stay in the catalog in every project: `/skills` lists them and `/cra-sbom` or the `skill` tool load them by name.

Each skill asks for your confirmation before it writes a file, and reminds you that reports are sent by hand on the ENISA platform.
To replace one, create a skill of the same name (for example `.tenon/skills/cra-triage/SKILL.md`, or in your user folder). A user or project skill wins silently, with no notice. See [skills](usage.md#skills).

## Limits

Out of scope:

- sending anything to the ENISA platform;
- generating SBOMs;
- conformity assessment, CE marking and the EU declaration of conformity;
- version ordering for ecosystems other than those above (they rely on explicit version lists);
- CSAF and provenance attestation;
- several products in one repository.

tenon helps keep evidence; the manufacturer remains responsible for its accuracy and for the legal deadlines. Check the dates and durations against the regulation text.
