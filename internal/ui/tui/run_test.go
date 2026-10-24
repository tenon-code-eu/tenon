package tui

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// panicEngine est un fakeEngine dont Respond panique.
type panicEngine struct{ *fakeEngine }

func (panicEngine) Respond(string, engine.Reply) error { panic("Respond en panique") }

type signalWriter struct {
	mu      sync.Mutex
	output  strings.Builder
	written chan struct{}
}

func newSignalWriter() *signalWriter {
	return &signalWriter{written: make(chan struct{}, 1)}
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.output.Write(p)
	w.mu.Unlock()
	select {
	case w.written <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (w *signalWriter) waitFor(t *testing.T, want string) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		w.mu.Lock()
		output := ansi.Strip(w.output.String())
		w.mu.Unlock()
		if strings.Contains(output, want) {
			return
		}
		select {
		case <-w.written:
		case <-timer.C:
			t.Fatalf("output never showed %q", want)
		}
	}
}

// Une panique pendant Update fait rendre la main à Run avec une erreur ; le
// tour en cours est annulé et Submit a rendu la main avant le retour de Run,
// même sans modèle final.
func TestRunPanicCancelsTurn(t *testing.T) {
	f := newFake()
	submitStarted := make(chan context.Context, 1)
	f.onSubmit = func(ctx context.Context, _ *fakeEngine) error {
		submitStarted <- ctx
		<-ctx.Done()
		return ctx.Err()
	}
	inR, inW := io.Pipe()
	defer inW.Close()
	out := newSignalWriter()
	errc := make(chan error, 1)
	go func() {
		errc <- Run(context.Background(), panicEngine{f}, Options{Lang: i18n.FR, SessionID: "s"}, inR, out)
	}()
	if _, err := inW.Write([]byte("x\r")); err != nil {
		t.Fatal(err)
	}
	var turnCtx context.Context
	select {
	case turnCtx = <-submitStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("Submit non appelé")
	}
	f.emit(t, event.PermissionRequested, shellPerm("p1"))
	// N'envoyer le refus qu'après le rendu du panneau : sinon il peut tomber
	// dans le champ de saisie avant que la demande soit appliquée.
	out.waitFor(t, "n : non")
	if _, err := inW.Write([]byte("n")); err != nil {
		t.Fatal(err)
	}
	var err error
	select {
	case err = <-errc:
	case <-time.After(10 * time.Second):
		t.Fatal("Run n'a pas rendu la main après la panique")
	}
	if err == nil {
		t.Fatal("Run sans erreur après une panique")
	}
	if turnCtx.Err() == nil {
		t.Fatal("tour non annulé après la panique")
	}
	select {
	case <-f.returned:
	default:
		t.Fatal("Submit n'a pas rendu la main avant le retour de Run")
	}
}
