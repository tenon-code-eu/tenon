---
name: cra-docs
description: Write the narrative parts of the Cyber Resilience Act technical dossier in cra/docs/*.md (risk analysis, security by default, update and end-of-support instructions) from the real project, then export with tenon cra export.
allowed-tools: [read_file, grep, glob, write_file, shell]
---
# cra-docs: narrative parts of the technical dossier

## Goal

Write Markdown files in `cra/docs/` that `tenon cra export` includes in the technical dossier (HTML and JSON). The text is rendered as escaped plain text, so keep it simple Markdown without HTML.

## Facts to keep in mind

- Regulation (EU) 2024/2847 (Cyber Resilience Act). Article 14 (reporting) applies from 11 September 2026, the rest from 11 December 2027. Reporting steps: early warning 24 h, notification 72 h, final report 14 days after a fix (vulnerability) or one month after the notification (severe incident, to confirm against the regulation text). Reports go through the ENISA single reporting platform; sending is manual and tenon sends nothing.
- tenon does not assess conformity, does not provide the EU declaration of conformity and does not apply the CE marking. These files document facts about the product; the manufacturer is responsible for their accuracy.

## Files to propose

- `cra/docs/risk-analysis.md`: intended use, assets and attack surface, threats considered, risks and how they are reduced.
- `cra/docs/security-by-default.md`: secure default configuration, authentication, data protection, logging, how the user can reset to the default state.
- `cra/docs/updates-and-support.md`: how security updates are delivered, how users are informed, the support end date (the `support_end` of `cra/product.toml`), what happens at end of support, how to contact the manufacturer (`security_contact`).

## Steps

1. Read `cra/product.toml`, the code and the existing documentation. Use facts you can point to; mark every gap as "to complete by the manufacturer" rather than inventing it.
2. Propose the list of files and their outline. Ask the user to confirm which files to write, the language, and the facts you could not find (intended use, user data processed, update channel).
3. Write or update the confirmed files in `cra/docs/`. Never overwrite a file without showing the difference and asking.
4. Run `tenon cra status` to check the dossier (exit 0, 7 if a reporting deadline is overdue, 1 if the dossier is invalid, including a listed SBOM that is missing).
5. Run `tenon cra export --out <dir> --lang en` (or `fr`) after the user chooses the folder, and tell them where `cra-dossier.html` and `cra-dossier.json` are. The export works without a scan when no database is present, or when no SBOM has been generated yet and `cra/product.toml` lists no `sboms`; a notice says so. A listed SBOM that is missing or an SBOM that cannot be read makes the export fail (exit 1).

## Commands

- `tenon cra status`
- `tenon cra export --out <dir> [--lang fr|en]`
