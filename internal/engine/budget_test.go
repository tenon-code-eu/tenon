package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/routing"
)

// withUsage renvoie ch dont le dernier chunk (Done) porte la consommation
// réelle in/out/cached.
func withUsage(ch []llm.Chunk, in, out, cached int) []llm.Chunk {
	c := slices.Clone(ch)
	c[len(c)-1].Usage = &llm.Usage{Input: in, Output: out, Cached: cached}
	return c
}

func usages(t *testing.T, evs []event.Event) []event.UsagePayload {
	t.Helper()
	var out []event.UsagePayload
	for _, e := range ofType(evs, event.Usage) {
		out = append(out, decode[event.UsagePayload](t, e))
	}
	return out
}

func purposes(us []event.UsagePayload) []string {
	var out []string
	for _, u := range us {
		out = append(out, u.Purpose)
	}
	return out
}

func budgetOpt(max int64, warnAt float64) func(*Deps) {
	return func(d *Deps) { d.Budget = Budget{SessionTokens: max, WarnAt: warnAt} }
}

// reqEstimate est l'estimation d'entrée d'une requête envoyée.
func reqEstimate(r llm.Request) int { return estimate(r.Messages, r.Tools) }

func TestUsagePerCall(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		withUsage(text("RÉSUMÉ"), 100, 10, 5),
		call("a1", "agent", agentCall),
		withUsage(text("sous-agent ok"), 50, 5, 0),
		text("fin"),
	}, nil, window(1000))
	seed(t, h, "s_1", longHistory()...)
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 4 {
		t.Fatalf("%d requêtes, events %v", len(reqs), types(evs))
	}
	us := usages(t, evs)
	if !slices.Equal(purposes(us), []string{"compaction", "turn", "subagent", "turn"}) {
		t.Fatalf("purposes %v", purposes(us))
	}
	for _, u := range us {
		if u.Provider != "mistral" || u.Model != "mistral-large-latest" {
			t.Fatalf("usage %+v", u)
		}
	}
	if u := us[0]; u.Input != 100 || u.Output != 10 || u.Cached != 5 || u.Estimated {
		t.Fatalf("usage réelle %+v", u)
	}
	if u := us[2]; u.Input != 50 || u.Output != 5 || u.Estimated || u.ParentCallID != "a1" {
		t.Fatalf("usage du sous-agent %+v", u)
	}
	argsOut := estimate([]llm.Message{{ToolCalls: []llm.ToolCall{{Args: json.RawMessage(agentCall)}}}}, nil)
	if u := us[1]; !u.Estimated || u.Input != reqEstimate(reqs[1]) || u.Output != argsOut || u.Cached != 0 {
		t.Fatalf("usage estimée %+v, attendu entrée %d sortie %d", u, reqEstimate(reqs[1]), argsOut)
	}
	if u := us[3]; !u.Estimated || u.Input != reqEstimate(reqs[3]) || u.Output != estimate([]llm.Message{{Content: "fin"}}, nil) {
		t.Fatalf("usage estimée %+v", u)
	}
	// Chaque Usage suit l'envoi qu'il mesure : le dernier précède TurnCompleted.
	if ty := types(evs); ty[len(ty)-1] != event.TurnCompleted {
		t.Fatalf("events %v", ty)
	}
}

func TestUsageNotEmittedOnProviderError(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{unavailable()})
	evs, _ := h.submit(t, "s_1", "salut", nil)
	if n := len(ofType(evs, event.Usage)); n != 0 {
		t.Fatalf("%d Usage, events %v", n, types(evs))
	}
}

