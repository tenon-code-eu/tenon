# Politique d'organisation

Une organisation peut imposer à tous ses postes un socle que l'utilisateur ne peut pas assouplir : profil minimal, fournisseurs autorisés, règles de permissions et de masquage, niveau d'audit, serveurs MCP autorisés.
La politique est un fichier déployé par l'administrateur (MDM, Ansible), signé avec [minisign](https://jedisct1.github.io/minisign/) et vérifié à chaque démarrage.
Elle est tracée dans le journal d'audit et dans l'export de conformité.

Sans politique, tenon se comporte comme décrit dans [utilisation.md](utilisation.md). Une politique ne fait que durcir : elle n'élargit jamais ce que la configuration permet.

## Emplacement

Le dossier système contient trois fichiers :

| Plateforme | Dossier |
|---|---|
| Linux | `/etc/tenon/` |
| macOS | `/Library/Application Support/tenon/` |

| Fichier | Contenu |
|---|---|
| `policy.toml` | la politique |
| `policy.toml.minisig` | signature minisign de `policy.toml` |
| `policy.pub` | la clé publique de l'organisation (format `.pub` de minisign) |

Le dossier et les trois fichiers doivent appartenir à root (uid 0) et ne pas être modifiables par le groupe ni par les autres (par exemple `chmod 755` sur le dossier et `chmod 644` sur les fichiers). Sinon tenon refuse de démarrer.

`TENON_POLICY_DIR` désigne un autre dossier. Elle n'est lue **que si le dossier système n'a pas de `policy.toml`** : elle peut ajouter une politique (politique personnelle ou de test), jamais en retirer ou en remplacer une système. Les permissions de ce dossier ne sont pas contrôlées ; la signature l'est toujours.

Pas de `policy.toml` dans l'un ni l'autre : pas de politique.

## Signer une politique

```sh
minisign -G -p policy.pub -s org.key       # une fois : paire de clés
minisign -Sm policy.toml                   # écrit policy.toml.minisig
sudo install -o root -m 644 policy.toml policy.toml.minisig policy.pub /etc/tenon/
```

Les deux formats de signature minisign sont acceptés : préhaché (`ED`) et historique (`Ed`).
L'identifiant de clé de la signature doit être celui de `policy.pub`, et la signature du fichier comme la signature globale (commentaire de confiance) doivent être valides.
Toute modification de `policy.toml` après la signature fait refuser le démarrage.

## Contenu

```toml
id = "acme-dev"                          # obligatoire, [a-z0-9_-]{1,64}
version = 3                              # obligatoire, entier >= 1
profile = "eu-only"                      # optionnel : plancher

[providers]                              # optionnel : liste blanche
allow_hosts = ["api.mistral.ai", "*.scaleway.ai"]
allow_local = true                       # hôtes locaux (localhost, littéraux IP de boucle et privés) autorisés

[[providers.define]]                     # optionnel : fournisseurs fournis par l'organisation
id = "interne"
name = "vLLM interne"
base_url = "https://llm.acme.eu/v1"
api_key_env = "ACME_LLM_KEY"
model = "qwen3"
country = "FR"
jurisdiction = "eu"
context_window = 128000

[[permissions.rules]]                    # deny / ask seulement
tool = "shell"
match = "curl *"
decision = "deny"

[[redaction.rules]]
name = "acme-token"
pattern = "ACME-[A-Z0-9]{32}"

[audit]
level = "full"                           # optionnel : full | metadata, imposé

[mcp]
mode = "allowlist"                       # any (défaut) | allowlist | off
allow_hosts = ["mcp.acme.eu"]
allow_commands = ["/usr/local/bin/mcp-git"]
```

Seuls `id` et `version` sont obligatoires.

Valeurs refusées au chargement : `id` ou `version` absents ou invalides ; profil inconnu ; règle `allow` ; décision ou outil invalides (mêmes règles que la configuration) ; motif de masquage qui ne compile pas ; entrée de `allow_hosts` vide, avec schéma, chemin, port ou joker ailleurs qu'en tête (`*.` suivi d'au moins deux labels) ; entrée de `allow_commands` non absolue ; `mcp.mode` inconnu ; `audit.level` inconnu ; entrée `providers.define` invalide (mêmes règles que `[[providers]]`, `jurisdiction` obligatoire) ; clé inconnue.

## Application

Ordre de fusion : défauts, configuration globale, configuration projet, politique.

