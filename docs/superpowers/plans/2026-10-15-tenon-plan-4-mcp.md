# tenon — Plan 4 : MCP (Model Context Protocol)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Brancher des serveurs MCP (stdio et HTTP streamable) déclarés dans la configuration globale ; leurs outils passent par les mêmes permissions, le même audit et la même transparence de juridiction que les outils internes.

**Architecture:** Nouveau paquet `internal/mcp` construit sur le SDK Go officiel. Un `Manager` se connecte aux serveurs au démarrage et expose leurs outils sous forme de `tools.Tool`. Un serveur injoignable au démarrage, ou qui tombe en cours de session, désactive ses outils et produit un événement `McpServerDown` ; la session continue. Un serveur distant déclaré `non-eu` n'est utilisable qu'avec un consentement explicite.

**Tech Stack:** Go 1.26 ; `github.com/modelcontextprotocol/go-sdk` v1.8.0 (publiée le 2026-09-04), vendorée.

**Spec:** `docs/superpowers/specs/2026-10-14-tenon-design.md` (§2 MCP, §4 « les outils MCP implémentent Tool », §6.3 « serveurs MCP HTTP distants : même champ jurisdiction, défaut non-eu », §9 « plantage d'un serveur MCP »). Plans 1 à 3 mergés.

## Global Constraints

- Contraintes des plans 1 à 3 en vigueur (vendoring, `GOFLAGS=-mod=vendor`, Linux/macOS, aucun secret en clair dans l'audit, les requêtes ni les sessions, `Outbound.Check` et `Permissions.Check` obligatoires, codes de sortie 0/1/3, textes utilisateur via `i18n.T`, panneau de validation sans ambiguïté).
- Les serveurs MCP se déclarent **uniquement dans la configuration globale** : un dépôt ne peut pas en ajouter (un serveur stdio est une exécution de code arbitraire).
- Aucun secret dans la configuration : les jetons et variables d'environnement d'un serveur sont référencés par nom de variable (`env_from`, `bearer_env`), jamais par valeur. Leurs valeurs sont ajoutées au `Redactor` (`AddLiteral`).
- Nom d'un outil MCP exposé au modèle : `mcp__<serveur>__<outil>`, caractères hors `[A-Za-z0-9_-]` remplacés par `_`, 64 caractères maximum (tronqué, puis suffixe `_<4 hex>` d'un hash si collision).
- Risque par défaut : `exec` pour un serveur stdio, `network` pour un serveur HTTP ; surchargeable par serveur (`risk`). Comme pour les outils internes, `exec`/`network` donnent `Ask` par défaut.
- Délai d'appel d'un outil MCP : `shell_timeout` (2 min par défaut). Sortie tronquée à `output_limit`.
- Description et schéma fournis par un serveur : texte non fiable — description tronquée à 1024 caractères, schéma refusé (outil ignoré avec avertissement) s'il n'est pas un objet JSON Schema valide ou dépasse 64 Ko.

## Review Focus

1. **Dépôt malveillant** déclarant un serveur MCP dans `.tenon/config.toml` : configuration refusée (Task 1 : `TestProjectCannotDeclareMCP`).
2. **Serveur stdio qui plante en plein appel** : l'appel rend une erreur, `McpServerDown` est émis une seule fois, les autres outils et la session continuent, l'audit se vérifie (Task 3 et Task 5 : `TestServerCrashMidCall`).
3. **Serveur HTTP `non-eu` sans `--allow-non-eu` en mode headless** : aucun octet n'est envoyé au serveur, l'action est refusée (code 3) (Task 4 : `TestNonEUServerRefusedHeadless`).
4. **Serveur qui renvoie un nom d'outil, une description ou un résultat contenant des séquences d'échappement, du bidi ou un secret** : rien de tout cela n'atteint le terminal en clair (Task 2 et Task 5 : `TestHostileServerText`).
5. **Serveur qui ne répond jamais** à `initialize` ou à un appel : le démarrage et l'appel rendent la main dans les délais (Task 2 : `TestConnectTimeout`, `TestCallTimeout`).

---

### Task 1: Configuration des serveurs MCP

**Files:**
- Modify: `internal/config/config.go`, `internal/config/load.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces :
  ```go
  type MCP struct { Servers []MCPServer `toml:"servers"` }
  type MCPServer struct {
      ID           string   `toml:"id"`            // [a-z0-9_-]{1,32}
      Transport    string   `toml:"transport"`     // "stdio" | "http"
      Command      []string `toml:"command"`       // stdio : argv, non vide
      EnvFrom      []string `toml:"env_from"`      // stdio : noms de variables transmises au serveur
      URL          string   `toml:"url"`           // http : https://… (http:// seulement vers localhost/127.0.0.1/::1)
      BearerEnv    string   `toml:"bearer_env"`    // http : variable contenant le jeton
      Jurisdiction string   `toml:"jurisdiction"`  // "eu" | "non-eu" | "local"
      Country      string   `toml:"country"`
      Risk         string   `toml:"risk"`          // "" | "read" | "write" | "exec" | "network"
      Tools        []string `toml:"tools"`         // liste blanche optionnelle des outils exposés
      Disabled     bool     `toml:"disabled"`
  }
  // Config gagne : MCP MCP `toml:"mcp"`
  ```
- Validation (erreurs en français, refus de démarrer) : identifiant invalide ou dupliqué ; transport inconnu ; stdio sans `command` ; http sans `url` ou URL non https vers un hôte non local ; **http sans `jurisdiction` → erreur** (spec §9) ; `jurisdiction` hors des trois valeurs ; stdio sans `jurisdiction` → `local` ; `risk` hors des quatre valeurs.
- La clé `mcp` est refusée dans la configuration projet par la liste blanche existante.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestMCPStdioParsed(t *testing.T)        // jurisdiction par défaut "local", risk ""
func TestMCPHTTPRequiresJurisdiction(t *testing.T)
func TestMCPHTTPRequiresHTTPS(t *testing.T)  // http://exemple.fr refusé ; http://127.0.0.1:8080 accepté
func TestMCPInvalidID(t *testing.T)          // "GitHub!" refusé ; doublon refusé
func TestMCPStdioRequiresCommand(t *testing.T)
func TestMCPInvalidRisk(t *testing.T)
func TestProjectCannotDeclareMCP(t *testing.T) // erreur "clé non autorisée"
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/config/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(config): déclaration des serveurs MCP"`

---

### Task 2: Client MCP et outils adaptés

**Files:**
- Create: `internal/mcp/manager.go`, `internal/mcp/tool.go`, `internal/mcp/names.go`
- Test: `internal/mcp/mcp_test.go`, `internal/mcp/server_test.go` (serveur de test en mémoire)

**Interfaces:**
- Consumes : `config.MCPServer`, `tools.Tool`, `tools.Result`, `tools.Target`, `tools.Risk`.
- Produces :
  ```go
  type Options struct {
      Getenv       func(string) string
      CallTimeout  time.Duration // shell_timeout
      ConnectTimeout time.Duration // 15 s par défaut
      OutputLimit  int
      Consent      func(serverID string) bool // juridiction (Task 4) ; nil = toujours vrai
  }
  type Down struct { Server, Error string }
  type Manager struct{ /* … */ }
  func Start(ctx context.Context, servers []config.MCPServer, opts Options) (*Manager, []Down)
  func (m *Manager) Tools() []tools.Tool       // outils de tous les serveurs joignables
  func (m *Manager) Secrets() []string          // valeurs des env_from et bearer_env (pour AddLiteral)
  func (m *Manager) Close() error               // ferme les sessions, tue les processus stdio
  func ToolName(server, tool string, taken map[string]bool) string
  ```
- Comportement :
  - `Start` se connecte à chaque serveur non désactivé, en parallèle, avec `ConnectTimeout` ; un échec donne un `Down` et ce serveur est ignoré. Serveur stdio : processus lancé avec un environnement **minimal** (`PATH`, `HOME`, `LANG`, plus les seules variables `env_from`), répertoire de travail = racine du projet. Serveur HTTP : transport streamable, en-tête `Authorization: Bearer <valeur de bearer_env>` si défini, aucun suivi de redirection.
  - Chaque outil listé (filtré par `Tools` si non vide) devient un `tools.Tool` : `Name()` = `ToolName`, `Description()` = description du serveur nettoyée (contrôles retirés) et tronquée à 1024, `Schema()` = `inputSchema` (outil ignoré si invalide ou > 64 Ko), `Risk()` = `risk` du serveur ou défaut du transport, `Target(args)` = `Target{Command: "mcp " + serveur + "." + outil + " " + args JSON compacts tronqués à 2000}`.
  - `Run` : si le serveur est marqué hors service → `Result{IsError: true, Content: "serveur MCP <id> indisponible"}` ; si `Consent` renvoie faux → `Result{IsError: true, Denied: "policy"}` (voir Task 4) ; sinon `CallTool` avec `CallTimeout`. Contenu : parties texte concaténées ; autres parties résumées (`[image image/png]`, `[ressource <uri>]`) ; `isError` du serveur → `IsError`. Sortie tronquée.
  - Erreur de transport pendant un appel (processus mort, connexion coupée) : le serveur est marqué hors service, `Result.IsError`, et le `Result` porte `ServerDown: "<id>"` (nouveau champ de `tools.Result`, vide par défaut) — une seule fois par serveur.
  - `Close` termine proprement les processus stdio (signal puis kill après 2 s).

- [ ] **Step 1: Écrire les tests qui échouent** (serveur MCP de test en mémoire via le SDK, et un sous-processus stdio : le binaire de test relancé avec une variable d'environnement qui le fait jouer un serveur)

```go
func TestToolNames(t *testing.T)          // "github", "create issue!" → "mcp__github__create_issue_" ; > 64 → tronqué ; collision → suffixe hash
func TestListAndCall(t *testing.T)        // en mémoire : outil "echo" → Result texte attendu
func TestAllowlistFilter(t *testing.T)    // Tools = ["a"] → seul a exposé
func TestInvalidSchemaSkipped(t *testing.T)
func TestServerIsErrorResult(t *testing.T)
func TestNonTextContentSummarised(t *testing.T)
func TestStdioMinimalEnv(t *testing.T)    // le serveur stdio voit env_from mais pas une variable SECRET_PARENT non listée
func TestConnectTimeout(t *testing.T)     // serveur qui ne répond pas à initialize → Down en ~ConnectTimeout (court en test)
func TestCallTimeout(t *testing.T)        // outil qui dort → Result.IsError avec délai, en ~CallTimeout
func TestServerCrashMidCall(t *testing.T) // le sous-processus quitte pendant l'appel → IsError, ServerDown "srv" ; appel suivant → "indisponible" sans ServerDown
func TestHostileServerText(t *testing.T)  // description avec \x1b[2J et U+202E → nettoyée ; nom d'outil hostile → nom exposé conforme
func TestSecretsListed(t *testing.T)      // env_from/bearer_env → Secrets() contient les valeurs
func TestCloseKillsStdio(t *testing.T)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/mcp/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** — `go get github.com/modelcontextprotocol/go-sdk@v1.8.0 && go mod tidy && go mod vendor`.
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/mcp/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(mcp): client MCP, outils adaptés, serveurs hors service"`

---

### Task 3: `McpServerDown` dans l'engine

**Files:**
- Modify: `internal/tools/tool.go` (champ `ServerDown`), `internal/event/payloads.go`, `internal/engine/engine.go`, `internal/engine/loop.go`
- Test: `internal/engine/mcp_test.go`

**Interfaces:**
- Produces : `event.McpServerDownPayload{Server, Error string}` (tags `server`, `error`) ; `Deps.UnavailableMCP []event.McpServerDownPayload` — émis juste après le premier `SessionStarted` de chaque session (y compris reprise) ; après un `ToolResult` dont le `Result.ServerDown` n'est pas vide, l'engine émet `McpServerDown{Server, Error: contenu du résultat}`.
- La reprise (`Restore`) ignore `McpServerDown` (aucun état à reconstruire).

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestUnavailableEmittedAfterSessionStarted(t *testing.T)
func TestServerDownAfterToolResult(t *testing.T) // outil factice renvoyant ServerDown "srv" → ToolResult puis McpServerDown{srv}
func TestServerDownNotDuplicated(t *testing.T)   // second appel sans ServerDown → pas d'événement
func TestRestoreIgnoresServerDown(t *testing.T)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/engine/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.** Le golden ne doit pas changer.
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/engine/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(engine): événement McpServerDown"`

---

### Task 4: Juridiction des serveurs MCP et affichage

**Files:**
- Modify: `internal/tools/tool.go`, `internal/policy/permissions.go`, `internal/mcp/tool.go`, `internal/ui/headless/headless.go`, `internal/ui/tui/transcript.go`, `internal/ui/tui/permission.go`, `internal/i18n/catalog.go`
- Test: `internal/mcp/consent_test.go`, `internal/ui/headless/headless_test.go`, `internal/ui/tui/mcp_test.go`

**Comportement :**
- Nouveau champ `tools.Target.ForceAsk bool` : `policy.Permissions.Check` renvoie `Ask` quand il est vrai, sauf si une règle `deny` correspond (une règle `allow` ne suffit pas). Test dans `internal/policy`.
- Un outil d'un serveur `non-eu` sans consentement d'avance (`--allow-non-eu`) : son `Target` porte `ForceAsk = true`, donc chaque appel exige une validation humaine (TUI) ou est refusé (headless, où toute demande est refusée).
- En plus, `Run` appelle `Options.Consent(serverID)` **avant tout envoi** ; faux → `Result{IsError: true, Denied: "policy", Content: "serveur MCP hors UE sans consentement"}`. Le câblage (Task 5) fournit : headless → `Consent = allowNonEU` ; TUI → `Consent = true` (l'appel n'arrive à `Run` qu'après la validation forcée par `ForceAsk`, ou avec `--allow-non-eu`).
- Pour que la TUI puisse afficher la juridiction, `Target.Command` d'un outil MCP commence par `mcp <serveur>.<outil>` et le paquet `mcp` expose `func Describe(toolName string) (server string, jurisdiction llm.Jurisdiction, ok bool)` à partir d'un registre global en lecture seule rempli par `Start`.
- Panneau de validation TUI pour un outil MCP : ligne d'en-tête supplémentaire `tui.perm.mcp` (FR « serveur MCP %s · %s · juridiction %s », EN « MCP server %s · %s · jurisdiction %s ») avec le libellé de juridiction coloré comme la barre d'état ; pour `non-eu`, ligne `tui.perm.mcp_non_eu` (FR « les arguments seront envoyés hors de l'UE », EN « arguments will be sent outside the EU »). Ces lignes font partie de l'en-tête (doivent être visibles pour valider).
- Transcription TUI et mode headless : `McpServerDown` → avis `mcp.down` (FR « serveur MCP %s indisponible : %s », EN « MCP server %s unavailable: %s »), texte masqué et nettoyé.
- Bannière headless et TUI : si des serveurs MCP sont actifs, ligne `banner.mcp` (FR « MCP : %s », EN « MCP: %s ») listant `id (juridiction)`.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestForceAskOverridesAllow(t *testing.T)   // policy : allow "*" + ForceAsk → Ask ; deny + ForceAsk → Deny
func TestNonEUServerNoConsentNoCall(t *testing.T) // Consent faux → aucun appel reçu par le serveur de test, Denied "policy"
func TestNonEUServerWithConsent(t *testing.T)
func TestHeadlessServerDownNotice(t *testing.T)   // FR et EN
func TestTUIPromptShowsMCPJurisdiction(t *testing.T) // en-tête contient « serveur MCP », « hors UE », la ligne non_eu ; non validable si cachée
func TestTUIServerDownNotice(t *testing.T)
func TestBannerListsMCP(t *testing.T)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/mcp/ ./internal/ui/... ./internal/i18n/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/mcp/ ./internal/ui/... ./internal/i18n/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(mcp): juridiction des serveurs et affichage"`

---

### Task 5: Câblage CLI et parcours de bout en bout

**Files:**
- Modify: `cmd/tenon/run.go`
- Test: `cmd/tenon/mcp_e2e_test.go`

**Comportement :**
- `startEngine` : `mcp.Start` avec les serveurs de la configuration globale, `Getenv`, `CallTimeout = shell_timeout`, `OutputLimit`, `Consent` (headless : `allowNonEU` ; TUI : voir Task 4) ; outils MCP ajoutés au registre **après** les outils internes (un nom MCP ne peut pas masquer un outil interne : préfixe `mcp__`) ; `Manager.Secrets()` → `Redactor.AddLiteral` ; `Deps.UnavailableMCP` ← liste `Down`. `Manager.Close()` est appelé à la fermeture, après la fin du tour et avant la fermeture des sinks.
- `tenon config check` valide aussi la section `mcp` (sans se connecter).
- Le serveur stdio de test est le binaire de test lui-même relancé (`os.Args[0]` + variable `TENON_TEST_MCP_SERVER=1` lue dans `TestMain`).

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestMCPToolAllowedByRule(t *testing.T)   // règle globale allow tool="mcp__test__echo" → le faux modèle appelle l'outil, la réponse contient le résultat, code 0
func TestMCPToolAskDeniedHeadless(t *testing.T) // sans règle → code 3, aucun appel reçu par le serveur
func TestServerCrashMidCall(t *testing.T)     // le serveur de test quitte pendant l'appel → stderr contient l'avis mcp.down, la session continue, audit verify OK
func TestUnreachableServerAtStart(t *testing.T) // commande inexistante → avis mcp.down dès le début, code 0 pour un tour sans outil MCP
func TestNonEUServerRefusedHeadless(t *testing.T) // serveur HTTP de test (httptest) déclaré non-eu, règle allow, sans --allow-non-eu → code 3, 0 requête reçue
func TestNonEUServerWithFlag(t *testing.T)    // --allow-non-eu → appel reçu
func TestMCPSecretNeverLeaks(t *testing.T)    // bearer_env = TOKEN_MCP ; le serveur renvoie la valeur du jeton dans son résultat → absente des requêtes, de l'audit, de la session et de stdout
func TestConfigCheckMCP(t *testing.T)         // http sans jurisdiction → code 1
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./cmd/tenon/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier toute la suite** — Run: `GOFLAGS=-mod=vendor go vet ./... && GOFLAGS=-mod=vendor go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(cli): serveurs MCP branchés sur l'engine"`
