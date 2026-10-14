package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/llm"
)

// fakeEngine est un Engine en mémoire : Submit enregistre l'appel, exécute
// onSubmit s'il est défini (qui peut émettre des événements ou bloquer) puis
// rend la main.
type fakeEngine struct {
	ch       chan event.Event
	onSubmit func(ctx context.Context, f *fakeEngine) error

	mu       sync.Mutex
	inputs   []string
	ctxs     []context.Context
	replies  map[string]engine.Reply
	returned chan struct{}
}

func newFake() *fakeEngine {
	return &fakeEngine{ch: make(chan event.Event, 256), replies: map[string]engine.Reply{}, returned: make(chan struct{}, 16)}
}

func (f *fakeEngine) Subscribe() <-chan event.Event { return f.ch }

func (f *fakeEngine) Submit(ctx context.Context, sessionID, input string) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.ctxs = append(f.ctxs, ctx)
	f.mu.Unlock()
	defer func() { f.returned <- struct{}{} }()
	if f.onSubmit != nil {
		return f.onSubmit(ctx, f)
	}
	return nil
}

func (f *fakeEngine) Respond(id string, r engine.Reply) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[id] = r
	return nil
}

func (f *fakeEngine) emit(t *testing.T, typ event.Type, payload any) {
	f.ch <- ev(t, typ, payload)
}

func (f *fakeEngine) submitted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.inputs...)
}

func (f *fakeEngine) lastCtx() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ctxs) == 0 {
		return nil
	}
	return f.ctxs[len(f.ctxs)-1]
}

// harness joue le rôle du runtime Bubble Tea : chaque commande s'exécute dans
// sa goroutine et son message revient par msgs pour être appliqué par Update.
type harness struct {
	t    *testing.T
	m    Model
	msgs chan tea.Msg
	quit bool
}

func newHarness(t *testing.T, f *fakeEngine, opts Options) *harness {
	t.Helper()
	if opts.SessionID == "" {
		opts.SessionID = "s"
	}
	h := &harness{t: t, m: New(f, opts), msgs: make(chan tea.Msg, 64)}
	h.run(h.m.Init())
	return h
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() { h.msgs <- cmd() }()
}

// send applique msg (en dépliant les lots) et lance les commandes produites.
func (h *harness) send(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
		return
	case tea.QuitMsg:
		h.quit = true
		return
	}
	m, cmd := h.m.Update(msg)
	h.m = m.(Model)
	h.run(cmd)
}

// update applique msg et renvoie la commande sans l'exécuter.
func (h *harness) update(msg tea.Msg) tea.Cmd {
	m, cmd := h.m.Update(msg)
	h.m = m.(Model)
	return cmd
}

// until applique les messages des commandes jusqu'à ce que cond soit vraie.
func (h *harness) until(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case msg := <-h.msgs:
			h.send(msg)
		case <-time.After(5 * time.Millisecond):
			// La condition peut dépendre d'une goroutine (Submit) sans message.
		case <-deadline:
			h.t.Fatalf("délai dépassé : %s", what)
		}
	}
}

func typeText(h *harness, s string) {
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func enter(h *harness) { h.send(tea.KeyMsg{Type: tea.KeyEnter}) }

// isQuit exécute cmd (en dépliant les lots) et indique s'il demande de quitter.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if c != nil {
				if _, ok := c().(tea.QuitMsg); ok {
					return true
				}
			}
		}
	}
	return false
}

// blockUntilCancel fait bloquer Submit jusqu'à l'annulation de son contexte.
func blockUntilCancel(ctx context.Context, _ *fakeEngine) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestSubmitOnEnter(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "bonjour")
	enter(h)
	h.until("Submit appelé", func() bool { return len(f.submitted()) == 1 })
	if got := f.submitted()[0]; got != "bonjour" {
		t.Fatalf("Submit(%q)", got)
	}
	if v := h.m.input.Value(); v != "" {
		t.Fatalf("saisie non vidée : %q", v)
	}
}

