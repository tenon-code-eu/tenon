# tenon — Spécification de conception (v1)

- **Date :** 2026-10-14
- **Statut :** validé en brainstorming, en attente de relecture de la spec
- **Licence :** EUPL-1.2
- **Langage :** Go

## 1. Contexte et objectifs

`tenon` est un agent de code en terminal, open source, français et européen — un équivalent souverain de Claude Code. Le tenon est la pièce qui s'emboîte dans une mortaise pour former un assemblage solide : l'agent assemble le code proprement, sous contrôle humain.

### Objectifs

1. Offrir un agent de code qui **fonctionne entièrement sur une infrastructure européenne ou locale** : modèles européens ou locaux préconfigurés, forge et CI européennes, distribution directe. Chaque recours à un service extérieur à l'UE est explicite et tracé.
2. Être **conforme by design** au règlement européen sur l'IA (AI Act) et au RGPD, avec des preuves vérifiables (journal d'audit, traçabilité du code généré).
3. Rester **ouvert** : tout endpoint compatible OpenAI est utilisable, l'utilisateur garde le choix, mais tout choix hors UE est explicite et tracé.

### Positionnement AI Act

Un agent de code n'est en général pas un système à haut risque (annexe III) et n'est pas un modèle GPAI : il utilise des modèles, il n'en fournit pas. Les obligations directes sont légères (art. 50 transparence, art. 4 maîtrise de l'IA côté déployeur). La conformité by design de `tenon` sert surtout à permettre aux organisations utilisatrices de **documenter et justifier** leur usage de l'IA.

### Souveraineté : définition retenue

La souveraineté de `tenon` ne repose pas sur la nationalité des fournisseurs, mais sur des **critères vérifiables**. Un service utilisé par défaut pour faire fonctionner, construire ou distribuer `tenon` doit remplir les quatre critères suivants :

1. **Ouvert** : logiciel open source, ou protocole ouvert et documenté.
2. **Auto-hébergeable** : l'utilisateur peut le remplacer par sa propre instance.
3. **Droit applicable européen** : l'opérateur n'est soumis à aucune loi extraterritoriale permettant à une autorité non européenne d'accéder aux données (ex. CLOUD Act, FISA section 702).
4. **Données maîtrisées** : aucun transfert de données hors UE sans consentement explicite de l'utilisateur, tracé dans le journal d'audit.

Un service qui ne remplit pas ces critères reste utilisable, mais jamais par défaut, et toujours de façon explicite et tracée (cf. §6.3).

Conséquences pour le projet :

- Les **bibliothèques** open source sont acceptées quelle que soit leur origine (ex. Bubble Tea) : elles sont vendorées (`go mod vendor`), auditables et compilées localement.
- Les builds utilisent `GOFLAGS=-mod=vendor` : aucun proxy de modules n'est requis.
- Forge, CI et distribution reposent sur des outils auto-hébergeables (Forgejo, Woodpecker).

## 2. Périmètre v1

### Inclus

- Boucle agentique avec streaming.
- Outils internes : lecture, écriture, édition de fichiers ; recherche (grep, glob) ; shell.
- Multi-fournisseurs via le protocole compatible OpenAI.
- TUI interactive riche (Bubble Tea) : diffs, validation des actions au clavier, historique.
- Mode headless (`tenon -p "..."`) avec sortie texte ou JSON.
- Support MCP (Model Context Protocol), transports stdio et HTTP streamable.
- Sessions persistantes : reprise, compaction du contexte.
- Fichier d'instructions projet chargé automatiquement.
- Interface bilingue FR/EN.
- Conformité : journal d'audit chaîné, contrôle humain par permissions, marquage git du code généré, politique de données (juridiction, masquage).

### Exclus de la v1 (feuille de route)

