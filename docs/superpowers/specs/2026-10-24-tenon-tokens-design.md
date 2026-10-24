# tenon : sobriété en tokens (v0.8)

Date : 2026-10-24. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Specs liées : [routage par sensibilité](2026-10-22-tenon-routing-design.md), [export de conformité](2026-10-20-tenon-audit-export-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md).

## 1. Objectif

Envoyer moins de tokens par tour sans perte de qualité visible, et le prouver par des mesures réelles inscrites au journal. Aujourd'hui tenon n'estime les tokens qu'en interne (`engine/compact.go`) et ne lit jamais l'`usage` du fournisseur ; les sorties d'outils sont coupées au début seulement ; la compaction résume tout d'un coup au seuil de 0,8.

Critères de succès :

- chaque appel au modèle produit un événement `Usage` (réel, ou estimé et marqué comme tel) ;
- les totaux, la part en cache et les tokens économisés apparaissent dans la TUI et dans l'export ;
- une longue session envoie nettement moins de tokens qu'avant à travail égal (masquage, déduplication), sans casser `--resume`, `/rewind`, l'audit ni le routage ;
- un budget de session avertit puis arrête proprement ;
- la compaction et les sous-agents peuvent utiliser un modèle plus petit, local de préférence ;
- sans configuration nouvelle, seule la mesure change le comportement visible (ligne d'état) ; masquage, déduplication et coupe début-fin s'appliquent par défaut.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Où façonner le contexte | Vue projetée, fonction pure de l'historique, calculée avant chaque envoi | L'historique reste complet pour l'audit, la reprise, le retour arrière et le routage |
| Masquage | Sous pression : au-delà de 50 % de la fenêtre, du plus ancien, jusqu'à 40 % ; 3 derniers tours intacts | Gain fort, risque faible ; la compaction reste le dernier recours |
| Mesure | `stream_options.include_usage`, repli sur l'estimation marquée | Chiffres réels quand le fournisseur les donne, jamais de trou |
| Énergie, CO₂ | Seulement avec des facteurs déclarés par l'utilisateur | Aucun chiffre inventé |
| Budget | Avis à 80 %, refus du tour suivant à 100 % (`budget_exceeded`, code 4 sous `--strict`) | Ne coupe jamais un outil en cours |
| Petits modèles | `[routing] compaction` et `[routing] subagents`, mêmes garde-fous | Sobriété, et la sensibilité prime toujours |

## 3. Mesure

- Chaque requête en flux envoie `stream_options: {"include_usage": true}`. Le dernier fragment porte `usage` : `prompt_tokens`, `completion_tokens`, `prompt_tokens_details.cached_tokens` (absent : 0).
- Fournisseur qui répond 400 en citant `stream_options` : nouvelle tentative unique sans l'option, retenue pour la session. Un preset ou une entrée `[[providers]]` peut la couper d'office : `stream_usage = false`.
- Sans `usage` dans la réponse : estimation actuelle (`estimate`), `estimated: true`.
- Événement `Usage` (un par appel au modèle) : `{provider, model, purpose, input, output, cached, estimated}`, `purpose` ∈ `turn`, `compaction`, `subagent`.
- TUI : ligne d'état `entrée 12,4 k · sortie 1,1 k · session 340 k` (FR) / `in 12.4k · out 1.1k · session 340k` (EN), mise à jour à chaque `Usage`.
- Headless, sortie texte : en fin de tour, sur stderr, `tokens: in <n> · out <n> · cached <n> · session <n>` ; sortie JSON : rien de plus (les événements suffisent).
- Export de conformité : par session et par fournisseur, entrée, sortie, cache, part estimée, tokens économisés (somme des `ContextShaped.saved_tokens`).
- Énergie et CO₂ : `[usage] wh_per_mtoken` et `gco2_per_kwh` (config globale, optionnels, réels positifs). Absents : aucune ligne énergie ni CO₂. Présents : l'export affiche `≈ <Wh> Wh · ≈ <g> g CO₂e (facteurs déclarés)`.

## 4. Budgets

```toml
[budget]
session_tokens = 2000000   # entrée + sortie ; compaction et sous-agents compris ; défaut : aucun
warn_at = 0.8              # 0 < warn_at < 1
```

- Le projet peut seulement baisser `session_tokens` ; la politique peut l'imposer (`[budget] session_tokens`). Plafond effectif : le plus bas.
- `session_tokens` ≤ 0 ou `warn_at` hors ]0, 1[ : `config: budget.<clé> must be …`.
- Compteur : somme `input + output` des `Usage` de la session ; `--resume` le reconstruit depuis le journal.
- À `warn_at` : avis unique `budget: <n> of <max> tokens used (80 %)` (TUI, stderr en headless) et événement `BudgetWarning`.
- À 100 % : le tour en cours se termine normalement ; le tour suivant ne part pas, `TurnCompleted` avec la raison `budget_exceeded`. Sous `--strict`, code de sortie 4 (raison ajoutée aux raisons strictes).

## 5. Vue envoyée au modèle

`view(history) []llm.Message`, appelée avant chaque envoi (tour, compaction, sous-agent sur son propre historique). Déterministe ; ne modifie jamais `history`.

Étapes, dans l'ordre :

1. **Déduplication.** Deux résultats d'outil identiques (même outil, mêmes arguments, même SHA-256 de contenu) : seul le plus récent reste ; l'ancien devient `[same content as the read at turn <n>]`.
2. **Masquage sous pression.** Si l'estimation de la vue dépasse 50 % de la fenêtre, les résultats d'outils les plus anciens sont remplacés, un par un, jusqu'à repasser sous 40 %. Jamais masqués : les résultats des 3 derniers tours utilisateur, le message de résumé de compaction, les messages de l'utilisateur et de l'assistant. Talon : `[output hidden to save context: <tool> <arguments abrégés à 60 car.>, <n> lines, sha256 <12 hex>; run the tool again if you need it]`.
3. Si la vue dépasse encore le seuil de compaction (0,8), la compaction actuelle s'applique (inchangée, sur l'historique).

