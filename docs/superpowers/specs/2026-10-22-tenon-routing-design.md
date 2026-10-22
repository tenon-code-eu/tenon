# tenon : routage par sensibilité des données (v0.8)

Date : 2026-10-22. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Specs liées : [profils de conformité](2026-10-20-tenon-compliance-profiles-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md), [liste blanche et secnumcloud](2026-10-21-tenon-allowlist-secnumcloud-design.md), [export de conformité](2026-10-20-tenon-audit-export-design.md).

## 1. Objectif

Le contenu classé (par chemin ou par marquage) ne quitte jamais la machine : il n'est envoyé qu'à un modèle local. Le reste part vers les fournisseurs configurés, avec bascule ordonnée en cas de panne. Le journal prouve chaque décision, et l'export le vérifie.

Chaque requête renvoie tout l'historique : dès qu'un contenu sensible y entre, toutes les requêtes suivantes le transportent. La sensibilité est donc un état de la session (« contamination »), pas une propriété d'une requête.

Critères de succès :

- après la lecture d'un fichier classé, aucune requête ne part vers un fournisseur non local, compaction comprise ;
- sans fournisseur local, le contenu classé n'entre jamais dans l'historique ;
- un marquage collé par l'utilisateur ou sorti d'une commande est rattrapé avant l'envoi ;
- une panne (connexion, 429, 5xx) bascule vers le fournisseur suivant, sous les mêmes garde-fous ;
- l'export signale toute requête non locale après le passage en mode sensible (code 2) ;
- sans `[classification]` ni `[routing]`, le comportement actuel est inchangé, à l'exception des marquages intégrés (§3) ;
- aucune dépendance nouvelle.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Source de la sensibilité | Motifs de chemins + marquages dans le contenu | Prévisible (chemins) et rattrape les fichiers mal rangés (marquages) |
| Portée | État de session, persistant à la reprise | L'historique est renvoyé à chaque requête |
| Sans modèle local | Blocage : lecture refusée avant, sortie masquée après, tour bloqué pour un message | Rien de sensible ne doit partir |
| Bascule | Liste ordonnée, sur connexion/délai/429/5xx avant le premier jeton | Disponibilité sans dupliquer de sortie |
| Mécanisme | Routeur dans le moteur + contrôle sortant des marquages | Décision avant lecture, filet avant envoi |
| Sous-agents | Partagent l'état de la session | Leur historique remonte au parent |

## 3. Configuration

Config globale :

```toml
[classification]
paths = ["rh/**", "contrats/**", "*.dr.md"]    # relatifs à la racine du projet
markers = ["C2 - Interne"]                      # en plus des marquages intégrés

[routing]
local = "ollama"                                # fournisseur du contenu sensible
fallback = ["mistral", "scaleway", "ovhcloud"]  # bascule ordonnée pour le reste
```

