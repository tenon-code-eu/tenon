# tenon : hooks (v0.5)

Date : 2026-10-19. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Skills : [2026-10-17-tenon-skills-design.md](2026-10-17-tenon-skills-design.md). Sous-agents : [2026-10-19-tenon-subagents-design.md](2026-10-19-tenon-subagents-design.md).

## 1. Objectif

Parité avec Claude Code : l'utilisateur déclare des hooks, des commandes (ou des évaluations par le modèle) que tenon exécute à des moments précis du cycle de vie : avant et après un outil, à l'envoi d'un prompt, à la fin d'un tour, au début et à la fin d'une session. Un hook peut bloquer, demander une validation, modifier l'entrée d'un outil, renvoyer un retour au modèle ou ajouter du contexte. Les hooks écrits pour Claude Code (clé `hooks` de `settings.json`) fonctionnent sans modification, sans renoncer aux garanties de tenon.

Critères de succès :

- un bloc `hooks` copié d'un `settings.json` de Claude Code est chargé et se déclenche sur les mêmes outils (noms `Bash`, `Edit`…) avec la même sémantique (codes de sortie, sortie JSON) ;
- un hook `PreToolUse` peut bloquer un appel, forcer une validation ou lever une validation issue des règles, jamais au-delà des garde-fous (section 6) ;
- aucun hook ne vient d'un dépôt : un projet ne peut pas faire exécuter de code par un hook ;
- chaque exécution de hook est dans la chaîne d'audit ; tout texte de hook envoyé au fournisseur est masqué et soumis au contrôle de juridiction.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Sources | Utilisateur seulement : `~/.config/tenon/hooks.json` toujours, `~/.claude/settings.json` sur option (`hooks.claude_settings = true`) | Un hook exécute du code : rien ne vient du dépôt ; les hooks Claude Code ne tournent pas par surprise sous tenon |
| Hooks du projet | Jamais exécutés ; avis s'ils existent | Même règle que MCP |
| Format | Celui de Claude Code (clé `hooks`) | Copiable tel quel |
| Entrée du hook | Noms Claude Code (`tool_name`, `tool_input`) **et** noms tenon (`tenon_tool`, `tenon_input`) | Compatibilité complète plus accès natif |
| `allow` | Lève un `ask` issu des règles (comme une règle `allow` globale) ; jamais un `deny`, `ForceAsk`, un chemin hors projet ou sensible, ni un `ask` du projet | Auto-approbation utile sans percer les garde-fous |
| Événements | Les 9 de Claude Code : `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`, `SessionStart`, `SessionEnd`, `PreCompact`, `Notification` | Parité |
| Types | `command`, `prompt` (fournisseur de la session), `agent` (sous-agent en lecture seule) | Parité ; `agent` réutilise les sous-agents v0.4 |
| Erreur d'un hook | Non bloquante (échec ouvert), avec avertissement visible et audité | Comportement de Claude Code ; un script cassé ne fige pas tenon |
| Fichier invalide | Refus de démarrer (échec fermé) | Un hook de sécurité ignoré en silence est dangereux |
| Architecture | Package `internal/hooks` appelé de façon synchrone par le moteur (approche A) | Un hook doit pouvoir agir avant l'action ; même schéma que skills et agents |

## 3. Configuration et chargement

### 3.1 Sources

Ordre de chargement :

1. `$XDG_CONFIG_HOME/tenon/hooks.json` (sinon `~/.config/tenon/hooks.json`) : toujours lu s'il existe. Source `tenon`.
2. `~/.claude/settings.json` : lu seulement si la configuration globale contient `hooks.claude_settings = true`. Source `claude`. Seule la clé `hooks` est lue ; les autres clés sont ignorées. Si le fichier contient `"disableAllHooks": true`, cette source ne fournit aucun hook.
3. Projet : `.tenon/hooks.json`, `.claude/settings.json`, `.claude/settings.local.json` ne sont jamais exécutés. S'ils existent et contiennent une clé `hooks` (ou, pour `.tenon/hooks.json`, s'ils existent), un avis est produit : `project hooks ignored: <fichier>`. Un fichier de projet illisible ou invalide ne produit qu'un avis.