func TestEnterIgnoredWhileBusy(t *testing.T) {
	f := newFake()
	f.onSubmit = blockUntilCancel
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "un")
	enter(h)
	h.until("Submit appelé", func() bool { return len(f.submitted()) == 1 })
	typeText(h, "deux")
	enter(h)
	if v := h.m.input.Value(); v != "deux" {
		t.Fatalf("saisie modifiée pendant le tour : %q", v)
	}
	// Laisse une chance à un éventuel second Submit.
	time.Sleep(50 * time.Millisecond)
	if n := len(f.submitted()); n != 1 {
		t.Fatalf("%d appels à Submit, attendu 1", n)
	}
	h.m.cancel()
}

func TestAltEnterNewline(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "a")
	h.send(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	typeText(h, "b")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeText(h, "c")
	if v := h.m.input.Value(); v != "a\nb\nc" {
		t.Fatalf("saisie %q", v)
	}
	if n := len(f.submitted()); n != 0 {
		t.Fatalf("Submit appelé %d fois", n)
	}
}

func TestEscCancelsTurn(t *testing.T) {
	f := newFake()
	f.onSubmit = blockUntilCancel
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "long")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })
	h.send(tea.KeyMsg{Type: tea.KeyEsc})
	select {
	case <-f.lastCtx().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("contexte de Submit non annulé")
	}
	h.until("tour terminé", func() bool { return !h.m.busy() })
}

func TestCtrlCQuitWhenIdle(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	if !isQuit(h.update(tea.KeyMsg{Type: tea.KeyCtrlC})) {
		t.Fatal("Ctrl+C hors tour ne quitte pas")
	}
}

func TestDoubleCtrlCQuits(t *testing.T) {
	f := newFake()
	f.onSubmit = blockUntilCancel
	h := newHarness(t, f, Options{Lang: i18n.FR})
	now := time.Unix(1000, 0)
	h.m.now = func() time.Time { return now }
	typeText(h, "long")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })

	if isQuit(h.update(tea.KeyMsg{Type: tea.KeyCtrlC})) {
		t.Fatal("le premier Ctrl+C pendant un tour quitte")
	}
	select {
	case <-f.lastCtx().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("le premier Ctrl+C n'annule pas le tour")
	}
	now = now.Add(1500 * time.Millisecond)
	if !isQuit(h.update(tea.KeyMsg{Type: tea.KeyCtrlC})) {
		t.Fatal("le second Ctrl+C en moins de 2 s ne quitte pas")
	}
}

func TestCtrlCWindowExpires(t *testing.T) {
	f := newFake()
	f.onSubmit = blockUntilCancel
	h := newHarness(t, f, Options{Lang: i18n.FR})
	now := time.Unix(1000, 0)
	h.m.now = func() time.Time { return now }
	typeText(h, "long")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })
	// Submit reste « en cours » côté modèle tant que submitDone n'est pas lu.
	if isQuit(h.update(tea.KeyMsg{Type: tea.KeyCtrlC})) {
		t.Fatal("premier Ctrl+C quitte")
	}
	now = now.Add(3 * time.Second)
	if isQuit(h.update(tea.KeyMsg{Type: tea.KeyCtrlC})) {
		t.Fatal("Ctrl+C après 3 s pendant un tour quitte")
	}
}

func statusOpts(l i18n.Lang) Options {
	return Options{
		Lang:          l,
		SessionID:     "sess-42",
		ContextWindow: 128000,
		Provider: llm.ProviderInfo{
			ID: "mistral", Name: "Mistral AI", Model: "mistral-large",
			Country: "FR", Jurisdiction: llm.EU,
		},
	}
}

func TestStatusBar(t *testing.T) {
	for l, jur := range map[i18n.Lang]string{i18n.FR: "juridiction UE", i18n.EN: "jurisdiction EU"} {
		f := newFake()
		h := newHarness(t, f, statusOpts(l))
		h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
		lines := strings.Split(h.m.View(), "\n")
		status := lines[len(lines)-1]
		for _, want := range []string{"tenon", "mistral-large", "Mistral AI", "FR", jur, "sess-42", "/128000"} {
			if !strings.Contains(status, want) {
				t.Errorf("%s : barre d'état %q sans %q", l, status, want)
			}
		}
	}
}

func TestStatusTokenEstimate(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, statusOpts(i18n.FR))
	h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: strings.Repeat("é", 400)})
	h.until("message appliqué", func() bool { return len(h.m.tr.Items()) == 1 })
	if v := h.m.View(); !strings.Contains(v, "~100/128000") {
		t.Fatalf("estimation absente : %q", v)
	}
}

