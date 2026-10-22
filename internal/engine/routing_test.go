package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/classify"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hostmatch"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/routing"
)

// Fournisseurs des tests de routage : h.prov (euInfo, « remote ») est le
// fournisseur courant ; remote2 et remote3 sont des replis, local le
// fournisseur local.
var (
	remote2Info = llm.ProviderInfo{ID: "remote2", BaseURL: "https://api.remote2.example/v1", Model: "m2", Country: "DE", Jurisdiction: llm.EU}
	remote3Info = llm.ProviderInfo{ID: "remote3", BaseURL: "https://api.remote3.example/v1", Model: "m3", Country: "NL", Jurisdiction: llm.EU}
	localInfo   = llm.ProviderInfo{ID: "local", BaseURL: "http://127.0.0.1:11434/v1", Model: "llama", Jurisdiction: llm.Local}
)

func unavailable() []llm.Chunk { return []llm.Chunk{{Done: true, Err: provider.ErrUnavailable}} }

func fake(info llm.ProviderInfo, script ...[]llm.Chunk) *fakeProvider {
	return &fakeProvider{info: info, script: script}
}

// routeOpt installe un routeur : courant = d.Provider, local (nil : aucun,
// window : sa fenêtre de contexte), replis dans l'ordre.
func routeOpt(local *fakeProvider, window int, fallback ...*fakeProvider) func(*Deps) {
	return func(d *Deps) {
		cur := routing.Candidate{ID: d.Provider.Info().ID, Provider: d.Provider}
		var lc *routing.Candidate
		if local != nil {
			lc = &routing.Candidate{ID: local.info.ID, Provider: local, Window: window}
		}
		var fb []routing.Candidate
		for _, f := range fallback {
			fb = append(fb, routing.Candidate{ID: f.info.ID, Provider: f})
		}
		d.Router = routing.New(cur, lc, fb)
	}
}

func selected(t *testing.T, evs []event.Event) []event.ProviderSelectedPayload {
	t.Helper()
	var out []event.ProviderSelectedPayload
	for _, e := range ofType(evs, event.ProviderSelected) {
		out = append(out, decode[event.ProviderSelectedPayload](t, e))
	}
	return out
}

// noRemoteAfterClassified vérifie qu'aucun ProviderSelected vers un
// fournisseur non local ne suit SessionClassified.
func noRemoteAfterClassified(t *testing.T, evs []event.Event) {
	t.Helper()
	seen := false
	for _, e := range evs {
		switch e.Type {
		case event.SessionClassified:
			seen = true
		case event.ProviderSelected:
			if p := decode[event.ProviderSelectedPayload](t, e); seen && p.Provider.Jurisdiction != llm.Local {
				t.Fatalf("ProviderSelected %+v après SessionClassified", p)
			}
		}
	}
}