func TestBudgetWarnOnce(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		withUsage(text("un"), 500, 100, 0),
		withUsage(text("deux"), 200, 50, 0),
		withUsage(text("trois"), 10, 10, 0),
	}, budgetOpt(1000, 0.8))
	var all []event.Event
	for i, in := range []string{"a", "b", "c"} {
		evs, err := h.submit(t, "s_1", in, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc := lastTurn(t, evs); tc.Reason != "done" {
			t.Fatalf("tour %d : %+v", i, tc)
		}
		all = append(all, evs...)
	}
	bw := ofType(all, event.BudgetWarning)
	if len(bw) != 1 {
		t.Fatalf("%d BudgetWarning, events %v", len(bw), types(all))
	}
	if p := decode[event.BudgetWarningPayload](t, bw[0]); p.Used != 850 || p.Max != 1000 {
		t.Fatalf("payload %+v", p)
	}
	// L'avis suit le Usage qui fait franchir le seuil.
	for i, e := range all {
		if e.Type == event.BudgetWarning && all[i-1].Type != event.Usage {
			t.Fatalf("BudgetWarning après %s", all[i-1].Type)
		}
	}
}

func TestBudgetDefaultWarnAt(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{withUsage(text("un"), 800, 0, 0)}, budgetOpt(1000, 0))
	evs, err := h.submit(t, "s_1", "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.BudgetWarning)); n != 1 {
		t.Fatalf("%d BudgetWarning (défaut 0.8)", n)
	}
}

