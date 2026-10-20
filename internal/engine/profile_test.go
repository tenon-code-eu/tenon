package engine

import (
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/profile"
)

func TestProfileBlocksTurn(t *testing.T) {
	h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.Profile = profile.EUOnly
		d.Outbound.SetProfile(profile.EUOnly)
		// Un consentement déjà donné ne contourne pas le profil.
		d.Outbound.Consent(nonEUInfo.ID)
	})
	evs, err := h.submit(t, "s_1", "salut", func(p event.PermissionRequestedPayload) Reply {
		t.Errorf("unexpected permission request %+v", p)
		return Reply{}
	})
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
	if p := decode[event.ErrorPayload](t, errs[0]); p.Code != "profile" || !strings.Contains(p.Message, "eu-only") {
		t.Fatalf("error payload %+v", p)
	}
	if tc := lastTurn(t, evs); tc.Reason != "blocked" {
		t.Fatalf("reason = %q", tc.Reason)
	}
	if len(ofType(evs, event.JurisdictionWarning)) != 0 {
		t.Fatal("no jurisdiction warning expected")
	}
}

func TestSessionStartedProfile(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.Profile = profile.EUOnly
		d.Outbound.SetProfile(profile.EUOnly)
	})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := decode[event.SessionStartedPayload](t, evs[0]); p.Profile != "eu-only" {
		t.Fatalf("payload %s", evs[0].Payload)
	}
	// Sans profil, le champ est omis du JSON.
	h = newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	evs, err = h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(evs[0].Payload), "profile") {
		t.Fatalf("payload %s", evs[0].Payload)
	}
}

// New transmet Deps.Profile à Outbound : la CLI ne fixe que Deps.Profile.
func TestNewAppliesProfileToOutbound(t *testing.T) {
	h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.Profile = profile.EUOnly
		d.Outbound.Consent(nonEUInfo.ID)
	})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 0 {
		t.Fatalf("provider called %d times", n)
	}
	if errs := ofType(evs, event.Error); len(errs) != 1 || decode[event.ErrorPayload](t, errs[0]).Code != "profile" {
		t.Fatalf("events %v", types(evs))
	}
}