- Mode client/serveur (approche B) : démon exposant l'engine en HTTP + SSE, pour intégrations IDE et web. L'architecture v1 le prépare (cf. §4).
- Isolation au niveau système (Landlock sous Linux, sandbox-exec sous macOS).
- Rapport de conformité exportable (dossier pour DPO ou auditeur, construit à partir du journal).
- Ancrage automatique du dernier hash d'audit hors de la machine.
- Restauration des valeurs masquées dans les arguments d'outils.
- Support Windows de premier rang (v1 : Linux et macOS ; Windows en best effort).

## 3. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Usage | Agent de code en terminal | Besoin principal identifié |
| Modèles | Endpoints ouverts, UE et local par défaut | Liberté de l'utilisateur, souveraineté par défaut |
| Langage | Go | Binaire statique, distribution directe sans registre tiers, accessible aux contributeurs |
| Licence | EUPL-1.2 | Licence officielle UE, copyleft faible, reconnue par l'administration française |
| Architecture | Monolithe modulaire piloté par événements (A), mode serveur (B) plus tard | Simple à livrer ; le flux d'événements unique rend la conformité exhaustive |
| Forge | Codeberg (ou Forgejo auto-hébergé) | Forge européenne |

## 4. Architecture

Un seul binaire Go. Le cœur (`engine`) ne connaît aucune interface utilisateur : il émet un flux d'**événements typés** et sérialisables en JSON. Trois consommateurs utilisent ce même format :

- les interfaces (`ui/tui`, `ui/headless`) ;
- le journal d'audit et la persistance de session ;
- plus tard, le flux SSE du mode serveur (B). Passer à B reviendra à exposer `Engine` en HTTP, sans réécrire le cœur.

Toute action passe par `policy` **avant** de s'exécuter et par `audit` **après**, sans exception possible.

### Structure du dépôt

```
cmd/tenon/            point d'entrée, parsing CLI
internal/
  engine/             boucle agentique, orchestration, bus d'événements
  provider/           client compatible OpenAI + registre des fournisseurs
  tools/              fichiers, grep/glob, shell
  mcp/                client MCP ; outils MCP adaptés à l'interface Tool
  policy/             permissions, juridiction, masquage
  audit/              journal JSONL chaîné par hash
  session/            persistance, reprise, compaction
  instructions/       chargement de AGENTS.md / TENON.md
  gitmark/            hook git et trailers du code généré
  i18n/               catalogues FR/EN
  config/             configuration globale + projet (TOML)
  ui/tui/             Bubble Tea
  ui/headless/        mode -p, sortie texte ou JSON
```

### Interfaces clés

```go
type Provider interface {
    Info() ProviderInfo // id, URL, modèle, pays, juridiction (eu / non-eu / local)
    Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

type Tool interface {
    Name() string
    Schema() json.RawMessage
    Risk() Risk // Read, Write, Exec, Network
    Run(ctx context.Context, args json.RawMessage) (Result, error)
}

type Policy interface {
    CheckTool(call ToolCall) Decision // Allow, Ask, Deny
    CheckOutbound(p ProviderInfo, msgs []Message) ([]Message, []Warning, error)
}

type Engine interface {
    Submit(ctx context.Context, sessionID, input string) error
    Respond(permissionID string, d Decision) error
    Subscribe() <-chan Event
}
```

### Événements

Chaque `Event` contient : `id`, `session`, `seq` (monotone par session), `ts` (UTC), `type`, `payload`.

Types v1 : `SessionStarted`, `UserMessage`, `ContextCompacted`, `JurisdictionWarning`, `RedactionApplied`, `AssistantDelta`, `AssistantMessage`, `ToolCallRequested`, `PermissionRequested`, `PermissionResolved`, `ToolResult`, `McpServerDown`, `TurnCompleted`, `Error`.

### Principes

- L'engine ne fait jamais d'entrée/sortie terminal. Une permission à demander devient `PermissionRequested` ; l'interface répond via `Respond()`.
- Les outils MCP implémentent `Tool` : ils passent par la même `Policy` et le même audit que les outils internes. Risque par défaut : `Network` ou `Exec`, donc `Ask`.
- Aucun message ne part vers un fournisseur ou un serveur MCP distant sans passer par `Policy.CheckOutbound`.