func TestBudgetRefusesNextTurn(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		withUsage(call("c1", "read_file", `{"path":"a.txt"}`), 600, 0, 0),
		withUsage(text("fin"), 500, 0, 0),
		text("jamais"),
	}, budgetOpt(1000, 0.8))
	writeFile(t, filepath.Join(h.root, "a.txt"), "contenu\n")
	evs, err := h.submit(t, "s_1", "lis", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Le tour en cours se termine normalement malgré le dépassement.
	if tc := lastTurn(t, evs); tc.Reason != "done" || tc.Iterations != 2 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	evs, err = h.submit(t, "s_1", "encore", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tc := lastTurn(t, evs); tc.Reason != "budget_exceeded" || tc.Iterations != 0 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	if n := len(h.prov.reqs()); n != 2 {
		t.Fatalf("%d requêtes : fournisseur contacté", n)
	}
	// Le prompt refusé est journalisé comme bloqué (audit), comme un prompt
	// bloqué par un hook.
	if !slices.Equal(types(evs), []event.Type{event.UserMessage, event.TurnCompleted}) {
		t.Fatalf("events %v", types(evs))
	}
	if um := decode[event.UserMessagePayload](t, evs[0]); !um.Blocked || um.Text != "encore" {
		t.Fatalf("UserMessage %+v", um)
	}

	// Reprise avec un budget plus large : le prompt refusé n'entre pas dans
	// l'historique, le compteur repart de 1100.
	h2 := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, budgetOpt(5000, 0.8))
	if err := h2.eng.Restore("s_1", h.sink.events); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.submit(t, "s_1", "suite", nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range h2.prov.reqs()[0].Messages {
		if m.Content == "encore" {
			t.Fatal("prompt refusé rejoué dans l'historique")
		}
	}
	if used, _, _ := h2.eng.session("s_1").overBudget(Budget{}); used < 1100 {
		t.Fatalf("compteur repris %d", used)
	}
}

func TestBudgetSubagentAndCompaction(t *testing.T) {
	// Dépassement au premier envoi ; l'appel agent est refusé, la
	// compaction forcée (ErrContextLength) a lieu quand même.
	h := newAgentEngine(t, [][]llm.Chunk{
		withUsage(call("a1", "agent", agentCall), 2000, 0, 0),
		{{Done: true, Err: provider.ErrContextLength}},
		text("R"),
		text("fin"),
	}, nil, window(4000), budgetOpt(1000, 0.8))
	seed(t, h, "s_1",
		llm.Message{Role: llm.User, Content: runes(1000)},
		llm.Message{Role: llm.Assistant, Content: runes(1000)},
		llm.Message{Role: llm.User, Content: runes(1000)},
		llm.Message{Role: llm.Assistant, Content: runes(1000)})
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.SubagentStarted)); n != 0 {
		t.Fatalf("sous-agent lancé hors budget, events %v", types(evs))
	}
	var res *event.ToolResultPayload
	for _, e := range ofType(evs, event.ToolResult) {
		p := decode[event.ToolResultPayload](t, e)
		if p.CallID == "a1" {
			res = &p
		}
	}
	if res == nil || !res.IsError || !strings.Contains(res.Content, "budget") ||
		!strings.Contains(res.Content, "2000") || !strings.Contains(res.Content, "1000") {
		t.Fatalf("résultat de l'appel agent %+v", res)
	}
	if n := len(ofType(evs, event.ContextCompacted)); n != 1 {
		t.Fatalf("%d ContextCompacted, events %v", n, types(evs))
	}
	if p := purposes(usages(t, evs)); !slices.Equal(p, []string{"turn", "compaction", "turn"}) {
		t.Fatalf("purposes %v", p)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestBudgetResume(t *testing.T) {
	logWith := func(t *testing.T, warned bool) []event.Event {
		l := &evLog{t: t, sid: "s_1"}
		l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
			add(event.UserMessage, event.UserMessagePayload{Text: "a"}).
			add(event.Usage, event.UsagePayload{Provider: "mistral", Purpose: "turn", Input: 600, Output: 100}).
			add(event.Usage, event.UsagePayload{Provider: "mistral", Purpose: "subagent", Input: 100, Output: 50, Cached: 40, ParentCallID: "a1"})
		if warned {
			l.add(event.BudgetWarning, event.BudgetWarningPayload{Used: 850, Max: 1000})
		}
		l.add(event.AssistantMessage, event.AssistantMessagePayload{Text: "ok"}).
			add(event.TurnCompleted, event.TurnCompletedPayload{Iterations: 1, Reason: "done"})
		return l.evs
	}

	t.Run("warned", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			withUsage(text("b"), 50, 50, 0),
			withUsage(text("c"), 100, 0, 0),
		}, budgetOpt(1000, 0.8))
		if err := h.eng.Restore("s_1", logWith(t, true)); err != nil {
			t.Fatal(err)
		}
		var all []event.Event
		for _, in := range []string{"b", "c"} {
			evs, err := h.submit(t, "s_1", in, nil)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, evs...)
		}
		if n := len(ofType(all, event.BudgetWarning)); n != 0 {
			t.Fatalf("%d BudgetWarning après reprise", n)
		}
		// 850 + 100 + 100 = 1050 : le tour suivant est refusé.
		evs, err := h.submit(t, "s_1", "d", nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc := lastTurn(t, evs); tc.Reason != "budget_exceeded" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
		if n := len(h.prov.reqs()); n != 2 {
			t.Fatalf("%d requêtes", n)
		}
	})

	t.Run("not warned", func(t *testing.T) {
		// Journal d'avant le budget : compteur reconstruit, l'avis part au
		// prochain Usage.
		h := newHarness(t, euInfo, [][]llm.Chunk{withUsage(text("b"), 1, 1, 0)}, budgetOpt(1000, 0.8))
		if err := h.eng.Restore("s_1", logWith(t, false)); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "b", nil)
		if err != nil {
			t.Fatal(err)
		}
		bw := ofType(evs, event.BudgetWarning)
		if len(bw) != 1 {
			t.Fatalf("%d BudgetWarning", len(bw))
		}
		if p := decode[event.BudgetWarningPayload](t, bw[0]); p.Used != 852 || p.Max != 1000 {
			t.Fatalf("payload %+v", p)
		}
	})

	t.Run("over budget", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{text("jamais")}, budgetOpt(800, 0.5))
		if err := h.eng.Restore("s_1", logWith(t, true)); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "b", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(types(evs), []event.Type{event.SessionStarted, event.UserMessage, event.TurnCompleted}) {
			t.Fatalf("events %v", types(evs))
		}
		if tc := lastTurn(t, evs); tc.Reason != "budget_exceeded" || tc.Iterations != 0 {
			t.Fatalf("TurnCompleted %+v", tc)
		}
		if n := len(h.prov.reqs()); n != 0 {
			t.Fatalf("%d requêtes", n)
		}
	})
}