Les hooks des deux sources utilisateur s'additionnent. Un dossier utilisateur est ignoré quand `HOME` (ou `XDG_CONFIG_HOME`) est vide ou relatif, comme pour les skills.

Configuration globale (clé interdite dans `.tenon/config.toml`, refusée comme les autres clés non autorisées) :

```toml
[hooks]
claude_settings = false   # true : lit aussi ~/.claude/settings.json
disabled = false          # true : aucun hook n'est chargé ni exécuté
```

### 3.2 Format

```json
{"hooks": {
  "PreToolUse": [
    {"matcher": "Bash|shell", "hooks": [
      {"type": "command", "command": "~/bin/check.sh", "timeout": 30},
      {"type": "prompt", "prompt": "Is this safe? $ARGUMENTS", "timeout": 30},
      {"type": "agent", "prompt": "Verify the change. $ARGUMENTS"}
    ]}
  ]
}}
```

| Champ | Règle |
|---|---|
| clé d'événement | Un des 9 événements (section 2). Inconnue : avis, groupe ignoré |
| `matcher` | Regex RE2, ancrée (`^(?:…)$`). Absente, vide ou `*` : tout. Pour les événements d'outil, testée contre le nom Claude Code **et** le nom tenon. Pour `SessionStart` : `startup`, `resume`, `compact` ; `PreCompact` : `auto`, `manual` ; `Notification` : `permission_prompt`. Ignorée pour les autres événements |
| `type` | `command`, `prompt` ou `agent`. Inconnu : avis, hook ignoré |
| `command` | `command` : obligatoire, non vide |
| `prompt` | `prompt` et `agent` : obligatoire, non vide |
| `timeout` | Entier > 0, en secondes. Défaut : 60 (`command`), 30 (`prompt`), 60 (`agent`) |
| `model` | Lu, sans effet (toujours le modèle de la session) |

Autres champs ignorés.

### 3.3 Erreurs et limites

- Échec fermé : JSON invalide, `hooks` qui n'est pas un objet, regex invalide, `command`/`prompt` vide, `timeout` non entier ou ≤ 0 → tenon refuse de démarrer, avec le fichier et le chemin JSON en cause (ex. `hooks.json: hooks.PreToolUse[0].hooks[1]: timeout must be a positive integer`).
- Fichier lu avec `O_NOFOLLOW|O_NONBLOCK` sur le dernier composant, fichier régulier seulement ; un fichier utilisateur qui est lui-même un lien symbolique est suivi (dotfiles) ; 1 Mio au plus (au-delà : refus de démarrer).
- 200 hooks au plus, toutes sources confondues (au-delà : refus de démarrer).
- Identifiant stable d'un hook : `<source>:<événement>:<i>.<j>` (indice du groupe, indice dans le groupe), ex. `tenon:PreToolUse:0.1`.

### 3.4 Affichage

- Bannière : `hooks: N (claude: M)` quand au moins un hook est chargé ; avis des hooks de projet ignorés.
- `tenon hooks [--lang fr|en]` : une ligne par hook (identifiant, `matcher`, type, commande ou prompt tronqué à 80 caractères), puis les avis. Texte nettoyé des caractères de contrôle.
- `/hooks` dans l'interface : même liste.

## 4. Exécution

### 4.1 Type `command`

- `bash -c <command>`, répertoire courant = racine du projet, groupe de processus propre tué en fin de commande, au délai ou à l'annulation du tour (Esc/Ctrl+C) : même mécanique que l'outil `shell`.
- Environnement : celui de tenon, plus `CLAUDE_PROJECT_DIR` et `TENON_PROJECT_DIR` (racine du projet).
- Entrée JSON sur stdin. Champs communs : `session_id`, `transcript_path` (fichier de session tenon ; son format diffère de celui de Claude Code), `cwd`, `hook_event_name`. Champs propres à l'événement : section 5.
- stdout et stderr plafonnés à 64 Kio chacun (le reste est jeté sans erreur).
- Code de sortie :
  - `0` : succès. Si stdout (sans les blancs initiaux) commence par `{`, il est lu comme JSON (section 4.4) ; un JSON illisible est une erreur non bloquante. Sinon, pour `UserPromptSubmit` et `SessionStart`, stdout non vide devient du contexte ajouté ; ailleurs il est seulement audité.
  - `2` : erreur bloquante ; stderr est la raison (effet par événement, section 5).
  - autre code, échec de lancement ou délai dépassé : erreur non bloquante. L'action continue ; avertissement affiché et audité.

