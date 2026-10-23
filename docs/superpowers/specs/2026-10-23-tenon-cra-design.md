# tenon : assistant Cyber Resilience Act (v0.8)

Date : 2026-10-23. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Specs liées : [export de conformité](2026-10-20-tenon-audit-export-design.md), [skills](2026-10-17-tenon-skills-design.md), [profils de conformité](2026-10-20-tenon-compliance-profiles-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md).

## 1. Objectif

Aider les fabricants de produits comportant des éléments numériques (règlement (UE) 2024/2847, « CRA ») à tenir, dans leur dépôt, un dossier vérifiable : SBOM, statut de chaque vulnérabilité (VEX), échéances de signalement, dossier technique exportable. Les commandes `tenon cra` sont déterministes (sans modèle, utilisables en CI) ; des skills intégrées guident l'agent pour rédiger.

Contexte réglementaire (sources secondaires, à confirmer dans le texte du règlement avant publication de la doc) : l'article 14 (signalement des vulnérabilités activement exploitées et des incidents graves via la plateforme unique de l'ENISA) s'applique depuis le 11 septembre 2026, le reste du règlement à partir du 11 décembre 2027. Délais : alerte précoce sous 24 h après la prise de connaissance, notification sous 72 h, rapport final sous 14 jours après la mise à disposition d'une mesure corrective (vulnérabilité) ou sous un mois après la notification (incident grave, à confirmer).

Critères de succès :

- aucune donnée du projet ne quitte la machine pour l'analyse des vulnérabilités (base OSV hors ligne) ;
- `tenon cra scan --fail-on open` fait échouer un job CI quand une vulnérabilité n'est pas évaluée ;
- `tenon cra status` signale une échéance de signalement dépassée (code 7) ;
- `tenon cra export` produit un dossier technique HTML et JSON (FR/EN) ;
- les skills `cra-*` sont disponibles sans installation et remplaçables ;
- aucune dépendance nouvelle.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Forme | Commandes déterministes + skills intégrées | Vérifiable en CI ; l'agent rédige |
| SBOM | Générée par les outils de l'écosystème, lue et validée par tenon | Pas de réimplémentation des résolveurs de dépendances |
| Vulnérabilités | Base OSV téléchargée, appariement local | Souveraineté : la liste des dépendances ne part pas |
| Versions | Comparateurs semver et PEP 440 (sous-ensemble) ; autres écosystèmes par listes explicites, sinon « à vérifier » | Couvre Go, npm, crates.io, PyPI sans dépendance |
| Signalement | Suivi des échéances + brouillons ; envoi ENISA manuel | tenon ne parle pas à la plateforme |
| Stockage | Dossier `cra/` versionné dans le dépôt ; base OSV dans le dossier de données | Le dossier suit le code ; la base est un cache |

## 3. Dossier dans le dépôt

```
cra/
  product.toml
  sbom*.cdx.json          # CycloneDX JSON 1.4 à 1.6
  vex.cdx.json            # CycloneDX VEX
  reports/<id>.toml       # un signalement par fichier
```

`cra/product.toml` :

```toml
name = "Mon produit"
version = "2.3.0"
manufacturer = "Exemple SAS"
security_contact = "security@exemple.eu"
support_end = 2031-12-31          # fin de la période de support
category = "default"              # default | important-1 | important-2 | critical
sboms = ["cra/sbom.cdx.json"]     # défaut : cra/sbom*.cdx.json
```

- Champs obligatoires : `name`, `version`, `manufacturer`, `security_contact`, `support_end`, `category` ; sinon `cra: product.toml: <champ> required`.
- `category` inconnue : `cra: product.toml: category must be default, important-1, important-2 or critical`.

`cra/vex.cdx.json` : document CycloneDX dont `vulnerabilities[]` porte `id`, `affects[].ref` (purl ou bom-ref du composant) et `analysis.state` parmi `not_affected`, `affected`, `fixed`, `under_investigation`, `in_triage` (traité comme `under_investigation`) ; `not_affected` exige `analysis.justification` (valeurs CycloneDX), sinon `cra: vex: <id>: not_affected requires a justification`.

`cra/reports/<id>.toml` (`id` : `AAAA-MM-JJ-<n>`) :

```toml
kind = "vulnerability"            # vulnerability | incident
summary = "…"
aware_at = 2026-10-23T09:12:00Z
fix_available_at = 2026-10-25T00:00:00Z   # vulnérabilité : départ du délai du rapport final
early_warning_at = 2026-10-23T15:00:00Z   # renseignés par `report mark`
notification_at = …
final_at = …
```

