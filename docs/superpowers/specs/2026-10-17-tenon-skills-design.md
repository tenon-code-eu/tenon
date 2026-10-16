# tenon : skills (v0.3)

Date : 2026-10-17. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md).
Les sous-agents feront l'objet d'une spec séparée.

## 1. Objectif

Parité avec Claude Code : un utilisateur retrouve la notion de skill et réutilise ses skills existants au format ouvert Agent Skills (un dossier par skill, un fichier `SKILL.md` à en-tête YAML), sans renoncer aux garanties de tenon.

Critères de succès :

- un skill écrit pour Claude Code (dans `~/.claude/skills` ou `.claude/skills`) est découvert, listé et utilisable sans modification ;
- le modèle charge un skill de lui-même quand sa description correspond à la demande ;
- l'utilisateur déclenche un skill avec `/<nom>` dans l'interface ou en mode non interactif ;
- un skill n'élargit jamais les permissions ; chaque chargement est masqué et journalisé avec l'empreinte du fichier lu.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Format | Agent Skills : `<dossier>/<nom>/SKILL.md`, en-tête YAML | Réutilisation des skills existants |
| Chargement | À la demande par un outil `skill` (approche A) ; seuls noms et descriptions vont dans le prompt système | Coût en jetons borné, modèle de Claude Code |
| Dossiers | tenon, `.claude` et `.agents`, côté utilisateur et côté projet | Réutilisation immédiate |
| Conflit de noms | Le skill utilisateur l'emporte sur le skill de projet | Un dépôt hostile ne remplace pas un skill de confiance |
| `allowed-tools` | Lu, affiché, journalisé, **sans effet** | Un projet ne peut que restreindre (spec parente §6.2) |
| Analyse YAML | Analyseur minimal interne, sans dépendance | Pas de nouvelle dépendance à vendorer et surveiller |
| Sous-agents | Hors périmètre | Spec séparée |

## 3. Découverte et format

### 3.1 Dossiers

Un skill est un sous-dossier `<nom>/` contenant `SKILL.md`. Dossiers parcourus, par ordre de priorité décroissante :

1. utilisateur : `~/.config/tenon/skills` (ou `$XDG_CONFIG_HOME/tenon/skills`), `~/.claude/skills`, `~/.agents/skills` ;
2. projet, à la racine uniquement : `.tenon/skills`, `.claude/skills`, `.agents/skills`.

À nom égal, le premier trouvé l'emporte ; les suivants sont ignorés et signalés par un avis. Un dossier absent est ignoré sans avis.

### 3.2 En-tête

`SKILL.md` commence par `---`, un bloc YAML, puis `---`. Champs lus :

| Champ | Règle |
|---|---|
| `name` | Obligatoire. `[a-z0-9-]`, 1 à 64 caractères, égal au nom du dossier |
| `description` | Obligatoire, non vide, 1024 caractères au plus |
| `disable-model-invocation` | Booléen, défaut `false`. `true` : absent de la liste donnée au modèle, déclenchable seulement par `/<nom>` |
| `allowed-tools` | Chaîne ou liste. Conservé pour l'affichage et l'audit, sans effet |

Les autres champs (`license`, `metadata`, `compatibility`…) sont ignorés.

L'analyseur interne accepte : clés de premier niveau ; valeurs simples, entre guillemets simples ou doubles ; blocs `>` et `|` (indentation constante) ; listes `- élément` et `[a, b]` ; booléens `true`/`false`. Les tables imbriquées (`metadata:`) sont sautées. L'en-tête est limité à 8 Kio et à 200 lignes ; au-delà, ou en cas d'erreur, le skill est écarté avec un avis.

### 3.3 Lecture sûre

Mêmes règles que les fichiers d'instructions (`internal/instructions`) :

- fichiers réguliers uniquement, ouverts avec `O_NOFOLLOW|O_NONBLOCK` ; liens symboliques et FIFO ignorés ; le dossier du skill lui-même ne doit pas être un lien symbolique ;
- `SKILL.md` limité à 32 768 octets ; au-delà, tronqué à la dernière rune entière et marqué tronqué ;
- 200 skills au plus ; au-delà, les suivants sont ignorés avec un avis ;
- un dossier illisible produit un avis et n'empêche pas le démarrage.

