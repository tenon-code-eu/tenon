# tenon — Plan 2 : sessions, compaction, instructions projet, i18n, trousseau

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rendre `tenon` utilisable au quotidien en mode headless : sessions persistantes et reprenables, compaction automatique du contexte, chargement de `AGENTS.md` / `TENON.md`, interface FR/EN, et clés API lues depuis le trousseau système.

**Architecture:** Les sessions sont un nouveau `event.Sink` (`internal/session`) écrit après l'audit ; la reprise rejoue les événements persistés dans l'engine (`Engine.Restore`). La compaction vit dans l'engine, avant chaque appel au fournisseur, et passe par le même contrôle sortant. L'i18n ne couvre que l'interface (CLI, headless) et le prompt système, comme le dit la spec §7.

**Tech Stack:** Go 1.26 ; nouvelles dépendances vendorées : `github.com/zalando/go-keyring` (et sa dépendance `github.com/godbus/dbus/v5`), `golang.org/x/term`.

**Spec:** `docs/superpowers/specs/2026-10-14-tenon-design.md` (§5 étape 2, §7, §9). Plan précédent : `docs/superpowers/plans/2026-10-14-tenon-plan-1-socle-headless.md`.

## Global Constraints

- Toutes les contraintes globales du plan 1 restent en vigueur (module, `go 1.26`, `GOFLAGS=-mod=vendor`, dépendances vendorées, Linux/macOS, `0600`/`0700`, aucun secret en clair dans l'audit ni dans une requête, `Outbound.Check` et `Permissions.Check` obligatoires).
- Aucun secret en clair dans un fichier de session : chaque payload passe par `Redactor.RedactJSON` avant écriture.
- Fichiers de session : `$XDG_DATA_HOME/tenon/sessions/<id>.jsonl` (défaut `~/.local/share/tenon/sessions/`), fichiers `0600`, dossier `0700`.
- Compaction : seuil par défaut `compaction_threshold = 0.8` de la fenêtre de contexte ; fenêtre par défaut `context_window = 32000` jetons si le fournisseur n'en déclare pas ; estimation = nombre de caractères / 4.
- Instructions projet : `AGENTS.md` puis `TENON.md`, à la racine du projet uniquement, 32 768 octets maximum chacun.
- Langues : `fr` et `en` ; l'i18n couvre la CLI, le mode headless et le prompt système. Les messages des outils et de l'engine destinés au modèle restent en français.
- Trousseau : service `tenon`, compte = identifiant du fournisseur ; la variable d'environnement est prioritaire sur le trousseau.

## Écart assumé avec la spec

La spec §7 décrit `tenon --resume` sans argument pour lister les sessions. Le paquet `flag` de Go ne gère pas les valeurs optionnelles ; ce plan utilise `tenon sessions` pour lister et `--resume <id|last>` pour reprendre.

## Review Focus

1. **Secret tapé dans le prompt** : il n'apparaît jamais en clair dans le fichier de session (Task 4 : `TestSessionFileRedacted`).
2. **Reprise d'une session dont le dernier tour a été annulé pendant un appel d'outil** : l'historique reconstruit contient un message `tool` pour chaque appel, sinon le fournisseur répond HTTP 400 (Task 5 : `TestRestoreClosesOrphanToolCalls`).
3. **Fichier de session tronqué par un crash** : la reprise s'arrête à la dernière ligne valide avec un avertissement, sans planter (Task 4 : `TestLoadStopsAtCorruptLine`).
4. **Un seul résultat d'outil plus grand que la fenêtre de contexte** : la compaction ne peut pas aider ; l'engine émet une erreur claire au lieu de boucler (Task 6 : `TestCompactionCannotFit`).
5. **`AGENTS.md` lien symbolique vers `~/.ssh/id_rsa`, ou fichier énorme** : non chargé, ou tronqué (Task 3 : `TestSymlinkNotLoaded`, `TestTruncated`).

---

### Task 1: Configuration de la fenêtre de contexte et de la compaction

**Files:**
- Modify: `internal/config/config.go`, `internal/config/presets.go`, `internal/config/load.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces : champ `Config.CompactionThreshold float64` (`toml:"compaction_threshold"`, défaut `0.8`) ; `Provider.ContextWindow` renseigné dans les préréglages ; `func (p Provider) Window() int` renvoie `ContextWindow`, ou `32000` s'il vaut 0.

**Valeurs des préréglages** (`context_window`, à vérifier dans la doc de chaque fournisseur, commentaire `TODO(release)`) : mistral `128000`, scaleway `128000`, ovhcloud `128000`, ollama `32000`, vllm `32000`.

**Validation :** `compaction_threshold` doit être dans `]0, 1[` ; `context_window` négatif refusé. `compaction_threshold` est une clé globale uniquement (la config projet reste limitée à `lang`, `permissions.rules`, `redaction.rules`).

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestDefaultsCompaction(t *testing.T)        // CompactionThreshold == 0.8 ; mistral.Window() == 128000
func TestWindowFallback(t *testing.T)            // Provider{}.Window() == 32000
func TestCompactionThresholdRange(t *testing.T)  // 0, 1, 1.5, -0.1 → erreur ; 0.5 → ok
func TestNegativeContextWindow(t *testing.T)     // context_window = -1 → erreur
func TestProjectCannotSetCompaction(t *testing.T) // clé projet → erreur "clé non autorisée"
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/config/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(config): fenêtre de contexte et seuil de compaction"`

---

### Task 2: i18n FR/EN et prompts système

**Files:**
- Create: `internal/i18n/i18n.go`, `internal/i18n/catalog.go`, `internal/i18n/prompt.go`
- Modify: `internal/engine/prompt.go` (le prompt FR actuel devient `i18n.SystemPrompt(i18n.FR)` ; `engine.DefaultSystemPrompt` reste, égal au prompt FR)
- Test: `internal/i18n/i18n_test.go`

**Interfaces:**
- Produces :
  ```go
  type Lang string // FR = "fr", EN = "en"
  func Parse(s string) Lang               // "en…" → EN, sinon FR
  func T(l Lang, key string, args ...any) string // fmt.Sprintf sur le gabarit ; clé inconnue → "!" + key
  func SystemPrompt(l Lang) string
  ```
- Clés du catalogue (toutes présentes dans les deux langues) : `banner.jurisdiction.eu`, `banner.jurisdiction.non_eu`, `banner.jurisdiction.local`, `banner.prefix` (`"juridiction %s"` / `"jurisdiction %s"`), `banner.instructions` (`"instructions : %s"` / `"instructions: %s"`), `warn.non_eu`, `refused.policy`, `refused.user`, `err.prefix` (`"tenon : %v"` / `"tenon: %v"`), `err.no_prompt`, `err.missing_env`, `err.unknown_provider`, `err.unexpected_args`, `audit.ok`, `config.ok`, `sessions.none`, `sessions.header`, `resume.warning_corrupt`, `compaction.done`, `key.saved`, `key.deleted`, `key.prompt`.
- Libellés de juridiction : FR `UE` / `hors UE` / `locale` (inchangés) ; EN `EU` / `non-EU` / `local`.
- Le prompt EN est la traduction fidèle du prompt FR, avec la règle « Réponds en français » remplacée par « Answer in English ».

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestCatalogComplete(t *testing.T)   // chaque clé FR existe en EN et inversement ; mêmes verbes %s/%d dans le même ordre
func TestParse(t *testing.T)             // "en_US.UTF-8" → EN ; "fr_FR" → FR ; "" → FR ; "de" → FR
func TestUnknownKey(t *testing.T)        // T(FR, "nope") == "!nope"
func TestSystemPromptsDiffer(t *testing.T) // FR contient "Réponds en français" ; EN contient "Answer in English" ; les deux citent les 6 outils
func TestEngineDefaultIsFrench(t *testing.T) // engine.DefaultSystemPrompt == i18n.SystemPrompt(i18n.FR) (test dans internal/engine)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/i18n/ ./internal/engine/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.** Le golden de l'engine ne doit pas changer.
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(i18n): catalogues FR/EN et prompts système"`

---

### Task 3: Instructions projet

**Files:**
- Create: `internal/instructions/instructions.go`
- Test: `internal/instructions/instructions_test.go`

**Interfaces:**
- Produces :
  ```go
  const MaxBytes = 32768
  type File struct { Name string; Content string; Truncated bool }
  func Load(root string) ([]File, error)        // AGENTS.md puis TENON.md
  func Render(base string, files []File) string // prompt système + sections
  ```

**Comportement :**
- Fichiers cherchés uniquement à `root` (pas de remontée de dossiers).
- Chargé seulement si `Lstat` indique un fichier régulier (un lien symbolique est ignoré) ; absent = ignoré sans erreur ; autre erreur d'E/S = erreur.
- Au-delà de `MaxBytes`, contenu tronqué à `MaxBytes` octets (sans couper une rune UTF-8) et `Truncated = true`.
- `Render` ajoute, pour chaque fichier : `"\n\n# Instructions du projet (<Name>)\n\n<Content>"`, plus `"\n[… tronqué]"` si `Truncated`. Sans fichier, renvoie `base` inchangé.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestLoadOrder(t *testing.T)       // les deux présents → [AGENTS.md, TENON.md]
func TestMissingIsFine(t *testing.T)   // aucun fichier → nil, nil
func TestSymlinkNotLoaded(t *testing.T) // AGENTS.md → lien vers un fichier hors du projet → non chargé
func TestTruncated(t *testing.T)       // 40 000 octets → len(Content) <= MaxBytes, Truncated, UTF-8 valide
func TestRender(t *testing.T)          // section exacte ; base inchangé sans fichier
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/instructions/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(instructions): chargement de AGENTS.md et TENON.md"`

---

### Task 4: Persistance des sessions

**Files:**
- Create: `internal/session/writer.go`, `internal/session/load.go`, `internal/session/list.go`
- Test: `internal/session/session_test.go`

**Interfaces:**
- Consumes : `event.Event`, `event.Sink`.
- Produces :
  ```go
  func Open(dir, sessionID string, redact func(string) string) (*Writer, error) // dir 0700, fichier 0600, mode append
  func (w *Writer) Write(e event.Event) error // implémente event.Sink
  func (w *Writer) Close() error
  func Load(dir, sessionID string) (events []event.Event, corrupt bool, err error)
  type Summary struct { ID, ProjectRoot, FirstPrompt string; Updated time.Time; Turns int }
  func List(dir, projectRoot string) ([]Summary, error) // plus récente d'abord
  func Latest(dir, projectRoot string) (string, error)  // ID de la plus récente ; ErrNoSession si aucune
  var ErrNoSession error
  ```

**Comportement :**
- `Write` ignore les événements d'une autre session et les `AssistantDelta` (bruit) ; écrit `json.Marshal(e)` dont le `Payload` est passé par `redact` (résultat invalide → erreur, fail closed) ; une ligne par événement, `fsync` à chaque ligne.
- `Load` lit ligne à ligne ; à la première ligne illisible il s'arrête, renvoie les événements valides et `corrupt = true`. Fichier absent → `ErrNoSession`.
- `List` ne garde que les sessions dont le `SessionStarted` a `project_root == projectRoot` ; `FirstPrompt` = premier `UserMessage`, tronqué à 80 runes ; `Turns` = nombre de `TurnCompleted` ; `Updated` = date de modification du fichier.
- L'identifiant de session est validé (`^s_[0-9a-f]{12}$`) dans `Open` et `Load` : rien d'autre ne peut servir de nom de fichier.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestWriteAndLoad(t *testing.T)          // 4 événements (dont 1 AssistantDelta, 1 autre session) → Load en renvoie 2
func TestSessionFileRedacted(t *testing.T)   // redact remplace "Zx8kq" → le fichier ne contient pas "Zx8kq"
func TestLoadStopsAtCorruptLine(t *testing.T) // ligne 3 tronquée → 2 événements, corrupt == true, err == nil
func TestLoadMissing(t *testing.T)           // errors.Is(err, ErrNoSession)
func TestInvalidSessionID(t *testing.T)      // "../x", "s_XYZ" → erreur dans Open et Load
func TestListFiltersProjectAndSorts(t *testing.T) // 3 sessions, 2 du projet → 2, la plus récente d'abord, FirstPrompt et Turns corrects
func TestPermissions(t *testing.T)           // fichier 0600, dossier 0700
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/session/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/session/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(session): persistance JSONL masquée, reprise tolérante, liste"`

---

### Task 5: Reprise d'une session dans l'engine

**Files:**
- Create: `internal/engine/restore.go`
- Modify: `internal/engine/engine.go`, `internal/engine/loop.go`, `internal/event/payloads.go`
- Test: `internal/engine/restore_test.go`

**Interfaces:**
- Consumes : `[]event.Event` de `session.Load`.
- Produces :
  ```go
  func (e *Engine) Restore(sessionID string, events []event.Event) error // avant tout Submit de cette session
  var ErrSessionActive error // Restore sur une session déjà utilisée dans ce processus
  ```
- Payloads : `SessionStartedPayload` gagne `Resumed bool \`json:"resumed,omitempty"\`` et `Instructions []string \`json:"instructions,omitempty"\``. `Deps` gagne `Instructions []string` (noms des fichiers chargés, recopiés dans `SessionStarted`).

**Reconstruction de l'historique** (dans l'ordre des événements) :
- `UserMessage` → message `user` ;
- `AssistantMessage` → message `assistant` (texte + `tool_calls`) ;
- `ToolResult` → message `tool` (`ToolCallID = call_id`, `Content`) ;
- `ContextCompacted` (Task 6) → les `Replaced` premiers messages sont remplacés par le message de résumé ;
- après le dernier événement, et à chaque nouveau `AssistantMessage` ou `UserMessage`, tout appel d'outil resté sans `ToolResult` reçoit un message `tool` synthétique `annulé : session interrompue`.

