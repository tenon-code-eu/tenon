package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/checkpoint"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func esc(h *harness)  { h.send(tea.KeyMsg{Type: tea.KeyEsc}) }
func down(h *harness) { h.send(tea.KeyMsg{Type: tea.KeyDown}) }

func rwFake() *fakeEngine {
	f := newFake()
	f.rwPoints = []checkpoint.Point{
		{Seq: 5, Excerpt: "second", Conversation: true},
		{Seq: 2, Excerpt: "first", Conversation: false},
	}
	f.rwPlan = checkpoint.Plan{
		Point:                 f.rwPoints[0],
		ConversationAvailable: true,
		UntrackedCalls:        2,
		Files: []checkpoint.FilePlan{
			{Path: "/p/a.go", Action: "restore", Status: "ok"},
			{Path: "/p/b.go", Action: "delete", Status: "ok"},
			{Path: "/p/c.go", Action: "restore", Status: "conflict"},
			{Path: "/p/d.go", Action: "restore", Status: "unrestorable"},
			{Path: "/p/e.go", Action: "restore", Status: "noop"},
			{Path: "/elsewhere/f.go", Action: "restore", Status: "ok"},
		},
	}
	return f
}

func rwHarness(t *testing.T, f *fakeEngine) *harness {
	return newHarness(t, f, Options{Lang: i18n.EN, ProjectRoot: "/p"})
}

func view(h *harness) string { return ansi.Strip(h.m.View()) }

func TestRewindEscTwiceOpens(t *testing.T) {
	h := rwHarness(t, rwFake())
	esc(h)
	if h.m.rw != nil {
		t.Fatal("un seul Échap ouvre")
	}
	h.clk = h.clk.Add(900 * time.Millisecond)
	esc(h)
	if h.m.rw == nil {
		t.Fatal("deux Échap en 0,9 s n'ouvrent pas")
	}
}

func TestRewindEscSlowDoesNotOpen(t *testing.T) {
	h := rwHarness(t, rwFake())
	esc(h)
	h.clk = h.clk.Add(2 * time.Second)
	esc(h)
	if h.m.rw != nil {
		t.Fatal("ouvert après 2 s")
	}
}

func TestRewindEscDuringTurnCancels(t *testing.T) {
	f := rwFake()
	f.onSubmit = func(ctx context.Context, _ *fakeEngine) error { <-ctx.Done(); return ctx.Err() }
	h := rwHarness(t, f)
	typeText(h, "go")
	enter(h)
	h.until("tour", func() bool { return h.m.busy() })
	esc(h)
	esc(h)
	if h.m.rw != nil {
		t.Fatal("ouvert pendant un tour")
	}
	h.until("tour annulé", func() bool { return !h.m.busy() })
}

func TestRewindCommandOpensAndCompletes(t *testing.T) {
	h := rwHarness(t, rwFake())
	typeText(h, "/rew")
	tab(h)
	if got := h.m.input.Value(); got != "/rewind " {
		t.Fatalf("complétion %q", got)
	}
	enter(h)
	if h.m.rw == nil || h.m.input.Value() != "" {
		t.Fatalf("rw %v input %q", h.m.rw, h.m.input.Value())
	}
}

func TestRewindNoPoints(t *testing.T) {
	f := rwFake()
	f.rwPoints = nil
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	if h.m.rw != nil || !strings.Contains(notices(h), "nothing to rewind") {
		t.Fatalf("notices %q", notices(h))
	}
}

func TestRewindPointsListAndPreview(t *testing.T) {
	h := rwHarness(t, rwFake())
	typeText(h, "/rewind")
	enter(h)
	v := view(h)
	if !strings.Contains(v, "#5  second") || !strings.Contains(v, "#2  first (code only)") {
		t.Fatalf("liste :\n%s", v)
	}
	enter(h)
	v = view(h)
	for _, want := range []string{
		"restore a.go", "delete b.go", "conflict: modified since c.go", "unrestorable d.go",
		"unchanged e.go", "restore /elsewhere/f.go", "2 shell or MCP calls are not undone",
		"code and conversation", "conversation only", "code only",
	} {
		if !strings.Contains(v, want) {
			t.Fatalf("manque %q :\n%s", want, v)
		}
	}
}

func TestRewindUnavailableModeNotSelectable(t *testing.T) {
	f := rwFake()
	f.rwPlan.ConversationAvailable = false
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	if h.m.rw.mode != "" || !h.m.rw.modes[h.m.rw.mcur].ok || h.m.rw.modes[h.m.rw.mcur].mode != engine.RewindCode {
		t.Fatalf("curseur sur un mode grisé : %+v", h.m.rw)
	}
	down(h)
	down(h)
	if h.m.rw.modes[h.m.rw.mcur].mode != engine.RewindCode {
		t.Fatal("le curseur a quitté le seul mode possible")
	}
}

