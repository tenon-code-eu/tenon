# tenon : sous-agents (v0.4)

Date : 2026-10-19. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Skills : [2026-10-17-tenon-skills-design.md](2026-10-17-tenon-skills-design.md).

## 1. Objectif

Parité avec Claude Code : l'agent principal délègue une tâche autonome à un sous-agent, qui travaille dans son propre contexte et renvoie un compte rendu. Les sous-agents définis pour Claude Code (`.claude/agents/*.md`) sont réutilisables sans modification, sans renoncer aux garanties de tenon.

Critères de succès :

- un sous-agent écrit pour Claude Code est découvert, listé et utilisable ;
- le modèle lance un sous-agent avec l'outil `agent` et reçoit sa réponse finale comme résultat d'outil ;
- chaque action d'un sous-agent passe par les mêmes permissions, le même masquage et le même journal d'audit que l'agent principal ;
- un sous-agent n'obtient jamais plus de droits que la session et ne peut pas lancer de sous-agent.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Exécution | Séquentielle : l'agent principal attend ; plusieurs appels d'un même message s'exécutent l'un après l'autre | Panneau de validation et audit simples ; parallèle plus tard |
| Types | Un sous-agent intégré `general-purpose` + fichiers au format Claude Code | Réutilisation des agents existants |
| Modèle | Toujours celui de la session ; `model` lu, affiché, sans effet | Aucune nouvelle juridiction ni nouvelle clé |
| Architecture | Tour imbriqué dans le même moteur et la même session (approche A) | Une chaîne d'audit, une interface, réutilisation du moteur |
| Imbrication | Interdite : `agent` absent des outils d'un sous-agent | Coût et contrôle bornés |
| Analyse YAML | Analyseur des skills extrait dans `internal/frontmatter` et partagé | Pas de duplication |

## 3. Définitions et découverte

### 3.1 Dossiers

Un sous-agent est un fichier `<nom>.md` (en-tête YAML, puis corps = prompt système). Dossiers, par priorité décroissante :

1. utilisateur : `$XDG_CONFIG_HOME/tenon/agents` (sinon `~/.config/tenon/agents`), `~/.claude/agents`, `~/.agents/agents` ;
2. projet, à la racine : `.tenon/agents`, `.claude/agents`, `.agents/agents`.

