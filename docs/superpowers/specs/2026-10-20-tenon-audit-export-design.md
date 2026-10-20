# tenon : export de conformité (v0.7)

Date : 2026-10-20. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Suite prévue : profils de conformité (v0.8, spec séparée).

## 1. Objectif

Donner au DPO, au RSSI ou à l'auditeur une preuve lisible de ce que tenon a fait : fournisseurs et juridictions contactés, masquages, permissions, fichiers modifiés, incidents, avec la chaîne d'audit vérifiée. Le rapport aide à documenter des obligations (RGPD, AI Act, NIS2, DORA) sans prétendre certifier une conformité.

Critères de succès :

- `tenon audit export` produit un HTML autonome et un JSON structuré à partir du journal d'audit, sans le modifier ;
- le rapport dit si la chaîne d'audit est intacte et donne de quoi refaire la vérification ;
- par défaut, aucun prompt, aucune réponse du modèle ni contenu de résultat d'outil n'apparaît (minimisation) ;
- aucun secret ni code actif ne passe dans le rapport.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Format | HTML autonome (imprimable en PDF par le navigateur) + JSON | Lisible par un public non technique ; exploitable par les outils ; aucune dépendance |
| Contenu | Synthèse par défaut ; chronologie avec prompts et commandes avec `--details` | Minimisation des données (RGPD) |
| Réglementation | Tableau de correspondance factuel + avertissement « pas un avis juridique » ; pas de numéros d'article | Utile sans promesse juridique |
| Source | Journal d'audit chaîné (`audit/*.jsonl`), vérifié d'abord | Seule source qui fait foi |
| Architecture | Package `internal/compliance` : chargement, modèle pur, deux rendus | Testable, sans dépendance |

## 3. Commande

```
tenon audit export [--since AAAA-MM-JJ] [--until AAAA-MM-JJ]
                   [--project <chemin> | --all-projects] [--session <id>]
                   [--details] [--out <dossier>] [--lang fr|en]
```

