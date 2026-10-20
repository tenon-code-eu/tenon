package engine

import (
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
)

func testPolicy(host string) *orgpolicy.Policy {
	return &orgpolicy.Policy{
		ID: "acme-dev", Version: 3, KeyID: "ABCDEF0123456789",
		Providers: &orgpolicy.Providers{AllowHosts: []string{host}},
	}
}

func TestPolicyBlocksTurn(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.Policy = testPolicy("other.example")
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
	if p := decode[event.ErrorPayload](t, errs[0]); p.Code != "policy" || !strings.Contains(p.Message, "acme-dev") {
		t.Fatalf("error payload %+v", p)
	}
	if tc := lastTurn(t, evs); tc.Reason != "blocked" {
		t.Fatalf("reason = %q", tc.Reason)
	}
}

func TestSessionStartedPolicy(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.Policy = testPolicy("api.mistral.ai")
	})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	p := decode[event.SessionStartedPayload](t, evs[0])
	if p.Policy == nil || *p.Policy != (event.PolicyRef{ID: "acme-dev", Version: 3, KeyID: "ABCDEF0123456789"}) {
		t.Fatalf("payload %s", evs[0].Payload)
	}
	h = newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	evs, err = h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(evs[0].Payload), "policy") {
		t.Fatalf("payload %s", evs[0].Payload)
	}
}