- **Profil** : le profil effectif est le plus strict de ceux de la configuration globale, du projet et de la politique. Si la configuration en demande un plus faible, tenon affiche une notice et le relève : `profil « <c> » de la configuration plus souple que le profil « <p> » de la politique <id> : relevé`.
- **Fournisseurs définis** : fusionnés après la configuration de l'utilisateur. Un fournisseur de l'utilisateur de même `id` est ignoré, avec un avertissement : `politique <id> : fournisseur « <pid> » de la configuration ignoré (défini par la politique)`.
- **Liste blanche de fournisseurs** (seulement si `[providers]` est présent) : l'hôte de `base_url` du fournisseur (minuscules, sans port) doit être égal à une entrée de `allow_hosts`, ou couvert par une entrée `*.x.eu` (tout sous-domaine de `x.eu`, jamais `x.eu` lui-même), ou être local avec `allow_local = true`.
  - `[providers]` présent sans `allow_hosts` et avec `allow_local = false` refuse tous les fournisseurs.
  - Les fournisseurs définis par la politique (`define`) sont soumis à `allow_hosts` comme les autres : listez leurs hôtes.
  - Un fournisseur choisi non autorisé arrête tenon au démarrage, avant tout accès réseau, avec le code 1 : `fournisseur « <pid> » (hôte « <h> ») non autorisé par la politique <id>`. `--allow-non-eu` ne lève pas ce refus.
  - Le même contrôle s'exécute avant chaque envoi, après celui du profil : rien n'est envoyé et le tour est bloqué (événement `Error`, `code: "policy"`).
- **Règles de permissions** : placées en tête. `deny` est absolue ; `ask` prime les règles `allow` de l'utilisateur.
- **Masquage** : les règles de la politique s'ajoutent aux autres.
- **Niveau d'audit** : `audit.level` de la politique remplace celui de la configuration, dans les deux sens : une organisation peut imposer `metadata` (minimisation des données) ou `full` (traçabilité). S'ils diffèrent, une notice est affichée : `politique <id> : niveau d'audit « <p> » imposé (configuration : « <c> »)`.
- **MCP** : `off` retire tous les serveurs ; `allowlist` garde un serveur seulement si son hôte (`http`) figure dans `allow_hosts` (même correspondance que les fournisseurs, sans `allow_local`) ou si `command[0]` (`stdio`) figure tel quel dans `allow_commands`. Un serveur retiré n'est pas démarré, avec une notice (`politique <id> : serveur MCP « <sid> » ignoré (non autorisé)`) et un événement `McpServerDown`. Le contrôle MCP du profil s'applique aussi aux serveurs gardés.

## Échecs au démarrage

tenon refuse de démarrer (code 1) quand une politique est présente mais pas digne de confiance. Le message est :

```
tenon : politique : <fichier> : <raison>
```

En anglais : `tenon: policy: <file>: <reason>`.

Causes :

- `policy.toml.minisig` ou `policy.pub` manque ;
- l'identifiant de clé de la signature diffère de celui de `policy.pub` ;
- la signature du fichier ou la signature globale est invalide ;
- dossier système seulement : le dossier ou un des trois fichiers est modifiable par le groupe ou les autres, ou n'appartient pas à root ;
- le TOML est invalide, contient une clé inconnue, ou une valeur est refusée (voir plus haut).

La politique est vérifiée une fois, au démarrage, avant le chargement de la configuration. `tenon config check` échoue de la même façon. Un fournisseur non autorisé est une erreur de démarrage distincte (voir plus haut).

## Vérifier une politique : `tenon policy check`

```sh
tenon policy check [dossier]
```

Vérifie la signature et le contenu de la politique du `dossier` (défaut : le dossier système) et affiche un résumé. Les propriétaire et permissions ne sont pas contrôlés : vous pouvez valider une politique avant de la déployer. La configuration de l'utilisateur et du projet n'est pas lue.

```
politique : acme-dev v3 (clé 0123456789ABCDEF)
profil : eu-only
fournisseurs : 2 hôtes autorisés, local oui, 1 définis
règles : 1
masquage : 1
audit : full
mcp : allowlist
```

Sans `[providers]` la ligne est `fournisseurs : pas de liste blanche` ; sans profil `profil : aucun` ; sans niveau d'audit `audit : non imposé`.
Code 0 si la politique est valide ; 1 si elle est invalide ou absente (`tenon : aucune politique dans <dossier>`).

## Traçabilité

- L'écran d'accueil et `tenon config check` affichent `politique : acme-dev v3 (clé <KeyID>)` quand une politique est active. L'identifiant de clé fait 16 caractères hexadécimaux majuscules, comme dans minisign.
- `SessionStarted` enregistre `policy` (`id`, `version`, `key_id`). Les sessions sans politique n'ont pas cette clé.
- L'[export de conformité](utilisation.md#export-de-conformité) liste les politiques des sessions dans la plage (clé `policies`), avec le nombre de sessions de chacune. Au niveau metadata, la politique est inconnue : le rapport indique « non disponible au niveau metadata ».

## Limites

Une politique borne ce que fait tenon. Elle ne restreint ni les commandes shell ni les programmes MCP stdio, et un administrateur de la machine peut toujours la retirer. Les hooks imposés par l'organisation, le rafraîchissement par URL, l'expiration ou la révocation, plusieurs clés de confiance et Windows ne sont pas pris en charge.
