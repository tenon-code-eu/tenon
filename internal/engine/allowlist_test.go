package engine

import (
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hostmatch"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

func TestAllowlistBlocksTurn(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.Allowlists = []hostmatch.List{{Name: "global", Hosts: []string{"other.example"}}}
		d.Outbound.Consent(euInfo.ID)
	})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 0 {
		t.Fatalf("provider called %d times", n)
	}
	errs := ofType(evs, event.Error)
	if len(errs) != 1 {
		t.Fatalf("Error x%d, events %v", len(errs), types(evs))
	}
	if p := decode[event.ErrorPayload](t, errs[0]); p.Code != "allowlist" || !strings.Contains(p.Message, "global") {
		t.Fatalf("error payload %+v", p)
	}
	if tc := lastTurn(t, evs); tc.Reason != "blocked" {
		t.Fatalf("reason = %q", tc.Reason)
	}
}
