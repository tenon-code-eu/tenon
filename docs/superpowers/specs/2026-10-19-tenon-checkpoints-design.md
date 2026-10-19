# tenon : points de restauration et écran d'accueil (v0.6)

Date : 2026-10-19. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Sous-agents : [2026-10-19-tenon-subagents-design.md](2026-10-19-tenon-subagents-design.md). Hooks : [2026-10-19-tenon-hooks-design.md](2026-10-19-tenon-hooks-design.md).

## 1. Objectif

Parité avec les checkpoints de Claude Code : chaque prompt de l'utilisateur est un point de restauration ; `/rewind` (ou Échap deux fois) revient à un point précédent en restaurant le code, la conversation, ou les deux. En complément, l'écran d'accueil de l'interface affiche le nombre de skills, d'agents et de hooks, et la liste des commandes `/`.

Critères de succès :

- toute modification faite par `write_file` ou `edit_file` (agent principal, sous-agent, entrée réécrite par un hook) peut être annulée ;
- un fichier modifié par l'utilisateur après le passage de l'agent n'est jamais écrasé sans son accord explicite ;
- le journal de session et l'audit ne sont jamais réécrits : un rewind ajoute un événement, et `--resume` reconstruit le même état ;
- l'écran d'accueil montre les compteurs et toutes les commandes intégrées.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Ce qui est annulable | Écritures de `write_file` et `edit_file` seulement ; `shell` et les outils MCP ne sont pas annulés, `/rewind` le signale | Comme Claude Code ; fiable, sans dépendre de git |
| Conflits | Aperçu, puis choix fichier par fichier ; par défaut, la version de l'utilisateur est gardée | Aucune perte de travail en silence |
| Conversation | Trois modes : code et conversation, conversation seule, code seul | Comme Claude Code |
| Journal | Jamais réécrit ; événement `Rewound` ajouté | Audit complet, reprise fidèle |
| Stockage | Magasin de contenus indexé par sha256, hors du dépôt | `ToolResult` porte déjà `hash_before`/`hash_after` ; pas de doublon ; le journal masqué ne peut pas contenir les contenus |
| Écran d'accueil | Compteurs skills/agents/hooks et liste des commandes intégrées | Demande de l'utilisateur |

## 3. Capture

### 3.1 Magasin

- Package `internal/checkpoint`, dossier `$XDG_DATA_HOME/tenon/checkpoints` (sinon `~/.local/share/tenon/checkpoints`), comme `config.DataDir`.
- Une copie par contenu : `checkpoints/<2 premiers caractères du sha256>/<sha256 complet>`. Dossiers 0700, fichiers 0600. Écriture atomique (fichier temporaire dans le même dossier, puis renommage). Un contenu déjà présent n'est pas réécrit.
- Lecture : le sha256 du contenu lu doit être égal au nom, sinon la copie est refusée (`corrupted checkpoint`).
- Taille maximale d'une copie : 10 Mio (10 485 760 octets).

### 3.2 Moment de la copie

- `tools.FS` accepte l'option `tools.WithSnapshot(save func(path string, old []byte) error)`. `write_file` et `edit_file` appellent `save` avec le contenu qu'ils viennent de lire, avant d'écrire ; c'est exactement le contenu dont le hash est `hash_before`.
- Échec de `save` : l'écriture est refusée, résultat d'erreur `cannot save checkpoint: <erreur>`.
- Fichier de plus de 10 Mio : écrit sans copie ; le résultat ajoute la ligne `checkpoint skipped (file too large)` ; `ToolResult.no_checkpoint = true` (nouveau champ, omitempty).
- Fichier qui n'existait pas : `hash_before` vide, rien n'est copié ; sa restauration est une suppression.
- `[checkpoints] retention_days = 0` désactive les copies : `save` n'est pas installé, les écritures portent `no_checkpoint`, et un avis le signale au démarrage.

### 3.3 Ce qui est suivi

- Points de restauration : chaque `UserMessage` de l'agent principal accepté (ni `blocked`, ni `hook`, sans `parent_call_id`). Un point est identifié par le `seq` de cet événement.
- Écritures : chaque `ToolResult` avec `path` et `hash_after` non vides (agent principal et sous-agents, `parent_call_id` compris).
- Appels non annulables : chaque `ToolResult` d'un appel `shell` exécuté, et de chaque outil `mcp__*` de risque `write` ou `exec` (le risque est lu dans le registre d'outils au moment de l'appel et enregistré dans le nouveau champ `ToolResult.untracked = true`, omitempty).
- Rien d'autre n'est écrit dans le journal pour la capture.

## 4. Rewind

### 4.1 État en mémoire

Le moteur tient pour chaque session, en direct et dans `Restore` :

- les points : `seq`, extrait du prompt (première ligne, 80 caractères), longueur de l'historique juste avant le message, et si la conversation est encore rembobinable (point postérieur au dernier `ContextCompacted` de l'agent principal) ;
- les écritures : `seq`, chemin absolu, `hash_before`, `hash_after`, `no_checkpoint` ;
- les appels non annulables : `seq`, outil ;
- les rewinds déjà faits (événements `Rewound`), qui comptent comme des écritures pour les fichiers restaurés ou supprimés.

