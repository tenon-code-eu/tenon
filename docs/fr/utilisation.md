# Guide d'utilisation

*Traduction française de [la version anglaise](../usage.md). En cas de différence, la version anglaise fait foi.*

Ce guide décrit le comportement de tenon tel qu'il est codé aujourd'hui.
Pour les garanties et les limites de sécurité, voir [securite.md](securite.md).
Pour un exemple de configuration commenté, voir [config.example.toml](../config.example.toml).

## Sommaire

- [Commandes](#commandes)
- [Interface interactive](#interface-interactive)
- [Mode non interactif](#mode-non-interactif)
- [Sessions](#sessions)
- [Langue](#langue)
- [Configuration](#configuration)
- [Skills](#skills)
- [Sous-agents](#sous-agents)
- [Fournisseurs et juridictions](#fournisseurs-et-juridictions)
- [Clés d'API et trousseau](#clés-dapi-et-trousseau)
- [Serveurs MCP](#serveurs-mcp)
- [Marquage git du code produit](#marquage-git-du-code-produit)
- [Journal d'audit](#journal-daudit)
- [Fichiers et dossiers](#fichiers-et-dossiers)

## Commandes

```
tenon [--resume <id|last>] [--lang fr|en] [--allow-non-eu] [--provider <id>]
tenon -p "<prompt>" [--resume <id|last>] [--lang fr|en] [--output text|json] [--allow-non-eu] [--provider <id>]
tenon sessions [--lang fr|en]
tenon skills [--lang fr|en]
tenon agents [--lang fr|en]
tenon key set|delete <fournisseur>
tenon audit verify
tenon config check
tenon git install-hook [--force]
tenon git mark <fichier-message>
tenon --version
tenon --help
```

| Option | Effet |
|---|---|
| `-p "<prompt>"` | Exécute un tour sans interface interactive. |
| `--output text\|json` | Format de sortie du mode non interactif. Défaut : `text`. Refusé sans `-p`. |
| `--provider <id>` | Fournisseur à utiliser. Défaut : `default_provider` de la configuration. |
| `--allow-non-eu` | Accepte l'envoi de données hors de l'UE, pour les fournisseurs et pour les serveurs MCP. |
| `--resume <id\|last>` | Reprend une session du projet courant. Refusé avec une sous-commande autre que `sessions`. |
| `--lang fr\|en` | Langue de l'interface pour cette exécution. |
| `--version` | Affiche la version et quitte. |

Les options s'écrivent avec un ou deux tirets (`-p` ou `--p`).
Elles se placent avant la sous-commande, sauf `--lang`, aussi accepté après `tenon sessions`.

Le **projet** est le dossier courant au lancement de tenon.
Ce n'est pas forcément la racine du dépôt git.

## Interface interactive

Lancez `tenon` sans `-p`.
L'entrée et la sortie standard doivent être un terminal ; sinon tenon s'arrête avec le code 1.

La barre d'état affiche en permanence :
`tenon · modèle · fournisseur · pays · juridiction · session · ~jetons/fenêtre`.
Le nombre de jetons est une estimation.

### Touches

| Touche | Effet |
|---|---|
| Entrée | Envoie le message. |
| Alt+Entrée ou Ctrl+J | Insère un retour à la ligne. |
| Échap | Annule le tour en cours. |
| Ctrl+C | Annule le tour en cours. Hors tour, quitte. Deux fois en moins de 2 s pendant un tour : quitte. |
| PgPréc / PgSuiv | Fait défiler la conversation. |
| `/help` | Affiche l'aide des touches et les skills disponibles. |
| `/skills` | Liste les skills. |
| `/agents` | Liste les sous-agents (voir [Sous-agents](#sous-agents)). |
| `/<nom> [arguments]` | Déclenche un skill (voir [Skills](#skills)). |
| Tab | Complète une commande ou un nom de skill après `/`. |
| `/quit` | Quitte. |

En quittant, tenon attend la fin du tour en cours au plus 10 s.
Au-delà, il sort avec le code 1.

### Valider une action

Quand l'agent veut écrire, exécuter une commande ou sortir du projet, un panneau remplace la zone de saisie.
Il montre l'outil, la commande complète ou les chemins, et un aperçu du changement pour `write_file` et `edit_file`.

| Réponse | Français | Anglais |
|---|---|---|
| Oui, une fois | `o` | `y` |
| Oui pour la session | `s` | `a` |
| Non | `n` | `n` |

Échap refuse aussi la demande.
Ctrl+C refuse toutes les demandes en attente, puis agit comme d'habitude.

« Oui pour la session » vaut pour le même outil avec exactement la même commande ou les mêmes chemins.
Il n'est pas proposé pour l'envoi à un fournisseur hors UE.
Il n'est pas conservé quand une session est reprise.

Pour éviter une acceptation par erreur, les touches d'acceptation sont ignorées tant que ces conditions ne sont pas réunies :

- **Délai de calme de 500 ms.** Il faut 500 ms sans aucune touche depuis l'affichage de la demande et depuis la touche précédente. Toute touche, même ignorée, relance ce délai. Un collage n'est jamais une réponse.
- **Commande entière visible.** Le titre, la commande complète, tous les chemins et les touches doivent tenir à l'écran. Seules les lignes de l'aperçu peuvent être coupées. Sinon le panneau affiche « commande trop longue pour être vérifiée : agrandissez le terminal ou refusez » ou « agrandissez le terminal pour valider ».
- **Largeur d'au moins 40 colonnes.**
- **Aperçu prêt.** Pendant son calcul, seul le refus agit.

Un refus (`n`, Échap) agit toujours immédiatement.

### Affichage sans ambiguïté

La commande et les chemins sont affichés de façon à ce qu'aucun caractère ne soit caché :

- `↪` commence la suite d'une ligne coupée à la largeur du terminal. Une coupure se distingue ainsi d'un vrai saut de ligne.
- `␠` remplace une espace située en fin de morceau, sinon invisible.
- `[U+XXXX]` remplace un caractère de contrôle, un caractère invisible ou de largeur nulle, un caractère bidirectionnel, une espace autre que l'espace ASCII, ainsi que les caractères `↪`, `␠` et U+FFFD eux-mêmes.
- `[0xXX]` remplace un octet invalide.
- Un `[` suivi de `U+` ou `0x` dans le texte d'origine est écrit `[U+005B]`.
- Une suite d'au moins 3 lignes vides devient `⏎ × N` quand la commande ne tient pas à l'écran ; la demande ne peut alors pas être acceptée.

Pour une commande shell, le panneau rappelle qu'une commande autorisée s'exécute avec vos droits, sans bac à sable.
Si la commande contient un caractère non ASCII, un avertissement s'affiche.
Pour un outil MCP, le panneau indique le serveur, son pays et sa juridiction.

## Mode non interactif

```
tenon -p "corrige les tests qui échouent"
tenon -p "résume ce dépôt" --output json
```

Toute action qui demanderait une validation est **refusée**.
Seules les lectures dans le projet et les actions couvertes par une règle `allow` s'exécutent.

### Sorties

- `--output text` (défaut) : la réponse finale va sur la sortie standard. La bannière, les avertissements et les refus vont sur la sortie d'erreur.
- `--output json` : un événement JSON par ligne sur la sortie standard, charge utile masquée. Champs : `id`, `session`, `seq`, `ts`, `type`, `payload`. Le premier événement, `SessionStarted`, contient le modèle, le fournisseur, le pays et la juridiction. Quand le modèle lance un sous-agent, les événements `SubagentStarted` et `SubagentCompleted` apparaissent, et les événements du sous-agent portent un champ `parent_call_id` (voir [Sous-agents](#sous-agents)).

### Codes de sortie

| Code | Signification |
|---|---|
| `0` | Tour terminé. Un tour arrêté par la limite d'itérations ou une réponse incomplète donne aussi 0, avec un message. |
| `1` | Erreur : configuration, clé absente, fournisseur, contexte trop volumineux, option invalide. |
| `3` | Une action a été refusée (validation impossible, règle `deny`) ou l'envoi au fournisseur a été bloqué (juridiction). |
| `130` | Second Ctrl+C : sortie immédiate. Le premier annule le tour. |

Les sous-commandes renvoient 0 en cas de succès et 1 en cas d'erreur.
`tenon git mark` renvoie toujours 0.

## Sessions

Chaque conversation est enregistrée, masquée, dans `$XDG_DATA_HOME/tenon/sessions/<id>.jsonl`.
Un identifiant a la forme `s_` suivi de 12 caractères hexadécimaux.

```
tenon sessions                 # sessions du projet courant, la plus récente d'abord
tenon --resume last            # reprend la plus récente
tenon --resume s_0123456789ab  # reprend une session précise
tenon --resume last -p "continue"
```

`tenon sessions` affiche pour chaque session : identifiant, date de mise à jour, nombre de tours, premier message.

Une session ne peut être reprise que depuis le projet où elle a été ouverte.
Une session ouverte dans un autre terminal est verrouillée : la reprise est refusée.

Si le fichier d'une session est endommagé, la reprise continue jusqu'à la dernière ligne lisible, avec un avertissement.
Le fichier est coupé à la première ligne illisible ; l'original complet est gardé dans `<id>.jsonl.corrupt`.

## Langue

La langue de l'interface et du prompt système est, par ordre de priorité :

1. l'option `--lang fr|en` ;
2. la clé `lang` de la configuration projet, puis globale ;
3. sinon l'anglais (`en`).

La langue par défaut est l'anglais.
Pour le français, mettez `lang = "fr"` dans la configuration ou lancez tenon avec `--lang fr`.

Les touches de validation suivent la langue : `o`/`s`/`n` en français, `y`/`a`/`n` en anglais.

## Configuration

| Fichier | Emplacement |
|---|---|
| Globale | `$XDG_CONFIG_HOME/tenon/config.toml`, sinon `~/.config/tenon/config.toml` |
| Projet | `.tenon/config.toml` dans le dossier courant |

Un fichier absent est ignoré.
Une clé inconnue ou mal orthographiée est refusée.
`tenon config check` charge les deux fichiers et affiche `OK`, ou l'erreur avec le code 1.
Il ne contacte ni fournisseur ni serveur MCP.

Valeurs par défaut :

| Clé | Défaut |
|---|---|
| `lang` | `"en"` (voir [Langue](#langue)) |
| `default_provider` | `"mistral"` |
| `max_iterations` | `50` |
| `shell_timeout` | `"2m"` |
| `output_limit` | `30000` caractères |
| `compaction_threshold` | `0.8` |
| `audit.level` | `"full"` |

### Ce que la configuration projet peut faire

Un dépôt peut être hostile. Sa configuration est donc limitée à :

- `lang` ;
- des règles de permission `deny` et `ask` ;
- des règles de masquage `[[redaction.rules]]`.

Elle **ne peut pas** :

- ajouter une règle `allow` (le chargement échoue) ;
- choisir ou déclarer un fournisseur ;
- déclarer un serveur MCP ;
- changer le niveau d'audit, les délais ou les limites.

Les règles du projet s'ajoutent aux règles globales.
Une règle `deny` du projet bloque toujours l'appel.
Une règle `ask` du projet l'emporte sur une règle `allow` globale : le projet ne peut que restreindre.
Une règle `ask` globale, elle, est évaluée après les règles `allow`.

### Règles de permission

```toml
[[permissions.rules]]
tool = "shell"          # ou read_file, write_file, edit_file, grep, glob, mcp__<serveur>__<outil>, "*"
match = "go test *"     # * = toute suite de caractères ; vide = tout appel de l'outil
decision = "allow"      # allow, ask ou deny
```

Ordre d'évaluation :

1. une règle `deny` qui correspond : refus ;
2. un chemin hors du projet, ou un fichier `.env*`, `*.pem`, `id_rsa*` : validation demandée ;
3. une règle `ask` de la configuration projet qui correspond : validation demandée ;
4. une règle `allow` qui correspond : autorisé ;
5. une règle `ask` globale qui correspond : validation demandée ;
6. sinon : lecture autorisée, tout le reste soumis à validation.

Le motif est ancré : il doit couvrir tout le texte comparé, `*` remplaçant n'importe quelle suite de caractères (`/` et espaces compris).
Le texte comparé dépend de l'outil :

| Outil | Texte comparé au motif |
|---|---|
| `read_file`, `write_file`, `edit_file`, `grep`, `glob` | le chemin, relatif à la racine du projet |
| `shell` | la commande, telle quelle |
| `mcp__<serveur>__<outil>` | `mcp <serveur>.<outil> <arguments JSON compacts>`, par exemple `mcp gh.create_issue {"title":"x"}` |
| `agent` | le nom de l'agent (`general-purpose` quand `agent` est omis) |

Un motif de chemin ne s'applique donc pas aux commandes : `tool = "*"`, `match = "secrets/*"` bloque `read_file secrets/k`, mais pas la commande `cat secrets/k`.
Pour protéger un dossier, combinez une règle de chemin et une règle shell :

```toml
[[permissions.rules]]
tool = "*"
match = "secrets/*"     # outils de fichiers
decision = "deny"

[[permissions.rules]]
tool = "shell"
match = "*secrets/*"    # toute commande qui cite secrets/
decision = "deny"
```

Même ainsi, `cd secrets && cat k` ou `cat secr*/k` passent : les permissions filtrent des appels, elles ne sont pas un bac à sable (voir [securite.md](securite.md#les-permissions-ne-sont-pas-un-bac-à-sable)).

Une règle `allow` sur une commande ne couvre jamais une commande enchaînée : `go test *` n'autorise pas `go test ./... ; rm x`.
`grep` et `glob` ne montrent que les fichiers que `read_file` lirait sans demande.

### Instructions du projet

`AGENTS.md`, puis `TENON.md`, à la racine du projet, sont ajoutés au prompt système.
Chaque fichier est limité à 32 768 octets.
Un lien symbolique est ignoré.

## Skills

Un skill est un dossier contenant un fichier `SKILL.md` : des instructions que le modèle, ou vous, chargez à la demande.
tenon lit le format ouvert Agent Skills. Un skill écrit pour Claude Code fonctionne sans modification.

### Dossiers et priorité

Un skill est un sous-dossier `<nom>/` contenant un fichier `SKILL.md`.
tenon parcourt ces dossiers, du plus prioritaire au moins prioritaire :

1. utilisateur : `$XDG_CONFIG_HOME/tenon/skills` (sinon `~/.config/tenon/skills`), `~/.claude/skills`, `~/.agents/skills` ;
2. projet, à la racine uniquement : `.tenon/skills`, `.claude/skills`, `.agents/skills`.

Quand deux skills portent le même nom, le premier trouvé l'emporte. L'autre est ignoré et un avis le signale.
Un skill utilisateur masque donc toujours un skill de projet du même nom.
Un dossier absent est ignoré sans avis.
`~/.claude/skills` et `~/.agents/skills` sont ignorés quand `HOME` est vide ou n'est pas un chemin absolu.
Un `XDG_CONFIG_HOME` vide ou relatif est remplacé par `$HOME/.config`. Si aucun des deux n'est absolu, le dossier utilisateur de tenon est ignoré aussi.

La lecture suit les mêmes règles que les [instructions du projet](#instructions-du-projet) : seuls les fichiers réguliers sont lus, un lien symbolique ou un FIFO est ignoré.
Un dossier de skill qui est lui-même un lien symbolique est écarté. Il en va de même d'un répertoire de skills du projet (par exemple `.claude/skills` dans le projet) qui est un lien symbolique : un avis le signale. Un répertoire de skills utilisateur peut être un lien symbolique (par exemple `~/.claude/skills` géré avec des dotfiles).
`SKILL.md` est limité à 32 768 octets : le reste est coupé et marqué comme tronqué.
tenon retient 200 skills au plus. Les suivants sont ignorés, avec un avis.
Un dossier illisible ou un skill invalide produit un avis et n'empêche jamais le démarrage.

### Format de `SKILL.md`

Le fichier commence par `---`, un bloc YAML, puis `---`. Le bloc est limité à 8 Kio et à 200 lignes.
Champs lus par tenon :

| Champ | Règle |
|---|---|
| `name` | Obligatoire. Caractères `[a-z0-9-]`, 1 à 64, égal au nom du dossier. |
| `description` | Obligatoire, non vide, 1024 caractères au plus. |
| `disable-model-invocation` | Booléen, défaut `false`. Avec `true`, le skill n'est pas listé pour le modèle : seul `/<nom>` le déclenche. |
| `allowed-tools` | Chaîne ou liste. Affiché et journalisé, mais sans effet : il ne change jamais les permissions. |

Les autres champs (`license`, `metadata`, `compatibility`...) sont ignorés.
tenon utilise son propre analyseur YAML minimal : clés de premier niveau, valeurs simples ou entre guillemets, blocs `>` et `|`, listes `- élément` et `[a, b]`, booléens.
Les tables imbriquées sont sautées.
Un skill dont l'en-tête ou le nom est invalide est ignoré, avec un avis.

### Utilisation par le modèle

Le prompt système reçoit une section après les instructions du projet :

```
## Skills disponibles
Appelez l'outil `skill` avec le nom d'un skill avant d'agir quand sa description correspond à la demande.
- <nom>: <description>
```

(En anglais : `## Available skills`.)
Les skills avec `disable-model-invocation: true` ne figurent pas dans cette liste.
La section est limitée à 8 000 octets. Au-delà, les descriptions sont coupées à 200 caractères. Si cela ne suffit pas, les derniers skills sont retirés et un avis donne leur nombre.

Le modèle charge un skill avec l'outil `skill`, avec `{"name": "<nom>"}`.
Le résultat est un en-tête `Skill <nom> (<source>, <dossier absolu>)`, le corps de `SKILL.md` sans l'en-tête YAML, puis la liste des fichiers du dossier (3 niveaux, 100 entrées au plus).
Avec `{"name": "<nom>", "file": "<chemin relatif>"}`, l'outil lit un fichier du dossier du skill.
Il refuse les chemins absolus, les composants `..`, les liens symboliques, les fichiers non réguliers et les fichiers sensibles (`.env*`, `*.pem`, `id_rsa*`).
Le contenu d'un fichier est limité par `output_limit`, comme `read_file`.
Une erreur (skill inconnu, fichier refusé ou absent) est renvoyée au modèle comme résultat en erreur, et le tour continue.

L'outil `skill` est un outil de lecture : il est autorisé sans demande, même pour un skill utilisateur hors du projet.
Les règles `deny` et `ask` s'appliquent à `tool = "skill"`. Le motif `match` est le nom du skill, ou `<nom>/<fichier>` pour un fichier.
Un projet peut interdire un skill avec `match = "<nom>*"`.
Les scripts d'un skill s'exécutent par l'outil `shell`, avec la validation habituelle.

### Utilisation par vous : `/<nom>`

Dans l'interface, ou avec `-p`, `/<nom> [arguments]` déclenche un skill.
tenon envoie un seul message au modèle :

```
Use the skill "<nom>".
<skill name="<nom>" source="<source>" dir="<dossier absolu>">
<corps de SKILL.md>
</skill>
User request: <arguments>
```

La dernière ligne est omise sans arguments.
Cela fonctionne aussi pour un skill avec `disable-model-invocation: true`.
Le fichier du skill est relu à chaque fois. S'il a été tronqué, `[… truncated]` termine le corps.

- `/help`, `/quit` et `/skills` sont des commandes intégrées et l'emportent sur un skill du même nom. Un tel skill n'est utilisable que par le modèle, et tenon affiche un avis au démarrage. Dans l'interface, un nom intégré suivi d'arguments (par exemple `/help foo`) est refusé avec `skill ou commande inconnu`, qu'un skill porte ce nom ou non.
- `/<inconnu>` affiche `skill ou commande inconnu : /<inconnu>` et n'envoie rien. Dans l'interface, votre texte reste dans la saisie pour être corrigé.
- Un texte qui commence par `/ ` (barre oblique puis espace) est envoyé tel quel. Un texte qui commence par `//` est envoyé sans la première barre oblique.
- Les substitutions propres à Claude Code (`$ARGUMENTS`, `` !commande ``, `@fichier`) ne sont pas prises en charge.

Dans l'interface :

- `/skills` liste les skills : nom, source et description.
- `Tab` complète une commande ou un nom de skill après un `/` en début de saisie : une seule correspondance est complétée avec une espace, plusieurs correspondances sont complétées jusqu'à leur préfixe commun.
- `/help` liste aussi les skills disponibles.

En mode non interactif, `-p "/<nom> ..."` développe le skill de la même façon.
Un `/x` inconnu s'arrête avec le code de sortie 1 avant la création de toute session ou entrée d'audit.
`/help`, `/quit` et `/skills` sont des commandes de l'interface interactive : avec `-p`, elles sont envoyées au modèle comme du texte ordinaire, avec ou sans arguments.

### Lister les skills

```
tenon skills [--lang fr|en]
```

Une ligne par skill : nom, source (`user` ou `project`), dossier absolu, description, et `allowed-tools: ...` si le skill en a.
Les avis de découverte suivent. Sans skill, tenon affiche `aucun skill`.
Au démarrage, la bannière ajoute `skills : N (projet : M)` quand il y a au moins un skill.
Noms, descriptions et avis sont affichés avec les caractères de contrôle remplacés par des espaces, car les skills de projet ne sont pas dignes de confiance.

### Session et audit

- Chaque chargement par l'outil `skill` est un appel d'outil ordinaire du [journal d'audit](#journal-daudit). Le champ `hash_after` du résultat contient le SHA-256 des octets lus, sous la forme `sha256:<hex>`. `SKILL.md` est lu jusqu'à 32 768 octets ; un fichier annexe est lu jusqu'à quatre fois `output_limit` plus un octet (1 Mio sans limite), donc le hash couvre ce préfixe si le fichier est plus long. Le journal prouve quelle version a guidé l'agent.
- Un message déclenché par `/<nom>` enregistre les champs `skill` (le nom) et `skill_sha256` dans l'événement `UserMessage`.
- `tenon sessions` affiche le `/<nom> ...` tapé comme premier message, pas le corps du skill.
- `--resume` rejoue le texte envoyé, pas le skill relu.

## Sous-agents

Un sous-agent est un assistant que l'agent principal lance pour une tâche autonome.
Il travaille dans son propre contexte et renvoie une seule réponse finale.
tenon lit le format de Claude Code (`.claude/agents/*.md`). Un agent écrit pour Claude Code fonctionne sans modification.

### Dossiers et priorité

Un sous-agent est un fichier `<nom>.md` : un en-tête YAML, puis le corps, qui est son prompt système.
tenon parcourt ces dossiers, du plus prioritaire au moins prioritaire :

1. utilisateur : `$XDG_CONFIG_HOME/tenon/agents` (sinon `~/.config/tenon/agents`), `~/.claude/agents`, `~/.agents/agents` ;
2. projet, à la racine uniquement : `.tenon/agents`, `.claude/agents`, `.agents/agents`.

Les règles sont celles des skills. Quand deux agents portent le même nom, le premier trouvé l'emporte et un avis signale l'autre.
Un dossier utilisateur est ignoré quand `HOME` est vide ou n'est pas un chemin absolu.
Un répertoire d'agents du projet qui est un lien symbolique est écarté, avec un avis. Seuls les fichiers réguliers sont lus.
Un fichier est limité à 32 768 octets : le reste est coupé. tenon retient 100 agents au plus.
Un dossier illisible ou un agent invalide produit un avis et n'empêche jamais le démarrage.

### Format d'un fichier d'agent

```markdown
---
name: reviewer
description: Relit une modification pour trouver des bogues et des tests manquants.
tools: Read, Grep
model: sonnet
---
Tu es un relecteur de code rigoureux. Lis les fichiers qu'on te donne et signale les problèmes.
```

| Champ | Règle |
|---|---|
| `name` | Obligatoire. Caractères `[a-z0-9-]`, 1 à 64, égal au nom du fichier sans `.md`. |
| `description` | Obligatoire, non vide, 1024 caractères au plus. |
| `tools` | Facultatif. Chaîne séparée par des virgules, ou liste. Sans ce champ, le sous-agent reçoit tous les outils de la session sauf `agent`. |
| `model` | Lu et affiché, mais sans effet : un sous-agent utilise toujours le fournisseur et le modèle de la session. |

Les autres champs (`color`...) sont ignorés.
Les noms d'outils de Claude Code sont traduits :

| Claude Code | tenon |
|---|---|
| `Read` | `read_file` |
| `Write` | `write_file` |
| `Edit`, `MultiEdit` | `edit_file` |
| `Bash` | `shell` |
| `Grep` | `grep` |
| `Glob` | `glob` |
| `Skill` | `skill` |

Les noms de tenon et les noms `mcp__<serveur>__<outil>` sont acceptés tels quels.
Une restriction entre parenthèses (`Bash(git:*)`) est retirée : seul le nom de l'outil compte.
Un nom inconnu (par exemple `WebFetch`, `Task` ou `agent`) est ignoré, avec un avis.
Le sous-agent reçoit seulement les outils listés qui existent dans la session, et jamais `agent`.

### Agent intégré

`general-purpose` existe toujours : tous les outils sauf `agent`, sans consigne supplémentaire.
Sa description est `Agent généraliste pour les recherches, la recherche dans le code et les tâches en plusieurs étapes.`
Un fichier nommé `general-purpose` le remplace.

### Utilisation par le modèle

Le prompt système reçoit une section après celle des skills :

```
## Agents disponibles
Utilisez l'outil `agent` pour déléguer une tâche autonome. Le sous-agent ne voit pas cette conversation : donnez-lui tout ce qu'il faut dans `prompt`.
- <nom>: <description>
```

(En anglais : `## Available agents`.)
La section est limitée à 4 000 octets. Au-delà, les descriptions sont coupées à 200 caractères. Si cela ne suffit pas, les derniers agents sont retirés et un avis donne leur nombre.

Le modèle lance un sous-agent avec l'outil `agent` :

| Argument | Règle |
|---|---|
| `agent` | Facultatif. Nom de l'agent. Défaut : `general-purpose`. |
| `description` | Étiquette courte de la tâche (3 à 5 mots), affichée dans l'interface. Exigée par le schéma de l'outil, mais non vérifiée. |
| `prompt` | Obligatoire et vérifié. Instructions complètes : le sous-agent ne voit pas la conversation. |

Le résultat est la dernière réponse du sous-agent, coupée par `output_limit` et masquée comme tout résultat d'outil.
Un agent inconnu (`unknown agent: <nom>`), un `prompt` vide ou des arguments invalides sont renvoyés au modèle comme résultat en erreur, et le tour continue.

Le sous-agent a son propre historique : les consignes de tenon et du projet, un court préambule, le corps du fichier d'agent, puis votre `prompt`.
Il partage avec l'agent principal le fournisseur, le modèle, le masquage, les règles de permission et les validations « oui pour la session ».
`max_iterations` s'applique au sous-agent. S'il est atteint, le résultat est une erreur qui commence par `sub-agent stopped: iteration limit reached`, suivie de la dernière réponse partielle.
Une erreur du fournisseur donne aussi un résultat en erreur. Échap ou Ctrl+C annule le tour entier.

### Permissions

L'outil `agent` est un outil de lecture : il est autorisé sans demande.
Les règles `deny` et `ask` s'appliquent à `tool = "agent"`. Le motif `match` est le nom de l'agent.

```toml
[[permissions.rules]]
tool = "agent"
match = "reviewer"
decision = "ask"
```

Les actions du sous-agent suivent les règles habituelles, une par une. La règle `agent` décide seulement s'il peut démarrer.
Les validations vous sont demandées comme d'habitude.

### Limites

- Les sous-agents s'exécutent l'un après l'autre : l'agent principal attend la réponse. Plusieurs appels `agent` d'un même message s'exécutent à la suite.
- Un sous-agent ne peut pas lancer de sous-agent.
- Un sous-agent utilise le modèle de la session. Il n'y a ni autre modèle ni autre fournisseur.

### Dans l'interface

- `⏺ agent <nom> · <description> [en attente]` apparaît quand le sous-agent démarre. À la fin, le statut devient `ok`, `erreur` ou `annulé`, suivi de `· N itérations`.
- Les appels d'outils du sous-agent sont affichés sous cette ligne, en retrait : `  ⎿ <outil> <détail> [statut]`.
- Les messages du sous-agent ne sont pas affichés. Son texte n'est pas diffusé au fil de l'eau.
- Un panneau de validation pour une action d'un sous-agent ajoute la ligne `sous-agent <nom> · <description>`.
- `/agents` liste les agents : nom, source et description. `/help` le mentionne.

### En mode non interactif

La sortie texte est inchangée : seulement la réponse de l'agent principal.
Avec `--output json`, les événements du sous-agent sont inclus. `SubagentStarted` (`call_id`, `agent`, `description`) et `SubagentCompleted` (`call_id`, `status`, `iterations`) les encadrent.
Les événements émis pendant le tour du sous-agent portent un champ `parent_call_id` : le `call_id` de l'appel `agent`. Le champ est absent pour l'agent principal.
Les actions qui demandent une validation sont refusées, comme pour l'agent principal.

### Lister les agents

```
tenon agents [--lang fr|en]
```

Une ligne par agent : nom, source (`builtin`, `user` ou `project`), fichier (pas pour `builtin`), description, `tools: ...` (`tools: all` sans champ `tools`) et `model: ...` si le fichier en a un.
Les avis de découverte suivent.
Au démarrage, la bannière ajoute `agents : N (projet : M)` quand il y a au moins un fichier d'agent.
Noms, descriptions et avis sont affichés avec les caractères de contrôle remplacés par des espaces, car les agents de projet ne sont pas dignes de confiance.

### Session et audit

- Les actions d'un sous-agent figurent dans le même [journal d'audit](#journal-daudit), dans la même chaîne. `tenon audit verify` est inchangé.
- Les fichiers écrits par un sous-agent comptent comme écrits par l'agent pour le [marquage git](#marquage-git-du-code-produit).
- `--resume` reconstruit l'historique de l'agent principal et ignore les événements des sous-agents. Un sous-agent interrompu par un arrêt brutal n'est pas relancé : son appel `agent` reçoit `cancelled: session interrupted`.

## Fournisseurs et juridictions

Tout point d'accès compatible OpenAI est utilisable.
Préréglages :

| id | Fournisseur | Juridiction | Clé |
|---|---|---|---|
| `mistral` (défaut) | Mistral AI, FR | `eu` | `MISTRAL_API_KEY` |
| `scaleway` | Scaleway, FR | `eu` | `SCW_SECRET_KEY` |
| `ovhcloud` | OVHcloud AI Endpoints, FR | `eu` | `OVH_AI_ENDPOINTS_ACCESS_TOKEN` |
| `ollama` | Ollama, `http://localhost:11434/v1` | `local` | aucune |
| `vllm` | vLLM, `http://localhost:8000/v1` | `local` | aucune |

La juridiction désigne le **droit applicable à l'opérateur**, pas l'emplacement de ses serveurs.
Un fournisseur ajouté sans `jurisdiction` est traité comme `non-eu`.
Un bloc `[[providers]]` qui reprend l'`id` d'un préréglage remplace tout le préréglage (sauf la détection des modèles locaux, voir ci-dessous).

Envoi à un fournisseur `non-eu` :

- **interface interactive** : tenon demande « envoyer la conversation à … ? » avant le premier envoi (`o` ou `n`). L'accord vaut jusqu'à la fin de l'exécution. Après un `o`, l'avertissement hors UE (`JurisdictionWarning`) est aussi affiché et enregistré dans le journal.
- **mode non interactif** : l'envoi est bloqué (code 3) sans `--allow-non-eu`.
- avec `--allow-non-eu` : aucune question ; un avertissement est affiché et enregistré dans le journal.

tenon ne bascule jamais automatiquement vers un autre fournisseur.

### Modèles locaux (Ollama, vLLM)

Pour `ollama` et `vllm`, tenon lit au démarrage le modèle servi et la fenêtre de contexte appliquée par le serveur, à l'adresse configurée seulement :

- **Ollama** : sans `model`, tenon choisit le modèle installé le plus récemment parmi ceux qui acceptent les appels d'outils (`ollama list`). Il charge le modèle, puis lit la fenêtre réelle (`/api/ps`).
- **vLLM** : tenon lit `GET /v1/models`. Sans `model`, le serveur doit servir un seul modèle. La fenêtre est `max_model_len`.

Le modèle et la fenêtre détectés s'affichent au démarrage.
Une valeur fixée dans la configuration (`model`, `context_window`) n'est jamais remplacée ; `detect = "off"` désactive la détection.

```
ollama pull qwen3:8b           # modèle de 5 Go, appels d'outils pris en charge
tenon --provider ollama
```

**Fenêtre de contexte d'Ollama.** Par défaut, Ollama limite la fenêtre selon la mémoire de la machine (4096 jetons sous 24 Gio de VRAM), quelle que soit celle du modèle.
4096 jetons ne suffisent pas à un agent : tenon l'indique au démarrage.
Augmentez la fenêtre du serveur, puis relancez Ollama :

- application macOS : réglage « Context length » d'Ollama, ou `launchctl setenv OLLAMA_CONTEXT_LENGTH 32768` puis redémarrage de l'application ;
- service Linux : `Environment="OLLAMA_CONTEXT_LENGTH=32768"` dans `systemctl edit ollama`, puis `systemctl restart ollama` ;
- en ligne de commande : `OLLAMA_CONTEXT_LENGTH=32768 ollama serve`.

Une fenêtre plus grande consomme plus de mémoire (environ 4,5 Go de plus pour 32768 jetons avec `qwen3:8b`).

Exemple pour un serveur Ollama sur une autre machine :

```toml
[[providers]]
id = "ollama"
name = "Ollama (serveur GPU)"
base_url = "http://gpu.lan:11434/v1"
model = "qwen3:8b"          # context_window absent : lu sur le serveur
jurisdiction = "local"
```

## Clés d'API et trousseau

Une clé n'est jamais lue dans un fichier de configuration.
tenon cherche la clé du fournisseur :

1. dans la variable d'environnement `api_key_env` ;
2. sinon dans le trousseau système (Trousseau sur macOS, Secret Service sur Linux), service `tenon`, compte = identifiant du fournisseur.

```
tenon key set mistral      # demande la clé sans l'afficher
echo "$CLE" | tenon key set mistral
tenon key delete mistral
```

Le fournisseur doit exister dans la configuration.
Une valeur vide est refusée.
Un trousseau illisible n'empêche pas de démarrer : la clé est considérée absente, avec un avertissement.

Les clés de tous les fournisseurs configurés, issues de l'environnement ou du trousseau, sont masquées dans tout ce que tenon écrit ou envoie.

## Serveurs MCP

Les serveurs MCP se déclarent **uniquement dans la configuration globale**.
Voir les deux exemples dans [config.example.toml](../config.example.toml).

Leurs outils sont proposés au modèle sous le nom `mcp__<serveur>__<outil>`.
Par défaut, chaque appel demande une validation ; `risk = "read"` les autorise sans demande.
Les arguments d'un appel sont affichés en entier dans le panneau de validation.
Un appel dont les arguments contiennent un marqueur de masquage (`[SECRET_1]`…) est refusé.

### Serveur stdio

- La commande est un chemin absolu, ou un nom nu cherché dans les dossiers absolus du `PATH`. Un chemin relatif est refusé.
- Le serveur est lancé **depuis votre dossier personnel (`HOME`)**, jamais depuis le projet.
- La racine du projet lui est transmise par la variable `TENON_PROJECT_ROOT`.
- Son environnement est réduit à `PATH`, `HOME`, `LANG`, `TENON_PROJECT_ROOT` et les variables listées dans `env_from`.
- Sans `jurisdiction`, il vaut `local`.
- À la fermeture : SIGTERM au groupe de processus, puis SIGKILL après 2 s.

### Serveur HTTP

- `https` est obligatoire, sauf vers `localhost`, `127.0.0.1` ou `::1`.
- L'URL ne contient ni identifiant, ni requête, ni fragment. Le jeton passe par `bearer_env`.
- `jurisdiction` est obligatoire.
- Les redirections ne sont pas suivies.

### Juridiction des serveurs

Un serveur `non-eu` n'est **ni lancé ni contacté** sans `--allow-non-eu`.
Il est signalé « serveur MCP … hors UE ignoré : relancez avec --allow-non-eu ».
En mode non interactif, ses outils n'existent pas et le code de sortie n'en dépend pas.

`--allow-non-eu` vaut pour toute l'exécution, **pour les fournisseurs et pour les serveurs MCP**.
Avec cette option, les outils d'un serveur `non-eu` suivent les règles de permission habituelles.

### Pannes

Un serveur injoignable au démarrage (délai : 15 s) est signalé et ignoré.
Un serveur qui tombe en cours de session voit ses outils désactivés ; la session continue.
Les valeurs des variables `env_from` et `bearer_env` sont masquées partout.

## Marquage git du code produit

```
tenon git install-hook          # dans le dépôt qui contient le dossier courant
tenon git install-hook --force  # remplace un hook existant
```

La commande installe un hook git **`commit-msg`** (exécutable, `core.hooksPath` respecté).
Si un autre hook `commit-msg` existe, elle refuse sans `--force`.
Avec `--force`, l'ancien hook est renommé `commit-msg.bak`.
Un ancien hook `prepare-commit-msg` de tenon est supprimé.

À chaque commit, le hook lance `tenon git mark`.
Celui-ci compare les fichiers indexés aux fichiers écrits avec succès par l'agent (`write_file`, `edit_file`), d'après les sessions ouvertes dans ce dépôt ou un de ses sous-dossiers.
S'il trouve une correspondance, il ajoute en fin de message :

```
AI-Assisted-By: tenon (<modèle>, <fournisseur>, <pays>)
AI-Session: <identifiant de session>
```

Plusieurs lignes peuvent apparaître : une ligne `AI-Assisted-By` par triplet (modèle, fournisseur, pays) distinct, et une ligne `AI-Session` par session concernée.
Les trailers ne contiennent ni secret ni contenu de fichier.

Le hook **ne bloque jamais un commit** :

- sans `tenon` dans le `PATH`, il ne fait rien ;
- toute erreur laisse le message intact ;
- le marquage s'arrête au bout de 5 s ;
- `TENON_DEBUG=1` affiche l'erreur éventuelle.

Cas particuliers :

- `git commit --no-verify` saute le hook : le commit n'est pas marqué.
- Les commits de fusion ne sont pas marqués.
- Un message vide reste vide : le commit est abandonné comme d'habitude.

## Journal d'audit

Chaque événement est ajouté au journal `$XDG_DATA_HOME/tenon/audit/AAAA-MM-JJ.jsonl`.
Chaque ligne contient l'empreinte de la précédente : la chaîne continue d'un fichier au suivant.

```
tenon audit verify
```

La commande affiche `OK : <n> lignes, dernier hash sha256:…`, ou la première ligne en faute (`fichier:ligne`) avec le code 1.
Notez le dernier hash ailleurs : voir [securite.md](securite.md#troncature-de-fin-du-journal).

Si l'écriture du journal échoue, tenon n'exécute plus aucune action.

## Fichiers et dossiers

| Chemin | Contenu | Droits |
|---|---|---|
| `~/.config/tenon/config.toml` | configuration globale | — |
| `.tenon/config.toml` | configuration projet | — |
| `~/.local/share/tenon/audit/` | journal d'audit | dossier `0700`, fichiers `0600` |
| `~/.local/share/tenon/sessions/` | sessions | dossier `0700`, fichiers `0600` |

`$XDG_CONFIG_HOME` et `$XDG_DATA_HOME` remplacent `~/.config` et `~/.local/share` quand ils sont définis.