- Marquages intégrés, toujours actifs : `DIFFUSION RESTREINTE`, `SPÉCIAL FRANCE`, `SPECIAL FRANCE`, `RESTREINT UE`, `EU RESTRICTED`.
- Correspondance des marquages : insensible à la casse et aux accents (NFD, marques combinantes retirées), espaces multiples et retours à la ligne réduits à un espace, phrase entière (bornes de mot aux deux extrémités).
- Motifs de chemins : syntaxe des motifs de permissions (`*` dans un segment, `**` sur plusieurs), comparés au chemin relatif à la racine résolue du projet ; un chemin hors projet ne correspond jamais à un motif relatif ; un motif absolu est accepté.
- `routing.local` : identifiant d'un fournisseur de juridiction `local` (preset `ollama`/`vllm` ou fournisseur déclaré) ; sinon `config: routing.local must name a local provider`.
- `routing.fallback` : identifiants connus ; sinon `config: routing.fallback: unknown provider "<id>"`. Le fournisseur courant n'est pas répété s'il y figure.
- Config projet (`.tenon/config.toml`) : `paths` et `markers` ajoutés ; `[routing]` refusé (`config: routing not allowed in project configuration`).
- Politique d'organisation : `[classification] paths/markers` ajoutés ; `[routing] local` imposé (fournisseur de la politique ou preset local), remplace celui de l'utilisateur avec la notice `policy <id>: routing.local imposed (<id>)`. Personne ne retire un motif ou un marquage.
- Sans `[routing]` : fournisseur unique, aucune bascule, aucun fournisseur local (la classification s'applique quand même : contenu sensible → blocage).

## 4. Passage en mode sensible

La session devient sensible (une fois, définitivement) quand, avec un fournisseur local disponible :

1. un outil de fichiers (`read_file`, `edit_file`, `write_file`, `search`) touche un chemin classé, ou son résultat contient un marquage ;
2. une commande shell contient dans son texte un chemin classé (correspondance d'un motif sur un mot de la commande), ou sa sortie contient un marquage ;
3. le résultat d'un outil MCP contient un marquage ;
4. un message de l'utilisateur contient un marquage ;
5. le contrôle sortant (§5) trouve un marquage dans une requête destinée à un fournisseur non local.

Sans fournisseur local :

- cas 1 et 2 par chemin : l'outil est refusé avant exécution (`ToolResult` `denied: "classification"`, contenu `classified content requires a local provider (routing.local)`) ;
- marquage dans un résultat (1, 2, 3) : le contenu est remplacé par `[classified content withheld: no local provider]` avant d'entrer dans l'historique ; la session ne devient pas sensible ;
- marquage dans un message de l'utilisateur ou dans une requête (4, 5) : le tour est bloqué (`Error` `code: "classification"`, tour `blocked`).

Session reprise : sensible si son journal contient `SessionClassified`.

## 5. Routage et bascule

Avant chaque envoi au modèle (tour, compaction, sous-agent) :

- session sensible : `routing.local` uniquement ; un échec termine le tour en erreur, jamais de bascule vers un fournisseur non local ;
- session normale : le fournisseur courant (`--provider` ou `default_provider`), puis `routing.fallback` dans l'ordre ;
- chaque candidat passe les garde-fous existants (profil, liste blanche, politique, accord `non-eu`) ; un candidat refusé est sauté avec une notice ; aucun candidat → tour bloqué ;
- bascule vers le candidat suivant sur : erreur de connexion, délai dépassé avant le premier jeton, HTTP 429, HTTP 5xx ; jamais après le premier jeton reçu, jamais sur une autre erreur 4xx ;
- contrôle sortant : avant un envoi à un fournisseur non local, recherche des marquages dans le texte de tous les messages de la requête ; trouvé → passage en mode sensible (source `outbound`) et envoi au local, ou blocage sans local ;
- fenêtre de contexte : celle du fournisseur retenu ; si l'historique la dépasse, compaction avant l'envoi, elle-même routée.

## 6. Journal, interface, export

- `SessionClassified` : `{reason: "path"|"marker", source: "tool"|"shell"|"mcp"|"user"|"outbound", call_id, path, marker}` (champs vides omis), émis une fois.
- `ProviderSelected` : `{provider, reason: "default"|"fallback"|"sensitive", error}` (`error` pour une bascule), émis quand le fournisseur effectif change, y compris au premier envoi.
- `SessionStarted.routing` : `{local, fallback}` (omis sans `[routing]`).
- TUI : indicateur `sensible → <local>` dans la barre d'état ; notice à chaque bascule.
- Headless (texte) : stderr `classification: session is sensitive (<reason>), routed to <local>` et `routing: <from> failed (<error>), using <to>`.
- `tenon config check` : `classification: <n> paths, <m> markers` et `routing: local <id>, fallback <ids>` (ou `routing: none`).
- Export de conformité : par session, `routing {sensitive, reason, providers, fallbacks, violation}` ; `violation` vaut true si un `ProviderSelected` vers un fournisseur non local suit `SessionClassified` ; une violation donne le code 2 (comme une chaîne cassée). Ligne HTML « Routage » / « Routing ».

## 7. Architecture

- `internal/classify` (nouveau) : `type Rules struct { Paths, Markers []string }`, `func (r Rules) MatchPath(root, path string) bool`, `func (r Rules) FindMarker(text string) (string, bool)`, `func (r Rules) MatchCommand(root, cmd string) (string, bool)`, `BuiltinMarkers`.
- `internal/routing` (nouveau) : `type Candidate struct { ID string; Provider provider.Provider }`, `type Router struct`, `func (r *Router) Candidates(sensitive bool) []Candidate`, `func Retryable(err error) bool` (connexion, délai avant premier jeton, 429, 5xx).
- `internal/engine` : état sensible de session, contrôle des outils avant exécution et des résultats, contrôle sortant, envoi et compaction via le routeur, garde-fous par candidat, événements ; sous-agents sur le même état.
- `internal/provider` : erreurs typées pour statut HTTP et absence de premier jeton (si absentes).
- `internal/config`, `internal/orgpolicy` : `[classification]`, `[routing]`, cumul projet et politique.
- `internal/event`, `internal/compliance`, `internal/ui/tui`, `internal/ui/headless`, `internal/i18n`, `cmd/tenon` (construction des fournisseurs, détection du local, notices, `config check`), docs EN/FR (usage, sécurité, politique, `docs/config.example.toml`).

## 8. Tests

- `classify` : motifs (`**`, absolu, hors projet), marquages (casse, accents, retours à la ligne, bornes de mot : `DIFFUSION RESTREINTES` ne correspond pas), commande shell.
- `routing` : ordre, bascule sur 429, 5xx, connexion refusée, délai ; pas de bascule après premier jeton ni sur 401 ; candidat sauté par un garde-fou ; session sensible → local seul.
- `config` / `orgpolicy` : `routing.local` non local refusé, `fallback` inconnu refusé, `[routing]` projet refusé, cumul des motifs, politique qui impose `local`.
- Bout en bout (faux fournisseurs local et distant) : lecture d'un fichier classé → envois suivants au local seul, distant plus jamais appelé ; sans local → lecture refusée ; marquage dans une sortie shell → bascule au local ou contenu masqué ; marquage dans un message → bascule ou blocage ; contrôle sortant ; reprise d'une session sensible ; sous-agent qui lit un fichier classé ; compaction routée au local ; bascule distante sur 503 avec `ProviderSelected` ; export avec ligne Routage et code 2 sur journal falsifié.

## 9. Hors périmètre

Détection automatique de données personnelles, de secrets ou d'identifiants (NIR, IBAN), déclassification ou purge de l'historique, routage par coût ou par taille de tâche, plusieurs fournisseurs locaux, équilibrage de charge.