Le compteur `seq` reprend au plus grand `Seq` lu ; `permN` reprend au plus grand `n` des `PermissionRequested` ; `remembered` repart vide (une autorisation « pour la session » ne survit pas à la reprise). Le premier `Submit` après `Restore` émet `SessionStarted{Resumed:true}`.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestRestoreRebuildsHistory(t *testing.T)    // tour user→assistant(tool_call)→tool→assistant, Restore, Submit :
                                                  // la requête reçue contient ces 4 messages puis le nouveau message user
func TestRestoreClosesOrphanToolCalls(t *testing.T) // AssistantMessage avec 2 appels, 1 seul ToolResult → chaque ToolCallID a son message tool
func TestRestoreContinuesSeq(t *testing.T)        // dernier Seq lu 17 → premier événement émis Seq 18
func TestRestoreEmitsResumed(t *testing.T)        // SessionStarted{Resumed:true}
func TestRestoreForgetsRemember(t *testing.T)     // une écriture déjà autorisée « pour la session » redemande la permission
func TestRestoreActiveSession(t *testing.T)       // Submit puis Restore sur la même session → ErrSessionActive
func TestInstructionsInSessionStarted(t *testing.T) // Deps.Instructions = ["AGENTS.md"] → payload instructions
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/engine/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.** Le golden existant ne doit pas changer (champs `omitempty`).
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/engine/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(engine): reprise d'une session à partir de ses événements"`