Mêmes règles que les skills (spec skills §3.1, §3.3) : à nom égal le premier trouvé l'emporte et les suivants sont signalés ; dossiers utilisateur ignorés si `HOME`/`XDG_CONFIG_HOME` est vide ou relatif (XDG relatif → `$HOME/.config`) ; un dossier d'agents du projet qui est un lien symbolique est ignoré avec un avis (un dossier utilisateur peut l'être) ; fichiers réguliers seulement, `O_NOFOLLOW|O_NONBLOCK` ; fichier ≤ 32 768 octets (tronqué à la dernière rune entière, marqué tronqué) ; 100 agents au plus ; un dossier illisible produit un avis.

### 3.2 En-tête

| Champ | Règle |
|---|---|
| `name` | Obligatoire. `[a-z0-9-]`, 1 à 64 caractères. Indépendant du nom de fichier : le catalogue est indexé par `name` |
| `description` | Obligatoire, non vide. Au-delà de 1024 caractères : coupée à 1024 (runes entières), avec un avis |
| `tools` | Facultatif. Chaîne séparée par des virgules ou liste. Absent : tous les outils de la session sauf `agent`. Clé présente et vide : aucun outil |

Limites d'en-tête des agents : 16 384 octets et 400 lignes (skills : 8 192 et 200). Un fichier tronqué à 32 768 octets produit un avis et `(truncated)` dans `tenon agents`.
| `model` | Lu et affiché, sans effet |

Autres champs (`color`…) ignorés. Traduction des noms d'outils Claude Code : `Read`→`read_file`, `Write`→`write_file`, `Edit` et `MultiEdit`→`edit_file`, `Bash`→`shell`, `Grep`→`grep`, `Glob`→`glob`, `Skill`→`skill`. Les noms tenon et `mcp__<serveur>__<outil>` sont acceptés tels quels. Une parenthèse de restriction (`Bash(git:*)`) est retirée : seul le nom de l'outil compte. Un nom inconnu (ex. `WebFetch`, `Task`, `agent`) est ignoré avec un avis.

### 3.3 Sous-agent intégré

`general-purpose` : description fixe (catalogue i18n), corps vide, tous les outils sauf `agent`. Un fichier nommé `general-purpose` le remplace.

### 3.4 Affichage

- Bannière : `agents: N (project: M)` quand des agents de fichier existent.
- `tenon agents` : nom, source (`builtin`, `user` ou `project`, avec le fichier), description, outils, `model` s'il existe, puis les avis. Texte nettoyé des caractères de contrôle et de format.

## 4. L'outil `agent` et l'exécution

### 4.1 Prompt système de l'agent principal

Section ajoutée après celle des skills, selon `lang` :

```
## Available agents
Use the `agent` tool to delegate a self-contained task. The sub-agent does not see this conversation: give it everything it needs in `prompt`.
- <nom>: <description>
```

Budget : 4 000 octets, même logique que les skills (descriptions coupées à 200 caractères, puis derniers agents retirés avec un avis).

### 4.2 Outil `agent`

- Nom `agent` ; arguments `{"agent"?: string, "description": string, "prompt": string}` ; `agent` vaut `general-purpose` par défaut.
- `Target` : `Command = <nom>`, sans `Paths`, risque `Read` : autorisé par défaut ; les règles `deny`/`ask` (`tool = "agent"`, `match = "<nom>"`) s'appliquent.
- Erreurs (nom inconnu, `prompt` vide, arguments invalides) : résultat en erreur, l'agent principal continue.
- Résultat : dernière réponse du sous-agent, tronquée par `output_limit`, masquée comme tout résultat d'outil.

### 4.3 Tour imbriqué

- **Historique propre** : un message système = prompt de base (consignes de tenon et instructions du projet) + section des skills si `skill` est dans ses outils + préambule (EN/FR) « You are a sub-agent launched by the main agent. Your final answer is returned to it as a tool result: make it complete and concise. You cannot launch sub-agents. » + corps du fichier ; puis un message utilisateur = `prompt`. Le sous-agent ne voit pas la conversation de l'agent principal.
- **Outils** : ceux de `tools` présents dans la session (intersection), jamais `agent`.
- **Partagé avec l'agent principal** : fournisseur et modèle, masquage `Outbound`, accord d'envoi hors UE déjà donné, règles de permission, validations « oui pour la session ».
- **Limites** : `max_iterations` s'applique au sous-agent ; compaction sur son propre historique avec le seuil de la session.
- **Fin** : réponse sans appel d'outil → statut `ok`. `max_iterations` atteint → résultat en erreur `sub-agent stopped: iteration limit reached` suivi de la dernière réponse partielle. Erreur du fournisseur → résultat en erreur avec le message. Annulation (`Échap`, `Ctrl+C`) → le tour entier est annulé.
- Le texte du sous-agent n'est pas diffusé au fil de l'eau (pas d'`AssistantDelta`) ; ses `AssistantMessage` sont émis avec `parent_call_id`.

### 4.4 Événements

- `SubagentStarted{call_id, agent, description}` et `SubagentCompleted{call_id, status ("ok"|"error"|"cancelled"), iterations}`.
- Champ `parent_call_id` (`omitempty`) sur `AssistantMessage`, `ToolCallRequested`, `ToolResult`, `PermissionRequested`, `PermissionResolved` (et tout autre événement émis pendant le tour imbriqué : `ContextCompacted`, `RedactionApplied`, `JurisdictionWarning`, `Error`). Absent : comportement et forme canonique inchangés pour les sessions et journaux existants.

## 5. Interface, sessions et audit

### 5.1 Interface interactive

- Ligne `⏺ agent <nom> · <description> [en cours]` à `SubagentStarted`, mise à jour à `SubagentCompleted` (`[ok]`, `[erreur]`, `[annulé]`, avec le nombre d'itérations).
- Appels d'outils du sous-agent affichés en retrait sous cette ligne : `  ⎿ <outil> <détail> [statut]`.
- Les `AssistantMessage` du sous-agent ne sont pas affichés.
- Panneau de validation : ligne de contexte `sous-agent <nom> · <description>` (i18n) quand la demande porte `parent_call_id`.
- `/agents` : liste nom, source, description ; `/help` mentionne `/agents`.

### 5.2 Mode non interactif

Sortie texte inchangée (réponse de l'agent principal seulement). Sortie `--output json` : événements du sous-agent inclus, avec `parent_call_id`. Les actions demandant validation restent refusées (code 3 si l'agent principal s'arrête sur un refus, comme aujourd'hui).

### 5.3 Sessions

La reprise reconstruit l'historique de l'agent principal en ignorant les événements portant `parent_call_id`. Un appel `agent` sans résultat (processus tué) reçoit `cancelled: session interrupted`, comme tout appel ; le sous-agent n'est pas relancé. `tenon sessions` n'est pas affecté.

### 5.4 Audit et marquage git

Chaîne unique ; `tenon audit verify` inchangé. Les écritures d'un sous-agent (`write_file`, `edit_file`) sont des écritures de l'agent : le hook git les compte pour `AI-Assisted-By`.

## 6. Architecture

| Élément | Rôle |
|---|---|
| `internal/frontmatter` | Analyseur YAML minimal extrait de `internal/skills` (API : `Parse(data) (Fields, body, err)`, champs génériques `Name`, `Description`, `List(key)`, `Bool(key)`, `String(key)`), utilisé par skills et agents |
| `internal/agents` | `Dirs`, `Discover`, `Agent{Name, Description, Source, Path, Tools, Model, Body}`, `Catalog.Get/List`, `PromptSection(lang, budget)`, traduction des noms d'outils |
| `internal/engine` | Boucle de tour paramétrée (historique, jeu d'outils, `parent_call_id`, diffusion du texte) ; outil `agent` fourni par le moteur ; `Deps.Agents`, `Deps.BasePrompt` ; reprise qui ignore `parent_call_id` |
| `internal/event` | `SubagentStarted`, `SubagentCompleted`, champs `ParentCallID` |
| `cmd/tenon` | Découverte, section du prompt, avis, `tenon agents` |
| `internal/ui/tui` | Lignes en retrait, contexte de validation, `/agents` |
| `internal/ui/headless` | Sortie texte ignore les événements du sous-agent |
| `internal/i18n` | Textes EN/FR |
| Docs | `docs/usage.md`, `docs/fr/utilisation.md` (« Sub-agents » / « Sous-agents »), `docs/security.md`, `docs/fr/securite.md` |

## 7. Gestion des erreurs

- Découverte : jamais bloquante ; avis au démarrage et dans `tenon agents`.
- Outil `agent` : nom inconnu, `prompt` vide, `deny` → résultat en erreur.
- Tour imbriqué : erreur fournisseur ou limite d'itérations → résultat en erreur avec la dernière réponse partielle ; annulation → tour entier annulé.
- Imbrication : `agent` absent du jeu d'outils ; un appel forcé est refusé comme outil inconnu.

## 8. Tests

- **Analyseur extrait** : tests existants des skills inchangés et verts.
- **Découverte** : priorités, doublons, liens symboliques (fichier, dossier de projet), limites, traduction des outils, `Bash(git:*)`, noms inconnus signalés, `general-purpose` remplacé par un fichier.
- **Moteur (faux fournisseur)** : délégation complète (principal → `agent` → sous-agent `read_file` → réponse → résultat d'outil) ; `parent_call_id` et `SubagentStarted/Completed` ; le sous-agent ne voit ni la conversation principale ni `agent` ; restriction `tools` ; validation refusée ; validation « pour la session » partagée ; `max_iterations` ; erreur fournisseur ; annulation au milieu.
- **Reprise** : session avec sous-agent terminé ; sous-agent interrompu.
- **Bout en bout** : `-p` avec sous-agent (texte = réponse finale, `tenon audit verify` passe) ; `--output json` contient les événements avec `parent_call_id` ; `tenon agents` ; compatibilité avec un `.claude/agents/reviewer.md` réel (`tools: Read, Grep`, `model: sonnet`).
- **Interface** : lignes en retrait, contexte du panneau, `/agents`.

## 9. Hors périmètre

Sous-agents parallèles ou en arrière-plan, choix d'un autre modèle ou fournisseur, imbrication, création d'agents depuis l'interface.
