---
name: cra-report
description: Draft the Cyber Resilience Act article 14 early warning, notification or final report from cra/reports/<id>.toml, and mark each step with tenon cra report mark after user confirmation.
allowed-tools: [read_file, write_file, shell]
---
# cra-report: draft article 14 reports

## Goal

Help the manufacturer open a report for an actively exploited vulnerability or a severe incident, follow its deadlines, and draft the text of each step. Drafts are text for the user to paste; tenon never contacts the authorities.

## Facts to keep in mind

- Regulation (EU) 2024/2847, article 14, applies from 11 September 2026.
- Steps and deadlines:
  - early warning: 24 h after becoming aware;
  - notification: 72 h after becoming aware;
  - final report, vulnerability: 14 days after a corrective or mitigating measure is available;
  - final report, severe incident: one month after the notification (to confirm against the regulation text).
- Reports are submitted on the ENISA single reporting platform, and also reach the designated CSIRT. Sending is manual: tenon does not send anything.
- All times are RFC 3339 with a time zone. Deadlines are computed in UTC, so a daylight saving change does not shift the 24 h and 72 h limits.

## Expected content

- the product: name, version, manufacturer, security contact (from `cra/product.toml`);
- the nature of the vulnerability or incident, with a CVE or OSV identifier when there is one;
- whether it is actively exploited, and the severity and impact;
- corrective and mitigating measures taken or planned, and what users can do;
- the Member States where the product is made available, when known (do not guess; leave it out or ask).

The early warning is short (is it exploited, which product, which Member States if known). The notification adds the nature, an initial assessment and the measures. The final report adds the root cause, the fix, and what was done for users.

## Steps

1. Run `tenon cra status` and read the reports with their deadlines (`due`, `overdue`, `done`, `done_late` for a step done after its deadline).
2. To open a report, ask the user for the kind (`vulnerability` or `incident`), a one-line summary, and when they became aware (RFC 3339 with zone). Confirm, then run `tenon cra report open --kind <kind> --summary "<text>" --aware-at <time>`. Show the deadlines printed.
3. Read `cra/reports/<id>.toml` and `cra/product.toml`. Draft the text of the requested step with the expected content above. Mark anything unknown as "to complete" instead of inventing it.
   Never add keys or comments to `cra/reports/<id>.toml`: unknown keys are refused and `report mark` does not keep comments. Save drafts and notes in a separate file such as `cra/reports/<id>.md`.
4. Let the user review and edit the draft. Remind them to submit it on the ENISA single reporting platform themselves.
5. Only after the user says the step was sent (or the fix is available), confirm the time and run `tenon cra report mark <id> <early-warning|notification|final|fix-available> --at <time>`. Never mark a step that was not done.
6. Run `tenon cra status` again. Exit codes: 0; 7 if a deadline is overdue; 1 if the dossier is invalid (unreadable file, unknown key, listed SBOM missing or SBOM unreadable). A step done late is shown but does not change the exit code.

## Commands

- `tenon cra report open --kind vulnerability|incident --summary "<text>" [--aware-at <RFC 3339>]`
- `tenon cra report mark <id> early-warning|notification|final|fix-available [--at <RFC 3339>]`
- `tenon cra status`