// dedicated installe un routeur dédié (compaction ou sous-agents) sur un
// seul fournisseur.
func dedicated(f *fakeProvider) *routing.Router {
	return routing.New(routing.Candidate{ID: f.info.ID, Provider: f}, nil, nil)
}

// sensitiveLog : journal d'une session classée, suivi de msgs.
func sensitiveLog(t *testing.T, msgs ...llm.Message) []event.Event {
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.SessionClassified, event.SessionClassifiedPayload{Reason: "marker", Source: "user"})
	for _, m := range msgs {
		switch m.Role {
		case llm.User:
			l.add(event.UserMessage, event.UserMessagePayload{Text: m.Content})
		case llm.Assistant:
			l.add(event.AssistantMessage, event.AssistantMessagePayload{Text: m.Content})
		}
	}
	return l.evs
}

func TestCompactionRouter(t *testing.T) {
	t.Run("dedicated", func(t *testing.T) {
		small := fake(remote2Info, withUsage(text("RÉSUMÉ"), 10, 2, 0))
		h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, window(1000),
			func(d *Deps) { d.CompactionRouter = dedicated(small) })
		seed(t, h, "s_1", longHistory()...)
		evs, err := h.submit(t, "s_1", "suite", nil)
		if err != nil {
			t.Fatal(err)
		}
		sr := small.reqs()
		if len(sr) != 1 || sr[0].Model != "m2" || !strings.HasPrefix(sr[0].Messages[0].Content, "Faithfully summarize") {
			t.Fatalf("requêtes du petit modèle %s", mustJSON(sr))
		}
		if n := len(h.prov.reqs()); n != 1 {
			t.Fatalf("session : %d requêtes", n)
		}
		us := usages(t, evs)
		if len(us) != 2 || us[0].Purpose != "compaction" || us[0].Provider != "remote2" || us[0].Model != "m2" ||
			us[1].Purpose != "turn" || us[1].Provider != "mistral" {
			t.Fatalf("usages %+v", us)
		}
		if n := len(ofType(evs, event.ContextCompacted)); n != 1 {
			t.Fatalf("%d ContextCompacted", n)
		}
		if sel := selected(t, evs); len(sel) != 2 || sel[0].Provider.ID != "remote2" || sel[1].Provider.ID != "mistral" {
			t.Fatalf("ProviderSelected %+v", sel)
		}
	})

	t.Run("refused falls back to session provider", func(t *testing.T) {
		small := fake(nonEUInfo, text("jamais"))
		h := newHarness(t, euInfo, [][]llm.Chunk{text("RÉSUMÉ"), text("ok")}, window(1000),
			func(d *Deps) { d.CompactionRouter = dedicated(small) })
		seed(t, h, "s_1", longHistory()...)
		evs, err := h.submit(t, "s_1", "suite", func(event.PermissionRequestedPayload) Reply { return Reply{} })
		if err != nil {
			t.Fatal(err)
		}
		if n := len(small.reqs()); n != 0 {
			t.Fatalf("petit modèle : %d requêtes", n)
		}
		if n := len(h.prov.reqs()); n != 2 {
			t.Fatalf("session : %d requêtes", n)
		}
		if n := len(ofType(evs, event.ContextCompacted)); n != 1 {
			t.Fatalf("%d ContextCompacted", n)
		}
		if us := usages(t, evs); len(us) != 2 || us[0].Provider != "mistral" || us[0].Purpose != "compaction" {
			t.Fatalf("usages %+v", us)
		}
	})

	t.Run("sensitive", func(t *testing.T) {
		small := fake(remote2Info, text("jamais"))
		local := fake(localInfo, text("RÉSUMÉ"), text("ok"))
		h := newHarness(t, euInfo, [][]llm.Chunk{text("jamais")}, window(1000), routeOpt(local, 1000),
			func(d *Deps) { d.CompactionRouter = dedicated(small) })
		if err := h.eng.Restore("s_1", sensitiveLog(t, longHistory()...)); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "suite", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(small.reqs()) + len(h.prov.reqs()); n != 0 {
			t.Fatalf("%d requêtes hors local", n)
		}
		if n := len(local.reqs()); n != 2 {
			t.Fatalf("local : %d requêtes, events %v", n, types(evs))
		}
		if us := usages(t, evs); len(us) != 2 || us[0].Provider != "local" || us[0].Purpose != "compaction" {
			t.Fatalf("usages %+v", us)
		}
	})
}