func TestSensitiveRoutesLocal(t *testing.T) {
	local := fake(localInfo,
		text("RÉSUMÉ"), // compaction (fenêtre du local)
		call("a1", "agent", agentCall),
		text("sous-agent ok"),
		text("fin"))
	h := newAgentEngine(t, [][]llm.Chunk{call("c1", "read_file", `{"path":"rh/paie.csv"}`)}, nil,
		routeOpt(local, 1000), func(d *Deps) { d.Classify = classify.Rules{Paths: []string{"rh/**"}} })
	writeProjectFiles(t, h.root)
	seed(t, h, "s_1", longHistory()...)
	evs, err := h.submit(t, "s_1", "lis la paie", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 1 {
		t.Fatalf("remote : %d requêtes, attendu 1", n)
	}
	reqs := local.reqs()
	if len(reqs) != 4 {
		t.Fatalf("local : %d requêtes, events %v", len(reqs), types(evs))
	}
	if !strings.HasPrefix(reqs[0].Messages[0].Content, "Faithfully summarize") || reqs[0].Model != "llama" {
		t.Fatalf("requête de résumé %s", mustJSON(reqs[0]))
	}
	if n := len(ofType(evs, event.ContextCompacted)); n != 1 {
		t.Fatalf("%d ContextCompacted", n)
	}
	if n := len(ofType(evs, event.SubagentCompleted)); n != 1 {
		t.Fatalf("%d SubagentCompleted", n)
	}
	if len(classified(t, evs)) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	var sens int
	for _, p := range selected(t, evs) {
		if p.Reason == "sensitive" {
			sens++
			if p.Provider.ID != "local" {
				t.Fatalf("ProviderSelected %+v", p)
			}
		}
	}
	if sens != 1 {
		t.Fatalf("%d ProviderSelected{sensitive} : %+v", sens, selected(t, evs))
	}
	noRemoteAfterClassified(t, evs)
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestSensitiveLocalFailsNoFallback(t *testing.T) {
	local := fake(localInfo, unavailable())
	remote2 := fake(remote2Info, text("non"))
	h := newHarness(t, euInfo, [][]llm.Chunk{text("non")}, routeOpt(local, 0, remote2))
	evs, err := h.submit(t, "s_1", "note EU RESTRICTED", nil)
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("err = %v, events %v", err, types(evs))
	}
	if n := len(local.reqs()); n != 1 {
		t.Fatalf("local : %d requêtes", n)
	}
	if n, m := len(h.prov.reqs()), len(remote2.reqs()); n+m != 0 {
		t.Fatalf("remote %d, remote2 %d requêtes", n, m)
	}
	noRemoteAfterClassified(t, evs)
}

func TestFallbackOn503(t *testing.T) {
	remote2 := fake(remote2Info, text("ok"))
	h := newHarness(t, euInfo, [][]llm.Chunk{unavailable()}, routeOpt(nil, 0, remote2))
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	if n := len(remote2.reqs()); n != 1 {
		t.Fatalf("remote2 : %d requêtes", n)
	}
	if got := remote2.reqs()[0].Model; got != "m2" {
		t.Fatalf("modèle %q", got)
	}
	ps := selected(t, evs)
	if len(ps) != 2 || ps[0].Provider.ID != "mistral" || ps[0].Reason != "default" || ps[0].Error != "" ||
		ps[1].Provider.ID != "remote2" || ps[1].Reason != "fallback" || ps[1].Error != "provider unavailable" {
		t.Fatalf("ProviderSelected %+v", ps)
	}
	if n := len(ofType(evs, event.Error)); n != 0 {
		t.Fatalf("%d Error", n)
	}
	// Envoi suivant : de nouveau le fournisseur courant, changement émis.
	h.prov.script = [][]llm.Chunk{text("ok")}
	evs, err = h.submit(t, "s_1", "encore", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ps := selected(t, evs); len(ps) != 1 || ps[0].Provider.ID != "mistral" || ps[0].Reason != "default" {
		t.Fatalf("ProviderSelected %+v", ps)
	}
}

func TestNoFallbackAfterFirstChunk(t *testing.T) {
	remote2 := fake(remote2Info, text("ok"))
	h := newHarness(t, euInfo, [][]llm.Chunk{{{Text: "a"}, {Done: true, Err: provider.ErrUnavailable}}}, routeOpt(nil, 0, remote2))
	evs, err := h.submit(t, "s_1", "salut", nil)
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("err = %v, events %v", err, types(evs))
	}
	if n := len(remote2.reqs()); n != 0 {
		t.Fatalf("remote2 : %d requêtes", n)
	}
}

func TestNoFallbackOnAuth(t *testing.T) {
	remote2 := fake(remote2Info, text("ok"))
	h := newHarness(t, euInfo, [][]llm.Chunk{{{Done: true, Err: provider.ErrAuth}}}, routeOpt(nil, 0, remote2))
	evs, err := h.submit(t, "s_1", "salut", nil)
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, events %v", err, types(evs))
	}
	if n := len(remote2.reqs()); n != 0 {
		t.Fatalf("remote2 : %d requêtes", n)
	}
}

