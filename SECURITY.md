# Security

## Reporting a vulnerability

Do not report a vulnerability in a public issue.

Use GitHub private vulnerability reporting:
the **Security** tab of the repository, then **Report a vulnerability**
(https://github.com/tenon-code-eu/tenon/security/advisories/new).

Include:

- the tenon version (`tenon --version`) and the operating system;
- the steps to reproduce;
- the observed or expected impact (secret leak, approval bypass, write outside the project, data sent outside the EU, tampering with the audit log…).

Do not include any API key or real personal data.

## Response times

- Acknowledgement: within 7 days.
- First assessment: within 30 days.
- Advisory published after the fix is released, in agreement with the person who reported the vulnerability.

## Supported versions

Only the latest published version receives security fixes.

## Scope

The threat model, the guarantees and the known limits are described in [docs/security.md](docs/security.md).
A limit documented in that file is not a vulnerability, unless the actual behavior differs from it.

## Verifying a version

Each version provides a `SHA256SUMS` file signed with minisign.
See the "Installation" section of the [README](README.md#binaries).

---

## En français

Ne signalez pas une vulnérabilité dans une issue publique.
Utilisez le signalement privé de GitHub : onglet **Security**, puis **Report a vulnerability**.
Indiquez la version de tenon, les étapes pour reproduire et l'impact.
N'incluez aucune clé d'API ni donnée personnelle réelle.
Nous accusons réception sous 7 jours.
Le périmètre est décrit dans [docs/fr/securite.md](docs/fr/securite.md).