## 5. Flux d'un tour d'agent

1. **Entrée.** L'utilisateur envoie un message, qui appelle `Engine.Submit`. L'engine émet `UserMessage`.
2. **Construction du contexte.** L'engine assemble le prompt système (dans la langue configurée), les fichiers d'instructions projet et l'historique de la session. Si le contexte dépasse 80 % de la fenêtre du modèle (seuil configurable), il déclenche une compaction : le modèle résume l'historique ancien, les derniers échanges restent intacts, et l'engine émet `ContextCompacted`. L'appel de résumé passe lui aussi par l'étape 3.
3. **Contrôle sortant (`Policy.CheckOutbound`).**
   - Masquage des secrets et données personnelles (cf. §6.4) ; l'engine émet `RedactionApplied` avec le nombre et le type de masquages, jamais les valeurs.
   - Juridiction : au premier appel d'une session vers un fournisseur `non-eu`, confirmation explicite dans la TUI ; en headless, refus sauf flag `--allow-non-eu`. L'engine émet `JurisdictionWarning`.
4. **Appel du modèle.** `Provider.Stream` envoie la requête. L'engine émet `AssistantDelta` pendant le streaming, puis `AssistantMessage` avec les éventuels appels d'outils.
5. **Pour chaque appel d'outil,** `Policy.CheckTool` décide :
   - `Allow` : l'outil s'exécute ;
   - `Ask` : l'engine émet `PermissionRequested` et attend `Respond()` ; en headless, refus sauf règle `allow` explicite ;
   - `Deny` : le modèle reçoit le résultat « action refusée ».
6. **Exécution.** L'engine émet `ToolResult`. Pour une écriture, l'événement contient le chemin et le hash SHA-256 du fichier avant et après.
7. **Boucle.** Les résultats d'outils sont renvoyés au modèle en repassant par l'étape 3. La boucle s'arrête quand le modèle répond sans appel d'outil ou au maximum d'itérations (50 par défaut, configurable). L'engine émet `TurnCompleted`.

En parallèle, chaque événement est ajouté au journal d'audit puis au fichier de session.

## 6. Conformité

### 6.1 Journal d'audit

- **Emplacement :** `$XDG_DATA_HOME/tenon/audit/AAAA-MM-JJ.jsonl` (défaut `~/.local/share/tenon/audit/`), droits `0600`, dossier `0700`.
- **Format d'une ligne :**
  ```json
  {"seq":42,"ts":"2026-10-14T10:02:11Z","session":"s_8f2…","type":"ToolResult",
   "payload":{…},"prev":"sha256:ab12…","hash":"sha256:9c4e…"}
  ```