func TestSubagentRouter(t *testing.T) {
	t.Run("dedicated", func(t *testing.T) {
		small := fake(remote2Info, text("sous-agent ok"))
		h := newAgentEngine(t, [][]llm.Chunk{call("a1", "agent", agentCall), text("fin")}, nil,
			func(d *Deps) { d.SubagentRouter = dedicated(small) })
		evs, err := h.submit(t, "s_1", "délègue", nil)
		if err != nil {
			t.Fatal(err)
		}
		sr := small.reqs()
		if len(sr) != 1 || sr[0].Model != "m2" || sr[0].Messages[1].Content != "read a.txt" {
			t.Fatalf("requêtes du petit modèle %s", mustJSON(sr))
		}
		if n := len(h.prov.reqs()); n != 2 {
			t.Fatalf("session : %d requêtes", n)
		}
		us := usages(t, evs)
		if len(us) != 3 || us[1].Purpose != "subagent" || us[1].Provider != "remote2" || us[1].ParentCallID != "a1" {
			t.Fatalf("usages %+v", us)
		}
		if tc := lastTurn(t, evs); tc.Reason != "done" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
	})

	t.Run("sensitive", func(t *testing.T) {
		small := fake(remote2Info, text("jamais"))
		local := fake(localInfo, call("a1", "agent", agentCall), text("sous-agent ok"), text("fin"))
		h := newAgentEngine(t, [][]llm.Chunk{text("jamais")}, nil, routeOpt(local, 0),
			func(d *Deps) { d.SubagentRouter = dedicated(small) })
		if err := h.eng.Restore("s_1", sensitiveLog(t)); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "délègue", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(small.reqs()) + len(h.prov.reqs()); n != 0 {
			t.Fatalf("%d requêtes hors local", n)
		}
		if n := len(local.reqs()); n != 3 {
			t.Fatalf("local : %d requêtes, events %v", n, types(evs))
		}
		if us := usages(t, evs); len(us) != 3 || us[1].Provider != "local" || us[1].Purpose != "subagent" {
			t.Fatalf("usages %+v", us)
		}
	})
}

func TestStablePrefix(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		text("vu"),
		text("encore"),
	}, nil)
	for _, in := range []string{"lis", "et ensuite"} {
		if _, err := h.submit(t, "s_1", in, nil); err != nil {
			t.Fatal(err)
		}
	}
	reqs := h.prov.reqs()
	if len(reqs) != 3 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	sys := mustJSON(reqs[0].Messages[0])
	tools := mustJSON(reqs[0].Tools)
	for i, r := range reqs[1:] {
		if s := mustJSON(r.Messages[0]); s != sys {
			t.Fatalf("requête %d : prompt système %s, attendu %s", i+1, s, sys)
		}
		if !bytes.Equal([]byte(mustJSON(r.Tools)), []byte(tools)) {
			t.Fatalf("requête %d : définitions d'outils différentes", i+1)
		}
	}
	if !slices.IsSortedFunc(reqs[0].Tools, func(a, b llm.ToolDef) int { return strings.Compare(a.Name, b.Name) }) {
		t.Fatalf("outils non triés : %v", reqs[0].Tools)
	}
	if len(reqs[0].Tools) < 2 {
		t.Fatalf("%d outils", len(reqs[0].Tools))
	}
}