### 4.2 Plan

`func (e *Engine) RewindPlan(sessionID string, toSeq uint64) (RewindPlan, error)` ne modifie rien.

- Fichiers concernés : ceux qui ont au moins une écriture de `seq > toSeq`. Pour chacun :
  - cible : `hash_before` de sa première écriture après `toSeq` ; action `restore` (cible non vide) ou `delete` (cible vide) ;
  - attendu : `hash_after` de sa dernière écriture (un rewind antérieur compte comme écriture : attendu = contenu restauré, ou « absent » pour une suppression) ;
  - état : `ok` si le fichier actuel correspond à l'attendu (même hash, ou absent quand l'attendu est « absent ») ; `conflict` sinon, ou si le chemin est devenu un lien symbolique ; `unrestorable` si l'action est `restore` et que la copie manque, est corrompue, ou si une écriture concernée porte `no_checkpoint` ; `noop` si le fichier actuel est déjà dans l'état cible (rien à faire).
- Avertissement : nombre d'appels non annulables de `seq > toSeq`.
- `ConversationAvailable` : vrai si le point est rembobinable.
- `toSeq` inconnu : erreur `unknown checkpoint`.

### 4.3 Exécution

`func (e *Engine) Rewind(ctx context.Context, sessionID string, toSeq uint64, mode RewindMode, overwrite []string) (RewindResult, error)`, `mode` ∈ `code`, `conversation`, `both`.

- Refusé si un tour est en cours (`a turn is in progress`) : le verrou de session est pris sans attendre (TryLock).
- Le plan est recalculé au moment de l'exécution.
- Code (`code`, `both`) : pour chaque fichier `ok`, ou `conflict` dont le chemin est dans `overwrite` : `restore` écrit la copie (fichier temporaire, puis renommage ; droits du fichier actuel s'il existe, sinon 0644 ; dossiers parents recréés au besoin), `delete` supprime le fichier (absent : rien). `unrestorable`, `noop` et les conflits non choisis sont sautés. Un échec sur un fichier n'arrête pas les autres.
- Seuls les chemins de la liste du plan sont touchés ; aucun lien symbolique n'est suivi.
- Conversation (`conversation`, `both`) : l'historique est tronqué à la longueur enregistrée pour le point ; le résultat porte le texte complet du prompt choisi (pour la zone de saisie). Refusé si `ConversationAvailable` est faux.
- `code` seul : note en attente, jointe au prochain message utilisateur comme un contexte de hook : `[tenon] The user restored files to their state before: "<extrait du prompt>"`.
- Événement `Rewound` émis à la fin, dans la chaîne d'audit (section 6).

### 4.4 Reprise

`Restore` applique un `Rewound` : modes `conversation` et `both` tronquent l'historique reconstruit à la longueur enregistrée pour `to_seq` ; la note d'un mode `code` reste en attente jusqu'au prochain `UserMessage` accepté, qui la porte dans `hook_context`. Les événements de la partie annulée restent dans le journal mais n'entrent plus dans l'historique ; leurs écritures restent dans l'état en mémoire (elles ont eu lieu).

## 5. Interface

### 5.1 Rewind

- Ouverture : `/rewind`, ou Échap deux fois en moins de 1 s quand aucun tour ne tourne. Pendant un tour, Échap annule le tour comme aujourd'hui.
- Étape 1, point : liste des points, du plus récent au plus ancien : `#<n>  <extrait>` ; marqueur `(code only)` / `(code seul)` quand la conversation n'est plus rembobinable. ↑/↓, Entrée, Échap ferme.
- Étape 2, aperçu et mode : une ligne par fichier (`restore`, `delete`, `conflict: modified since`, `unrestorable`, `unchanged`), l'avertissement `N shell or MCP calls are not undone` s'il y en a, puis le choix du mode (`code and conversation`, `conversation only`, `code only`) ; les modes impossibles sont grisés. Aucun fichier à restaurer et conversation impossible : message `nothing to rewind`.
- Étape 3, conflits : pour chaque conflit, `overwrite <chemin>? y/N` ; défaut : garder.
- Bilan : `rewind: N restored · N deleted · N kept · N failed` (+ erreurs) ; en mode conversation, le prompt choisi revient dans la zone de saisie.
- Tous les textes passent par i18n (EN/FR) ; chemins et extraits nettoyés des caractères de contrôle et masqués comme le reste de l'affichage.

### 5.2 Écran d'accueil et `/help`

Sous la ligne du projet :

```
skills : 5 · agents : 3 · hooks : 2
commandes : /help · /skills · /agents · /hooks · /rewind · /quit
Entrée pour envoyer · Échap ×2 pour revenir en arrière · Ctrl+C pour quitter
```

- Compteurs : nombre d'entrées listées par `/skills`, `/agents` (`general-purpose` compris) et `/hooks` ; affichés même à 0.
- Liste des commandes : construite depuis la liste des commandes intégrées (celle de la complétion Tab) ; une nouvelle commande y apparaît sans autre changement. Les skills invocables n'y figurent pas.
- Ces lignes passent à la ligne (retour à la ligne aux séparateurs ` · `) au lieu d'être tronquées.
- `/help` affiche la même liste, une commande par ligne avec une description courte (i18n).

### 5.3 Autres modes

- Mode `-p` : pas de rewind. `--output json` inclut les événements `Rewound` d'une session reprise.
- `tenon rewind --list --resume <id|last> [--lang fr|en]` : liste les points d'une session (`#<n>  seq  <extrait>  (code only)`), sans rien restaurer.

## 6. Événements et audit

- `Rewound` : `to_seq`, `mode`, `files` (liste de `{path, action, status, error}`, `status` ∈ `restored`, `deleted`, `kept`, `skipped`, `failed`, `unchanged`), `untracked_calls` (nombre), `note` (mode code seul).
- `ToolResult` gagne `no_checkpoint` et `untracked` (omitempty).
- Les événements sans ces champs se sérialisent comme avant.

## 7. Configuration et nettoyage

```toml
[checkpoints]
retention_days = 30   # 0 : pas de copie, pas de rewind du code
```

- Clé globale uniquement (refusée dans `.tenon/config.toml`).
- Au démarrage : les copies de plus de `retention_days` jours qu'aucune session de moins de `retention_days` jours ne référence (`hash_before` d'un `ToolResult`) sont supprimées. Erreurs de nettoyage : avis, jamais bloquantes.