func TestFallbackGuardSkips(t *testing.T) {
	allow := func(hosts ...string) func(*Deps) {
		return func(d *Deps) { d.Allowlists = []hostmatch.List{{Name: "global", Hosts: hosts}} }
	}
	t.Run("skip", func(t *testing.T) {
		remote2 := fake(remote2Info, text("non"))
		remote3 := fake(remote3Info, text("ok"))
		h := newHarness(t, euInfo, [][]llm.Chunk{unavailable()}, routeOpt(nil, 0, remote2, remote3),
			allow("api.mistral.ai", "api.remote3.example"))
		evs, err := h.submit(t, "s_1", "salut", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(remote2.reqs()); n != 0 {
			t.Fatalf("remote2 : %d requêtes", n)
		}
		if n := len(remote3.reqs()); n != 1 {
			t.Fatalf("remote3 : %d requêtes", n)
		}
		errs := ofType(evs, event.Error)
		if len(errs) != 1 || decode[event.ErrorPayload](t, errs[0]).Code != "allowlist" {
			t.Fatalf("events %v", types(evs))
		}
		ps := selected(t, evs)
		if len(ps) != 2 || ps[1].Provider.ID != "remote3" || ps[1].Reason != "fallback" || ps[1].Error != "provider unavailable" {
			t.Fatalf("ProviderSelected %+v", ps)
		}
		if tc := lastTurn(t, evs); tc.Reason != "done" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
	})
	t.Run("none left", func(t *testing.T) {
		remote2 := fake(remote2Info, text("non"))
		h := newHarness(t, euInfo, [][]llm.Chunk{text("non")}, routeOpt(nil, 0, remote2), allow("other.example"))
		evs, err := h.submit(t, "s_1", "salut", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n, m := len(h.prov.reqs()), len(remote2.reqs()); n+m != 0 {
			t.Fatalf("remote %d, remote2 %d requêtes", n, m)
		}
		if n := len(ofType(evs, event.Error)); n != 2 {
			t.Fatalf("events %v", types(evs))
		}
		if n := len(selected(t, evs)); n != 0 {
			t.Fatalf("%d ProviderSelected", n)
		}
		if tc := lastTurn(t, evs); tc.Reason != "blocked" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
	})
}

func TestOutboundScan(t *testing.T) {
	marked := []llm.Message{
		{Role: llm.User, Content: "résume la note"},
		{Role: llm.Assistant, Content: "La note porte la mention EU RESTRICTED."},
	}
	t.Run("local", func(t *testing.T) {
		local := fake(localInfo, text("ok"))
		h := newHarness(t, euInfo, [][]llm.Chunk{text("non")}, routeOpt(local, 0))
		seed(t, h, "s_1", marked...)
		evs, err := h.submit(t, "s_1", "suite", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(h.prov.reqs()); n != 0 {
			t.Fatalf("remote : %d requêtes", n)
		}
		if n := len(local.reqs()); n != 1 {
			t.Fatalf("local : %d requêtes", n)
		}
		sc := classified(t, evs)
		if len(sc) != 1 || sc[0].Source != "outbound" || sc[0].Reason != "marker" || sc[0].Marker != "EU RESTRICTED" {
			t.Fatalf("SessionClassified %+v", sc)
		}
		if ps := selected(t, evs); len(ps) != 1 || ps[0].Provider.ID != "local" || ps[0].Reason != "sensitive" {
			t.Fatalf("ProviderSelected %+v", ps)
		}
		// Le masquage n'est pas compté pour le candidat abandonné.
		if n := len(ofType(evs, event.RedactionApplied)); n != 0 {
			t.Fatalf("%d RedactionApplied", n)
		}
		noRemoteAfterClassified(t, evs)
	})
	t.Run("no local", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{text("non")}, routeOpt(nil, 0))
		seed(t, h, "s_1", marked...)
		evs, err := h.submit(t, "s_1", "suite", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(h.prov.reqs()); n != 0 {
			t.Fatalf("remote : %d requêtes", n)
		}
		errs := ofType(evs, event.Error)
		if len(errs) != 1 || decode[event.ErrorPayload](t, errs[0]).Code != "classification" {
			t.Fatalf("events %v", types(evs))
		}
		if tc := lastTurn(t, evs); tc.Reason != "blocked" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
	})
}

// hangProvider n'envoie jamais rien : le flux ne se ferme qu'à l'annulation.
type hangProvider struct {
	info  llm.ProviderInfo
	calls int
}

func (p *hangProvider) Info() llm.ProviderInfo { return p.info }

func (p *hangProvider) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Chunk, error) {
	p.calls++
	ch := make(chan llm.Chunk)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func TestFirstChunkTimeout(t *testing.T) {
	remote2 := fake(remote2Info, text("ok"))
	hang := &hangProvider{info: euInfo}
	h := newHarness(t, euInfo, nil, func(d *Deps) {
		d.Provider = hang
		routeOpt(nil, 0, remote2)(d)
	})
	h.eng.firstChunkTimeout = 50 * time.Millisecond
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hang.calls != 1 || len(remote2.reqs()) != 1 {
		t.Fatalf("remote %d, remote2 %d requêtes", hang.calls, len(remote2.reqs()))
	}
	ps := selected(t, evs)
	if len(ps) != 2 || ps[1].Provider.ID != "remote2" || ps[1].Reason != "fallback" || ps[1].Error != routing.ErrNoFirstChunk.Error() {
		t.Fatalf("ProviderSelected %+v", ps)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestFallbackJurisdictionRefused(t *testing.T) {
	remote2 := fake(remote2Info, text("ok"))
	h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("non")}, routeOpt(nil, 0, remote2))
	asked := 0
	evs, err := h.submit(t, "s_1", "salut", func(p event.PermissionRequestedPayload) Reply {
		asked++
		return Reply{Allow: false}
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 || len(h.prov.reqs()) != 0 || len(remote2.reqs()) != 1 {
		t.Fatalf("demandes %d, remote %d, remote2 %d", asked, len(h.prov.reqs()), len(remote2.reqs()))
	}
	if ps := selected(t, evs); len(ps) != 1 || ps[0].Provider.ID != "remote2" || ps[0].Reason != "fallback" || ps[0].Error != "" {
		t.Fatalf("ProviderSelected %+v", ps)
	}
	if n := len(ofType(evs, event.JurisdictionWarning)); n != 0 {
		t.Fatalf("%d JurisdictionWarning pour un candidat non utilisé", n)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

// Session devenue sensible entre la sélection et l'envoi (tour de hook agent
// concurrent) : le candidat non local n'est pas appelé.
func TestTryRechecksSensitive(t *testing.T) {
	local := fake(localInfo, text("ok"))
	h := newHarness(t, euInfo, [][]llm.Chunk{text("non")}, routeOpt(local, 0))
	s := h.eng.session("s_1")
	s.sensitive = true
	tr := &turn{e: h.eng, s: s, ctx: context.Background(), h: &s.history}
	cur := routing.Candidate{ID: euInfo.ID, Provider: h.prov}
	res, err := tr.try(cur, euInfo, llm.Request{Messages: s.history}, nil, false, nil, false)
	if err != nil || res.sent || len(h.prov.reqs()) != 0 {
		t.Fatalf("res %+v, err %v, remote %d requêtes", res, err, len(h.prov.reqs()))
	}
	got, _, provErr, err := tr.send(llm.Request{Messages: s.history}, false)
	if err != nil || provErr != nil || got != "ok" || len(local.reqs()) != 1 || len(h.prov.reqs()) != 0 {
		t.Fatalf("send %q %v %v, local %d, remote %d", got, provErr, err, len(local.reqs()), len(h.prov.reqs()))
	}
}

// cancelDoneProvider imite OpenAICompat : rien avant l'annulation, puis un
// chunk final portant l'erreur du contexte.
type cancelDoneProvider struct {
	info  llm.ProviderInfo
	calls int
}

func (p *cancelDoneProvider) Info() llm.ProviderInfo { return p.info }

func (p *cancelDoneProvider) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Chunk, error) {
	p.calls++
	ch := make(chan llm.Chunk, 1)
	go func() {
		<-ctx.Done()
		ch <- llm.Chunk{Done: true, Err: ctx.Err()}
		close(ch)
	}()
	return ch, nil
}

func TestFirstChunkTimeoutDoneChunk(t *testing.T) {
	remote2 := fake(remote2Info, text("ok"))
	slow := &cancelDoneProvider{info: euInfo}
	h := newHarness(t, euInfo, nil, func(d *Deps) {
		d.Provider = slow
		routeOpt(nil, 0, remote2)(d)
	})
	h.eng.firstChunkTimeout = 50 * time.Millisecond
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatalf("err %v, events %v", err, types(evs))
	}
	if slow.calls != 1 || len(remote2.reqs()) != 1 {
		t.Fatalf("remote %d, remote2 %d requêtes", slow.calls, len(remote2.reqs()))
	}
	ps := selected(t, evs)
	if len(ps) != 2 || ps[1].Error != routing.ErrNoFirstChunk.Error() {
		t.Fatalf("ProviderSelected %+v", ps)
	}
}

// Une attente de classMu (envoi concurrent) ne compte pas dans le délai du
// premier chunk.
func TestFirstChunkTimerAfterLock(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, routeOpt(nil, 0))
	h.eng.firstChunkTimeout = 50 * time.Millisecond
	s := h.eng.session("s_1")
	tr := &turn{e: h.eng, s: s, ctx: context.Background(), h: &s.history}
	cur := routing.Candidate{ID: euInfo.ID, Provider: h.prov}
	s.classMu.Lock()
	done := make(chan tryResult, 1)
	go func() {
		res, err := tr.try(cur, euInfo, llm.Request{Messages: s.history}, nil, false, nil, false)
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	time.Sleep(150 * time.Millisecond)
	s.classMu.Unlock()
	res := <-done
	if res.provErr != nil || res.text != "ok" {
		t.Fatalf("res %+v", res)
	}
}

func TestRefusedConsentRemembered(t *testing.T) {
	remote2 := fake(remote2Info, text("ok"))
	h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("non")}, routeOpt(nil, 0, remote2))
	asked := 0
	refuse := func(event.PermissionRequestedPayload) Reply {
		asked++
		return Reply{}
	}
	for i := 0; i < 2; i++ {
		evs, err := h.submit(t, "s_1", "salut", refuse)
		if err != nil {
			t.Fatal(err)
		}
		if tc := lastTurn(t, evs); tc.Reason != "done" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
	}
	if asked != 1 || len(h.prov.reqs()) != 0 || len(remote2.reqs()) != 2 {
		t.Fatalf("demandes %d, remote %d, remote2 %d", asked, len(h.prov.reqs()), len(remote2.reqs()))
	}
}