- Événement `ContextShaped` quand l'ensemble des appels masqués ou dédupliqués change : `{masked: [call_id], deduplicated: [call_id], saved_tokens}`.
- Le routage par sensibilité, le scan sortant, l'audit, `--resume` et `/rewind` utilisent l'historique complet ; masquer ne lève jamais une sensibilité.

## 6. Sorties longues et préfixe stable

- `tools.Truncate(s, limit)` garde les premiers 70 % et les derniers 30 % de `limit`, avec au milieu `[… <n> lines omitted …]` (ou `[… <n> characters omitted …]` sur une seule ligne). Jamais de coupure au milieu d'un caractère UTF-8.
- Préfixe stable : prompt système, instructions de projet, index des skills, définitions d'outils triées par nom (MCP compris), sérialisés de façon déterministe ; aucune date ni valeur variable dans ce préfixe. Les éléments variables (contexte des hooks, avis) vont dans les messages.

## 7. Petits modèles

```toml
[routing]
compaction = "ollama"   # défaut : fournisseur de la session
subagents  = "ollama"   # défaut : fournisseur de la session
```

- Identifiant inconnu : `config: routing.<clé>: unknown provider "<id>"`.
- Chaque appel passe les mêmes contrôles que la session : profil, listes blanches, politique, consentement hors UE. Refus : la compaction ou le sous-agent revient au fournisseur de la session, avec un avis.
- Session sensible : tout appel va au fournisseur local (`routing.local`), quoi que disent ces clés.
- Le fournisseur utilisé apparaît dans `Usage.provider` et dans `ProviderSelected`.

## 8. Architecture

- `internal/provider` : `stream_options`, lecture de `usage`, repli 400 ; `llm.Chunk.Usage *llm.Usage`.
- `internal/llm` : `type Usage struct { Input, Output, Cached int; Estimated bool }`.
- `internal/engine` : `view.go` (déduplication, masquage, `ContextShaped`), compteur et budget, événements `Usage`, `BudgetWarning`, sélection du fournisseur de compaction et des sous-agents.
- `internal/tools` : `Truncate` début-fin.
- `internal/config` : `[budget]`, `[usage]`, `routing.compaction`, `routing.subagents`, `stream_usage` ; durcissement projet et politique.
- `internal/orgpolicy` : `Policy.Budget.SessionTokens`.
- `internal/event`, `internal/compliance` (section tokens), `internal/ui/tui` (ligne d'état, avis), `internal/ui/headless` (ligne stderr, raison stricte), `internal/i18n`, docs EN/FR.

## 9. Tests

- `provider` : `usage` lu sur le dernier fragment ; `cached_tokens` absent → 0 ; 400 citant `stream_options` → relance sans l'option, une seule fois ; `stream_usage = false` → option absente ; réponse sans `usage` → estimation marquée.
- `engine/view` : déduplication garde le plus récent ; masquage seulement au-delà de 50 %, s'arrête sous 40 %, épargne les 3 derniers tours et le résumé ; vue déterministe ; historique inchangé ; `ContextShaped` émis seulement sur changement.
- `engine` : `Usage` par appel (tour, compaction, sous-agent) ; budget : avis à 80 % une fois, refus du tour suivant à 100 %, compteur reconstruit à la reprise ; session sensible → compaction et sous-agents au fournisseur local.
- `tools` : coupe 70/30 sur plusieurs lignes et sur une ligne, UTF-8 préservé.
- Préfixe stable : deux requêtes successives d'une même session ont un préfixe identique octet pour octet (prompt système et outils).
- `config` / `orgpolicy` : valeurs invalides, projet qui ne peut que baisser, politique qui impose.
- Bout en bout : `--strict` avec budget dépassé → code 4 ; ligne `tokens:` en sortie texte ; export avec la section tokens et l'énergie seulement si facteurs déclarés.

## 10. Hors périmètre

Mémoire persistante (spec suivante), tarification en euros, cache de réponses, résumé partiel progressif, compression sémantique des fichiers, choix automatique du modèle selon la difficulté.
