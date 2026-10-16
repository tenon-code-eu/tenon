package tui

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// panicEngine est un fakeEngine dont Respond panique.
type panicEngine struct{ *fakeEngine }

func (panicEngine) Respond(string, engine.Reply) error { panic("Respond en panique") }

// Une panique pendant Update fait rendre la main à Run avec une erreur ; le
// tour en cours est annulé et Submit a rendu la main avant le retour de Run,
// même sans modèle final.
func TestRunPanicCancelsTurn(t *testing.T) {
	f := newFake()
	f.onSubmit = blockUntilCancel
	inR, inW := io.Pipe()
	defer inW.Close()
	errc := make(chan error, 1)
	go func() {
		errc <- Run(context.Background(), panicEngine{f}, Options{Lang: i18n.FR, SessionID: "s"}, inR, io.Discard)
	}()
	if _, err := inW.Write([]byte("x\r")); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for f.lastCtx() == nil {
		select {
		case <-deadline:
			t.Fatal("Submit non appelé")
		case <-time.After(5 * time.Millisecond):
		}
	}
	f.emit(t, event.PermissionRequested, shellPerm("p1"))
	var err error
	// « n » refuse immédiatement dès que la demande est affichée ; avant, il
	// va dans la saisie.
loop:
	for {
		go func() { _, _ = inW.Write([]byte("n")) }()
		select {
		case err = <-errc:
			break loop
		case <-deadline:
			t.Fatal("Run n'a pas rendu la main après la panique")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err == nil {
		t.Fatal("Run sans erreur après une panique")
	}
	if f.lastCtx().Err() == nil {
		t.Fatal("tour non annulé après la panique")
	}
	select {
	case <-f.returned:
	default:
		t.Fatal("Submit n'a pas rendu la main avant le retour de Run")
	}
}
