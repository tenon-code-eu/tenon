# tenon : agent ACP pour les éditeurs (`tenon acp`)

Date : 2026-10-26. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md) (feuille de route §2 : « mode client/serveur pour intégrations IDE »). Specs liées : [isolation système](2026-10-22-tenon-sandbox-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md), [export de conformité](2026-10-20-tenon-audit-export-design.md).

## 1. Objectif

Utiliser tenon depuis un éditeur sans rien perdre de ses garanties. Plutôt qu'un démon HTTP et un plugin par éditeur, tenon devient un agent [Agent Client Protocol](https://agentclientprotocol.com) (ACP) : JSON-RPC 2.0 sur stdin/stdout, l'éditeur lance l'agent en sous-processus. Un seul travail rend tenon utilisable dans Zed, les IDE JetBrains (AI Assistant), Neovim, Emacs et VS Code (client ACP).

Critères de succès :

- `tenon acp` répond à `initialize`, `session/new`, `session/load`, `session/prompt`, `session/cancel` selon le schéma ACP ;
- tout s'exécute avec les outils de tenon : permissions, isolation système, masquage, routage, points de reprise et journal d'audit s'appliquent comme en ligne de commande ;
- les demandes d'accord s'affichent dans l'éditeur, y compris « autoriser avec réseau » et le consentement hors UE ;
- les clés d'API ne passent jamais par l'éditeur ;
- sans `tenon acp`, rien ne change.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Intégration | Agent ACP (`tenon acp`) | Standard ouvert, déjà pris en charge par les principaux éditeurs ; pas de protocole maison |
| Exécution | Outils de tenon uniquement ; pas de `fs/*` ni `terminal/*` de l'éditeur | Garder sandbox, points de reprise, masquage et audit exacts |
| Démarrage | Même chemin que `tenon` (config, politique, profil, sonde, routage), un moteur par dossier | Aucune garantie contournable par l'éditeur |
| MCP de l'éditeur | Ignorés par défaut ; `[acp] accept_editor_mcp = true` les fait passer par les mêmes filtres | Une extension tierce ne doit pas pouvoir injecter un serveur |
| Authentification | `authMethods: []` | Clés dans la config et le trousseau de tenon |
| Implémentation | JSON-RPC écrit sur la stdlib | Aucune nouvelle dépendance |

## 3. Protocole

`tenon acp` : JSON-RPC 2.0, un message JSON par ligne sur stdin/stdout ; stderr pour les journaux techniques (aucune TUI, aucune sortie hors protocole sur stdout).

**`initialize`** — réponse : `protocolVersion: 1` ; `agentCapabilities: { loadSession: true, promptCapabilities: { image: false, audio: false, embeddedContext: true }, mcpCapabilities: { http: true, sse: false } }` ; `authMethods: []` ; `agentInfo: { name: "tenon", version }`. Le `clientInfo` reçu (nom, version) est retenu pour l'audit. Version de protocole différente de 1 : erreur JSON-RPC `-32602` (`unsupported protocol version`).

**`session/new { cwd, mcpServers }`** — construit (ou réutilise) le moteur du dossier `cwd` par le même démarrage que `tenon` en ligne de commande : config globale et projet, politique signée, profil, liste blanche, sonde d'isolation, routage, serveurs MCP. Un refus au démarrage (isolation `required` indisponible, fournisseur hors profil, politique invalide…) renvoie une erreur JSON-RPC `-32603` dont le message est celui de tenon. Résultat : `sessionId` = identifiant de session tenon (celui du journal d'audit). `cwd` non absolu : `-32602`.

**`session/load { sessionId, cwd, mcpServers }`** — reprend une session tenon du dossier : l'historique est rejoué en `session/update` (messages utilisateur et agent, appels d'outils et résultats), puis la réponse est vide. Session inconnue ou d'un autre dossier : `-32002`.

**`session/prompt { sessionId, prompt }`** — blocs : `text` concaténés ; `resource_link` → mention du chemin dans le message (tenon le lit ensuite avec ses outils, sous permission) ; `resource` (contenu intégré) → joint au message, donc soumis au masquage et à la classification comme un texte saisi ; `image`, `audio` → refus `-32602`. Un prompt `/nom args` invoque le skill `nom` comme dans la TUI. Réponse `stopReason` : `completed` → `end_turn` ; `max_iterations` → `max_turn_requests` ; `incomplete` → `max_tokens` ; `cancelled` → `cancelled` ; arrêt par hook ou tour bloqué → `end_turn` précédé d'une notice.

**`session/cancel { sessionId }`** — annule le contexte du tour en cours ; une demande de permission en attente vaut refus.

Un seul tour à la fois par session : un second `session/prompt` pendant un tour renvoie `-32603` (`turn in progress`).

ACP réserve `-32000` à `auth_required` : les refus de tenon utilisent `-32603`, message de tenon tel quel.

## 4. Événements → `session/update`

