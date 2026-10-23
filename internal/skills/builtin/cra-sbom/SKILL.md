---
name: cra-sbom
description: Generate the CycloneDX SBOM of the product for the Cyber Resilience Act dossier, place it in cra/ and check the dossier with tenon cra status.
allowed-tools: [read_file, write_file, shell]
---
# cra-sbom: generate the SBOM for the CRA dossier

## Goal

Produce one CycloneDX JSON SBOM (spec 1.4 to 1.6) per product component set, store it as `cra/sbom*.cdx.json`, and check that the dossier reads it. tenon never generates SBOMs itself and never resolves dependencies: it reads and validates the files you place in `cra/`.

## Facts to keep in mind

- Regulation (EU) 2024/2847 (Cyber Resilience Act). Article 14 (reporting of actively exploited vulnerabilities and severe incidents) applies from 11 September 2026; the rest of the regulation from 11 December 2027.
- Article 14 steps: early warning within 24 h of becoming aware, notification within 72 h, final report 14 days after a corrective measure is available (vulnerability) or one month after the notification (severe incident, to confirm against the regulation text).
- Reports go through the ENISA single reporting platform. tenon sends nothing: sending is manual.
- The SBOM is the input of `tenon cra scan`: a component without a usable purl (`pkg:<type>/...@<version>`) is listed as unchecked, never as clean.

## Steps

1. Look at the project to find its ecosystems (go.mod, package.json, Cargo.toml, pyproject.toml, pom.xml, ...). Check whether `cra/product.toml` exists; if not, propose `tenon cra init` and ask before running it.
2. Pick the generator that matches each ecosystem and show the exact command before running it:
   - Go: `cyclonedx-gomod app -json -std -output cra/sbom-go.cdx.json` (or `cyclonedx-gomod mod -licenses -json -std -output cra/sbom-go.cdx.json` for a library). Keep `-std`: it adds the Go standard library, which `tenon cra scan` matches against the OSV `stdlib` entries. Flag names vary between versions: check `cyclonedx-gomod app -h` / `cyclonedx-gomod mod -h` first.
   - npm: `npx @cyclonedx/cyclonedx-npm --output-file cra/sbom-npm.cdx.json`
   - Python: `cyclonedx-py environment -o cra/sbom-python.cdx.json` (or `requirements`, `poetry`)
   - Rust: `cargo cyclonedx --format json` then move the file to `cra/sbom-rust.cdx.json`
   - Fallback for anything else: `syft dir:. -o cyclonedx-json=cra/sbom.cdx.json`
3. Confirm with the user before writing or overwriting any file in `cra/`: file names, which ecosystems are covered, and whether test or dev dependencies should be excluded.
4. Run the generator. Do not edit the SBOM by hand.
5. Run `tenon cra status` and read the result: SBOM present and valid, component count, product fields, support period. Fix what is reported.
6. If a vulnerability database is present, run `tenon cra scan` and summarise: open, to verify, not affected, fixed, conflicts, unchecked. Without a database, tell the user to run `tenon cra db update` (the only command that uses the network).
7. List the unchecked components (empty or unparsable purl, unsupported type such as deb or apk) and say they are not verified.

## Commands

- `tenon cra init`
- `tenon cra status` (exit 0, 7 if a reporting deadline is overdue, 1 if the dossier is invalid, including a listed SBOM that is missing; an SBOM not generated yet with no `sboms` listed is only reported as missing)
- `tenon cra db update`
- `tenon cra scan` (exit 0, 7 with `--fail-on open` and an open vulnerability, 1 on error)