func TestRewindNothingToDo(t *testing.T) {
	f := rwFake()
	f.rwPlan = checkpoint.Plan{ConversationAvailable: false, Files: []checkpoint.FilePlan{{Path: "/p/e.go", Status: "noop"}}}
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	if h.m.rw != nil || !strings.Contains(notices(h), "nothing to rewind") {
		t.Fatalf("notices %q", notices(h))
	}
}

func TestRewindConflictAnswers(t *testing.T) {
	f := rwFake()
	f.rwPlan.Files = append(f.rwPlan.Files, checkpoint.FilePlan{Path: "/p/g.go", Action: "restore", Status: "conflict"})
	f.rwRes = engine.RewindResult{}
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	enter(h) // mode : code et conversation
	if !strings.Contains(view(h), "overwrite c.go? y/N") {
		t.Fatalf("vue :\n%s", view(h))
	}
	typeText(h, "y")
	if !strings.Contains(view(h), "overwrite g.go? y/N") {
		t.Fatalf("vue :\n%s", view(h))
	}
	enter(h) // défaut : garder
	h.until("rewind appelé", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.rwCalls) == 1 })
	c := f.rwCalls[0]
	if c.seq != 5 || c.mode != engine.RewindBoth || len(c.overwrite) != 1 || c.overwrite[0] != "/p/c.go" {
		t.Fatalf("appel %+v", c)
	}
}

func TestRewindConflictN(t *testing.T) {
	f := rwFake()
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	enter(h)
	typeText(h, "n")
	h.until("rewind appelé", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.rwCalls) == 1 })
	if len(f.rwCalls[0].overwrite) != 0 {
		t.Fatalf("overwrite %v", f.rwCalls[0].overwrite)
	}
}

func TestRewindConversationRestoresPrompt(t *testing.T) {
	f := rwFake()
	f.rwRes = engine.RewindResult{Prompt: "fix the bug"}
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	down(h) // conversation seule : pas de conflit à interroger
	enter(h)
	h.until("fin", func() bool { return h.m.rw == nil })
	if h.m.input.Value() != "fix the bug" {
		t.Fatalf("saisie %q", h.m.input.Value())
	}
	if f.rwCalls[0].mode != engine.RewindConversation {
		t.Fatalf("mode %v", f.rwCalls[0].mode)
	}
}

func TestRewindResultNotice(t *testing.T) {
	f := rwFake()
	f.rwRes = engine.RewindResult{Files: []event.RewoundFile{
		{Path: "/p/a.go", Status: "restored"}, {Path: "/p/b.go", Status: "deleted"},
		{Path: "/p/c.go", Status: "kept"}, {Path: "/p/d.go", Status: "failed", Error: "boom\x1b[31m"},
		{Path: "/p/e.go", Status: "unchanged"},
	}, Prompt: "again"}
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	enter(h)
	typeText(h, "n")
	h.until("fin", func() bool { return h.m.rw == nil })
	n := notices(h)
	if !strings.Contains(n, "rewind: 1 restored · 1 deleted · 1 kept · 1 failed") || !strings.Contains(n, "failed: d.go: boom [31m") {
		t.Fatalf("notices %q", n)
	}
	if strings.ContainsRune(n, 0x1b) {
		t.Fatal("ESC dans l'avis")
	}
}

func TestRewindEngineErrors(t *testing.T) {
	f := rwFake()
	f.rwPlanErr = engine.ErrTurnInProgress
	h := rwHarness(t, f)
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	if h.m.rw != nil || !strings.Contains(notices(h), "a turn is in progress") {
		t.Fatalf("notices %q", notices(h))
	}
	f.rwPlanErr = nil
	f.rwErr = errors.New("disk full")
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	enter(h)
	typeText(h, "n")
	h.until("fin", func() bool { return h.m.rw == nil })
	if !strings.Contains(notices(h), "rewind: disk full") {
		t.Fatalf("notices %q", notices(h))
	}
}

func TestRewindEscBack(t *testing.T) {
	h := rwHarness(t, rwFake())
	typeText(h, "/rewind")
	enter(h)
	enter(h)
	esc(h)
	if h.m.rw == nil || h.m.rw.step != stepPoints {
		t.Fatal("Échap ne revient pas à l'étape 1")
	}
	esc(h)
	if h.m.rw != nil {
		t.Fatal("Échap ne ferme pas")
	}
}

func TestRewindListCleanedAndMasked(t *testing.T) {
	f := rwFake()
	f.rwPoints = []checkpoint.Point{{Seq: 1, Excerpt: "hello \x1b[31mred SECRET123 end", Conversation: true}}
	h := newHarness(t, f, Options{Lang: i18n.EN, ProjectRoot: "/p", Redact: func(s string) string {
		return strings.ReplaceAll(s, "SECRET123", "[REDACTED]")
	}})
	typeText(h, "/rewind")
	enter(h)
	v := h.m.View()
	if strings.ContainsRune(v, 0x1b) && strings.Contains(v, "\x1b[31mred") {
		t.Fatalf("ESC non nettoyé : %q", v)
	}
	if strings.Contains(v, "SECRET123") || !strings.Contains(v, "[REDACTED]") {
		t.Fatalf("secret non masqué :\n%s", v)
	}
}
