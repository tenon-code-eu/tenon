# Assistant Cyber Resilience Act

`tenon cra` aide un fabricant de produits comportant des éléments numériques à tenir, dans son dépôt, un dossier vérifiable pour le règlement (UE) 2024/2847 (Cyber Resilience Act, « CRA ») : SBOM, statut de chaque vulnérabilité (VEX), échéances de signalement et dossier technique exportable.
Les commandes sont déterministes : pas de modèle, pas de réseau sauf `tenon cra db update`. Elles fonctionnent en CI. Des skills intégrés guident l'agent quand il doit rédiger.

## Sommaire

- [Contexte réglementaire](#contexte-réglementaire)
- [Dossier dans le dépôt](#dossier-dans-le-dépôt)
- [Commandes et codes de sortie](#commandes-et-codes-de-sortie)
- [Scan](#scan)
- [Base OSV hors ligne](#base-osv-hors-ligne)
- [Ordre des versions](#ordre-des-versions)
- [Signalements et échéances](#signalements-et-échéances)
- [Export](#export)
- [CI](#ci)
- [Skills intégrés](#skills-intégrés)
- [Limites](#limites)

## Contexte réglementaire

Les sources sont secondaires : vérifiez-les dans le texte du règlement avant de vous y fier.

- L'article 14 (signalement des vulnérabilités activement exploitées et des incidents graves) s'applique depuis le 11 septembre 2026. Le reste du règlement s'applique à partir du 11 décembre 2027.
- Les signalements passent par la plateforme unique de signalement de l'ENISA.
- Délais pour une vulnérabilité : alerte précoce 24 h après la prise de connaissance, notification 72 h après, rapport final 14 jours après la mise à disposition d'une mesure corrective.
- Délai pour un incident grave : rapport final un mois calendaire après la notification (à confirmer dans le texte du règlement).

tenon n'envoie rien à l'ENISA ni à aucune autorité. Il suit les échéances et aide à rédiger le texte ; l'envoi est manuel.

## Dossier dans le dépôt

```
cra/
  product.toml
  sbom*.cdx.json          # CycloneDX JSON 1.4 à 1.6
  vex.cdx.json            # CycloneDX VEX
  reports/<id>.toml       # un signalement par fichier, id = AAAA-MM-JJ-<n>
  docs/*.md               # parties rédigées, facultatives
```

`tenon cra init` crée `cra/product.toml` (modèle commenté) et `cra/reports/`. Il refuse de s'exécuter si `product.toml` existe.

### product.toml

```toml
name = "Mon produit"
version = "2.3.0"
manufacturer = "Exemple SAS"
security_contact = "security@exemple.eu"
support_end = 2031-12-31          # fin de la période de support
category = "default"              # default | important-1 | important-2 | critical
sboms = ["cra/sbom.cdx.json"]     # défaut : cra/sbom*.cdx.json
```

Tous les champs sauf `sboms` sont obligatoires : `cra: product.toml: <champ> required`. Une catégorie inconnue donne `cra: product.toml: category must be default, important-1, important-2 or critical`.

### SBOM

tenon lit du CycloneDX JSON 1.4 à 1.6 et ne génère pas de SBOM : utilisez l'outil de votre écosystème (`cyclonedx-gomod`, `@cyclonedx/cyclonedx-npm`, `cyclonedx-py`, `cargo cyclonedx`, `syft` en repli). Les composants imbriqués sous `components[].components` sont collectés. Les qualificateurs de purl (`?type=jar`) et les sous-chemins sont ignorés pour l'appariement.

### VEX

`vulnerabilities[]` de `cra/vex.cdx.json` porte `id`, `affects[].ref` (purl ou bom-ref du composant) et `analysis.state` :

| État | Effet |
|---|---|
| `not_affected` | Non comptée comme ouverte. Exige `analysis.justification`, sinon `cra: vex: <id>: not_affected requires a justification`. |
| `affected` | Ouverte. |
| `under_investigation` | Ouverte. |
| `in_triage` | Lu comme `under_investigation`. |
| `fixed` | Résolue. |

Les valeurs CycloneDX qu'on trouve dans les exports d'autres outils sont converties : `in_triage` en `under_investigation`, `false_positive` en `not_affected` (avec une justification), `resolved_with_pedigree` en `fixed`.

### Signalements

`cra/reports/<id>.toml` :

```toml
kind = "vulnerability"            # vulnerability | incident
summary = "..."
aware_at = 2026-10-23T09:12:00Z
fix_available_at = 2026-10-25T00:00:00Z
early_warning_at = 2026-10-23T15:00:00Z   # renseigné par `report mark`
notification_at = 2026-10-24T08:00:00Z
final_at = ...
```

Les heures sont en RFC 3339 et le fuseau est obligatoire (`2026-10-23T09:12:00+02:00` ou `Z`). Une heure sans fuseau est refusée. Les échéances sont calculées en UTC : un changement d'heure ne décale pas les délais de 24 h et 72 h.

## Commandes et codes de sortie

| Commande | Rôle | Codes de sortie |
|---|---|---|
| `tenon cra init` | Créer le squelette du dossier. | 0, 1 |
| `tenon cra db update [--mirror <url>] [--ecosystem <eco>...]` | Télécharger la base OSV (seul usage du réseau). | 0, 1 |
| `tenon cra scan [--fail-on open] [--json]` | Apparier les composants des SBOM à la base et appliquer le VEX. | 0 ; 7 avec `--fail-on open` et au moins une vulnérabilité ouverte ; 1 en cas d'erreur |
| `tenon cra status` | Complétude du dossier, dernier scan, signalements et échéances. | 0 ; 7 si une échéance est dépassée ; 1 si le dossier est invalide |
| `tenon cra report open --kind vulnerability\|incident --summary <texte> [--aware-at <RFC 3339>]` | Créer `cra/reports/<id>.toml` (`aware_at` = maintenant par défaut) et afficher les échéances. | 0, 1 |
| `tenon cra report mark <id> early-warning\|notification\|final\|fix-available [--at <RFC 3339>]` | Enregistrer une étape. | 0, 1 |
| `tenon cra export --out <dir> [--lang fr\|en]` | Dossier technique, HTML et JSON. | 0, 1 |

`status` signale aussi un dossier incomplet (SBOM absente, période de support échue) par des lignes de texte, sans changer le code de sortie : seule une échéance dépassée donne 7, et seul un dossier invalide donne 1.

`tenon config check` affiche `cra: dossier <ok|absent|invalid>, osv db <date|absent>`.

Les commandes qui scannent ou modifient un signalement écrivent un événement dans le journal d'audit (`CRAScan`, `CRAReport`). Hors session, tenon ouvre une courte session de type `cra`, écrit l'événement et la ferme.

## Scan

`tenon cra scan` lit les SBOM, apparie chaque composant (purl `pkg:<type>/<namespace>/<name>@<version>`) aux entrées OSV de son écosystème, puis applique le VEX. Le résumé donne :

- ouvertes : sans entrée VEX, ou `affected` ou `under_investigation` ;
- à vérifier : l'écosystème n'a pas de comparateur de versions et la version est absente de la liste `versions` d'OSV ;
- non affectées et corrigées : d'après le VEX ;
- conflits : le VEX dit `fixed` alors qu'OSV liste la version exacte comme affectée. L'entrée reste résolue, à cause des rétroportages, et un avertissement est affiché. Vérifiez que la construction contient bien le correctif ;
- non vérifiés : composants dont la purl est vide ou illisible, ou d'un type que tenon ne sait pas associer (comme `deb` ou `apk`). Ils ne sont jamais comptés comme sains.

Erreurs avec le code 1 :

- aucune SBOM trouvée ;
- pas de base : `cra: no vulnerability database (run tenon cra db update)` ;
- un écosystème des SBOM manque dans la base (le scan n'affirme pas « aucune vulnérabilité » pour lui) ;
- une archive corrompue (signalée comme illisible, jamais comme « aucune vulnérabilité »).

Une base de plus de 7 jours produit l'avis `cra: vulnerability database is <n> days old`. Un résultat est rapporté une fois par (vulnérabilité, composant), et une entrée `withdrawn` est ignorée.

## Base OSV hors ligne

Les vulnérabilités viennent des archives [OSV](https://osv.dev), appariées en local : la liste de vos dépendances ne quitte pas la machine.

- Base par défaut : `https://storage.googleapis.com/osv-vulnerabilities`. Archive : `<base>/<ECO>/all.zip`.
- Autre base (miroir) : `--mirror <url>` ou, dans la configuration globale seulement, `[cra] osv_mirror = "<url>"`. La clé est refusée dans une configuration de projet : `config: cra not allowed in project configuration`.
- https uniquement. http n'est accepté que vers un hôte local. Les redirections doivent être en https. Chaque archive est limitée à 1 Gio.
- Mise à jour par écosystème : sans `--ecosystem`, tenon met à jour les écosystèmes des SBOM.
- Stockage : `<data>/cra/osv/<ECO>/all.zip` et `<data>/cra/osv/index.json` (`{eco: {date, sha256, entries}}`). Le remplacement est atomique : un téléchargement échoué ou tronqué conserve l'archive précédente.
- Un `index.json` corrompu est mis de côté et reconstruit.
- Type de purl vers écosystème OSV : `golang` vers `Go`, `npm`, `cargo` vers `crates.io`, `pypi` vers `PyPI`, `maven`, `nuget`, `gem` vers `RubyGems`, `composer` vers `Packagist`.

Sous le profil `strict-local` :

- un miroir local est obligatoire, sinon `cra: profile strict-local requires a local osv mirror` (code 1) ;
- les redirections doivent rester sur des hôtes locaux ;
- les proxys d'environnement sont ignorés. Ailleurs ils sont respectés.

La liste de fournisseurs autorisés de la politique d'organisation ne s'applique pas à OSV, qui n'est pas un fournisseur.

## Ordre des versions

- semver pour Go (pseudo-versions comprises, traitées comme des pré-versions), npm et crates.io ;
- PEP 440 pour PyPI (époque, release, pré, post et dev ; les étiquettes locales sont ignorées) ;
- la bibliothèque standard Go et les versions de toolchain de la forme `go1.x` sont gérées ;
- les noms NuGet sont comparés sans tenir compte de la casse ;
- les autres écosystèmes (Maven, RubyGems, Packagist...) utilisent les listes `versions` explicites d'OSV. Une liste complète signifie qu'une version absente n'est pas affectée. Sinon le résultat est « à vérifier » ;
- les plages OSV (`introduced`, `fixed`, `last_affected`) sont évaluées après tri des événements ; `limit` est ignoré.

Une entrée OSV peut avoir plusieurs blocs `affected[]` pour le même paquet (par exemple la bibliothèque standard Go et un module) ou toucher plusieurs paquets : chaque paquet est apparié selon ses propres plages.

## Signalements et échéances

| Étape | Vulnérabilité | Incident |
|---|---|---|
| Alerte précoce | `aware_at` + 24 h | `aware_at` + 24 h |
| Notification | `aware_at` + 72 h | `aware_at` + 72 h |
| Rapport final | `fix_available_at` + 14 jours | `notification_at` + 1 mois calendaire (à confirmer dans le texte du règlement) |

Pour une vulnérabilité, l'échéance finale est inconnue tant que `fix_available_at` est absent : `status` affiche « échéance inconnue » et ne la masque pas. Chaque étape est `due`, `overdue` ou `done`.

## Export

`tenon cra export --out <dir> [--lang fr|en]` écrit `cra-dossier.json` et `cra-dossier.html`, avec le même gabarit et le même style que l'export de conformité : produit, SBOM (composants, générateur, date), dernier scan avec le VEX, signalements et respect des délais, période de support, et les parties rédigées `cra/docs/*.md` (rendues en texte échappé).
Sans base ni SBOM, l'export fonctionne quand même, sans résultats de scan, et des avis disent ce qui manque.

`tenon audit export` ajoute une section « CRA » : dernier scan (date, ouvertes, à vérifier) et signalements avec respect des délais. Les échéances sont calculées sur tout le journal, quel que soit `--since`. Les signalements sont listés tant que leur étape finale n'est pas faite, et « échéances inconnues » reste visible. Une échéance dépassée marque la ligne, sans code de sortie 2.

## CI

```
tenon cra db update && tenon cra scan --fail-on open
```

Le code 7 fait échouer le job tant qu'une vulnérabilité est ouverte. Ajoutez `tenon cra status` pour échouer sur une échéance de signalement dépassée. Dans un pipeline isolé, définissez `[cra] osv_mirror` vers un miroir interne dans la configuration globale.

## Skills intégrés

Quatre skills sont embarqués dans le binaire, avec la source `builtin` (la priorité la plus basse). `/skills` et `tenon skills` affichent la source.

| Skill | Rôle |
|---|---|
| `cra-sbom` | Générer les SBOM avec l'outil de l'écosystème, les placer dans `cra/`, lancer `tenon cra status`. |
| `cra-triage` | Pour chaque vulnérabilité ouverte, analyser l'usage réel du composant, proposer l'entrée VEX, l'écrire après confirmation. |
| `cra-report` | Rédiger l'alerte précoce, la notification ou le rapport final à partir de `cra/reports/<id>.toml`, puis marquer l'étape après confirmation. |
| `cra-docs` | Rédiger `cra/docs/*.md` (analyse de risque, sécurité par défaut, mises à jour et fin de support). |

Les descriptions des skills intégrés ne sont ajoutées au prompt système que si la racine du projet contient un dossier `cra/`. Ils restent dans le catalogue dans tous les projets : `/skills` les liste et `/cra-sbom` ou l'outil `skill` les chargent par leur nom.

Chaque skill demande votre confirmation avant d'écrire un fichier, et rappelle que les signalements s'envoient à la main sur la plateforme de l'ENISA.
Pour en remplacer un, créez un skill du même nom (par exemple `.tenon/skills/cra-triage/SKILL.md`, ou dans votre dossier utilisateur). Un skill utilisateur ou projet l'emporte sans avertissement. Voir les [skills](utilisation.md#skills).

## Limites

Hors périmètre :

- l'envoi de quoi que ce soit à la plateforme de l'ENISA ;
- la génération de SBOM ;
- l'évaluation de conformité, le marquage CE et la déclaration UE de conformité ;
- l'ordre des versions pour d'autres écosystèmes que ceux ci-dessus (ils reposent sur des listes de versions explicites) ;
- CSAF et l'attestation de provenance ;
- plusieurs produits dans un même dépôt.

tenon aide à conserver des preuves ; le fabricant reste responsable de leur exactitude et des délais légaux. Vérifiez les dates et durées dans le texte du règlement.
