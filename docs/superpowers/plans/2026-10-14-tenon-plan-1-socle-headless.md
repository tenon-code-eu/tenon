# tenon — Plan 1 : socle headless conforme

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Livrer un binaire `tenon` qui exécute un tour d'agent complet en mode headless (`tenon -p "..."`), avec outils fichiers et shell, permissions, contrôle de juridiction, masquage, et journal d'audit chaîné vérifiable.

**Architecture:** Monolithe Go modulaire. Le cœur `engine` émet des événements typés (`internal/event`) ; le journal d'audit est un `Sink` synchrone écrit avant toute exécution d'outil (fermeture par défaut) ; le mode headless est un simple consommateur d'événements. Les types LLM partagés vivent dans `internal/llm` pour éviter les cycles d'import.

**Tech Stack:** Go 1.26, `net/http` (client compatible OpenAI écrit à la main, sans SDK), `github.com/BurntSushi/toml`, `github.com/bmatcuk/doublestar/v4`.

**Spec:** `docs/superpowers/specs/2026-10-14-tenon-design.md`

## Découpage de la v1 en plans

La spec couvre plusieurs sous-systèmes indépendants. Chaque plan produit un logiciel testable :

1. **Plan 1 (ce document)** — socle headless conforme : config, fournisseurs, outils internes, permissions, juridiction, masquage, audit, engine, CLI headless, `tenon audit verify`, `tenon config check`.
2. **Plan 2** — sessions persistantes, reprise, compaction, instructions projet (`AGENTS.md`, `TENON.md`), i18n FR/EN, trousseau système pour les clés API.
3. **Plan 3** — TUI Bubble Tea (diffs, permissions au clavier, barre d'état de transparence).
4. **Plan 4** — MCP (stdio, HTTP streamable, juridiction des serveurs distants, `McpServerDown`).
5. **Plan 5** — marquage git (`tenon git install-hook`), CI Forgejo/Woodpecker, release (SBOM, minisign, `.deb`/`.rpm`).

## Global Constraints

- Module : `codeberg.org/tenon/tenon` ; `go 1.26` dans `go.mod`.
- Licence EUPL-1.2 ; texte officiel anglais dans `LICENSE`.
- Dépendances vendorées et commitées (`go mod vendor`) ; toute commande de build ou de test s'exécute avec `GOFLAGS=-mod=vendor`.
- Plateformes : Linux et macOS. Le shell utilise `bash -c` et les groupes de processus Unix.
- Messages utilisateur en français dans ce plan (l'i18n arrive au plan 2) ; identifiants de code en anglais.
- Valeurs par défaut : `max_iterations = 50`, `shell_timeout = "2m"`, `output_limit = 30000`, `audit.level = "full"`, fournisseur par défaut `mistral`.
- Fichiers d'audit en `0600`, dossiers de données en `0700`.
- Aucun secret en clair dans le journal d'audit ni dans une requête vers un fournisseur.
- Toute requête vers un fournisseur passe par `policy.Outbound.Check` ; tout outil passe par `policy.Permissions.Check`.

## Review Focus

1. **Dépôt malveillant** avec `.tenon/config.toml` qui ajoute des règles `allow` ou un fournisseur pointant vers un serveur tiers : la configuration est refusée (Task 3 : `TestProjectCannotAllow`, `TestProjectCannotDefineProviders`).
2. **Chemin qui sort du projet** via `../` ou via un lien symbolique interne pointant vers `/etc` : décision `Ask`, jamais `Allow`, même avec une règle `allow` (Task 7 : `TestSymlinkEscapeIsAsk`, `TestAllowRuleCannotEscapeRoot`).
3. **Secret collé dans le prompt** : il n'apparaît ni dans le corps de la requête HTTP, ni dans le journal d'audit (Task 11 : `TestSecretNeverInAuditOrRequest`).
4. **Commande shell qui laisse un processus en arrière-plan** ou ne termine jamais : l'outil rend la main dans les délais et tue le groupe de processus (Task 6 : `TestShellBackgroundChildDoesNotHang`, `TestShellTimeoutKillsGroup`).
5. **Modèle qui renvoie un nom d'outil inconnu ou des arguments JSON invalides** : un résultat d'erreur est renvoyé au modèle, le tour continue sans plantage (Task 10 : `TestUnknownToolAndMalformedArgs`).

---

### Task 1: Amorçage du dépôt et CLI minimale

**Files:**
- Create: `go.mod`, `LICENSE`, `README.md`, `.gitignore`
- Create: `cmd/tenon/main.go`, `cmd/tenon/run.go`
- Test: `cmd/tenon/run_test.go`

**Interfaces:**
- Produces: `func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string, wd string) int` dans `cmd/tenon/run.go` ; `var version = "dev"` (surchargée par `-ldflags "-X main.version=..."`). `main.go` appelle `os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv, wd))` avec un `ctx` annulé sur `SIGINT`.

- [ ] **Step 1: Initialiser le dépôt**

```bash
git init -b main
go mod init codeberg.org/tenon/tenon
```
Dans `go.mod`, fixer `go 1.26`. Télécharger le texte officiel anglais de l'EUPL-1.2 depuis le site de la Commission européenne (interoperable-europe.ec.europa.eu, page « EUPL text ») dans `LICENSE`. `README.md` : nom, une phrase de description, licence. `.gitignore` : `/tenon`, `/dist/`.

- [ ] **Step 2: Écrire les tests qui échouent**

```go
func TestVersionFlag(t *testing.T) {
    var out, errb bytes.Buffer
    code := run(context.Background(), []string{"--version"}, &out, &errb, func(string) string { return "" }, t.TempDir())
    if code != 0 || out.String() != "tenon dev\n" {
        t.Fatalf("code=%d out=%q", code, out.String())
    }
}

func TestUnknownFlag(t *testing.T) {
    var out, errb bytes.Buffer
    code := run(context.Background(), []string{"--nope"}, &out, &errb, func(string) string { return "" }, t.TempDir())
    if code != 1 || errb.Len() == 0 {
        t.Fatalf("code=%d stderr=%q", code, errb.String())
    }
}
```

- [ ] **Step 3: Vérifier l'échec**

Run: `GOFLAGS=-mod=vendor go test ./cmd/tenon/`
Expected: FAIL (`undefined: run`).

- [ ] **Step 4: Implémenter `run` avec `flag.NewFlagSet("tenon", flag.ContinueOnError)`**

Erreur de parsing : code de sortie `1`.

- [ ] **Step 5: Vérifier le succès**

Run: `go mod vendor && GOFLAGS=-mod=vendor go test ./cmd/tenon/ && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add .
git commit -m "chore: amorçage du dépôt tenon (EUPL-1.2, CLI minimale)"
```
Ce premier commit inclut aussi `docs/` (spec et plan).

---

### Task 2: Types partagés (`llm`, `event`)

**Files:**
- Create: `internal/llm/types.go`
- Create: `internal/event/event.go`, `internal/event/payloads.go`
- Test: `internal/event/event_test.go`

**Interfaces:**
- Produces (`internal/llm`) :
  ```go
  type Role string // "system", "user", "assistant", "tool"
  type Message struct {
      Role       Role       `json:"role"`
      Content    string     `json:"content,omitempty"`
      ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
      ToolCallID string     `json:"tool_call_id,omitempty"`
  }
  type ToolCall struct {
      ID   string          `json:"id"`
      Name string          `json:"name"`
      Args json.RawMessage `json:"args"`
  }
  type ToolDef struct { Name, Description string; Schema json.RawMessage }
  type Request struct { Model string; Messages []Message; Tools []ToolDef }
  type Chunk struct { Text string; ToolCalls []ToolCall; Done bool; Err error }
  type Jurisdiction string // EU = "eu", NonEU = "non-eu", Local = "local"
  type ProviderInfo struct {
      ID, Name, BaseURL, Model, Country string // tags json: id, name, base_url, model, country
      Jurisdiction Jurisdiction                // tag json: jurisdiction
  }
  ```
- Produces (`internal/event`) :
  ```go
  type Type string // constantes : SessionStarted, UserMessage, ContextCompacted, JurisdictionWarning,
                   // RedactionApplied, AssistantDelta, AssistantMessage, ToolCallRequested,
                   // PermissionRequested, PermissionResolved, ToolResult, McpServerDown,
                   // TurnCompleted, Error (valeur = nom de la constante)
  type Event struct {
      ID      string          `json:"id"`      // "<session>-<seq>"
      Session string          `json:"session"`
      Seq     uint64          `json:"seq"`
      TS      time.Time       `json:"ts"`
      Type    Type            `json:"type"`
      Payload json.RawMessage `json:"payload"`
  }
  func New(session string, seq uint64, ts time.Time, t Type, payload any) (Event, error)
  type Sink interface { Write(e Event) error }
  ```
- Produces (payloads, tags JSON en snake_case) : `SessionStartedPayload{Provider llm.ProviderInfo; ProjectRoot string}`, `UserMessagePayload{Text}`, `AssistantDeltaPayload{Text}`, `AssistantMessagePayload{Text; ToolCalls []llm.ToolCall; Incomplete bool}`, `ToolCallRequestedPayload{CallID, Tool string; Args json.RawMessage}`, `PermissionRequestedPayload{PermissionID, Kind, Tool string; Paths []string; Command string; Provider *llm.ProviderInfo}` (`Kind` vaut `"tool"` ou `"jurisdiction"`), `PermissionResolvedPayload{PermissionID string; Allowed, Remember bool}`, `ToolResultPayload{CallID, Tool, Content string; IsError bool; Path, HashBefore, HashAfter string}`, `JurisdictionWarningPayload{Provider llm.ProviderInfo}`, `RedactionAppliedPayload{Counts map[string]int}`, `TurnCompletedPayload{Iterations int; Reason string}` (`"done"`, `"max_iterations"`, `"incomplete"`, `"blocked"`), `ErrorPayload{Code, Message string}`.

- [ ] **Step 1: Écrire le test qui échoue**

```go
func TestNewMarshalsPayload(t *testing.T) {
    ts := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
    e, err := New("s_1", 1, ts, UserMessage, UserMessagePayload{Text: "salut"})
    if err != nil { t.Fatal(err) }
    got, _ := json.Marshal(e)
    want := `{"id":"s_1-1","session":"s_1","seq":1,"ts":"2026-10-14T10:00:00Z","type":"UserMessage","payload":{"text":"salut"}}`
    if string(got) != want { t.Fatalf("got %s", got) }
}
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/event/` — Expected: FAIL (`undefined: New`).
- [ ] **Step 3: Implémenter les deux paquets** selon les signatures ci-dessus.
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: types partagés llm et événements"`

---

### Task 3: Configuration

**Files:**
- Create: `internal/config/config.go`, `internal/config/presets.go`, `internal/config/load.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces :
  ```go
  type Config struct {
      Lang            string      `toml:"lang"`             // "fr" | "en"
      DefaultProvider string      `toml:"default_provider"`
      MaxIterations   int         `toml:"max_iterations"`
      ShellTimeout    Duration    `toml:"shell_timeout"`
      OutputLimit     int         `toml:"output_limit"`
      Audit           Audit       `toml:"audit"`            // Level string `toml:"level"` ("full" | "metadata")
      Providers       []Provider  `toml:"providers"`
      Permissions     Permissions `toml:"permissions"`      // Rules []Rule `toml:"rules"`
      Redaction       Redaction   `toml:"redaction"`        // Rules []RedactionRule `toml:"rules"`
  }
  type Duration struct{ time.Duration } // implémente encoding.TextUnmarshaler ("2m")
  type Provider struct { ID, Name, BaseURL, APIKeyEnv, Model, Country, Jurisdiction string; ContextWindow int }
  // tags toml : id, name, base_url, api_key_env, model, country, jurisdiction, context_window
  type Rule struct { Tool, Match, Decision string }     // decision : "allow" | "ask" | "deny"
  type RedactionRule struct { Name, Pattern string }
  func Defaults(getenv func(string) string) Config
  func Load(globalPath, projectPath string, getenv func(string) string) (Config, error)
  func (c Config) Provider(id string) (Provider, bool)
  func GlobalPath(getenv func(string) string) string // $XDG_CONFIG_HOME/tenon/config.toml, sinon $HOME/.config/tenon/config.toml
  func DataDir(getenv func(string) string) string    // $XDG_DATA_HOME/tenon, sinon $HOME/.local/share/tenon
  ```

**Préréglages** (`presets.go`). Vérifier l'ID exact du modèle dans la documentation de chaque fournisseur au moment de l'implémentation :

| id | name | base_url | api_key_env | country | jurisdiction |
|---|---|---|---|---|---|
| `mistral` | Mistral AI | `https://api.mistral.ai/v1` | `MISTRAL_API_KEY` | FR | eu |
| `scaleway` | Scaleway | `https://api.scaleway.ai/v1` | `SCW_SECRET_KEY` | FR | eu |
| `ovhcloud` | OVHcloud AI Endpoints | `https://oai.endpoints.kepler.ai.cloud.ovh.net/v1` | `OVH_AI_ENDPOINTS_ACCESS_TOKEN` | FR | eu |
| `ollama` | Ollama | `http://localhost:11434/v1` | (vide) | — | local |
| `vllm` | vLLM | `http://localhost:8000/v1` | (vide) | — | local |

**Règles de fusion et de validation :**
- Les fournisseurs globaux s'ajoutent aux préréglages ; même `id` = remplacement.
- Fournisseur global sans `jurisdiction` : `non-eu`.
- Configuration projet : seules les clés `lang`, `default_provider`, `permissions.rules`, `redaction.rules` sont acceptées (détection via `toml.MetaData.Undecoded()`). Toute autre clé : erreur `config projet : clé non autorisée "<clé>"`.
- Règle `allow` dans la configuration projet : erreur `config projet : les règles "allow" sont interdites`.
- Règles projet ajoutées après les règles globales.
- Erreurs de validation : `decision` hors `allow|ask|deny`, `jurisdiction` hors `eu|non-eu|local`, `lang` hors `fr|en`, `audit.level` hors `full|metadata`, `pattern` de masquage qui ne compile pas, `default_provider` inconnu.
- Fichier absent : ignoré.
- `lang` par défaut : `"en"` si `LANG` commence par `en`, sinon `"fr"`.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestDefaults(t *testing.T)                          // LANG=fr_FR.UTF-8 → Lang "fr" ; MaxIterations 50 ; ShellTimeout 2m ;
                                                         // OutputLimit 30000 ; Audit.Level "full" ; DefaultProvider "mistral" ;
                                                         // préréglages mistral/scaleway/ovhcloud en "eu", ollama/vllm en "local"
func TestLangEnglish(t *testing.T)                       // LANG=en_US.UTF-8 → "en"
func TestCustomProviderWithoutJurisdictionIsNonEU(t *testing.T)
func TestProjectCannotAllow(t *testing.T)                // erreur contenant `"allow"`
func TestProjectCannotDefineProviders(t *testing.T)      // erreur contenant `"providers"`
func TestProjectCanAddDeny(t *testing.T)                 // la règle deny projet est la dernière de Permissions.Rules
func TestInvalidRegexRejected(t *testing.T)
func TestInvalidDecisionRejected(t *testing.T)
func TestUnknownDefaultProvider(t *testing.T)
func TestMissingFilesUseDefaults(t *testing.T)
```
Les fichiers TOML de test sont écrits dans `t.TempDir()`.

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/config/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** — `go get github.com/BurntSushi/toml@v1.5.0 && go mod vendor`.
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: configuration globale et projet, préréglages européens et locaux"`

---

### Task 4: Fournisseur compatible OpenAI

**Files:**
- Create: `internal/provider/provider.go`, `internal/provider/openai.go`, `internal/provider/sse.go`
- Test: `internal/provider/openai_test.go`

**Interfaces:**
- Consumes : `llm.Request`, `llm.Chunk`, `llm.ProviderInfo`.
- Produces :
  ```go
  type Provider interface {
      Info() llm.ProviderInfo
      Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error)
  }
  func NewOpenAICompat(info llm.ProviderInfo, apiKey string, opts ...Option) *OpenAICompat
  func WithHTTPClient(c *http.Client) Option
  func WithSleep(f func(time.Duration)) Option // défaut time.Sleep ; injecté en test
  var ErrAuth, ErrContextLength, ErrIncomplete, ErrUnavailable error
  ```

**Comportement :**
- `POST {base_url}/chat/completions`, en-tête `Authorization: Bearer <clé>` si la clé n'est pas vide, corps `{"model","messages","tools","stream":true}` au format OpenAI. Les `tool_calls` sont envoyés avec `type:"function"` et `function.arguments` en chaîne JSON.
- Le canal émet des `Chunk{Text}` pour chaque delta, puis un dernier `Chunk{ToolCalls, Done:true}`, puis il est fermé. En cas d'erreur pendant le stream : `Chunk{Err, Done:true}`.
- Les deltas `tool_calls` sont accumulés par `index` : `id` et `function.name` au premier delta, `function.arguments` concaténés.
- Nouvelles tentatives, uniquement avant le premier octet du stream : 429, 5xx et erreurs réseau. Maximum 3 nouvelles tentatives, attentes 1 s, 2 s, 4 s ; `Retry-After` (en secondes) remplace l'attente calculée. Après épuisement : `ErrUnavailable`.
- 401, 403 : `ErrAuth`, sans nouvelle tentative.
- 400 dont le corps contient `context_length_exceeded` ou `maximum context length` : `ErrContextLength`.
- Fin du flux sans `[DONE]` ni `finish_reason` : dernier chunk avec `Err: ErrIncomplete` ; le texte déjà reçu a été émis.

- [ ] **Step 1: Écrire les tests qui échouent** (serveurs `httptest`)

```go
func TestStreamText(t *testing.T)                  // deltas "Bon","jour" + [DONE] → texte "Bonjour", Done sans Err ;
                                                   // la requête a "Authorization: Bearer k" et "stream":true
func TestStreamToolCallAccumulated(t *testing.T)   // arguments en 3 deltas `{"pa`, `th":"a`, `.go"}` →
                                                   // ToolCalls[0] == {ID:"call_1", Name:"read_file", Args:`{"path":"a.go"}`}
func TestRetryOn429ThenOK(t *testing.T)            // 429 + Retry-After: 2, puis 200 → sleeps == [2s]
func TestRetryExhaustedReturnsUnavailable(t *testing.T) // 500 ×4 → errors.Is(err, ErrUnavailable), sleeps == [1s,2s,4s]
func TestAuthErrorNoRetry(t *testing.T)            // 401 → ErrAuth, 1 seule requête
func TestContextLength(t *testing.T)               // 400 {"error":{"code":"context_length_exceeded"}} → ErrContextLength
func TestStreamCutIsIncomplete(t *testing.T)       // un delta "Bon" puis fermeture → dernier chunk Err == ErrIncomplete
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/provider/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** selon les signatures et le comportement ci-dessus.
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/provider/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: client de streaming compatible OpenAI avec nouvelles tentatives"`

---

### Task 5: Outils fichiers et recherche

**Files:**
- Create: `internal/tools/tool.go`, `internal/tools/fs.go`, `internal/tools/search.go`, `internal/tools/truncate.go`
- Test: `internal/tools/fs_test.go`, `internal/tools/search_test.go`

**Interfaces:**
- Consumes : `llm.ToolDef`.
- Produces :
  ```go
  type Risk int // Read, Write, Exec, Network (dans cet ordre, à partir de 0)
  type Target struct { Paths []string; Command string } // chemins absolus nettoyés
  type Result struct { Content string; IsError bool; Path, HashBefore, HashAfter string }
  type Tool interface {
      Name() string
      Description() string
      Schema() json.RawMessage
      Risk() Risk
      Target(args json.RawMessage) (Target, error) // erreur si arguments invalides
      Run(ctx context.Context, args json.RawMessage) (Result, error) // error réservée à l'annulation du contexte
  }
  type Registry struct{ /* … */ }
  func NewRegistry(ts ...Tool) *Registry
  func (r *Registry) Get(name string) (Tool, bool)
  func (r *Registry) Defs() []llm.ToolDef // triés par nom
  func FS(root string, limit int) []Tool  // read_file, write_file, edit_file, glob, grep
  func Truncate(s string, limit int) string
  ```

**Outils** (chemin relatif résolu depuis `root`) :

| Outil | Risque | Arguments | Comportement |
|---|---|---|---|
| `read_file` | Read | `path`, `offset?` (1 par défaut), `limit?` (2000 par défaut) | lignes préfixées `N\t` ; fichier binaire (octet NUL dans les 8000 premiers octets) : erreur `fichier binaire` |
| `write_file` | Write | `path`, `content` | crée les dossiers parents ; `HashBefore` vide si fichier absent ; hash au format `sha256:<hex>` |
| `edit_file` | Write | `path`, `old_string`, `new_string`, `replace_all?` | 0 occurrence : erreur `texte introuvable` ; plus d'une sans `replace_all` : erreur `texte présent N fois` |
| `glob` | Read | `pattern` | doublestar depuis `root`, trié, ignore `.git`, 1000 résultats maximum |
| `grep` | Read | `pattern` (regexp Go), `glob?` | ignore `.git` et les fichiers binaires ; lignes `chemin:ligne:texte` ; 500 correspondances maximum |

`Truncate` ajoute `\n[… sortie tronquée à <limit> caractères]` au-delà de `limit`.

- [ ] **Step 1: Écrire les tests qui échouent** (dans `t.TempDir()`)

```go
func TestReadFileNumbersLines(t *testing.T)        // "a\nb\n" → "1\ta\n2\tb\n"
func TestReadFileBinaryRejected(t *testing.T)
func TestWriteFileHashes(t *testing.T)             // HashBefore "" puis, en réécriture, HashBefore == ancien HashAfter
func TestEditFileUniqueMatch(t *testing.T)
func TestEditFileAmbiguous(t *testing.T)           // IsError, Content contient "présent 2 fois"
func TestEditFileNotFound(t *testing.T)
func TestTargetResolvesRelativePath(t *testing.T)  // Target(`{"path":"x/y.go"}`).Paths == [root/x/y.go]
func TestTargetInvalidArgs(t *testing.T)           // `{"path":` → erreur
func TestGlobSkipsGit(t *testing.T)
func TestGrepSkipsBinary(t *testing.T)
func TestTruncate(t *testing.T)
func TestRegistryDefsSorted(t *testing.T)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/tools/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** — `go get github.com/bmatcuk/doublestar/v4@v4.8.1 && go mod vendor`.
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: outils fichiers, glob et grep"`

---

### Task 6: Outil shell

**Files:**
- Create: `internal/tools/shell.go`
- Test: `internal/tools/shell_test.go`

**Interfaces:**
- Consumes : `Tool`, `Target`, `Result`, `Truncate` (Task 5).
- Produces : `func Shell(root string, timeout time.Duration, limit int) Tool` ; nom `shell`, risque `Exec`, arguments `command`, `timeout_seconds?` ; `Target` = `{Command: command}`.

**Comportement :**
- `exec.Command("bash", "-c", command)`, `Dir = root`, `SysProcAttr.Setpgid = true`, `WaitDelay = 1s`.
- Sortie standard et erreur combinées, tronquées, suivies de `\n[code de sortie: N]`. Code non nul : `IsError`.
- Délai dépassé : `syscall.Kill(-pgid, SIGKILL)`, `IsError`, contenu contenant `délai dépassé`.
- Annulation du contexte : même kill ; `Run` renvoie `ctx.Err()`.
- Après la fin du processus principal, toujours tuer le groupe : aucun processus d'arrière-plan ne survit.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestShellEcho(t *testing.T)                    // "echo bonjour" → contient "bonjour" et "[code de sortie: 0]"
func TestShellNonZero(t *testing.T)                 // "exit 3" → IsError, contient "[code de sortie: 3]"
func TestShellRunsInRoot(t *testing.T)              // "pwd" == EvalSymlinks(root)
func TestShellTimeoutKillsGroup(t *testing.T)       // timeout 1s, "sleep 30" → rend la main en < 3s, "délai dépassé"
func TestShellBackgroundChildDoesNotHang(t *testing.T) // "sleep 30 & echo ok" → < 3s, contient "ok"
func TestShellCancel(t *testing.T)                  // annulation après 200ms sur "sleep 30" → < 2s, errors.Is(err, context.Canceled)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/tools/ -run Shell` — Expected: FAIL.
- [ ] **Step 3: Implémenter** selon le comportement ci-dessus.
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/tools/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: outil shell avec timeout et kill du groupe de processus"`

---

### Task 7: Permissions

**Files:**
- Create: `internal/policy/permissions.go`, `internal/policy/wildcard.go`
- Test: `internal/policy/permissions_test.go`

**Interfaces:**
- Consumes : `tools.Risk`, `tools.Target`.
- Produces :
  ```go
  type Decision int // Allow, Ask, Deny
  type Rule struct { Tool, Match string; Decision Decision } // Tool "*" = tous les outils
  type Call struct { Tool string; Risk tools.Risk; Target tools.Target }
  func NewPermissions(root string, rules []Rule) (*Permissions, error) // root résolu via EvalSymlinks
  func (p *Permissions) Check(c Call) Decision
  func ParseDecision(s string) (Decision, error) // "allow" | "ask" | "deny"
  ```

**Algorithme de `Check`, dans cet ordre :**
1. Une règle correspond si son `Tool` vaut le nom de l'outil ou `"*"`, et si son `Match` correspond à `Target.Command` (si non vide) ou à au moins un chemin. Un chemin dans le projet est comparé sous forme relative à `root`, sinon sous forme absolue. `Match` vide correspond à tout.
2. Une règle `deny` correspond : `Deny`.
3. Un chemin est hors de `root` après résolution des liens symboliques (on résout le plus long préfixe existant), ou son nom de base correspond à `.env*`, `*.pem` ou `id_rsa*` : `Ask`.
4. Une règle `allow` correspond : `Allow`.
5. Une règle `ask` correspond : `Ask`.
6. Défaut : `Read` donne `Allow`, les autres risques donnent `Ask`.

`wildcard.go` : `func matchWildcard(pattern, s string) bool`, où `*` correspond à toute suite de caractères, y compris les espaces et `/` ; les autres caractères sont littéraux.

- [ ] **Step 1: Écrire les tests qui échouent** (table pilotée, projet dans `t.TempDir()`)

```go
func TestDefaultsByRisk(t *testing.T)            // read dans le projet → Allow ; write → Ask ; shell → Ask
func TestDenyWinsOverAllow(t *testing.T)         // allow shell "*" + deny shell "rm -rf *" ; "rm -rf /" → Deny
func TestAllowShellPattern(t *testing.T)         // allow shell "go test *" ; "go test ./..." → Allow ; "go build" → Ask
func TestDotDotEscapeIsAsk(t *testing.T)         // read "../secret" → Ask
func TestSymlinkEscapeIsAsk(t *testing.T)        // lien root/etc → /etc ; read root/etc/hosts → Ask
func TestAllowRuleCannotEscapeRoot(t *testing.T) // allow read_file "*" ; read hors projet → Ask
func TestSensitiveFilesAsk(t *testing.T)         // read ".env.local", "cert.pem", "id_rsa" → Ask, même avec allow "*"
func TestWildcardToolDeny(t *testing.T)          // deny "*" match "secrets/*" ; write "secrets/a" → Deny
func TestMatchWildcard(t *testing.T)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/policy/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** selon l'algorithme ci-dessus.
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: moteur de permissions (deny prioritaire, confinement au projet)"`

---

### Task 8: Masquage et contrôle sortant

**Files:**
- Create: `internal/policy/redact.go`, `internal/policy/outbound.go`
- Test: `internal/policy/redact_test.go`, `internal/policy/outbound_test.go`

**Interfaces:**
- Consumes : `llm.Message`, `llm.ProviderInfo`.
- Produces :
  ```go
  type RedactionRule struct { Name, Pattern string }
  func NewRedactor(custom []RedactionRule) (*Redactor, error)
  func (r *Redactor) Redact(s string) (string, map[string]int) // texte masqué, nombre de masquages par type
  func NewOutbound(r *Redactor) *Outbound
  func (o *Outbound) Consent(providerID string)
  func (o *Outbound) Check(p llm.ProviderInfo, msgs []llm.Message) ([]llm.Message, map[string]int, error)
  var ErrJurisdiction error // "fournisseur hors UE sans consentement"
  ```

**Règles intégrées, dans cet ordre :**
1. `SECRET` : blocs `-----BEGIN … PRIVATE KEY----- … -----END … PRIVATE KEY-----` ; `AKIA[0-9A-Z]{16}` ; `gh[pousr]_[A-Za-z0-9]{36}` ; `sk-[A-Za-z0-9_-]{20,}` ; JWT `eyJ[\w-]+\.[\w-]+\.[\w-]+` ; affectations `(?i)(api[_-]?key|secret|token|password|passwd)\w*\s*[:=]\s*['"]?([^\s'"]{12,})` (seule la valeur est masquée).
2. `IBAN` : motif IBAN avec espaces optionnels, conservé seulement si le contrôle mod-97 vaut 1.
3. `NIR` : 13 chiffres + clé de 2 chiffres, espaces optionnels, conservé seulement si `clé == 97 - (n mod 97)` (`2A` → `19`, `2B` → `18` pour le calcul).
4. `EMAIL`.
5. `TEL` : `(?:\+33\s?|0)[1-9](?:[\s.-]?\d{2}){4}`.
6. Règles personnalisées : type = `strings.ToUpper(Name)`.

Marqueur : `[<TYPE>_<n>]`, numéroté par type. Une même valeur reçoit toujours le même marqueur pour un même `Redactor`.

`Check` : renvoie `ErrJurisdiction` si `p.Jurisdiction == non-eu` sans `Consent(p.ID)` préalable ; sinon renvoie une copie des messages avec `Content` et `ToolCalls[].Args` masqués, sans modifier l'original.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestRedactTable(t *testing.T) {
    cases := []struct{ in, want string }{
        {"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----", "[SECRET_1]"},
        {"MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf", "MISTRAL_API_KEY=[SECRET_1]"},
        {"FR76 3000 6000 0112 3456 7890 189", "[IBAN_1]"},
        {"FR76 3000 6000 0112 3456 7890 188", "FR76 3000 6000 0112 3456 7890 188"}, // checksum invalide
        {"NIR 1 85 05 78 006 084 91", "NIR [NIR_1]"},
        {"NIR 1 85 05 78 006 084 92", "NIR 1 85 05 78 006 084 92"},                // clé invalide
        {"jean.dupont@exemple.fr et jean.dupont@exemple.fr", "[EMAIL_1] et [EMAIL_1]"},
        {"06 12 34 56 78", "[TEL_1]"},
        {"+33 6 12 34 56 78", "[TEL_1]"},
        {"version 1.2.3, port 8080, func main()", "version 1.2.3, port 8080, func main()"},
        {"550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
    }
    // nouveau Redactor par cas
}
func TestRedactCounts(t *testing.T)               // 2 emails distincts + 1 secret → {"EMAIL":2,"SECRET":1}
func TestCustomRule(t *testing.T)                 // {Name:"client", Pattern:`CLT-\d{6}`} → "[CLIENT_1]"
func TestOutboundNonEUWithoutConsent(t *testing.T) // errors.Is(err, ErrJurisdiction)
func TestOutboundNonEUAfterConsent(t *testing.T)
func TestOutboundEUAndLocalPass(t *testing.T)
func TestOutboundRedactsContentAndArgs(t *testing.T) // l'original n'est pas modifié
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/policy/ -run 'Redact|Outbound|Custom'` — Expected: FAIL.
- [ ] **Step 3: Implémenter** selon les règles ci-dessus.
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test ./internal/policy/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: masquage des secrets et données personnelles, contrôle de juridiction"`

---

### Task 9: Journal d'audit chaîné

**Files:**
- Create: `internal/audit/writer.go`, `internal/audit/verify.go`
- Test: `internal/audit/audit_test.go`

**Interfaces:**
- Consumes : `event.Event`, `event.Sink`.
- Produces :
  ```go
  type Level string // Full = "full", Metadata = "metadata"
  func Open(dir string, level Level, redact func(string) string) (*Writer, error) // crée dir en 0700
  func (w *Writer) Write(e event.Event) error // implémente event.Sink ; fsync à chaque ligne
  func (w *Writer) Close() error
  func Verify(dir string) (lastHash string, lines int, err error)
  ```

**Format et chaînage :**
- Une ligne : `{"seq","ts","session","type","payload","prev","hash"}`, valeurs reprises de l'événement.
- `payload` : redact appliqué au texte JSON, puis `json.Compact`. En niveau `metadata`, `payload` devient `{"sha256":"<hex du payload masqué>","size":<octets>}`.
- `hash = "sha256:" + hex(sha256(prev + JSON de la ligne sans le champ hash))`.
- Première ligne jamais écrite : `prev = "sha256:0"`.
- Fichier : `<dir>/<date UTC de e.TS>.jsonl`, créé en `0600`.
- `Open` reprend la chaîne depuis la dernière ligne du fichier le plus récent.
- `Verify` parcourt les fichiers triés par nom ; une erreur indique `<fichier>:<ligne>`.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestChainVerifies(t *testing.T)            // 3 événements → Verify ok, lines == 3, lastHash == hash de la ligne 3
func TestTamperedPayloadDetected(t *testing.T)  // modifier le payload de la ligne 2 → erreur contenant "2026-10-14.jsonl:2"
func TestDeletedLineDetected(t *testing.T)      // supprimer la ligne 2 → erreur
func TestReopenContinuesChain(t *testing.T)     // Close, Open, Write → Verify ok sur 4 lignes
func TestDailyRotation(t *testing.T)            // TS 14/10 et 15/10 → 2 fichiers, Verify ok
func TestPermissions(t *testing.T)              // fichier 0600, dossier 0700
func TestMetadataLevelHidesContent(t *testing.T) // le fichier ne contient pas "salut"
func TestRedactApplied(t *testing.T)            // redact remplace "Zx8kq" → le fichier ne contient pas "Zx8kq"
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/audit/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** selon le format ci-dessus.
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat: journal d'audit JSONL chaîné par hash et vérification"`

---

### Task 10: Engine (boucle agentique)

**Files:**
- Create: `internal/engine/engine.go`, `internal/engine/loop.go`, `internal/engine/prompt.go`
- Test: `internal/engine/engine_test.go`, `internal/engine/fake_test.go`, `internal/engine/testdata/text_turn.golden`

**Interfaces:**
- Consumes : `provider.Provider`, `tools.Registry`, `policy.Permissions`, `policy.Outbound`, `event.Sink`, payloads de Task 2.
- Produces :
  ```go
  type Deps struct {
      Provider      provider.Provider
      Tools         *tools.Registry
      Permissions   *policy.Permissions
      Outbound      *policy.Outbound
      Sinks         []event.Sink // audit en premier
      MaxIterations int
      SystemPrompt  string
      ProjectRoot   string
      Now           func() time.Time
  }
  type Reply struct { Allow, Remember bool }
  func New(d Deps) *Engine
  func (e *Engine) Subscribe() <-chan event.Event // tampon de 256 ; appeler avant Submit
  func (e *Engine) Submit(ctx context.Context, sessionID, input string) error // bloque jusqu'à TurnCompleted
  func (e *Engine) Respond(permissionID string, r Reply) error
  var ErrAuditFailure, ErrUnknownPermission error
  ```
- `prompt.go` : `const DefaultSystemPrompt` en français, qui décrit le rôle d'agent de code, les outils et la règle « demander avant toute action destructive ».

**Comportement :**
- **Émission :** chaque événement est écrit dans chaque `Sink`, dans l'ordre, puis envoyé aux abonnés. Si un `Sink` échoue, l'engine passe en fermeture par défaut : il envoie aux abonnés seulement un `Error{Code:"audit_failure"}`, puis `Submit` renvoie `ErrAuditFailure`. Aucun outil ne s'exécute ensuite.
- **Début de session :** `SessionStarted` (fournisseur, racine du projet) au premier `Submit` d'une session. L'historique reste en mémoire, par session (la persistance arrive au plan 2).
- **Étapes 1 à 7 de la spec §5**, avec ces précisions :
  - `Outbound.Check` renvoie `ErrJurisdiction` : l'engine émet `PermissionRequested{Kind:"jurisdiction", Provider}` et attend la réponse. Accord : `Consent`, puis on recommence. Refus : `TurnCompleted{Reason:"blocked"}`.
  - Premier appel d'une session vers un fournisseur `non-eu` : `JurisdictionWarning`.
  - `RedactionApplied` est émis si au moins un masquage a eu lieu.
  - Pour chaque appel d'outil, `ToolCallRequested` est émis **avant** la décision et l'exécution. Une erreur d'audit à ce moment bloque donc l'outil.
  - Outil inconnu : résultat `IsError` « outil inconnu : X ». `Target` en erreur : « arguments invalides : … ». `Deny` : « action refusée par la politique ».
  - `Ask` : si la clé `outil + commande + chemins` a été retenue avec `Remember` dans la session, l'outil s'exécute ; sinon `PermissionRequested{Kind:"tool"}`, puis attente de `Respond` ou de l'annulation du contexte. Refus : « action refusée par l'utilisateur ». La réponse est tracée par `PermissionResolved`.
  - `ErrIncomplete` : `AssistantMessage{Incomplete:true}`, puis `TurnCompleted{Reason:"incomplete"}`. Autre erreur du fournisseur : `Error{Code:"provider"}`, et `Submit` renvoie l'erreur.
  - Limite d'itérations atteinte : `TurnCompleted{Reason:"max_iterations"}`.

- [ ] **Step 1: Écrire le faux fournisseur** (`fake_test.go`) : `type fakeProvider struct{ info llm.ProviderInfo; script [][]llm.Chunk; requests []llm.Request }`. Chaque appel à `Stream` rejoue l'entrée suivante du script et enregistre la requête. Script épuisé : l'appel rejoue la dernière entrée.

- [ ] **Step 2: Écrire les tests qui échouent**

```go
func TestTextOnlyTurn(t *testing.T)               // types : SessionStarted, UserMessage, AssistantDelta, AssistantMessage,
                                                  // TurnCompleted{done} ; JSON complet comparé à testdata/text_turn.golden (flag -update)
func TestToolCallAllowedRead(t *testing.T)        // read_file → ToolResult contient le contenu ; 2e requête contient un message role "tool"
func TestToolCallAskDeniedByReply(t *testing.T)   // write_file → PermissionRequested ; Reply{Allow:false} → fichier absent,
                                                  // ToolResult IsError "refusée par l'utilisateur"
func TestRememberForSession(t *testing.T)         // 2 écritures identiques ; 1re réponse {Allow, Remember} → 1 seul PermissionRequested
func TestUnknownToolAndMalformedArgs(t *testing.T) // 2 ToolResult IsError, puis TurnCompleted{done}
func TestMaxIterations(t *testing.T)              // MaxIterations 3, le fournisseur appelle toujours un outil →
                                                  // 3 requêtes, TurnCompleted{max_iterations}
func TestAuditFailureFailsClosed(t *testing.T)    // Sink en erreur sur ToolCallRequested → write_file non exécuté, ErrAuditFailure
func TestNonEUConsentAsked(t *testing.T)          // réponse Allow → JurisdictionWarning puis tour normal
func TestNonEURefused(t *testing.T)               // réponse refus → aucune requête au fournisseur, TurnCompleted{blocked}
func TestSecretRedactedBeforeProvider(t *testing.T) // entrée avec "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf" →
                                                  // aucune requête ne contient "Zx8kq" ; RedactionApplied émis
func TestIncompleteStream(t *testing.T)           // TurnCompleted{incomplete}, AssistantMessage.Incomplete == true
```

- [ ] **Step 3: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/engine/` — Expected: FAIL.
- [ ] **Step 4: Implémenter** selon le comportement ci-dessus.
- [ ] **Step 5: Générer le golden et vérifier** — Run: `GOFLAGS=-mod=vendor go test ./internal/engine/ -run TestTextOnlyTurn -update && GOFLAGS=-mod=vendor go test -race ./internal/engine/` — Expected: PASS. Relire `testdata/text_turn.golden` avant de le commiter.
- [ ] **Step 6: Commit** — `git commit -m "feat: boucle agentique pilotée par événements, fermeture par défaut sur l'audit"`

---

### Task 11: Mode headless et commandes CLI

**Files:**
- Create: `internal/ui/headless/headless.go`
- Modify: `cmd/tenon/run.go`
- Test: `internal/ui/headless/headless_test.go`, `cmd/tenon/e2e_test.go`

**Interfaces:**
- Consumes : tout ce qui précède.
- Produces :
  ```go
  // internal/ui/headless
  type Output string // "text" | "json"
  func Run(ctx context.Context, eng *engine.Engine, sessionID, prompt string, out Output, stdout, stderr io.Writer) int
  ```

**Comportement de `headless.Run` :**
- Répond à tout `PermissionRequested` par `Reply{Allow:false}`.
- **Sortie `text` :**
  - texte final de l'assistant sur stdout ;
  - sur stderr, la bannière de transparence `tenon · <modèle> · <nom fournisseur> · <pays> · juridiction <UE|hors UE|locale>` (à partir de `SessionStarted`), les avertissements de juridiction et les refus.
- **Sortie `json` :** chaque événement est une ligne JSON sur stdout.
- **Code de sortie :** `3` si au moins une action ou un appel fournisseur a été refusé, `1` si `Submit` renvoie une erreur, sinon `0`.

**CLI (`run`) :**
- `tenon -p "<prompt>" [--output text|json] [--allow-non-eu] [--provider <id>]`
- `tenon audit verify` : affiche `OK : <n> lignes, dernier hash <hash>` (code `0`), ou l'erreur (code `1`).
- `tenon config check` : affiche `OK` (code `0`), ou l'erreur (code `1`).
- Sans `-p` ni sous-commande : `l'interface interactive arrive bientôt : utilisez -p "<prompt>"`, code `1`.
- **Démarrage :**
  1. `config.Load(GlobalPath, <wd>/.tenon/config.toml)`.
  2. Fournisseur : `--provider`, sinon `default_provider`.
  3. Clé API lue depuis `api_key_env`. Elle est obligatoire sauf pour la juridiction `local` ; si elle manque : `variable <NOM> absente`, code `1`.
  4. `audit.Open(DataDir/audit, level, redactor)` ; le même `Redactor` sert à l'audit et à `Outbound`.
  5. `--allow-non-eu` : `Outbound.Consent(id)` avant le tour.
  6. Outils `FS(wd, output_limit)` et `Shell(wd, shell_timeout, output_limit)`.
  7. ID de session : `s_` suivi de 12 caractères hexadécimaux aléatoires.

- [ ] **Step 1: Écrire les tests qui échouent**

Tests de bout en bout dans `cmd/tenon/e2e_test.go`, avec un faux serveur `httptest` compatible OpenAI piloté par script. `XDG_CONFIG_HOME` et `XDG_DATA_HOME` pointent vers des dossiers temporaires. Le `config.toml` global définit un fournisseur `test` (`base_url` = serveur, `api_key_env = "TEST_KEY"`, `jurisdiction = "eu"`) et `default_provider = "test"`.

```go
func TestHeadlessReadsFileAndAnswers(t *testing.T)   // appel read_file README.md puis texte "Le fichier contient bonjour" →
                                                     // stdout contient ce texte, code 0 ; `audit verify` → code 0, "OK"
func TestHeadlessWriteDeniedExit3(t *testing.T)      // appel write_file → code 3, fichier absent
func TestHeadlessNonEUWithoutFlagExit3(t *testing.T) // jurisdiction "non-eu" → code 3, aucune requête reçue par le serveur
func TestHeadlessNonEUWithFlag(t *testing.T)         // --allow-non-eu → code 0, stderr contient "hors UE"
func TestHeadlessJSONOutput(t *testing.T)            // chaque ligne se décode en event.Event ; 1re = SessionStarted,
                                                     // provider.jurisdiction == "eu"
func TestSecretNeverInAuditOrRequest(t *testing.T)   // prompt avec "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf" →
                                                     // aucun corps reçu ni fichier d'audit ne contient "Zx8kq"
func TestConfigCheckRejectsProjectAllow(t *testing.T) // .tenon/config.toml avec allow → code 1, stderr contient "allow"
func TestMissingAPIKey(t *testing.T)                 // TEST_KEY absente → code 1, stderr contient "TEST_KEY"
func TestBannerOnStderr(t *testing.T)                // stderr contient "juridiction UE"
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./cmd/tenon/ ./internal/ui/headless/` — Expected: FAIL.
- [ ] **Step 3: Implémenter `headless.Run` puis le câblage de `run`.**
- [ ] **Step 4: Vérifier toute la suite** — Run: `GOFLAGS=-mod=vendor go vet ./... && GOFLAGS=-mod=vendor go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Essai manuel** (si Ollama est installé) — Run: `go build -o tenon ./cmd/tenon && ./tenon --provider ollama -p "liste les fichiers Go"` — Expected: bannière `juridiction locale` sur stderr, réponse sur stdout, puis `./tenon audit verify` affiche `OK`.
- [ ] **Step 6: Commit** — `git commit -m "feat: mode headless, audit verify et config check"`