### 3.4 Affichage

- La bannière de démarrage ajoute `skills: N (project: M)` quand N > 0.
- `tenon skills` liste les skills : nom, source (`user` ou `project`, avec le dossier), description, `allowed-tools` s'il existe, et les avis de découverte.

## 4. Déclenchement par le modèle

### 4.1 Section du prompt système

Ajoutée après les instructions de projet, en anglais ou en français selon `lang` :

```
## Available skills
Call the `skill` tool with a skill's name before acting when its description matches the request.
- <nom>: <description>
```

Les skills `disable-model-invocation` n'y figurent pas. Budget : 8 000 caractères. Au-delà, les descriptions sont tronquées à 200 caractères ; si cela ne suffit pas, les derniers skills (ordre alphabétique au sein de chaque priorité) sont retirés et un avis donne leur nombre.

### 4.2 Outil `skill`

- Nom `skill`, risque `Read`.
- Arguments : `{"name": string, "file"?: string}`.
- Sans `file` : renvoie l'en-tête textuel `Skill <nom> (<source>, <dossier absolu>)`, le corps de `SKILL.md` sans l'en-tête YAML, puis la liste des fichiers du dossier (3 niveaux, 100 entrées au plus, chemins relatifs, liens symboliques exclus).
- Avec `file` : lit un fichier du dossier du skill.
  - chemin relatif obligatoire ; refus des chemins absolus, des composants `..` et de tout composant lien symbolique (vérifié composant par composant avec `Lstat`, puis ouverture `O_NOFOLLOW`) ;
  - fichier régulier uniquement ;
  - refus des fichiers sensibles selon les motifs existants de la politique (`.env`, clés, etc.) ;
  - contenu limité par `output_limit`, comme `read_file`.
- Erreurs (nom inconnu, fichier refusé ou absent) : résultat en erreur (`IsError`), le tour continue.

### 4.3 Permissions

- `Target` : `Command = <nom>` (ou `<nom>/<file>`), sans `Paths` : l'outil est autorisé par défaut, y compris pour un skill hors du projet, car ses dossiers sont des sources déclarées.
- Les règles `deny` et `ask` s'appliquent sur `tool = "skill"` ; la configuration de projet peut donc interdire un skill (`match = "<nom>*"`).
- Les scripts d'un skill s'exécutent par l'outil `shell`, avec la validation habituelle. `allowed-tools` n'a aucun effet.

### 4.4 Masquage et audit

- Le résultat passe par `Outbound` (masquage) comme tout résultat d'outil.
- `ToolResultPayload` reçoit l'empreinte SHA-256 du fichier lu dans `HashAfter` (champ existant), et la source dans le détail. Le journal prouve quelle version a guidé l'agent.

## 5. Déclenchement par l'utilisateur

### 5.1 Syntaxe

`/<nom> [arguments]`, dans l'interface ou avec `-p "/<nom> …"`.

- `/help`, `/quit` et la nouvelle commande `/skills` sont prioritaires ; un skill portant l'un de ces noms n'est déclenchable que par le modèle, et le conflit est signalé au démarrage.
- `/<inconnu>` : message `unknown skill or command: /<inconnu>` (catalogue i18n), rien n'est envoyé. En mode non interactif, code de sortie 1.
- Un texte commençant par `/ ` (barre oblique puis espace) ou par `//` est envoyé tel quel, sans la première barre oblique pour `//`.

### 5.2 Message envoyé

Un seul `UserMessage` :

```
Use the skill "<nom>".
<skill name="<nom>" source="<source>" dir="<dossier absolu>">
<corps de SKILL.md>
</skill>
User request: <arguments>
```

La dernière ligne est omise sans arguments. Fonctionne aussi pour un skill `disable-model-invocation`.

### 5.3 Session et audit

`UserMessagePayload` reçoit deux champs facultatifs : `skill` (nom) et `skill_sha256`. La reprise (`--resume`) rejoue le texte envoyé, pas le skill relu.

### 5.4 Interface

- `/skills` affiche la liste (nom, source, description).
- `Tab` complète un nom de skill ou de commande après `/` en début de saisie.
- `/help` liste les skills disponibles.

## 6. Architecture

Nouveau paquet `internal/skills` :

