# tenon en CI

tenon s'exécute dans un pipeline en mode non interactif (`-p`), sans terminal et sans validation : toute action qui en demanderait une est refusée. Cette page donne des modèles prêts à copier pour GitLab CI, Forgejo/Gitea Actions et GitHub Actions, et explique comment conserver les preuves de chaque exécution.

## Sommaire

- [Modèles](#modèles)
- [Ce que fait le job](#ce-que-fait-le-job)
- [Codes de sortie](#codes-de-sortie)
- [Configuration du job](#configuration-du-job)
- [Installer depuis un miroir](#installer-depuis-un-miroir)
- [Artefacts](#artefacts)
- [Sécurité](#sécurité)

## Modèles

| Forge | Modèle |
|---|---|
| GitLab CI | [docs/ci/gitlab-ci.yml](../ci/gitlab-ci.yml) |
| Forgejo et Gitea Actions | [docs/ci/forgejo.yml](../ci/forgejo.yml) |
| GitHub Actions | [docs/ci/github.yml](../ci/github.yml) |
| Configuration du job | [docs/ci/tenon.toml](../ci/tenon.toml) |

Copiez le modèle de votre forge et la configuration dans `ci/tenon.toml` de votre dépôt. Ensuite :

1. Définissez la clé du fournisseur (`MISTRAL_API_KEY` dans l'exemple) en variable masquée et protégée (GitLab) ou en secret (Forgejo, Gitea, GitHub).
2. Épinglez la version : les modèles installent `v0.7.0`. Relevez-la volontairement, n'utilisez jamais une version flottante.
3. Lancez le job à la main avec la tâche (`TENON_TASK` dans GitLab, l'entrée `task` ailleurs), ou sur les merge requests et pull requests de votre propre dépôt.

## Ce que fait le job

1. **Installer.** `TENON_VERSION=<vX.Y.Z> TENON_REQUIRE_SIGNATURE=1 sh install.sh`. L'image contient `minisign` (les modèles l'installent avec `apt-get`) : la signature de la version est vérifiée, pas seulement son empreinte SHA-256.
2. **Exécuter.** `XDG_DATA_HOME="$PWD/.tenon-data"` garde le journal d'audit dans l'espace de travail. Puis `tenon --config ci/tenon.toml -p "$TENON_TASK" --strict --output json > tenon-events.jsonl`. Le code de sortie du job est celui de tenon.
3. **Toujours, même en cas d'échec.** `tenon audit verify` (intégrité de la chaîne de hachage) et `tenon audit export --all-projects --out tenon-audit` (rapport de conformité), puis le dépôt des artefacts.

## Codes de sortie

| Code | Signification en CI |
|---|---|
| `0` | Tour terminé. |
| `1` | Erreur : configuration, clé absente, fournisseur, option invalide. |
| `3` | Une action a été refusée (aucune validation possible en CI, ou règle `deny`) ou l'envoi au fournisseur a été bloqué par la juridiction. |
| `4` | Avec `--strict` : le tour s'est terminé par `max_iterations`, `incomplete`, `stopped_by_hook` ou `cancelled`. Sans `--strict`, ces cas donnent 0. |
| `130` | Seconde interruption (Ctrl+C ou arrêt forcé) : sortie immédiate. |

Le code 3 l'emporte sur 4, et le code 1 l'emporte sur les deux. Voir [Codes de sortie](utilisation.md#codes-de-sortie). Utilisez `--strict` en CI : un job arrêté par la limite d'itérations ne doit pas passer pour un succès.

## Configuration du job

`--config <fichier>` remplace le fichier de configuration global (`$XDG_CONFIG_HOME/tenon/config.toml`) par un fichier du dépôt : le job ne dépend pas du répertoire personnel du runner. Le fichier projet `.tenon/config.toml` et la politique d'organisation s'appliquent toujours : une politique ne fait que durcir ce que dit `ci/tenon.toml`. Le fichier doit exister, sinon tenon sort avec le code 1.

[docs/ci/tenon.toml](../ci/tenon.toml) utilise le préréglage `mistral`, qui lit sa clé dans `MISTRAL_API_KEY` (un nom de variable, jamais la clé), fixe le profil `eu-only` et ajoute une seule règle `allow`, `go test ./...` (les lectures dans le projet sont déjà autorisées par défaut), `[audit] level = "full"` et, en commentaire, `[audit.timestamp]`. Vérifiez-le sans réseau :

```
MISTRAL_API_KEY=x tenon --config ci/tenon.toml config check
```

## Installer depuis un miroir

`TENON_DOWNLOAD_BASE` remplace GitHub comme source de la version (forge souveraine, miroir isolé du réseau). Elle exige `TENON_VERSION`, n'accepte que `https://` (et `http://localhost` ou `http://127.0.0.1` pour les tests), et les redirections doivent être en `https://`. Le miroir contient `<base>/<version>/<archive>`, `SHA256SUMS` et `SHA256SUMS.minisig`.

Chaque modèle a la ligne en commentaire. La signature minisign reste vérifiée avec `TENON_REQUIRE_SIGNATURE=1` : un miroir compromis ne peut pas servir un binaire falsifié.

## Artefacts

| Artefact | Contenu |
|---|---|
| `tenon-events.jsonl` | Un événement JSON par ligne (`--output json`), charge utile masquée. |
| `tenon-audit/` | Export de conformité : rapport HTML et JSON. |
| `.tenon-data/tenon/audit/` | Le journal d'audit lui-même (chaîne de hachage) et, si `[audit.timestamp]` est défini, les jetons RFC 3161. |

Voir [Journal d'audit](utilisation.md#journal-daudit), [Horodatage](utilisation.md#horodatage-rfc-3161) et [Export de conformité](utilisation.md#export-de-conformité) pour `audit verify`, `audit export` et `audit timestamp`. Les artefacts sont conservés même si le job échoue.

## Sécurité

**Une merge request peut modifier `ci/tenon.toml` comme elle modifie le pipeline.** Qui change la configuration change le fournisseur, le profil et les permissions du job. Soit le job ne s'exécute que sur des branches protégées avec des variables protégées, soit la configuration vient d'un dépôt séparé que les merge requests ne peuvent pas modifier (extrayez-le et passez son chemin à `--config`).

**La clé n'existe que sur les références protégées (GitLab).** Une variable protégée n'atteint que les pipelines des branches ou étiquettes protégées : la règle de merge request du modèle exige donc une branche source protégée. Retirer la protection exposerait la clé au pipeline de n'importe quelle branche, y compris une branche qui réécrit `ci/tenon.toml` : cela fonctionne, mais ce n'est pas recommandé.

**Gardez les règles `allow` minimales.** En CI personne ne valide rien : chaque règle `allow` est une autorisation permanente donnée au modèle. N'autorisez que les commandes exactes dont la tâche a besoin (`go test ./...`), rien de plus large. Une règle `allow` sur une commande ne couvre pas une commande enchaînée, mais les permissions ne sont pas un bac à sable (voir [Sécurité et limites](securite.md)) : exécutez le job sur un runner qui ne contient rien que vous ne puissiez perdre.

**Ne lancez jamais un job avec des secrets sur les merge requests de forks.** L'auteur d'un fork contrôle le code, `ci/tenon.toml` et la tâche. Les modèles ignorent les pipelines de forks : gardez cette protection.

**Un runner éphémère ne garde rien.** Le journal d'audit, l'export de conformité et les jetons d'horodatage disparaissent avec le runner. Archivez les artefacts, jetons d'horodatage compris, hors du runner (stockage d'artefacts avec une durée de rétention, ou copie vers un espace que vous maîtrisez). Un journal non archivé ne peut pas être vérifié plus tard.
