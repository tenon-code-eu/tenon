# tenon — Plan 3 : interface interactive (TUI)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `tenon` lancé sans `-p` ouvre une interface plein écran dans le terminal : conversation en streaming, validation des actions au clavier avec aperçu des diffs, barre d'état de transparence permanente, reprise de session.

**Architecture:** Nouveau paquet `internal/ui/tui` construit sur Bubble Tea. Le modèle consomme le flux d'événements de l'engine comme le mode headless (abonnement lu en continu, `Submit` dans une commande Bubble Tea en arrière-plan). La logique de transcription (événements → éléments affichés) et le calcul des diffs sont des fonctions pures testées sans terminal. Un verrou de fichier empêche deux processus d'écrire la même session.

**Tech Stack:** Go 1.26 ; `github.com/charmbracelet/bubbletea` (v1, dernière version stable publiée depuis au moins deux semaines), `github.com/charmbracelet/bubbles` (textarea, viewport) et `github.com/charmbracelet/lipgloss` aux versions compatibles ; `github.com/charmbracelet/x/exp/teatest` pour les tests de parcours. Tout est vendoré.

**Spec:** `docs/superpowers/specs/2026-10-14-tenon-design.md` (§2 TUI, §6.2 réponses « oui une fois / oui pour la session / non », §6.6 barre d'état, §9 Ctrl+C, §10 teatest). Plans précédents : plans 1 et 2 (mergés).

## Global Constraints

- Contraintes globales des plans 1 et 2 en vigueur (vendoring, `GOFLAGS=-mod=vendor`, Linux/macOS, aucun secret en clair dans l'audit, les requêtes ni les fichiers de session, `Outbound.Check` et `Permissions.Check` obligatoires, codes de sortie inchangés en headless).
- Tous les textes de la TUI passent par `i18n.T` (FR et EN).
- La TUI lit l'abonnement de l'engine en continu, y compris pendant une demande de permission : l'engine ne doit jamais se bloquer sur un tampon plein.
- Une demande de permission bloque la saisie tant qu'elle n'a pas de réponse ; aucune action n'est autorisée par défaut (Échap = refus).
- Touches : `Entrée` envoie ; `Alt+Entrée` et `Ctrl+J` insèrent un retour à la ligne ; `Échap` annule le tour en cours ; `Ctrl+C` annule le tour en cours, ou quitte s'il n'y en a pas ; un second `Ctrl+C` quitte toujours ; `PgPréc`/`PgSuiv` font défiler la conversation.
- Réponses à une permission d'outil : FR `o` (oui une fois), `s` (oui pour la session), `n` ou `Échap` (non) ; EN `y`, `a` (always for this session), `n` ou `Esc`. Juridiction : uniquement oui une fois / non.
- Aperçu de diff : 400 lignes maximum, au-delà « … (N lignes de plus) ».
- `tenon` sans `-p` alors que l'entrée ou la sortie standard n'est pas un terminal : erreur `err.no_tty`, code 1 (comportement scriptable inchangé : `-p` reste le mode headless).

## Review Focus

1. **Deux `tenon` ouverts sur la même session** (`--resume last` dans deux terminaux) : le second refuse de démarrer avec un message clair, aucune ligne entrelacée (Task 1 : `TestSecondWriterRefused`).
2. **Le modèle demande une permission pendant que l'utilisateur tape** : la saisie en cours est conservée, la demande s'affiche, la réponse part, la saisie revient intacte (Task 5 : `TestPermissionKeepsDraft`).
3. **Flux d'événements très rapide** (milliers de deltas) : l'engine n'est jamais bloqué, l'affichage reste cohérent (Task 4 : `TestFastStreamNeverBlocks`).
4. **Écriture d'un fichier binaire ou énorme** : l'aperçu de diff ne plante pas et reste borné (Task 2 : `TestBinaryAndHuge`).
5. **Fenêtre redimensionnée en cours de streaming** : pas de panique, le texte se replie à la nouvelle largeur (Task 4 : `TestResize`).

---

### Task 1: Verrou exclusif sur les fichiers de session

**Files:**
- Modify: `internal/session/writer.go`
- Test: `internal/session/session_test.go`

**Interfaces:**
- Produces : `var ErrLocked error` ; `session.Open` prend un `flock(LOCK_EX|LOCK_NB)` sur `<id>.jsonl.lock` (créé `0600`) avant la réparation et l'ouverture, et le garde jusqu'à `Close`. Verrou déjà pris → `ErrLocked`.
- `cmd/tenon` : `ErrLocked` affiché via une nouvelle clé `err.session_locked` (FR « session %s déjà ouverte dans un autre terminal », EN « session %s is already open in another terminal »), code 1.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestSecondWriterRefused(t *testing.T) // Open deux fois la même session → 2e : errors.Is(err, ErrLocked) ; après Close du 1er, Open réussit
func TestLockFilePermissions(t *testing.T) // <id>.jsonl.lock en 0600
func TestDifferentSessionsIndependent(t *testing.T) // deux sessions différentes s'ouvrent en parallèle
```
Plus, dans `cmd/tenon/e2e_test.go` : `TestResumeLockedSession` — un `session.Open` tenu par le test, puis `--resume <id> -p x` → code 1, stderr contient le message `err.session_locked`, 0 requête HTTP.

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/session/ ./cmd/tenon/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** (`syscall.Flock`, comme `internal/audit`).
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/session/ ./cmd/tenon/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(session): verrou exclusif par session"`

---

### Task 2: Calcul des diffs

**Files:**
- Create: `internal/ui/tui/diff/diff.go`
- Test: `internal/ui/tui/diff/diff_test.go`

**Interfaces:**
- Produces :
  ```go
  type Kind int // Same, Add, Del
  type Line struct { Kind Kind; Text string }
  const MaxLines = 400
  func Lines(before, after string) []Line            // diff ligne à ligne (LCS), sans contexte limité
  func Unified(before, after string, context int) []Line // garde `context` lignes autour des changements, insère Line{Kind: Same, Text: "…"} entre deux blocs
  func Preview(tool string, args json.RawMessage, root string) (lines []Line, more int, err error)
  ```
- `Preview` : `write_file` → `Unified(contenu actuel ou "", content, 3)` ; `edit_file` → `Unified(old_string, new_string, 3)` (avec `replace_all`, une seule occurrence montrée suffit) ; autres outils → `nil, 0, nil`. Chemin relatif résolu depuis `root`. Fichier binaire (octet NUL dans les 8000 premiers octets, avant ou après) → une seule ligne `Line{Same, "(fichier binaire)"}`. Résultat tronqué à `MaxLines`, `more` = lignes omises.
- Algorithme : LCS en programmation dynamique sur les lignes, avec un garde-fou : au-delà de 2000 lignes de part ou d'autre, renvoyer la suppression complète puis l'ajout complet (pas de LCS quadratique).

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestLinesSimple(t *testing.T)   // "a\nb\nc" → "a\nX\nc" : Same a, Del b, Add X, Same c
func TestUnifiedContext(t *testing.T) // 20 lignes, 1 changement ligne 10, context 3 → 7 lignes + "…" aux bords coupés
func TestPreviewWriteNewFile(t *testing.T) // fichier absent → toutes les lignes en Add
func TestPreviewEdit(t *testing.T)
func TestBinaryAndHuge(t *testing.T)  // contenu binaire → "(fichier binaire)" ; 5000 lignes modifiées → len(lines) == MaxLines, more > 0, sans délai notable (< 1 s)
func TestPreviewOtherTool(t *testing.T) // shell → nil
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/ui/tui/diff/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** (bibliothèque standard uniquement).
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(tui): calcul des diffs pour l'aperçu des écritures"`

---

### Task 3: Transcription des événements

**Files:**
- Create: `internal/ui/tui/transcript.go`
- Test: `internal/ui/tui/transcript_test.go`

**Interfaces:**
- Produces (logique pure, sans Bubble Tea) :
  ```go
  type ItemKind int // ItemBanner, ItemUser, ItemAssistant, ItemTool, ItemNotice, ItemError
  type Item struct {
      Kind      ItemKind
      Text      string // texte affiché (assistant : accumulé pendant le streaming)
      Tool      string // ItemTool
      Detail    string // ItemTool : commande ou chemins ; résultat abrégé (première ligne, 120 runes max)
      Status    string // ItemTool : "pending", "ok", "error", "denied"
      Streaming bool   // ItemAssistant en cours
  }
  type Transcript struct{ /* … */ }
  func NewTranscript(l i18n.Lang) *Transcript
  func (t *Transcript) Apply(ev event.Event)   // met à jour Items
  func (t *Transcript) Items() []Item
  func (t *Transcript) Busy() bool              // un tour est en cours (entre UserMessage et TurnCompleted)
  ```
- Règles : `SessionStarted` → `ItemBanner` (même texte que la bannière headless, `headless.Banner`, plus « (reprise) » / « (resumed) » si `Resumed`, plus la liste des instructions) ; `UserMessage` → `ItemUser` ; `AssistantDelta` → ajoute au dernier `ItemAssistant` en streaming, ou en crée un ; `AssistantMessage` → remplace le texte du dernier assistant en streaming par le texte final et termine le streaming (crée l'item s'il n'existe pas) ; `ToolCallRequested` → `ItemTool{Status:"pending"}` ; `ToolResult` → met à jour l'item de même `call_id` (`denied` si `Denied != ""`, `error` si `IsError`, sinon `ok`) ; `JurisdictionWarning`, `ContextCompacted`, `RedactionApplied` → `ItemNotice` (clés i18n existantes ou nouvelles) ; `Error` → `ItemError` ; `TurnCompleted` → fin de tour (raison autre que `done` → `ItemNotice` avec la raison traduite). Les autres types sont ignorés.
- Nouvelles clés i18n : `tui.resumed`, `tui.redaction` (« %d valeur(s) masquée(s) »), `tui.turn_reason.max_iterations`, `tui.turn_reason.incomplete`, `tui.turn_reason.blocked`, `tui.turn_reason.cancelled`, `tui.turn_reason.context_too_large`.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestBannerAndUser(t *testing.T)
func TestStreamingThenFinal(t *testing.T)   // 3 deltas puis AssistantMessage → un seul item, texte final, Streaming false
func TestToolLifecycle(t *testing.T)        // pending → ok ; pending → denied (Denied "policy") ; pending → error
func TestTurnReasons(t *testing.T)          // cancelled → notice traduite ; done → aucune notice ; Busy() suit le tour
func TestEnglishLabels(t *testing.T)
func TestUnknownEventIgnored(t *testing.T)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/ui/tui/ ./internal/i18n/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(tui): transcription des événements en éléments affichables"`

---

### Task 4: Application Bubble Tea (conversation, saisie, barre d'état)

**Files:**
- Create: `internal/ui/tui/model.go`, `internal/ui/tui/view.go`, `internal/ui/tui/keys.go`, `internal/ui/tui/engine.go`
- Test: `internal/ui/tui/model_test.go`

**Interfaces:**
- Produces :
  ```go
  type Engine interface {
      Subscribe() <-chan event.Event
      Submit(ctx context.Context, sessionID, input string) error
      Respond(permissionID string, r engine.Reply) error
  }
  type Options struct {
      Lang          i18n.Lang
      SessionID     string
      Provider      llm.ProviderInfo
      ContextWindow int
      ProjectRoot   string
  }
  func New(eng Engine, opts Options) Model // Model implémente tea.Model
  func Run(ctx context.Context, eng Engine, opts Options, in io.Reader, out io.Writer) error // tea.NewProgram avec AltScreen
  ```
- Comportement :
  - `Init` lance une commande qui lit **un** événement de l'abonnement et le renvoie comme message ; à chaque événement reçu, `Update` l'applique au `Transcript` puis relance la lecture. La lecture n'est jamais suspendue.
  - `Entrée` sur une saisie non vide et hors tour : ajoute la saisie, lance `Submit` dans une commande avec un `context` annulable dont la fonction `cancel` est gardée dans le modèle ; vide la saisie. Pendant un tour, `Entrée` ne fait rien (la saisie reste éditable).
  - `Échap` ou premier `Ctrl+C` pendant un tour : `cancel()`. `Ctrl+C` hors tour : quitter. Second `Ctrl+C` en moins de 2 s : quitter.
  - Commandes texte : `/quit` quitte ; `/help` ajoute un `ItemNotice` listant les touches (clé `tui.help`).
  - Barre d'état (une ligne, en bas) : `tenon · <modèle> · <fournisseur> · <pays> · <juridiction> · <session> · ~<jetons estimés>/<fenêtre>` ; l'estimation est la somme des runes des items / 4. Couleur de la juridiction : `eu` vert, `local` bleu, `non-eu` orange.
  - Vue : viewport défilant (items rendus avec lipgloss, repliés à la largeur), zone de saisie (textarea, 3 lignes), barre d'état. `tea.WindowSizeMsg` met à jour les largeurs.
- Nouvelles clés i18n : `tui.help`, `tui.placeholder`, `tui.status_tokens`, `tui.busy`, `tui.tool.pending`, `tui.tool.ok`, `tui.tool.error`, `tui.tool.denied`.

- [ ] **Step 1: Écrire les tests qui échouent** (faux `Engine` en mémoire ; appels directs à `Update`/`View`)

```go
func TestSubmitOnEnter(t *testing.T)        // saisie "bonjour" + Entrée → Submit appelé avec "bonjour", saisie vidée
func TestEnterIgnoredWhileBusy(t *testing.T)
func TestAltEnterNewline(t *testing.T)
func TestEscCancelsTurn(t *testing.T)       // le context passé à Submit est annulé
func TestCtrlCQuitWhenIdle(t *testing.T)    // renvoie tea.Quit
func TestDoubleCtrlCQuits(t *testing.T)     // pendant un tour : 1er annule, 2e quitte
func TestStatusBar(t *testing.T)            // contient modèle, pays, "juridiction UE" (FR) / "jurisdiction EU" (EN), id de session
func TestFastStreamNeverBlocks(t *testing.T) // 5000 AssistantDelta dans l'abonnement → tous appliqués, Submit rend la main
func TestResize(t *testing.T)               // WindowSizeMsg 40 puis 120 colonnes pendant un streaming → aucune panique, aucune ligne > largeur
func TestHelpCommand(t *testing.T)
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/ui/tui/` — Expected: FAIL.
- [ ] **Step 3: Implémenter** — `go get` des modules Charm (versions publiées depuis au moins deux semaines), `go mod vendor`.
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/ui/tui/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(tui): conversation en streaming, saisie et barre d'état"`

---

### Task 5: Demandes de permission et aperçu des diffs

**Files:**
- Create: `internal/ui/tui/permission.go`
- Modify: `internal/ui/tui/model.go`, `internal/ui/tui/view.go`, `internal/ui/tui/transcript.go`
- Test: `internal/ui/tui/permission_test.go`

**Interfaces:**
- Consumes : `diff.Preview`, `event.PermissionRequestedPayload`, `engine.Reply`.
- Comportement :
  - À la réception d'un `PermissionRequested`, le modèle passe en mode « demande » : la zone de saisie est remplacée par un panneau (outil, commande ou chemins, et pour `write_file`/`edit_file` l'aperçu `diff.Preview` coloré : ajouts en vert, suppressions en rouge, « … (N lignes de plus) ») ; la saisie en cours est conservée et réapparaît après la réponse.
  - Le panneau affiche les touches de la langue courante. `o`/`y` → `Respond(id, Reply{Allow: true})` ; `s`/`a` → `Reply{Allow: true, Remember: true}` ; `n` ou `Échap` → `Reply{Allow: false}`. Pour `Kind == "jurisdiction"` : seulement oui une fois / non, avec le nom du fournisseur, son pays et le libellé « hors UE ».
  - Toute autre touche est ignorée. `Ctrl+C` pendant une demande : refus puis annulation du tour.
  - Les arguments pour l'aperçu viennent du dernier `ToolCallRequested` de même outil et de mêmes chemins (le modèle garde les arguments des appels en attente, par `call_id`).
  - Plusieurs demandes en attente : traitées une par une, dans l'ordre.
- Nouvelles clés i18n : `tui.perm.title`, `tui.perm.keys_tool` (FR « o : oui une fois · s : oui pour la session · n : non », EN « y: yes once · a: always for this session · n: no »), `tui.perm.keys_jurisdiction`, `tui.perm.jurisdiction`, `tui.perm.more_lines`, `tui.perm.binary`.

- [ ] **Step 1: Écrire les tests qui échouent**

```go
func TestPermissionYesOnce(t *testing.T)     // touche o → Respond(id, {Allow:true, Remember:false})
func TestPermissionSession(t *testing.T)     // s → {true, true}
func TestPermissionNoAndEsc(t *testing.T)
func TestEnglishKeys(t *testing.T)           // y / a / n en EN ; o ignoré en EN
func TestOtherKeysIgnored(t *testing.T)
func TestJurisdictionNoRemember(t *testing.T) // s/a ignorés pour une juridiction
func TestDiffShownForWrite(t *testing.T)     // write_file sur un fichier existant → la vue contient les lignes - et +
func TestPermissionKeepsDraft(t *testing.T)  // saisie "brouillon", demande, réponse → saisie "brouillon"
func TestQueuedPermissions(t *testing.T)     // 2 demandes → 2 réponses dans l'ordre
func TestCtrlCDuringPermission(t *testing.T) // refus envoyé puis tour annulé
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/ui/tui/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./internal/ui/tui/` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(tui): validation des actions au clavier avec aperçu des diffs"`

---

### Task 6: Câblage CLI et parcours de bout en bout

**Files:**
- Modify: `cmd/tenon/run.go`, `cmd/tenon/main.go`, `internal/i18n/catalog.go`
- Test: `cmd/tenon/tui_e2e_test.go`

**Interfaces:**
- Produces (CLI) : `tenon [--resume <id|last>] [--lang fr|en] [--provider <id>] [--allow-non-eu]` sans `-p` → TUI. `--output` est refusé sans `-p` (`err.output_requires_p`). Le message `err.no_prompt` disparaît des chemins sans `-p` ; il reste pour `--resume` utilisé avec une sous-commande.
- `run()` reçoit une fonction `isTerminal func() bool` injectable (vraie implémentation : `term.IsTerminal` sur stdin **et** stdout) ; faux → `err.no_tty`, code 1.
- Le démarrage réutilise `startEngine` (mêmes sinks audit + session, même Redactor, mêmes clés, même reprise et même vérification de projet) ; la TUI reçoit l'`*engine.Engine` via l'interface `tui.Engine`.
- `--allow-non-eu` en TUI : consentement donné d'avance (comme en headless) ; sans lui, la TUI pose la question de juridiction.
- Code de sortie de la TUI : `0` à la sortie normale, `1` si `tui.Run` renvoie une erreur.
- Nouvelles clés i18n : `err.no_tty`, `err.output_requires_p`, `flag.resume` mis à jour pour mentionner la TUI.

- [ ] **Step 1: Écrire les tests qui échouent** (teatest, faux serveur compatible OpenAI du harnais e2e)

```go
func TestNoTTYRefused(t *testing.T)          // isTerminal faux, sans -p → code 1, stderr err.no_tty
func TestOutputRequiresPrompt(t *testing.T)  // --output json sans -p → code 1
func TestTUIConversation(t *testing.T)       // teatest : taper "bonjour", Entrée → la sortie finale contient la réponse du faux serveur
                                             // et la barre d'état "juridiction UE" ; /quit → code 0 ; le fichier de session existe
func TestTUIPermissionFlow(t *testing.T)     // le faux serveur demande write_file ; teatest envoie "o" → fichier écrit ;
                                             // second run : "n" → fichier absent, résultat "denied" affiché
func TestTUIResume(t *testing.T)             // run headless "je m'appelle Ada", puis TUI --resume last → la bannière indique la reprise
                                             // et la requête suivante contient "je m'appelle Ada"
func TestTUIAuditVerifies(t *testing.T)      // après un parcours TUI, `tenon audit verify` → code 0
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./cmd/tenon/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier toute la suite** — Run: `GOFLAGS=-mod=vendor go vet ./... && GOFLAGS=-mod=vendor go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Essai manuel** (si un fournisseur est disponible) — Run: `go build -o tenon ./cmd/tenon && ./tenon --provider ollama` — Expected: TUI plein écran, barre d'état « juridiction locale », `/quit` rend la main.
- [ ] **Step 6: Commit** — `git commit -m "feat(cli): tenon sans -p ouvre l'interface interactive"`