func TestFastStreamNeverBlocks(t *testing.T) {
	const n = 5000
	f := newFake()
	f.onSubmit = func(ctx context.Context, f *fakeEngine) error {
		f.ch <- ev(t, event.UserMessage, event.UserMessagePayload{Text: "go"})
		for i := 0; i < n; i++ {
			// Envoi bloquant, comme l'engine quand le tampon est plein.
			f.ch <- ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "x"})
		}
		f.ch <- ev(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "end_turn"})
		return nil
	}
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "go")
	enter(h)
	h.until("tous les deltas appliqués et Submit terminé", func() bool {
		if h.m.busy() {
			return false
		}
		for _, it := range h.m.tr.Items() {
			if it.Kind == ItemAssistant && len(it.Text) == n {
				return true
			}
		}
		return false
	})
	select {
	case <-f.returned:
	default:
		t.Fatal("Submit n'a pas rendu la main")
	}
}

func assertWidth(t *testing.T, view string, w int) {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		if lw := lipgloss.Width(line); lw > w {
			t.Fatalf("ligne %d de largeur %d > %d : %q", i, lw, w, line)
		}
	}
}

func TestResize(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, statusOpts(i18n.FR))
	assertWidth(t, h.m.View(), 80) // avant le premier WindowSizeMsg : 80×24
	long := strings.Repeat("y", 300)
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "question " + long})
	f.emit(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "début d'une réponse assez longue pour être repliée "})
	h.until("deltas", func() bool { return len(h.m.tr.Items()) == 2 })

	h.send(tea.WindowSizeMsg{Width: 40, Height: 20})
	v := h.m.View()
	assertWidth(t, v, 40)
	if n := len(strings.Split(v, "\n")); n > 20 {
		t.Fatalf("%d lignes > hauteur 20", n)
	}
	f.emit(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: long + "\tfin"})
	f.emit(t, event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "shell", Args: []byte(`{"command":"` + long + `"}`)})
	h.until("outil", func() bool { return len(h.m.tr.Items()) == 3 })
	assertWidth(t, h.m.View(), 40)

	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	assertWidth(t, h.m.View(), 120)
	h.send(tea.WindowSizeMsg{Width: 5, Height: 2})
	assertWidth(t, h.m.View(), 5)
}

func TestHelpCommand(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN})
	typeText(h, "/help")
	enter(h)
	items := h.m.tr.Items()
	if len(items) != 1 || items[0].Kind != ItemNotice || items[0].Text != i18n.T(i18n.EN, "tui.help") {
		t.Fatalf("items %+v", items)
	}
	if h.m.input.Value() != "" {
		t.Fatal("saisie non vidée")
	}
	if n := len(f.submitted()); n != 0 {
		t.Fatalf("/help envoyé au modèle (%d)", n)
	}
}

func TestQuitCommand(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "/quit")
	if !isQuit(h.update(tea.KeyMsg{Type: tea.KeyEnter})) {
		t.Fatal("/quit ne quitte pas")
	}
	if n := len(f.submitted()); n != 0 {
		t.Fatalf("/quit envoyé au modèle (%d)", n)
	}
}

func TestPermissionRequestNotAnswered(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	f.emit(t, event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "shell"})
	f.emit(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "après"})
	// La lecture continue après la demande de permission.
	h.until("lecture continue", func() bool { return len(h.m.tr.Items()) == 1 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.replies) != 0 {
		t.Fatalf("permission répondue automatiquement : %v", f.replies)
	}
}

func TestSubmitErrorShown(t *testing.T) {
	f := newFake()
	f.onSubmit = func(context.Context, *fakeEngine) error { return engine.ErrAuditFailure }
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "x")
	enter(h)
	h.until("erreur affichée", func() bool {
		for _, it := range h.m.tr.Items() {
			if it.Kind == ItemError {
				return true
			}
		}
		return false
	})
	if h.m.busy() {
		t.Fatal("encore occupé après l'échec de Submit")
	}
}