- **Chaînage :** `hash = sha256(prev || JSON canonique de la ligne sans le champ hash)`. La chaîne continue d'un fichier quotidien au suivant ; la première ligne jamais écrite a `prev = "sha256:0"`.
- **Vérification :** `tenon audit verify` détecte toute ligne modifiée, insérée ou supprimée au milieu du journal, et affiche le dernier hash.
- **Limite connue :** une troncature de la fin du journal n'est pas détectable localement. Parade v1 : l'utilisateur ancre manuellement le dernier hash affiché ailleurs.
- **Niveau de détail :** `audit.level = "full"` (défaut) ou `"metadata"` (types d'événements, hash et tailles seulement, sans contenu de prompts ni de fichiers).
- **Secrets :** le masquage s'applique aussi au journal. Aucun secret n'y est écrit en clair.

### 6.2 Contrôle humain (permissions)

Règles par défaut :

| Action | Décision |
|---|---|
| Lecture dans le dossier projet | `Allow` |
| Lecture de `.env*`, `*.pem`, `id_rsa*` | `Ask` |
| Écriture, édition | `Ask` |
| Shell | `Ask` |
| Réseau, outils MCP | `Ask` |
| Tout accès hors du dossier projet | `Ask` (toujours) |

Configuration :

```toml
[[permissions.rules]]
tool = "shell"
match = "go test *"
decision = "allow"

[[permissions.rules]]
tool = "shell"
match = "rm -rf *"
decision = "deny"
```

- `deny` l'emporte toujours sur `allow`.
- Dans la TUI, l'utilisateur peut répondre : oui une fois, oui pour la session, non.

> **Avertissement :** les permissions ne sont pas un bac à sable. Une commande shell autorisée s'exécute avec les droits de l'utilisateur. L'isolation système est prévue après la v1.

### 6.3 Fournisseurs et juridiction

```toml
[[providers]]
id = "mistral"
base_url = "https://api.mistral.ai/v1"
api_key_env = "MISTRAL_API_KEY"
model = "devstral"
country = "FR"
jurisdiction = "eu"
```

- **Préconfigurés :** Mistral (FR), Scaleway (FR), OVHcloud AI Endpoints (FR) en `eu` ; Ollama et vLLM en `local`. Fournisseur par défaut : Mistral. Les noms de modèles des préréglages sont mis à jour à chaque release.
- **Définition :** `jurisdiction` applique le critère 3 du §1 : elle désigne le **droit applicable à l'opérateur**, pas l'emplacement du datacenter. Un opérateur soumis à une loi extraterritoriale d'accès aux données est classé `non-eu`, même si ses serveurs sont situés dans l'UE.
- **Fournisseur personnalisé :** sans déclaration, traité comme `non-eu`. La valeur déclarée n'est pas vérifiable techniquement ; la documentation le dit.
- **Serveurs MCP HTTP distants :** même champ `jurisdiction`, même règle par défaut (`non-eu`).
- **Clés API :** lues depuis une variable d'environnement ou le trousseau système, jamais depuis le fichier de configuration.

### 6.4 Masquage

- Règles intégrées pour les secrets (style gitleaks : clés API, jetons, clés privées, valeurs `.env`).
- Règles intégrées pour les données personnelles : IBAN, numéro de sécurité sociale (NIR), e-mail, téléphone.
- Regex personnalisées dans la configuration (`[[redaction.rules]]`).
- Remplacement par des marqueurs (`[SECRET_1]`, `[EMAIL_1]`…), jamais restaurés en v1.
- **Limite :** la détection de données personnelles par regex est approximative. La documentation ne promet pas une conformité RGPD garantie.

### 6.5 Marquage du code généré

- `tenon git install-hook` installe un hook `prepare-commit-msg` dans le dépôt courant.
- Au commit, le hook compare les fichiers indexés aux écritures de l'agent enregistrées dans le journal d'audit.
- En cas de correspondance, il ajoute :
  ```
  AI-Assisted-By: tenon (devstral, Mistral AI, FR)
  AI-Session: <session-id>
  ```
- Fonctionne aussi quand l'humain commite après des modifications de l'agent.

### 6.6 Transparence (art. 50)

- Bannière au démarrage : modèle, fournisseur, pays, juridiction.
- Barre d'état permanente dans la TUI avec les mêmes informations.
- En headless, chaque sortie JSON inclut ces informations.

## 7. Configuration, sessions et instructions

- **Configuration globale :** `$XDG_CONFIG_HOME/tenon/config.toml` (défaut `~/.config/tenon/config.toml`).
- **Configuration projet :** `.tenon/config.toml`. Elle peut restreindre les règles globales mais pas les assouplir : une règle `deny` globale ne peut pas être levée par un projet.
- **Validation :** `tenon config check`.
- **Sessions :** `$XDG_DATA_HOME/tenon/sessions/<id>.jsonl`, un événement par ligne. `tenon --resume` liste les sessions récentes du projet ; `tenon --resume <id>` reprend une session.
- **Instructions projet :** `AGENTS.md` (standard ouvert) puis `TENON.md` s'il existe, à la racine du projet ; leur contenu est ajouté au prompt système.
- **Langue :** `lang = "fr" | "en"` dans la configuration, sinon déduite de `LANG`. Elle s'applique à l'interface et au prompt système.

## 8. Mode headless

```
tenon -p "corrige les tests qui échouent" [--output text|json] [--allow-non-eu]
```

- `--output json` : un événement JSON par ligne sur la sortie standard (même format que le journal, sans chaînage).
- Permissions : toute décision `Ask` devient un refus, sauf règle `allow` explicite.
- Codes de sortie : `0` succès, `1` erreur, `3` action bloquée par la politique.

## 9. Gestion des erreurs

### Fournisseur

- 429, 5xx, erreurs réseau : 3 nouvelles tentatives, backoff exponentiel, respect de `Retry-After`.
- 401, 403 : message clair, sans nouvelle tentative.
- Contexte trop long : compaction forcée, puis une seule nouvelle tentative.
- Stream coupé : réponse partielle enregistrée avec le marqueur `incomplete` ; l'utilisateur peut relancer.
- **Aucune bascule automatique vers un autre fournisseur**, car elle changerait la juridiction à l'insu de l'utilisateur.

### Outils

- Erreur d'outil : renvoyée au modèle comme résultat, sans faire planter l'agent.
- Timeout shell : 2 minutes par défaut, configurable.
- Sorties tronquées à 30 000 caractères, avec mention de la troncature.
- Plantage d'un serveur MCP : ses outils sont désactivés, l'engine émet `McpServerDown`, la session continue.

### Fermeture par défaut

- Échec d'écriture du journal d'audit : l'engine arrête d'exécuter des outils et affiche un message clair. La conformité passe avant la continuité.
- Configuration invalide (regex cassée, juridiction absente sur un MCP distant…) : refus de démarrer.

### Interruptions

- Premier Ctrl+C : annule le tour en cours via `context` et tue le groupe de processus du shell.
- Second Ctrl+C : quitte.
- Fichier de session corrompu : reprise jusqu'à la dernière ligne valide, avec avertissement.

## 10. Tests

- **Unitaires** (pilotés par tables) :
  - `policy` : règles de permission, priorité `deny`, corpus de masquage avec cas positifs et négatifs ;
  - `audit` : `verify` détecte une ligne modifiée, insérée ou supprimée ;
  - `tools` : dans des dossiers temporaires ;
  - `gitmark` : dans des dépôts git temporaires.
- **Engine :** faux `Provider` rejouant des réponses scénarisées ; flux d'événements comparé à des fichiers golden. Scénarios : boucle, permissions, compaction, erreurs fournisseur, échec d'audit.
- **Intégration :**
  - headless de bout en bout contre un faux serveur compatible OpenAI (`httptest`) ;
  - test réel contre Ollama, marqué `//go:build integration`.
- **TUI :** `teatest` sur les parcours clés (demande de permission, reprise de session).

## 11. Build, CI et distribution

- **Forge :** Codeberg (ou Forgejo auto-hébergé).
- **CI :** Forgejo Actions ou Woodpecker : `go vet`, `staticcheck`, `govulncheck`, `go test -race`.
- **Dépendances :** vendorées ; builds avec `GOFLAGS=-mod=vendor`.
- **Releases :** builds reproductibles (`-trimpath`), SBOM CycloneDX, signatures minisign.
- **Distribution :** binaires téléchargeables depuis la forge (Linux, macOS ; amd64, arm64), paquets `.deb` et `.rpm`.

## 12. Risques et limites assumées

| Risque | Mitigation |
|---|---|
| Permissions perçues comme un bac à sable | Avertissement dans la doc et la TUI ; isolation système en feuille de route |
| Juridiction déclarée non vérifiable | Défaut `non-eu` ; documentation explicite |
| Masquage des données personnelles incomplet | Documentation explicite ; regex personnalisables |
| Troncature de fin du journal indétectable | Ancrage manuel v1, automatique plus tard |
| Qualité des modèles UE/locaux parfois inférieure aux modèles propriétaires de pointe | Endpoints ouverts ; architecture indépendante du modèle |
| Disponibilité du nom `tenon` | Vérifier Codeberg, domaines `.fr`/`.eu`, marques INPI et EUIPO avant publication |
