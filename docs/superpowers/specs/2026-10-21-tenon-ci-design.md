# tenon : intégration CI pour forges souveraines (v0.7)

Date : 2026-10-21. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Specs liées : [export de conformité](2026-10-20-tenon-audit-export-design.md), [horodatage RFC 3161](2026-10-21-tenon-audit-timestamp-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md).

## 1. Objectif

Faire tourner tenon dans un job de CI (GitLab CI, Forgejo/Gitea Actions, GitHub Actions) : une tâche lancée par `tenon -p`, un échec strict quand le tour ne se termine pas proprement, et la preuve (événements, export de conformité, journal et jetons d'horodatage) publiée en artefact.

Critères de succès :

- un tour arrêté (limite d'itérations, réponse incomplète, hook, annulation) fait échouer le job avec `--strict` ;
- un pipeline lit sa configuration depuis un fichier nommé, sans détour par `XDG_CONFIG_HOME` ;
- tenon s'installe en CI depuis un miroir souverain, version épinglée, signature vérifiée ;
- trois modèles prêts à copier, et une page de doc EN/FR ;
- sans `--strict` ni `--config`, le comportement actuel est inchangé.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Usage servi | Tâche + preuve en artefact | Cas commun aux trois forges ; pas d'API de forge à appeler |
| Sortie stricte | Option `--strict`, code 4 | Pas de rupture pour les scripts existants |
| Configuration | Option globale `--config <fichier>` | Lisible dans les pipelines ; la politique s'applique toujours |
| Livraison | Fichiers à copier dans `docs/ci/` | Pas de dépôt ni de publication par forge |
| Installation | `install.sh` + `TENON_DOWNLOAD_BASE` | Miroir souverain sûr : la clé minisign est dans le script |

## 3. CLI

`--strict` (avec `-p` seulement) :

- code de sortie `4` si le tour se termine avec la raison `max_iterations`, `incomplete`, `stopped_by_hook` ou `cancelled` ;
- les codes `0`, `1`, `3`, `130` sont inchangés ; `3` prime sur `4` (un refus pendant un tour ensuite incomplet rend 3) ;
- en sortie texte, ligne sur stderr : `strict: turn ended with <reason>` (traduite) ; en sortie JSON, rien de plus (la raison est dans les événements) ;
- sans `-p` : `--strict requires -p`, code 1 (comme `--output requires -p`).

`--config <fichier>` (option globale, toutes commandes) :

- remplace le chemin de la configuration globale (`$XDG_CONFIG_HOME/tenon/config.toml` par défaut) ;
- fichier absent : `config: <fichier>: no such file or directory`, code 1 (le chemin par défaut absent reste ignoré) ;
- la configuration projet `.tenon/config.toml` reste chargée et ne fait que restreindre ; la politique d'organisation s'applique par-dessus ;
- `tenon config check` affiche `config: <chemin>` (chemin effectif de la configuration globale, `(absent)` si le fichier par défaut n'existe pas).

Données : pas d'option ; les modèles posent `XDG_DATA_HOME` dans l'espace de travail du job.

## 4. Installation depuis un miroir

`install.sh` :

- `TENON_DOWNLOAD_BASE` : URL de base d'un miroir ; les fichiers sont lus à `<base>/<version>/<archive>`, `<base>/<version>/SHA256SUMS`, `<base>/<version>/SHA256SUMS.minisig` (même arborescence que les releases GitHub) ;
- avec `TENON_DOWNLOAD_BASE`, `TENON_VERSION` est obligatoire : `TENON_DOWNLOAD_BASE requires TENON_VERSION=vX.Y.Z.` ;
- `https://` obligatoire pour le miroir, sauf `http://` vers `localhost`/`127.0.0.1` (tests) : `TENON_DOWNLOAD_BASE must be https.` ;
- vérifications inchangées : hash SHA-256 toujours, signature minisign avec la clé intégrée au script (le miroir ne peut pas substituer un binaire) ; `TENON_REQUIRE_SIGNATURE=1` dans tous les modèles.

## 5. Modèles

Fichiers (commentaires en anglais, comme `docs/config.example.toml`) :

- `docs/ci/gitlab-ci.yml`, `docs/ci/forgejo.yml` (Forgejo et Gitea Actions), `docs/ci/github.yml` ;
- `docs/ci/tenon.toml` : fournisseur avec `api_key_env`, `profile`, règles `allow` minimales (lectures, `go test ./...`), `[audit] level = "full"`, `[audit.timestamp]` commenté.

Étapes communes :

1. installer : `TENON_VERSION=<vX.Y.Z épinglée> TENON_REQUIRE_SIGNATURE=1 sh install.sh` (minisign installé dans l'image ; `TENON_DOWNLOAD_BASE` commenté) ;
2. exécuter : `XDG_DATA_HOME="$PWD/.tenon-data"`, puis `tenon --config ci/tenon.toml -p "$TENON_TASK" --strict --output json > tenon-events.jsonl` ; le code de sortie de tenon est celui du job ;
3. toujours (même en échec) : `tenon audit verify`, `tenon audit export --all-projects --out tenon-audit`, artefacts `tenon-events.jsonl`, `tenon-audit/`, `.tenon-data/tenon/audit/` ;
4. déclenchement manuel ou sur MR/PR, tâche par variable (`TENON_TASK`), clé d'API en variable masquée et protégée.

## 6. Documentation

`docs/ci.md` et `docs/fr/ci.md` : les trois modèles, les codes de sortie 0/1/3/4, `--config`, `TENON_DOWNLOAD_BASE`, les artefacts. Avertissement de sécurité :

- une MR peut modifier `ci/tenon.toml` comme le pipeline : branches et variables protégées, ou configuration tirée d'un dépôt séparé ;
- règles `allow` minimales ; jamais de job avec secrets sur des MR de forks ;
- un runner éphémère ne garde rien : archiver les artefacts, jetons d'horodatage compris, hors du runner.

Mises à jour : `docs/usage.md` / `docs/fr/utilisation.md` (options `--strict`, `--config`, code 4), `README.md` (lien CI, `TENON_DOWNLOAD_BASE`).

## 7. Architecture

- `cmd/tenon/run.go` : options `--strict` et `--config` ; chemin global passé à `loadSetup` et aux sous-commandes qui chargent la configuration ; `config check`.
- `internal/ui/headless` : `Options.Strict bool` ; code `exitIncomplete = 4`.
- `internal/i18n` : `err.strict_requires_p`, `strict.turn_ended`, `config.path`.
- `install.sh` : `TENON_DOWNLOAD_BASE`.
- `docs/ci/*`, `docs/ci.md`, `docs/fr/ci.md`.

## 8. Tests

- `headless` : chaque raison stricte → 4 avec `Strict`, 0 sans ; refus + `incomplete` → 3.
- Bout en bout : `--strict` sans `-p` → 1 ; `--config` absent → 1, présent → utilisé (fournisseur lu), projet et politique toujours appliqués ; `tenon --config docs/ci/tenon.toml config check` réussit (clé par l'environnement).
- Modèles (contrôle textuel, aucun module YAML) : chacun contient `--strict`, `--config`, `TENON_REQUIRE_SIGNATURE=1`, `audit verify`, `audit export`, une version qui suit `v[0-9]+\.[0-9]+\.[0-9]+` et aucune occurrence de `latest`.
- `install.sh` (test Go qui lance `sh install.sh` avec un faux miroir `httptest`, `PATH` restreint) : miroir sans `TENON_VERSION` refusé ; miroir `http://` distant refusé ; hash faux refusé ; `TENON_REQUIRE_SIGNATURE=1` sans minisign refusé ; hash bon sans minisign → binaire installé dans `TENON_INSTALL_DIR`.

## 9. Hors périmètre

Commentaires sur les MR/PR, composants publiés par forge (composant GitLab, action Forgejo), commande `tenon ci init`, cache entre jobs, image conteneur officielle.
