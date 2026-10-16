# Sécurité

## Signaler une vulnérabilité

Ne signalez pas une vulnérabilité dans une issue publique.

Utilisez le signalement privé de GitHub :
onglet **Security** du dépôt, puis **Report a vulnerability**
(https://github.com/tenon-code-eu/tenon/security/advisories/new).

Indiquez :

- la version de tenon (`tenon --version`) et le système ;
- les étapes pour reproduire ;
- l'impact constaté ou supposé (fuite de secret, contournement d'une validation, écriture hors du projet, envoi hors de l'UE, altération du journal d'audit…).

N'incluez aucune clé d'API ni donnée personnelle réelle.

## Délais

- Accusé de réception : sous 7 jours.
- Premier diagnostic : sous 30 jours.
- Publication de l'avis après la sortie du correctif, en accord avec la personne qui a signalé la faille.

## Versions prises en charge

Seule la dernière version publiée reçoit des correctifs de sécurité.

## Périmètre

Le modèle de menace, les garanties et les limites connues sont décrits dans [docs/securite.md](docs/securite.md).
Une limite documentée dans ce fichier n'est pas une vulnérabilité, sauf si le comportement réel s'en écarte.

## Vérifier une version

Chaque version fournit un fichier `SHA256SUMS` signé avec minisign.
Voir la section « Installation » du [README](README.md#binaires).

---

## Reporting a vulnerability (English)

Do not open a public issue.
Use GitHub private vulnerability reporting: **Security** tab, then **Report a vulnerability**.
Include the tenon version, steps to reproduce and the impact.
Do not include real API keys or personal data.
We acknowledge reports within 7 days.
