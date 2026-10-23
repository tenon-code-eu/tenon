---
name: cra-triage
description: Triage open vulnerabilities found by tenon cra scan - analyse real usage of the component in the code and write VEX entries (cra/vex.cdx.json) after user confirmation.
allowed-tools: [read_file, grep, glob, write_file, shell]
---
# cra-triage: assess open vulnerabilities and write the VEX

## Goal

For each vulnerability that `tenon cra scan` reports as open (no VEX entry, or state `affected` or `under_investigation`) or to verify, decide with evidence whether the product is affected, and record the decision in `cra/vex.cdx.json` (CycloneDX VEX).

## Facts to keep in mind

- Regulation (EU) 2024/2847 (Cyber Resilience Act). Article 14: an actively exploited vulnerability must be reported by early warning within 24 h of becoming aware, notification within 72 h, and a final report 14 days after a corrective measure is available. Reports go through the ENISA single reporting platform; tenon sends nothing, sending is manual.
- If a vulnerability is actively exploited and affects the product, stop triage and propose the `cra-report` skill: the 24 h clock starts when the manufacturer becomes aware.
- Times are RFC 3339 with a time zone.

## VEX states

- `not_affected`: requires `analysis.justification` (CycloneDX values, for example `code_not_present`, `code_not_reachable`, `requires_configuration`, `requires_dependency`, `requires_environment`, `protected_by_compiler`, `protected_at_runtime`, `protected_at_perimeter`, `protected_by_mitigating_control`). Without it tenon refuses the file (`cra: vex: <id>: not_affected requires a justification`).
- `affected`: stays open. Add `analysis.detail` and plan a fix.
- `fixed`: the dependency was upgraded. If the OSV database still lists the exact version as affected, `scan` shows a conflict (kept resolved because of backports, and warned): check the version really contains the fix.
- `under_investigation`: stays open. `in_triage` is read as `under_investigation`. Other CycloneDX values are mapped: `false_positive` to `not_affected` with a justification, `resolved_with_pedigree` to `fixed`.

## Steps

1. Run `tenon cra scan --json` (or `tenon cra scan`) and list the open and to-verify vulnerabilities with component, version and fixed version.
2. For each one, search the code for real use of the component and of the vulnerable function or feature named in the advisory (imports, call sites, configuration). Quote the files and lines you relied on.
3. Propose an entry: state, justification (for `not_affected`), detail, and the component reference (purl or bom-ref). A purl-only reference matches every SBOM component with that purl.
4. Ask the user to confirm each entry. Never mark `not_affected` without evidence and an explicit confirmation.
5. Write the confirmed entries into `cra/vex.cdx.json`, keeping existing entries and the document structure (create a valid CycloneDX VEX document if the file does not exist).
6. Run `tenon cra scan` again and report the new counts.

## Commands

- `tenon cra scan` and `tenon cra scan --json`
- `tenon cra scan --fail-on open` (exit 7 while an open vulnerability remains; use it as the final check)
- `tenon cra status`