---

### Task 6: Compaction du contexte

**Files:**
- Create: `internal/engine/compact.go`
- Modify: `internal/engine/engine.go`, `internal/engine/loop.go`, `internal/event/payloads.go`
- Test: `internal/engine/compact_test.go`

**Interfaces:**
- Produces : `Deps.ContextWindow int` (0 → 32000) et `Deps.CompactionThreshold float64` (0 → 0.8) ; payload `ContextCompactedPayload{Summary string; Replaced int; TokensBefore, TokensAfter int}` (tags snake_case) ; `func estimate(msgs []llm.Message, defs []llm.ToolDef) int` = (somme des runes du contenu, des arguments et des schémas) / 4.

**Algorithme**, avant chaque appel au fournisseur du tour :
1. Si `estimate(prompt système + historique + outils) <= threshold × window`, rien à faire.
2. Choisir la queue conservée : le plus long suffixe de l'historique qui commence par un message `user` et dont l'estimation tient dans `0.3 × window`. S'il n'existe pas, la queue est le dernier message `user` et tout ce qui le suit.
3. Si la partie à résumer est vide, ou si la queue seule dépasse `threshold × window` : émettre `Error{Code:"context_too_large"}` et terminer le tour avec `TurnCompleted{Reason:"context_too_large"}`, sans appel au fournisseur.
4. Sinon, demander un résumé au fournisseur : prompt système fixe `"Résume fidèlement la conversation suivante pour qu'un agent de code puisse la poursuivre : objectifs, décisions, fichiers modifiés, points en suspens. Réponds uniquement par le résumé."`, puis un message `user` contenant la partie à résumer rendue en texte (`<rôle> : <contenu>`, appels d'outils compris), sans outils. Cette requête passe par `Outbound.Check` ; un refus de juridiction suit la même règle que le tour.
5. Remplacer la partie résumée par un message `user` : `"Résumé de la conversation précédente :\n\n<résumé>"`. Émettre `ContextCompacted`.
- Si le fournisseur renvoie `provider.ErrContextLength` sur un appel normal : forcer une compaction (étapes 2 à 5), puis réessayer une seule fois ; un second `ErrContextLength` donne `Error{Code:"provider"}`.
- La reprise (Task 5) rejoue `ContextCompacted` en remplaçant les `Replaced` premiers messages par le message de résumé.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestEstimate(t *testing.T)                  // 400 runes de contenu → 100
func TestNoCompactionUnderThreshold(t *testing.T) // aucune requête de résumé, aucun ContextCompacted
func TestCompactionReplacesOldTurns(t *testing.T) // ContextWindow 1000, historique long : 1 requête de résumé sans outils,
                                                  // puis la requête normale commence par "Résumé de la conversation précédente"
                                                  // et se termine par le dernier message user ; ContextCompacted.Replaced > 0
func TestCompactionKeepsToolPairs(t *testing.T)   // la queue conservée ne commence jamais par un message tool
func TestCompactionCannotFit(t *testing.T)        // un seul message user énorme → Error{context_too_large}, TurnCompleted{context_too_large},
                                                  // aucune requête au fournisseur
func TestContextLengthForcesCompaction(t *testing.T) // 1re réponse ErrContextLength → résumé → nouvelle tentative réussie
func TestCompactionGoesThroughOutbound(t *testing.T) // un secret dans l'historique n'apparaît pas dans la requête de résumé
func TestRestoreReplaysCompaction(t *testing.T)   // événements avec ContextCompacted → historique reconstruit = résumé + queue
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/engine/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/engine/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(engine): compaction automatique et forcée du contexte"`

---

### Task 7: Clés API dans le trousseau système

**Files:**
- Create: `internal/secrets/secrets.go`
- Test: `internal/secrets/secrets_test.go`

**Interfaces:**
- Produces :
  ```go
  const Service = "tenon"
  func Lookup(providerID, envName string, getenv func(string) string) (value, source string, err error)
  // source : "env", "keyring" ou "" (absente) ; env prioritaire ; absence dans le trousseau → ("", "", nil)
  func Set(providerID, value string) error
  func Delete(providerID string) error
  ```
- Dépendance : `go get github.com/zalando/go-keyring@v0.2.6 && go mod vendor` (vérifier la dernière version publiée depuis au moins deux semaines). Les tests utilisent `keyring.MockInit()`.
- Une erreur du trousseau autre que « introuvable » (pas de service D-Bus sous Linux, par exemple) est renvoyée telle quelle par `Lookup` ; l'appelant la traite comme une clé absente avec un avertissement.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestEnvWins(t *testing.T)         // env et trousseau présents → source "env"
func TestKeyringFallback(t *testing.T) // env vide → source "keyring"
func TestAbsent(t *testing.T)          // rien → "", "", nil
func TestSetDelete(t *testing.T)       // Set puis Lookup → valeur ; Delete puis Lookup → absente
func TestEmptyValueRefused(t *testing.T) // Set("mistral", "") → erreur
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/secrets/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(secrets): clés API dans le trousseau système"`

---

### Task 8: Câblage CLI et tests de bout en bout

**Files:**
- Modify: `cmd/tenon/run.go`, `internal/ui/headless/headless.go`
- Test: `cmd/tenon/e2e_test.go`, `internal/ui/headless/headless_test.go`

**Interfaces:**
- Consumes : tout ce qui précède.
- Produces (CLI) :
  - `tenon -p "<prompt>" [--resume <id|last>] [--lang fr|en] [--output text|json] [--allow-non-eu] [--provider <id>]`
  - `tenon sessions` : liste les sessions du projet (`sessions.header`, puis une ligne `<id>  <date AAAA-MM-JJ HH:MM>  <tours>  <premier prompt>`), ou `sessions.none` ; code `0`.
  - `tenon key set <provider>` : lit la clé sur l'entrée standard (sans écho si c'est un terminal, via `golang.org/x/term`), l'enregistre, affiche `key.saved` ; `tenon key delete <provider>` : `key.deleted`. Fournisseur inconnu → code `1`.
  - `headless.Options` gagne `Lang i18n.Lang` ; tous les textes affichés passent par `i18n.T`.