## 8. Sécurité

- Le magasin contient le contenu brut des fichiers, secrets compris (restauration à l'identique) : hors du dépôt, 0700/0600, jamais envoyé au fournisseur, jamais copié dans le journal ou l'audit.
- Un rewind n'écrit que des chemins déjà écrits par les outils dans cette session ; un chemin devenu lien symbolique est un conflit et n'est pas suivi.
- Copie vérifiée par son sha256 avant restauration.
- Le rewind est une action de l'utilisateur : aucun outil ne le déclenche, aucun hook non plus.
- La documentation de sécurité décrit le magasin et la commande pour le vider (`rm -rf ~/.local/share/tenon/checkpoints`).

## 9. Architecture

- `internal/checkpoint` (nouveau) : `Store` (`Save`, `Load`, `Has`, `Prune`), plan pur (`Plan(state, toSeq, current func(path) FileState) Plan`) testable sans moteur.
- `internal/tools` : option `WithSnapshot`, champ `Result.NoCheckpoint`.
- `internal/engine` : état de rewind par session, `RewindPlan`, `Rewind`, `Restore` des `Rewound`, note en attente, `ToolResult.untracked`.
- `internal/event` : `Rewound`, champs `no_checkpoint`, `untracked`.
- `internal/config` : `[checkpoints] retention_days`.
- `cmd/tenon` : magasin, nettoyage au démarrage, `tenon rewind --list`.
- `internal/ui/tui` : Échap ×2, `/rewind` en trois étapes, écran d'accueil, `/help`.
- `internal/i18n` : textes.

## 10. Gestion des erreurs

| Situation | Comportement |
|---|---|
| Copie impossible avant une écriture | Écriture refusée, `cannot save checkpoint: <erreur>` |
| Fichier > 10 Mio | Écrit sans copie, avis, `no_checkpoint` |
| Copie absente ou corrompue | `unrestorable`, sauté, signalé |
| Échec d'écriture ou de suppression pendant un rewind | `failed` avec l'erreur, les autres continuent |
| Tour en cours | Rewind refusé |
| Conversation non rembobinable | Mode refusé (grisé dans l'interface) |
| Nettoyage en échec | Avis, démarrage continue |
| Échec d'audit sur `Rewound` | Fatal, comme tout événement (les fichiers déjà restaurés le restent) |

## 11. Tests

- `internal/checkpoint` : déduplication, droits, écriture atomique, intégrité, taille maximale, nettoyage (âge et références).
- Plan : fichier créé puis rewind (suppression), modifié une fois, deux fois, modifié par l'utilisateur (conflit), supprimé par l'utilisateur, remplacé par un lien symbolique, copie manquante, `no_checkpoint`, écritures de sous-agent, appels shell/MCP comptés, point avant compaction, deux rewinds successifs, `noop`.
- Moteur : `Rewind` des trois modes, `overwrite`, échec partiel, tour en cours, note du mode code seul jointe au message suivant, `Rewound` émis, `--resume` après `Rewound` (historique et note identiques au direct), écriture refusée si la copie échoue.
- Interface : Échap ×2, trois étapes, `y/N`, prompt remis dans la saisie, écran d'accueil (compteurs, commandes, retour à la ligne), `/help`.
- Bout en bout : session réelle qui écrit, rewind, fichier restauré ; `tenon rewind --list`.

## 12. Hors périmètre

Annulation des effets de `shell` et des outils MCP, snapshots git, rewind en mode `-p`, rewind déclenché par le modèle ou un hook, restauration au-delà d'une compaction pour la conversation, partage des points de restauration entre machines.