| Élément | Rôle |
|---|---|
| `Skill` | `Name`, `Description`, `Source` (`user`/`project`), `Dir`, `AllowedTools`, `ModelInvocable` |
| `Discover(dirs []Dir) (*Catalog, []Notice)` | Parcourt les dossiers dans l'ordre, applique priorités et limites |
| `parseFrontmatter([]byte) (fields, body, error)` | Analyseur YAML minimal (§3.2) |
| `Catalog.Get(name)`, `Catalog.List()` | Accès |
| `Catalog.Load(name) (body, sha256, error)` | Relit `SKILL.md` (pas de cache) |
| `Catalog.ReadFile(name, rel) (content, sha256, error)` | Lecture sûre d'un fichier annexe (§4.2) |
| `Catalog.Files(name)` | Liste bornée des fichiers |
| `Catalog.PromptSection(lang, budget) (string, dropped int)` | Section du prompt (§4.1) |
| `Catalog.UserMessage(name, args) (text, sha256, error)` | Message du §5.2 |

Autres changements :

- `internal/tools/skill.go` : l'outil, adossé au `Catalog`.
- `cmd/tenon/run.go` : `Discover` au démarrage (dossiers selon `HOME`, `XDG_CONFIG_HOME` et la racine du projet), section ajoutée au prompt système, avis, commande `tenon skills`, traitement de `/<nom>` pour `-p`.
- `internal/ui/tui` : `/<nom>`, `/skills`, complétion `Tab`, aide.
- `internal/event` : champs `Skill`, `SkillSHA256` de `UserMessagePayload` (facultatifs, `omitempty` : compatibilité des sessions et de l'audit existants).
- `internal/i18n` : textes EN et FR.
- Documentation : `docs/usage.md` et `docs/fr/utilisation.md` (section « Skills »), `docs/security.md` et `docs/fr/securite.md` (skills de projet : même modèle de menace qu'`AGENTS.md`, injection d'instructions possible, aucune élévation de droits).

## 7. Gestion des erreurs

Aucun problème de skill ne bloque le démarrage ni un tour :

- dossier illisible, en-tête invalide, nom invalide, doublon, limite atteinte : skill écarté, avis au démarrage et dans `tenon skills` ;
- outil `skill` en échec : résultat en erreur, le modèle peut corriger ;
- `SKILL.md` modifié ou supprimé pendant la session : relu à chaque chargement ; supprimé, le chargement échoue proprement ; l'empreinte journalisée identifie la version utilisée.

## 8. Tests

- **Analyseur** : valeurs simples et entre guillemets, blocs `>` et `|`, listes des deux formes, booléens, table imbriquée sautée, champs manquants, en-tête absent ou non fermé, en-tête trop long, entrées hostiles (lignes très longues, indentation incohérente).
- **Découverte** : ordre de priorité (utilisateur avant projet, tenon avant `.claude` avant `.agents`), doublons signalés, nom différent du dossier, dossier lien symbolique, `SKILL.md` lien symbolique ou FIFO, limites de 200 skills et de 32 Kio.
- **Outil** : chargement normal, liste des fichiers, `file` avec `..`, chemin absolu, composant intermédiaire lien symbolique, fichier sensible, fichier absent, règle `deny` du projet, empreinte dans `HashAfter`.
- **Prompt** : budget respecté, descriptions tronquées puis skills retirés, `disable-model-invocation` absent.
- **Utilisateur** : `/<nom> args` produit le message du §5.2 et les champs `skill`/`skill_sha256` ; `/help`, `/quit`, `/skills` prioritaires ; `/inconnu` refusé (code 1 en `-p`) ; `//texte` envoyé tel quel ; reprise de session.
- **Bout en bout** : faux modèle appelant `skill`, vérification du résultat et du journal d'audit (`tenon audit verify` passe).
- **Compatibilité** : un skill au format Claude Code avec `allowed-tools` et un dossier `scripts/` est découvert, listé, chargé ; le script n'est exécuté qu'après validation.

## 9. Hors périmètre

- Sous-agents (spec séparée).
- Substitutions propres aux commandes Claude Code (`$ARGUMENTS`, `!commande`, `@fichier`).
- Marketplace et plugins.
- Effet de `allowed-tools` sur les permissions.