- `AssistantDelta` → `agent_message_chunk` (texte, déjà masqué).
- `ToolCallRequested` → `tool_call` : `toolCallId` = identifiant tenon de l'appel (préfixé par l'appel `agent` parent pour un sous-agent : `a1/c1`), `status: pending`, `rawInput` masqué, `title` = outil · cible (ex. `shell · go test ./...`), `locations: [{ path }]` pour les outils de fichiers ; `kind` : `read_file` → `read`, `write_file`/`edit` → `edit`, `search` → `search`, `shell` → `execute`, `agent` → `think`, MCP → `other`.
- Début d'exécution → `tool_call_update` `status: in_progress`.
- `ToolResult` → `tool_call_update` `status: completed | failed` ; `content` : texte du résultat (tronqué comme dans tenon) ; pour une écriture : `{ type: "diff", path, oldText, newText }` (ancien texte depuis le point de reprise, nouveau depuis les arguments ; sans point de reprise : texte seul).
- Notices de tenon (profil, politique, isolation TCP seulement, session classée, fournisseur de repli, arrêt par hook, serveurs MCP de l'éditeur ignorés) → `agent_thought_chunk`, contenu `{ type: "text", text: "tenon: <notice>" }` (une mise à jour `notice` non standard serait ignorée par les clients).
- Jetons → `usage_update`.
- Skills disponibles → `available_commands_update` après `session/new` et `session/load` (nom, description).

## 5. Permissions → `session/request_permission`

- Options : `allow_once` « Autoriser » ; `allow_always` « Toujours pour cette session » (mémorise comme « toujours » dans la TUI, jamais le réseau) ; `reject_once` « Refuser » ; pour un shell isolé, une option `allow_once` supplémentaire « Autoriser avec réseau » (`optionId: "allow_network"`). Si une règle `network = true` accorde déjà le réseau, le libellé de « Autoriser » le dit.
- Consentement à un fournisseur hors UE : demande de permission dont le titre nomme le fournisseur et sa juridiction ; refus par défaut.
- Réponse `cancelled` ou erreur du client : refus. Libellés via `internal/i18n` (langue de la config).
- `PermissionResolved` dans le journal comme aujourd'hui.

## 6. Serveurs MCP de l'éditeur

- `[acp] accept_editor_mcp = false` par défaut (config globale ; refusé dans la config projet ; la politique peut l'imposer à `false`).
- `false` : les `mcpServers` reçus sont ignorés, notice : `editor MCP servers ignored ([acp] accept_editor_mcp)`.
- `true` : chaque serveur devient un `config.MCPServer` et suit le même chemin que ceux de la config (profil, politique, liste blanche, isolation stdio selon `risk`, journal). Valeurs par défaut : stdio → `jurisdiction = "local"`, `risk` vide (pas de réseau, projet en lecture seule) ; http → `jurisdiction = "non-eu"` (consentement), sauf hôte local → `local`. Un identifiant en conflit avec un serveur de la config : le serveur de la config l'emporte, notice.

## 7. Journal et export

- `SessionStarted` gagne `client: { name, version }` (depuis `clientInfo`) et `transport: "acp"`.
- L'export de conformité affiche l'éditeur utilisé par session.

## 8. Architecture

- `internal/acp` (nouveau) : `rpc.go` (JSON-RPC 2.0 ligne par ligne, requêtes et notifications dans les deux sens, identifiants, erreurs), `server.go` (méthodes ACP, sessions, un tour à la fois), `bridge.go` (événements tenon → `session/update`, permissions → `session/request_permission`), `types.go` (structures ACP utilisées, `camelCase`).
- `cmd/tenon` : sous-commande `acp` ; démarrage par dossier extrait de `run` en une fonction réutilisable (`setupForDir(cwd) (*engine.Engine, notices, error)`).
- `internal/config` : `[acp] accept_editor_mcp` ; `internal/orgpolicy` : `[acp] accept_editor_mcp = false` imposable.
- `internal/event`, `internal/compliance` : `client`, `transport`.
- Docs EN/FR : `docs/editors.md` / `docs/fr/editeurs.md` (Zed, JetBrains, Neovim, VS Code : configuration de l'éditeur pour lancer `tenon acp`).

## 9. Tests

- `acp` : JSON-RPC (requête, notification, erreur, ligne invalide → `-32700`, méthode inconnue → `-32601`) ; conformité des formes au schéma ACP (noms de méthodes, discriminants `sessionUpdate`, `PermissionOptionKind`, `stopReason`).
- Bout en bout avec un client ACP factice (tubes) et le faux LLM existant : `initialize` ; `session/new` ; prompt avec réponse texte → `agent_message_chunk` puis `end_turn` ; appel d'outil `write_file` → `tool_call`, demande de permission, `allow_once`, `tool_call_update` avec diff ; shell isolé → option « Autoriser avec réseau » ; `session/cancel` pendant une permission → refus et `cancelled` ; `session/load` rejoue l'historique ; MCP de l'éditeur ignorés puis acceptés avec l'option ; refus au démarrage (isolation `required` simulée indisponible) → `-32603` ; `SessionStarted.client` dans le journal.

## 10. Hors périmètre

Extension VS Code à la marque tenon, application de bureau, `fs/*` et `terminal/*` de l'éditeur, modes ACP (`session/set_mode`), images et audio dans les prompts, transport HTTP/WebSocket distant.