### 4.2 Type `prompt`

- `$ARGUMENTS` est remplacé par le JSON d'entrée ; absent, le JSON est ajouté en fin de prompt après une ligne vide.
- Une requête au fournisseur et au modèle de la session, sans outils, avec un prompt système fixe (catalogue i18n) qui exige la réponse JSON `{"ok": true|false, "reason": "…"}`.
- Même chemin sortant que les autres requêtes (`outbound`) : masquage, blocage si le fournisseur n'est pas autorisé.
- `ok: false` vaut un blocage avec `reason` ; `ok: true` vaut un succès sans effet. Réponse illisible, erreur du fournisseur ou délai : erreur non bloquante.
- La requête n'entre pas dans l'historique de la session.

### 4.3 Type `agent`

- Sous-agent (tour imbriqué v0.4) avec les seuls outils `read_file`, `grep`, `glob` ; prompt construit comme pour `prompt` ; préambule fixe demandant de finir par le JSON `{"ok", "reason"}`.
- Borné par `max_iterations` et par le délai du hook (contexte annulé au délai).
- Ses appels d'outils suivent les permissions habituelles (lecture : autorisée sans demande dans le projet) et sont audités avec `parent_call_id` = identifiant d'appel du hook.
- Son dernier message est interprété comme pour `prompt`.

### 4.4 Sortie JSON

Champs communs :