// Des définitions dans le désordre sont triées par nom avant l'envoi (MCP
// compris), sans modifier l'entrée.
func TestStablePrefixSortsDefs(t *testing.T) {
	defs := []llm.ToolDef{{Name: "zeta"}, {Name: "mcp__b__x"}, {Name: "alpha"}}
	got := sortedDefs(defs)
	if names := []string{got[0].Name, got[1].Name, got[2].Name}; !slices.Equal(names, []string{"alpha", "mcp__b__x", "zeta"}) {
		t.Fatalf("%v", names)
	}
	if defs[0].Name != "zeta" {
		t.Fatal("entrée modifiée")
	}
}

func TestUsagePromptHookCountsAsTurn(t *testing.T) {
	// R8 : l'évaluation d'un hook prompt compte dans le tour (purpose
	// turn) et dans le budget, sans être refusée par lui.
	h := newHarness(t, euInfo, [][]llm.Chunk{
		withUsage(call("c1", "write_file", `{"path":"b.txt","content":"x"}`), 2000, 0, 0),
		withUsage(text(`{"ok":true}`), 30, 3, 0),
		text("fin"),
	}, budgetOpt(1000, 0.8), withHooks(t, hk{"PreToolUse", "Write", map[string]any{"type": "prompt", "prompt": "Judge: $ARGUMENTS"}}))
	evs, err := h.submit(t, "s_1", "écris", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 3 {
		t.Fatalf("%d requêtes, events %v", n, types(evs))
	}
	us := usages(t, evs)
	if !slices.Equal(purposes(us), []string{"turn", "turn", "turn"}) || us[1].Input != 30 {
		t.Fatalf("usages %+v", us)
	}
	if used, _, _ := h.eng.session("s_1").overBudget(Budget{}); used != 2033+int64(us[2].Input+us[2].Output) {
		t.Fatalf("compteur %d", used)
	}
}

// partialProvider envoie un premier fragment de texte puis, si fail n'est
// pas nil, termine le flux sur cette erreur ; sinon attend l'annulation.
type partialProvider struct {
	info llm.ProviderInfo
	fail error
}

func (p *partialProvider) Info() llm.ProviderInfo { return p.info }

func (p *partialProvider) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		select {
		case ch <- llm.Chunk{Text: "début de réponse"}:
		case <-ctx.Done():
			return
		}
		err := p.fail
		if err == nil {
			<-ctx.Done()
			err = ctx.Err()
		}
		select {
		case ch <- llm.Chunk{Done: true, Err: err}:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func TestUsageInterruptedCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail error
	}{
		{"cancelled", nil},
		{"transport error", errors.New("connection reset by peer")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pp := &partialProvider{info: euInfo, fail: tc.fail}
			h := newHarness(t, euInfo, nil, func(d *Deps) { d.Provider = pp }, budgetOpt(1000, 0.8))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				for ev := range h.sub {
					if ev.Type == event.AssistantDelta && tc.fail == nil {
						cancel()
					}
				}
			}()
			err := h.eng.Submit(ctx, "s_1", "salut")
			if tc.fail == nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("err %v", err)
			}
			if tc.fail != nil && (err == nil || !strings.Contains(err.Error(), "connection reset")) {
				t.Fatalf("err %v", err)
			}
			h.sink.mu.Lock()
			evs := append([]event.Event(nil), h.sink.events...)
			h.sink.mu.Unlock()
			us := usages(t, evs)
			if len(us) != 1 || !us[0].Estimated || us[0].Input <= 0 || us[0].Output <= 0 || us[0].Purpose != "turn" {
				t.Fatalf("usages %+v, events %v", us, types(evs))
			}
			if used, _, _ := h.eng.session("s_1").overBudget(Budget{}); used != int64(us[0].Input+us[0].Output) {
				t.Fatalf("compteur %d", used)
			}
		})
	}
}