- Période : dates UTC, bornes incluses (`--until` couvre la journée entière). Défaut : les 30 derniers jours jusqu'à aujourd'hui inclus.
- Périmètre : défaut = projet courant, identifié par le `project_root` de `SessionStarted` (même comparaison que `tenon sessions` : `session.SameRoot`). `--project <chemin>` : un autre projet. `--all-projects` : tous. `--session <id>` : une seule session (combinable avec la période). `--project`, `--all-projects` et `--session` sont exclusifs deux à deux.
- Un événement appartient au périmètre si sa session appartient au périmètre (la session est rattachée à un projet par son premier `SessionStarted`, même antérieur à la période). Au niveau `metadata`, ce projet est inconnu : la session n'entre que dans `--all-projects` ou `--session`, et le rapport indique le nombre de sessions écartées pour cette raison.
- Sortie : `--out` (défaut : dossier courant) reçoit `tenon-audit-<since>_<until>.html` et `.json`, en 0600. Si l'un des deux existe déjà : erreur, rien n'est écrit.
- `--lang` : langue du HTML (défaut : langue de l'interface). Le JSON a des clés anglaises.
- Codes de sortie : 0 succès ; 1 erreur d'usage ou d'écriture ; 2 rapport produit mais chaîne d'audit cassée.

## 4. Intégrité

- L'export vérifie la chaîne entière avec `audit.Verify` avant de lire les données.
- Intacte : en-tête `chaîne intacte : N lignes, hash final <hash>`. Cassée : bandeau rouge `journal altéré : <erreur de Verify>` en tête du rapport, l'export continue avec les lignes lisibles, code de sortie 2.
- Le rapport indique : premier et dernier hash des lignes de la plage exportée, nombre de lignes de la plage, niveau d'audit détecté (`full` si les payloads sont complets, `metadata` si les payloads sont `{sha256,size}`, `mixed` sinon), version de tenon, date de génération (UTC).
- Lien entre les deux fichiers (sans dépendance circulaire) : `report_id` = sha256 de la sérialisation JSON canonique des données du rapport (le champ `report` du JSON). Le HTML affiche ce `report_id` ; le JSON contient `report_id` et `html_sha256` (sha256 du fichier HTML, calculé après son rendu). On peut ainsi vérifier que le HTML et le JSON décrivent les mêmes données, et que le HTML n'a pas été modifié.
- Niveau `metadata` : seuls types d'événements, dates et sessions sont exploitables ; bandeau `journal au niveau metadata : contenu non disponible` ; les sections de contenu affichent `non disponible au niveau metadata`.

## 5. Contenu

Calculé sur les événements du périmètre et de la période.

1. En-tête : période, périmètre, date de génération, version, niveau d'audit, intégrité (§4), avertissement : « Ce rapport décrit l'activité enregistrée par tenon. Il ne constitue pas un avis juridique et ne certifie aucune conformité. »
2. Synthèse : nombre de sessions, de tours (`TurnCompleted`), d'appels d'outils (`ToolCallRequested`), de fichiers modifiés, de refus ; fournisseurs avec juridiction (`UE`, `hors UE`, `local`).
3. Fournisseurs et transferts : par fournisseur (`SessionStarted.provider`) : nom, modèle, pays, juridiction, nombre de sessions, première et dernière date ; consentements de juridiction (`PermissionRequested{kind:"jurisdiction"}` + `PermissionResolved`) accordés/refusés ; sessions avec `JurisdictionWarning` ; serveurs MCP (`SessionStarted.mcp`) avec juridiction, et pannes (`McpServerDown`).
4. Masquages : total par type (`RedactionApplied.counts`).
5. Permissions : par outil, demandes (`PermissionRequested{kind:"tool"}`), accordées une fois, accordées pour la session (`remember`), refusées par l'utilisateur ; refus par règle (`ToolResult.denied = "policy"`) et par hook (`"hook"`).
6. Fichiers modifiés : par `ToolResult` avec `path` et `hash_after` : chemin, outil, date, hash avant/après, auteur (agent principal, ou sous-agent si `parent_call_id`), `no_checkpoint` ; état après rewind si un `Rewound` le concerne (`restored`, `deleted`, ...).
7. Commandes : nombre d'appels `shell` par session ; avec `--details`, le texte des commandes.
8. Sous-agents, hooks, rewinds : `SubagentStarted/Completed` (agent, statut) ; `HookCompleted` avec statut `blocked`, `error`, `timeout` ou décision `ask` ; `Rewound` (point, mode, bilan).
9. Incidents : `Error` (code, message), `TurnCompleted` avec raisons `blocked`, `blocked_by_hook`, `stopped_by_hook`, `context_too_large`, `max_iterations`, `incomplete`, `cancelled` ; lignes illisibles (nombre, fichier:ligne).
10. Correspondance réglementaire : tableau « élément du rapport → aide à documenter », formulation prudente :
    - Fournisseurs, juridictions, consentements → registre des traitements et transferts de données (RGPD) ;
    - Masquages, permissions → mesures techniques de protection (RGPD, NIS2) ;
    - Chaîne d'audit vérifiée, incidents → traçabilité et gestion des incidents (NIS2, DORA) ;
    - Fournisseurs et modèles utilisés, fichiers modifiés par l'agent → transparence sur l'usage de l'IA (AI Act).
11. Chronologie (`--details` seulement) : par session, dans l'ordre : prompts, réponses (500 premiers caractères), appels d'outils (outil, arguments) et résultats (500 premiers caractères), permissions, hooks. Bandeau en tête du rapport : « Ce rapport contient des données de conversation. »

Code généré : le marquage git n'est pas dans le journal ; le rapport l'indique et donne la commande `git log --grep "AI-Assisted-By"`.

### JSON

Mêmes sections, clés anglaises stables, `schema_version: 1`, `generator: "tenon <version>"`. Schéma documenté dans la documentation (champs, types, unités ; dates RFC 3339 UTC).

## 6. Sécurité

- Masquage au rendu : tout texte issu du journal passe par le masqueur courant (config globale et projet) avant rendu, en plus du masquage déjà appliqué à l'écriture.
- HTML : `html/template` (échappement automatique), CSS en ligne, aucun script, aucune ressource externe ; `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`.
- Texte affiché : caractères de contrôle remplacés par des espaces.
- Fichiers 0600, jamais écrasés ; le journal n'est jamais modifié ni verrouillé en écriture.
- Sans `--details`, le rapport ne contient ni prompt, ni réponse, ni argument d'outil, ni contenu de résultat d'outil (les chemins de fichiers modifiés et les noms d'outils restent).

## 7. Architecture

- `internal/audit` : `ReadLines(dir string, fn func(file string, lineNo int, l Line, err error) error) error` (lecture en ordre des fichiers, ligne par ligne ; `Line` exporte seq, ts, session, type, payload, prev, hash).
- `internal/compliance` (nouveau) :
  - `Filter{Since, Until time.Time; Project string; AllProjects bool; Session string}` ;
  - `Load(dir string, f Filter) (Dataset, error)` (vérification de la chaîne, lignes du périmètre, lignes illisibles, niveau détecté) ;
  - `Build(ds Dataset, opts Options) Report` (pur : sections §5, masquage et nettoyage des textes) ;
  - `ReportID(r Report) (string, error)`, `RenderHTML(r Report, reportID string, lang i18n.Lang) ([]byte, error)` (gabarit et CSS via `embed`), `RenderJSON(r Report, reportID, htmlSHA256 string) ([]byte, error)`.
- `cmd/tenon` : `tenon audit export` (analyse des options, vérification, écriture des deux fichiers, codes de sortie).
- `internal/i18n` : textes du HTML et des messages (EN/FR).
- Docs EN/FR : usage, sécurité, schéma JSON.

## 8. Erreurs

| Situation | Comportement |
|---|---|
| Chaîne cassée | Rapport produit, bandeau `journal altéré`, code 2 |
| Ligne illisible | Comptée et listée dans les incidents, ignorée pour les autres sections |
| Aucun événement | Rapport valide, mention `aucune activité sur la période`, code 0 |
| Fichier de sortie existant | Erreur, rien n'est écrit, code 1 |
| Date ou option invalide, `--since` après `--until`, options de périmètre combinées | Erreur d'usage, code 1 |
| Dossier d'audit absent | Rapport vide valide (chaîne vide intacte), code 0 |

## 9. Tests

- `Build` sur journaux construits à la main : chaque section ; filtres période (bornes incluses), projet, tous projets, session ; niveau `metadata` et `mixed` ; lignes illisibles ; aucune activité ; fichiers modifiés par un sous-agent ; état après rewind.
- Rendu JSON : clés et `schema_version` stables (fichier de référence) ; `html_sha256` correct.
- Rendu HTML : un prompt `<script>alert(1)</script>` est échappé ; aucune balise `<script>`, aucune URL `http(s)://` de ressource ; CSP présente ; bandeau `--details` présent avec l'option, aucun prompt sans elle ; FR et EN.
- Masquage au rendu : un secret présent en clair dans le journal est masqué dans les deux fichiers.
- Bout en bout : session réelle (fournisseur simulé) puis `tenon audit export` ; chaîne cassée → code 2 et bandeau ; fichier existant → code 1 sans écriture ; options invalides.

## 10. Hors périmètre

Profils de conformité (v0.8), PDF natif, signature du rapport, horodatage qualifié, export du marquage git, envoi automatique du rapport, agrégation multi-postes.