| Champ | Effet |
|---|---|
| `continue: false` | Arrête le tour après ce hook ; `stopReason` est affiché à l'utilisateur. Raison du tour : `stopped_by_hook` |
| `systemMessage` | Affiché à l'utilisateur (avertissement), audité |
| `suppressOutput` | Accepté, sans effet visible (stdout n'est jamais affiché) |
| `decision`, `reason` | Selon l'événement (section 5) |
| `hookSpecificOutput` | Doit contenir `hookEventName` égal à l'événement, sinon ignoré ; champs propres à l'événement |

### 4.5 Plusieurs hooks

- Les hooks correspondant à un même événement s'exécutent en parallèle. Deux hooks `command` de même texte ne s'exécutent qu'une fois.
- Décisions : `deny`/`block` > `ask` > `allow`. Raisons des hooks retenus jointes par des sauts de ligne, dans l'ordre de la configuration.
- Contextes ajoutés : concaténés dans l'ordre de la configuration.
- Plusieurs `updatedInput` différents : l'appel est refusé (`conflicting updatedInput from hooks`), `denied: "hook"`.
- `continue: false` d'un seul hook suffit.

### 4.6 Pas de récursion

Les requêtes des hooks `prompt` et `agent` et les appels d'outils d'un hook `agent` ne déclenchent aucun hook.

## 5. Sémantique par événement

Correspondance des noms d'outils : `shell`↔`Bash`, `read_file`↔`Read`, `write_file`↔`Write`, `edit_file`↔`Edit`, `grep`↔`Grep`, `glob`↔`Glob`, `skill`↔`Skill`, `agent`↔`Task`. Les outils `mcp__…` gardent leur nom. Traduction des arguments (aller et retour) :

| tenon | Claude Code |
|---|---|
| `read_file {path, offset, limit}` | `Read {file_path, offset, limit}` |
| `write_file {path, content}` | `Write {file_path, content}` |
| `edit_file {path, old_string, new_string, replace_all}` | `Edit {file_path, old_string, new_string, replace_all}` |
| `shell {command, timeout_seconds}` | `Bash {command, timeout}` (`timeout` en millisecondes, converti) |
| `grep {pattern, glob}` | `Grep {pattern, glob}` |
| `glob {pattern}` | `Glob {pattern}` |
| `skill {name, file}` | `Skill {skill, file}` |
| `agent {agent, description, prompt}` | `Task {subagent_type, description, prompt}` |

Les champs inconnus sont conservés tels quels dans les deux sens.

### 5.1 PreToolUse

- Moment : dans `decideAndRun`, après la recherche de l'outil, la validation des arguments (`Target`) et les garde-fous de marqueurs de masquage, avant `Permissions.Check`.
- Entrée : `tool_name`, `tool_input`, `tenon_tool`, `tenon_input`, `tool_use_id` (identifiant d'appel).
- Sortie : `hookSpecificOutput.permissionDecision` (`allow`, `deny`, `ask`) et `permissionDecisionReason` ; ancien format `decision: approve|block` + `reason` accepté (`approve`→`allow`, `block`→`deny`). Code `2` → `deny`, raison = stderr.
- `deny` : l'outil n'est pas exécuté ; résultat d'erreur `blocked by hook: <raison>` ; `ToolResult.denied = "hook"`.
- `ask` : validation demandée même si les règles autorisent ; le panneau affiche `hook: <raison>` ; une approbation mémorisée (« oui pour la session ») ne dispense pas d'une demande issue d'un hook, et la réponse n'est pas mémorisée pour ce motif. En mode non interactif : refus, comme toute demande.
- `allow` : équivaut à une règle `allow` globale. `Permissions.Check` indique si son `Ask` est levable : levable pour un `ask` global ou le défaut ; non levable pour `ForceAsk`, un chemin hors projet ou sensible, un `ask` du projet. Un `Deny` n'est jamais levé.
- `updatedInput` : retraduit vers les noms tenon, puis revalidé (`Target`, marqueurs) ; invalide → refus `invalid updatedInput from hook: <erreur>`, `denied: "hook"`. Les permissions sont évaluées sur l'entrée modifiée, qui est celle exécutée. L'événement `ToolCallRequested` garde l'entrée d'origine ; `HookCompleted.updated_input` enregistre la nouvelle.

### 5.2 PostToolUse

- Moment : après chaque exécution réelle de l'outil (succès ou erreur), jamais après un refus.
- Entrée : champs de 5.1 + `tool_response: {content, is_error}` (format plus simple que celui de Claude Code, documenté).
- Sortie : `decision: block` + `reason`, ou code `2` + stderr → ligne `hook feedback: <raison>` ajoutée au contenu du résultat envoyé au modèle. `hookSpecificOutput.additionalContext` ajouté de la même façon. L'action déjà faite n'est pas annulée ; `is_error` du résultat est inchangé.

### 5.3 UserPromptSubmit

- Moment : dans `run`, avant l'ajout du message à l'historique.
- Entrée : `prompt` (texte tapé, `/skill args` compris).
- Sortie : `decision: block` + `reason`, ou code `2` + stderr → prompt non envoyé, non ajouté à l'historique ; l'utilisateur voit la raison ; `TurnCompleted` avec la raison `blocked_by_hook`. `hookSpecificOutput.additionalContext` ou stdout texte → joint au message utilisateur dans un bloc séparé, enregistré dans `UserMessage.hook_context`.

### 5.4 Stop et SubagentStop

- Moment : quand la boucle de l'agent principal (`Stop`) ou d'un sous-agent (`SubagentStop`) se termine avec la raison `done`. Jamais sur annulation, erreur, `incomplete`, `blocked` ou `max_iterations`.
- Entrée : `stop_hook_active` (vrai si le tour ou le sous-agent a déjà été relancé par un hook de ce type) ; `SubagentStop` ajoute `agent` et `tool_use_id` de l'appel `agent`.
- Sortie : `decision: block` + `reason`, ou code `2` + stderr → message utilisateur `Stop hook feedback: <raison>` ajouté à l'historique, la boucle continue (les itérations comptent pour `max_iterations`).

### 5.5 SessionStart

- Moment : au premier tour de la session, après `SessionStarted` (`source` = `startup`, ou `resume` avec `--resume`) ; après chaque compaction (`source` = `compact`).
- Sortie : `hookSpecificOutput.additionalContext` ou stdout texte → joint au prochain message utilisateur (`startup`, `resume`) ou au message de résumé de compaction (`compact`), et enregistré (`UserMessage.hook_context`, champ équivalent de l'événement de compaction) pour `--resume`.

### 5.6 Événements informatifs

Sortie sans effet, sauf `systemMessage` et `continue: false` (sauf `SessionEnd`) :

- `PreCompact` : avant une compaction ; `trigger` = `auto` (tenon n'a pas de compaction manuelle ; `manual` est réservé).
- `Notification` : à l'ouverture du panneau de validation dans l'interface ; `message` = `tenon needs your permission to use <outil>`, `notification_type` = `permission_prompt`. Jamais en mode non interactif.
- `SessionEnd` : à la fermeture de tenon si une session a démarré ; `reason` = `prompt_input_exit` (sortie de l'interface) ou `other`. Délai plafonné à 5 s.

### 5.7 Sous-agents

Les hooks d'outils (`PreToolUse`, `PostToolUse`) se déclenchent aussi dans un sous-agent ; leurs événements portent `parent_call_id`. `Stop` ne se déclenche pas à la fin d'un sous-agent (c'est `SubagentStop`).

## 6. Sécurité

- Aucune élévation : un hook restreint, ou lève un `ask` issu des règles comme le ferait une règle `allow` globale (5.1). `deny`, `ForceAsk`, chemins hors projet ou sensibles et `ask` du projet restent intacts.
- Aucun code venu du dépôt : seules les sources utilisateur sont exécutées (3.1).
- Hooks `prompt` et `agent`, `additionalContext`, raisons et stderr renvoyés au modèle : envoyés au fournisseur par le chemin `outbound` (masquage, contrôle de juridiction), comme toute sortie d'outil.
- Hooks `command` : ils reçoivent l'entrée non masquée (exécution locale). Ce qu'ils en font (par exemple un hook de notification qui envoie des données à un service hors UE) relève de l'utilisateur, comme un serveur MCP stdio. La documentation de sécurité le dit explicitement.
- Texte de hook affiché (raisons, `systemMessage`, `stopReason`) : caractères de contrôle remplacés par des espaces.

## 7. Interface, sessions et audit

### 7.1 Événements

- `HookStarted` : `hook_id`, `event`, `type`, `command` ou `prompt` (tronqué à 4 Kio), `call_id` pour les hooks d'outils, `parent_call_id` dans un sous-agent.
- `HookCompleted` : `hook_id`, `status` (`ok`, `blocked`, `error`, `timeout`, `cancelled`), `exit_code` (type `command`), `decision`, `reason`, `updated_input`, `duration_ms`, `stdout` et `stderr` tronqués à 4 Kio.
- `ToolResult.denied` admet `"hook"`. `TurnCompleted.reason` admet `blocked_by_hook` et `stopped_by_hook`. `UserMessage` et l'événement de compaction gagnent `hook_context` (omitempty).

### 7.2 Interface interactive

- Un hook réussi sans effet est invisible. Une ligne `⏺ hook <événement> · <commande ou type> [blocked|asked|error|timeout] <raison>` s'affiche pour un blocage, une demande, une erreur ou un délai ; `systemMessage` et `stopReason` s'affichent comme avertissements.
- Panneau de validation : ligne `hook: <raison>` quand la demande vient d'un hook.
- `/hooks` liste les hooks ; `/help` le mentionne.

### 7.3 Mode non interactif

Sortie texte inchangée ; avertissements des hooks (erreurs, délais, `systemMessage`, blocages) sur stderr. `--output json` inclut tous les événements de hook.

### 7.4 Sessions

`--resume` ignore `HookStarted`/`HookCompleted` ; il reconstruit les messages utilisateur et le résumé de compaction avec `hook_context`. Les messages `Stop hook feedback` sont des messages utilisateur enregistrés comme tels et restaurés.

## 8. Architecture

- `internal/hooks` (nouveau) : `Load(getenv, cfg, projectRoot) (*Set, []Notice, error)` ; `Set.Match(event, matcherSubject) []Hook` ; `Set.Run(ctx, event, input, evaluator) Outcome` (exécution parallèle, combinaison) ; traduction des noms et arguments (`ToClaude`, `FromClaude`) ; interface `Evaluator` pour `prompt` et `agent`, implémentée par le moteur. Le package ne dépend ni du moteur ni du LLM.
- `internal/config` : section `[hooks]` globale (`claude_settings`, `disabled`), refusée dans la configuration projet.
- `internal/policy` : `Check` renvoie aussi si un `Ask` est levable par un hook.
- `internal/engine` : appels synchrones aux points de branchement (`run`, `decideAndRun`, fin de `loop`, `compactOrStop`, `ask`, fermeture) ; `Deps.Hooks` ; émission de `HookStarted`/`HookCompleted` ; implémentation d'`Evaluator` (requête sans outils via `outbound` ; tour imbriqué en lecture seule).
- `internal/event` : nouveaux types et payloads, champs ajoutés (7.1).
- `cmd/tenon` : chargement, bannière, sous-commande `tenon hooks`, `SessionEnd` à la fermeture.
- `internal/ui/tui`, `internal/ui/headless` : affichage (7.2, 7.3), `/hooks`.
- `internal/i18n` : textes (prompts système des types `prompt` et `agent`, bannière, avis, interface).

## 9. Gestion des erreurs

| Situation | Comportement |
|---|---|
| Fichier de hooks utilisateur invalide | Refus de démarrer, fichier et chemin JSON indiqués |
| Événement ou type inconnu | Avis, groupe ou hook ignoré |
| Hook de projet présent | Avis, jamais exécuté |
| Hook en erreur, délai, JSON de sortie illisible | Non bloquant : l'action continue, avertissement affiché et audité |
| Fournisseur en erreur pour un hook `prompt`/`agent` | Non bloquant |
| `updatedInput` invalide ou en conflit | Appel refusé, `denied: "hook"` |
| Annulation du tour pendant un hook | Hook tué, `status: cancelled`, le tour s'arrête comme pour une annulation |
| Échec d'écriture de l'audit | Fatal, comme pour tout événement |

## 10. Tests

- `internal/hooks` : chargement (sources, option `claude_settings`, `disableAllHooks`, `disabled`, avis de projet), cas d'échec fermé, avis des inconnus, limites ; `matcher` ancré sur les deux noms et sur la source ; traduction aller-retour des arguments ; combinaison `deny` > `ask` > `allow` ; conflit d'`updatedInput` ; exécution réelle de scripts `sh` (codes 0, 2 et autre, JSON, stdout texte, délai, annulation, plafonds, déduplication, parallélisme).
- `internal/policy` : indicateur « levable » pour chaque branche de `Check`.
- `internal/engine`, avec exécuteur simulé : chaque événement ; `allow` ne lève jamais `deny`, `ForceAsk`, un chemin hors projet ou sensible, ni un `ask` du projet ; `ask` d'un hook ignore les approbations mémorisées ; `updatedInput` revalidé puis soumis aux permissions ; relance par `Stop` avec `stop_hook_active` ; `continue: false` ; `--resume` avec `hook_context` ; absence de récursion ; hooks dans un sous-agent (`parent_call_id`) ; `prompt` et `agent` avec un fournisseur simulé, masquage compris ; échec d'audit.
- Bout en bout : `hooks.json` réel qui bloque `shell` ; `settings.json` de Claude Code lu avec `claude_settings = true`, ignoré sans ; avis pour un hook de projet ; `tenon hooks`.

## 11. Hors périmètre

Hooks de projet (même sur confiance), types de hook autres que `command`, `prompt` et `agent`, hooks HTTP, édition des hooks depuis l'interface, compaction manuelle (`PreCompact` `manual`), événement `PostToolUseFailure` et autres événements absents de la liste de la section 2.