func TestBudgetRefusedPromptClassified(t *testing.T) {
	// Budget déjà épuisé (journal repris) ; le prompt porte un marquage.
	over := func(t *testing.T, h *harness) {
		l := &evLog{t: t, sid: "s_1"}
		l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
			add(event.Usage, event.UsagePayload{Provider: "mistral", Purpose: "turn", Input: 2000})
		if err := h.eng.Restore("s_1", l.evs); err != nil {
			t.Fatal(err)
		}
	}
	const prompt = "voici la note DIFFUSION RESTREINTE à relire"

	t.Run("no local", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{text("jamais")}, budgetOpt(1000, 0.8))
		over(t, h)
		evs, err := h.submit(t, "s_1", prompt, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if strings.Contains(string(e.Payload), "relire") {
				t.Fatalf("texte du prompt journalisé : %s %s", e.Type, e.Payload)
			}
		}
		errs := ofType(evs, event.Error)
		if len(errs) != 1 || decode[event.ErrorPayload](t, errs[0]).Code != deniedClassification {
			t.Fatalf("events %v", types(evs))
		}
		if tc := lastTurn(t, evs); tc.Reason != "blocked" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
		if n := len(h.prov.reqs()); n != 0 {
			t.Fatalf("%d requêtes", n)
		}
	})

	t.Run("local", func(t *testing.T) {
		local := fake(localInfo, text("jamais"))
		h := newHarness(t, euInfo, [][]llm.Chunk{text("jamais")}, budgetOpt(1000, 0.8), routeOpt(local, 0))
		over(t, h)
		evs, err := h.submit(t, "s_1", prompt, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(classified(t, evs)) != 1 {
			t.Fatalf("session non classée, events %v", types(evs))
		}
		um := ofType(evs, event.UserMessage)
		if len(um) != 1 || !decode[event.UserMessagePayload](t, um[0]).Blocked {
			t.Fatalf("events %v", types(evs))
		}
		if tc := lastTurn(t, evs); tc.Reason != "budget_exceeded" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
		if !h.eng.session("s_1").isSensitive() {
			t.Fatal("session non sensible")
		}
		if n := len(h.prov.reqs()) + len(local.reqs()); n != 0 {
			t.Fatalf("%d requêtes", n)
		}
	})
}

// Chaque Usage porte la fenêtre de contexte du fournisseur de l'appel : celle
// du candidat, ou celle de la session (Deps.ContextWindow, défaut compris).
func TestUsageWindow(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if us := usages(t, evs); len(us) != 1 || us[0].Window != DefaultContextWindow {
		t.Fatalf("usage %+v", us)
	}
	h = newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, window(1000))
	evs, _ = h.submit(t, "s_1", "salut", nil)
	if us := usages(t, evs); len(us) != 1 || us[0].Window != 1000 {
		t.Fatalf("usage %+v", us)
	}
	// Repli : la fenêtre du fournisseur qui a répondu.
	remote2 := fake(remote2Info, text("ok"))
	h = newHarness(t, euInfo, [][]llm.Chunk{unavailable()}, window(1000), func(d *Deps) {
		cur := routing.Candidate{ID: d.Provider.Info().ID, Provider: d.Provider}
		d.Router = routing.New(cur, nil, []routing.Candidate{{ID: "remote2", Provider: remote2, Window: 64000}})
	})
	evs, _ = h.submit(t, "s_1", "salut", nil)
	if us := usages(t, evs); len(us) != 1 || us[0].Provider != "remote2" || us[0].Window != 64000 {
		t.Fatalf("usage %+v", us)
	}
}