**Câblage :**
1. Langue : `--lang`, sinon `cfg.Lang`.
2. Clé API : `secrets.Lookup` ; une valeur du trousseau est aussi ajoutée au `Redactor` (`AddLiteral`), comme celles de l'environnement. Erreur du trousseau : avertissement sur stderr, clé considérée absente.
3. Prompt système : `instructions.Render(i18n.SystemPrompt(lang), files)` ; `Deps.Instructions` = noms des fichiers ; la bannière affiche `banner.instructions` quand la liste n'est pas vide.
4. Session : `--resume last` → `session.Latest` ; `--resume <id>` → `session.Load` puis `Engine.Restore` ; `corrupt` → avertissement `resume.warning_corrupt` sur stderr. Sans `--resume`, un nouvel ID. Le `session.Writer` est ajouté aux `Sinks` **après** l'audit, avec le même `RedactJSON`.
5. `Deps.ContextWindow = provider.Window()`, `Deps.CompactionThreshold = cfg.CompactionThreshold`. Le headless affiche `compaction.done` sur stderr à chaque `ContextCompacted` (mode texte).

- [ ] **Step 1: Écrire les tests qui échouent** (même harnais e2e que le plan 1)

```go
func TestResumeContinuesConversation(t *testing.T) // run 1 : "je m'appelle Ada" ; run 2 : --resume last "comment je m'appelle ?"
                                                   // → la 2e requête HTTP contient "je m'appelle Ada"
func TestResumeUnknownID(t *testing.T)            // --resume s_000000000000 → code 1
func TestSessionsList(t *testing.T)               // 2 runs → `tenon sessions` affiche 2 lignes, la plus récente d'abord
func TestSessionFileHasNoSecret(t *testing.T)     // prompt avec "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf" → aucun fichier de session ne contient "Zx8kq"
func TestAgentsMdInSystemPrompt(t *testing.T)     // AGENTS.md "Utilise des tabulations." → message system de la requête le contient ;
                                                  // stderr contient "instructions : AGENTS.md"
func TestEnglishBanner(t *testing.T)              // LANG=en_US.UTF-8 → stderr contient "jurisdiction EU" ; message system contient "Answer in English"
func TestLangFlagOverrides(t *testing.T)          // LANG=en_US + --lang fr → "juridiction UE"
func TestCompactionEndToEnd(t *testing.T)         // provider de test avec context_window = 600, 3 runs reprenant la même session
                                                  // → au moins une requête de résumé, stderr contient le texte compaction.done
func TestKeyFromKeyring(t *testing.T)             // keyring.MockInit, clé enregistrée via `tenon key set test` (stdin), TEST_KEY absente
                                                  // → code 0 et en-tête Authorization "Bearer <clé>"
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./cmd/tenon/ ./internal/ui/headless/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** — `go get golang.org/x/term && go mod vendor` (version publiée depuis au moins deux semaines).
- [ ] **Step 4: Vérifier toute la suite** — Run: `GOFLAGS=-mod=vendor go vet ./... && GOFLAGS=-mod=vendor go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(cli): reprise de session, sessions, instructions, langue, trousseau"`