func TestPageUpScrolls(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	h.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	for i := 0; i < 30; i++ {
		f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "ligne"})
	}
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "DERNIERE"})
	h.until("messages", func() bool { return len(h.m.tr.Items()) == 31 })
	if !strings.Contains(h.m.View(), "DERNIERE") {
		t.Fatal("la vue ne suit pas le bas")
	}
	h.send(tea.KeyMsg{Type: tea.KeyPgUp})
	if strings.Contains(h.m.View(), "DERNIERE") {
		t.Fatal("PgUp ne fait pas défiler")
	}
	h.send(tea.KeyMsg{Type: tea.KeyPgDown})
	if !strings.Contains(h.m.View(), "DERNIERE") {
		t.Fatal("PgDn ne revient pas en bas")
	}
	if h.m.input.Value() != "" {
		t.Fatal("PgUp/PgDn modifient la saisie")
	}
}

func TestControlSequencesStripped(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "a\x1b]0;titre\x07b\x1b[2Jc\td"})
	h.until("message", func() bool { return len(h.m.tr.Items()) == 1 })
	v := h.m.View()
	if strings.Contains(v, "\x1b]0") || strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x07") {
		t.Fatalf("séquence de contrôle transmise au terminal : %q", v)
	}
	if !strings.Contains(v, "a]0;titreb[2Jc    d") {
		t.Fatalf("texte nettoyé inattendu : %q", v)
	}
}

// emitAfterCancel fait émettre à Submit, après l'annulation, plus d'événements
// que le tampon de l'abonnement n'en contient (envois bloquants).
func emitAfterCancel(t *testing.T, n int) func(context.Context, *fakeEngine) error {
	return func(ctx context.Context, f *fakeEngine) error {
		<-ctx.Done()
		for i := 0; i < n; i++ {
			f.ch <- ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "x"})
		}
		f.ch <- ev(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "cancelled"})
		return ctx.Err()
	}
}

func TestShutdownDrainsUntilSubmitReturns(t *testing.T) {
	f := newFake()
	f.onSubmit = emitAfterCancel(t, 600)
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "long")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })
	// Le programme s'est arrêté : plus aucun message n'est traité.
	if err := h.m.shutdown(5 * time.Second); err != nil {
		t.Fatalf("shutdown : %v", err)
	}
	select {
	case <-f.returned:
	default:
		t.Fatal("Submit n'a pas rendu la main")
	}
	if f.lastCtx().Err() == nil {
		t.Fatal("tour non annulé")
	}
}

func TestShutdownIdle(t *testing.T) {
	h := newHarness(t, newFake(), Options{Lang: i18n.FR})
	if err := h.m.shutdown(time.Millisecond); err != nil {
		t.Fatalf("shutdown hors tour : %v", err)
	}
}

func TestShutdownTimeout(t *testing.T) {
	f := newFake()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.onSubmit = func(context.Context, *fakeEngine) error { <-release; return nil }
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "bloqué")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })
	if err := h.m.shutdown(50 * time.Millisecond); !errors.Is(err, ErrTurnNotFinished) {
		t.Fatalf("shutdown = %v, attendu ErrTurnNotFinished", err)
	}
}

func TestSanitizeFormatCharacters(t *testing.T) {
	in := "a\u202Eb\u200Bc\u2066d\uFEFFe\u2028f\u2029g"
	if got := sanitize(in); got != "abcde f g" {
		t.Fatalf("sanitize(%q) = %q", in, got)
	}
}

func TestSubmitFailureRestoresInput(t *testing.T) {
	f := newFake()
	f.onSubmit = func(context.Context, *fakeEngine) error { return engine.ErrAuditFailure }
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "à renvoyer")
	enter(h)
	h.until("tour terminé", func() bool { return len(f.submitted()) == 1 && !h.m.busy() })
	if v := h.m.input.Value(); v != "à renvoyer" {
		t.Fatalf("saisie non restaurée : %q", v)
	}
}

func TestSubmitFailureAfterUserMessageKeepsInputEmpty(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	release := make(chan struct{})
	f.onSubmit = func(context.Context, *fakeEngine) error {
		<-release
		return engine.ErrAuditFailure
	}
	typeText(h, "envoyé")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "envoyé"})
	h.until("UserMessage appliqué", func() bool { return len(h.m.tr.Items()) == 1 })
	close(release)
	h.until("tour terminé", func() bool { return !h.m.busy() })
	if v := h.m.input.Value(); v != "" {
		t.Fatalf("saisie restaurée alors que le message est parti : %q", v)
	}
}