## 4. Commandes

- `tenon cra init` : crée `cra/product.toml` (modèle commenté) et `cra/reports/` ; refuse si `product.toml` existe.
- `tenon cra db update [--mirror <url>] [--ecosystem <eco>…]` : télécharge `<base>/<ECO>/all.zip` pour les écosystèmes présents dans les SBOM (ou ceux donnés), base par défaut `https://storage.googleapis.com/osv-vulnerabilities`, ou `[cra] osv_mirror` / `--mirror`. https obligatoire (http seulement vers un hôte local), redirections https seulement, réponse limitée à 1 Gio par archive. Écrit `<data>/cra/osv/<ECO>/all.zip` (remplacement atomique) et `<data>/cra/osv/index.json` (`{eco: {date, sha256, entries}}`). Respecte la liste d'hôtes de la politique d'organisation (`network` de la politique si présente, sinon libre). Sous `strict-local` : miroir local exigé, sinon `cra: profile strict-local requires a local osv mirror`, code 1. Proxys d'environnement respectés, sauf sous `strict-local`.
- `tenon cra scan [--fail-on open] [--json]` : lit les SBOM, apparie chaque composant (purl `pkg:<type>/<namespace>/<name>@<version>`) aux entrées OSV de son écosystème (`affected[].package` + `ranges` `SEMVER`/`ECOSYSTEM` + `versions`), applique le VEX, affiche les vulnérabilités `open` (sans entrée VEX, ou `affected`/`under_investigation`), `to_verify` (écosystème sans comparateur et version absente des listes) et le compte des `not_affected`/`fixed`. Base absente : `cra: no vulnerability database (run tenon cra db update)`, code 1 ; base de plus de 7 jours : notice `cra: vulnerability database is <n> days old`. Codes : 0 ; 7 si `--fail-on open` et au moins une vulnérabilité `open`.
- `tenon cra status` : complétude du dossier (champs, SBOM présentes et valides, VEX valide, période de support non échue), résultat du dernier scan, signalements en cours avec leurs échéances (`due`, `overdue`, `done`). Codes : 0 ; 7 si une échéance est dépassée ; 1 si le dossier est invalide.
- `tenon cra report open --kind vulnerability|incident --summary <texte> [--aware-at <RFC 3339>]` : crée `cra/reports/<id>.toml` (`aware_at` = maintenant par défaut) et affiche les échéances.
- `tenon cra report mark <id> early-warning|notification|final|fix-available [--at <RFC 3339>]`.
- `tenon cra export --out <dir> [--lang fr|en]` : `cra-dossier.json` et `cra-dossier.html` (même gabarit et même style que l'export de conformité) : produit, SBOM (composants, générateur, date), dernier scan avec VEX, signalements et respect des délais, période de support, parties narratives (`cra/docs/*.md` si présents, rendus en texte échappé).

Échéances (fonction pure `Deadlines(report, now)`) : alerte précoce `aware_at + 24h`, notification `aware_at + 72h`, rapport final `fix_available_at + 14j` (vulnérabilité ; inconnu tant que `fix_available_at` absent) ou `notification_at + 1 mois calendaire` (incident).

## 5. Base OSV et versions

- Chaque archive est lue sans être décompressée sur disque (`archive/zip`), entrées JSON OSV ; index mémoire par nom de paquet, construit à la demande pour les écosystèmes des SBOM.
- Correspondance purl → écosystème OSV : `golang`→`Go`, `npm`→`npm`, `cargo`→`crates.io`, `pypi`→`PyPI`, `maven`→`Maven`, `nuget`→`NuGet`, `gem`→`RubyGems`, `composer`→`Packagist`. Noms : Go = chemin de module, npm avec scope `@ns/name`, Maven `group:artifact`, PyPI normalisé (PEP 503).
- Comparateurs : semver 2.0 (Go avec préfixe `v` et pseudo-versions traitées comme pré-versions, npm, crates.io), PEP 440 (époque, release, pré/post/dev ; pas de local). Autres écosystèmes : seulement `versions` explicites ; sinon `to_verify`.
- Une plage OSV `introduced`/`fixed`/`last_affected` est évaluée dans l'ordre des événements ; `limit` ignoré.
- `withdrawn` présent : entrée ignorée.

## 6. Skills intégrées

- Embarquées dans le binaire (`embed`), source `builtin` ; une skill utilisateur ou projet du même nom l'emporte, sans avertissement ; `/skills` indique la source.
- `cra-sbom` : générer les SBOM avec l'outil de l'écosystème (`cyclonedx-gomod`, `@cyclonedx/cyclonedx-npm`, `cyclonedx-py`, `cargo cyclonedx`, `syft` en repli), les placer dans `cra/`, lancer `tenon cra status`.
- `cra-triage` : pour une vulnérabilité `open`, analyser l'usage réel du composant dans le code, proposer l'entrée VEX (état, justification, détail), l'écrire dans `cra/vex.cdx.json` après confirmation.
- `cra-report` : rédiger l'alerte précoce, la notification ou le rapport final à partir de `cra/reports/<id>.toml`, avec les rubriques attendues par l'article 14 ; rappeler que l'envoi se fait sur la plateforme de l'ENISA ; marquer l'étape avec `tenon cra report mark` après confirmation.
- `cra-docs` : rédiger `cra/docs/*.md` (analyse de risque, sécurité par défaut, instructions de mise à jour et de fin de support).

## 7. Journal et export de conformité

- Événements : `CRAScan {sboms, components, open, to_verify, not_affected, fixed, db_date}`, `CRAReport {id, kind, step: "open"|"early-warning"|"notification"|"final"|"fix-available", at}`. Écrits dans le journal d'audit quand une commande `tenon cra` modifie l'état ou scanne (hors session : une session courte de type `cra` est ouverte puis fermée, comme `audit timestamp`).
- Export de conformité : section « CRA » : dernier scan (date, ouvertes, à vérifier), signalements avec respect des délais (en retard → ligne marquée, pas de code 2).
- `tenon config check` : `cra: dossier <ok|absent|invalid>, osv db <date|absent>`.

## 8. Architecture

- `internal/cra/dossier` : `Product`, `Report`, `VEX`, `Load(root)`, `Deadlines(r Report, now time.Time) []Deadline`, validation.
- `internal/cra/sbom` : `Read(path) (BOM, error)`, `Component{Name, Version, PURL, BOMRef}`, `ParsePURL`.
- `internal/cra/osv` : `Update(ctx, client, base string, ecos []string, dir string) error`, `Open(dir) (*DB, error)`, `(*DB) Match(c sbom.Component) []Finding`, comparateurs `semver.go`, `pep440.go`.
- `internal/cra/scan` : `Run(dossier, boms, db) Result` (VEX appliqué).
- `internal/cra/report` : export HTML/JSON.
- `internal/skills` : catalogue des skills intégrées (`embed`), priorité utilisateur/projet.
- `cmd/tenon/cra.go` : sous-commandes, codes de sortie, journal.
- `internal/event`, `internal/compliance`, `internal/config` (`[cra] osv_mirror`), `internal/i18n`, docs EN/FR (`docs/cra.md`, `docs/fr/cra.md`, usage).

## 9. Tests

- `semver`/`pep440` : tables issues des spécifications (pré-versions, métadonnées de build ignorées, époques, post/dev), pseudo-versions Go.
- `osv` : archive fabriquée (zip en mémoire) avec entrées `SEMVER`, `ECOSYSTEM`, `versions`, `withdrawn` ; appariement par écosystème ; nom npm à scope, Maven, PyPI normalisé.
- `osv.Update` : faux miroir `httptest` ; redirection vers http refusée ; taille limite ; remplacement atomique ; index.
- `scan` : SBOM d'exemple + base fabriquée + VEX (`not_affected` écarte, `affected` reste ouverte) ; `to_verify` ; code 7 avec `--fail-on open`.
- `dossier` : champs manquants, catégorie inconnue, `not_affected` sans justification, échéances (24 h, 72 h, 14 j, 1 mois calendaire, fin de février), horloge injectée.
- Bout en bout : `cra init`, `report open`/`mark`, `status` code 7 sur échéance dépassée, `scan` sans base code 1, `strict-local` sans miroir code 1, `export` (gabarit d'or), événements dans le journal, section CRA de l'export de conformité.
- Skills : les quatre présentes avec la source `builtin` ; une skill utilisateur `cra-triage` la remplace.

## 10. Hors périmètre

Envoi à la plateforme de l'ENISA, génération de SBOM par tenon, évaluation de conformité et marquage CE, déclaration UE de conformité, écosystèmes autres que ceux du §5 pour l'ordre des versions, CSAF, attestation de provenance (feature séparée), suivi multi-produits dans un même dépôt.
