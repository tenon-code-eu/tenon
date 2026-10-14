package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/provider"
)

// runes renvoie une chaîne de n runes (2 octets chacune en UTF-8).
func runes(n int) string { return strings.Repeat("é", n) }

// seed restaure dans la session sid un historique décrit par des messages.
func seed(t *testing.T, h *harness, sid string, msgs ...llm.Message) {
	t.Helper()
	l := &evLog{t: t, sid: sid}
	for _, m := range msgs {
		switch m.Role {
		case llm.User:
			l.add(event.UserMessage, event.UserMessagePayload{Text: m.Content})
		case llm.Assistant:
			l.add(event.AssistantMessage, event.AssistantMessagePayload{Text: m.Content, ToolCalls: m.ToolCalls})
		case llm.Tool:
			l.add(event.ToolResult, event.ToolResultPayload{CallID: m.ToolCallID, Content: m.Content})
		}
	}
	if err := h.eng.Restore(sid, l.evs); err != nil {
		t.Fatal(err)
	}
}

func window(n int) func(*Deps) { return func(d *Deps) { d.ContextWindow = n } }

func TestEstimate(t *testing.T) {
	if n := estimate([]llm.Message{{Role: llm.User, Content: runes(400)}}, nil); n != 100 {
		t.Fatalf("estimate %d, attendu 100", n)
	}
	msgs := []llm.Message{
		{Role: llm.System, Content: runes(200)},
		{Role: llm.Assistant, Content: runes(40), ToolCalls: []llm.ToolCall{{ID: "c1", Name: "x", Args: json.RawMessage(`"` + runes(58) + `"`)}}},
	}
	defs := []llm.ToolDef{{Name: "x", Schema: json.RawMessage(`"` + runes(98) + `"`)}}
	if n := estimate(msgs, defs); n != 100 {
		t.Fatalf("estimate avec appels et schémas %d, attendu 100", n)
	}
}

func TestNoCompactionUnderThreshold(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	seed(t, h, "s_1",
		llm.Message{Role: llm.User, Content: runes(400)},
		llm.Message{Role: llm.Assistant, Content: runes(400)})
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 1 {
		t.Fatalf("%d requêtes", n)
	}
	if n := len(ofType(evs, event.ContextCompacted)); n != 0 {
		t.Fatalf("%d ContextCompacted", n)
	}
}

// longHistory : 12 messages de 100 jetons, soit 1200 jetons (+ 309 de schémas).
func longHistory() []llm.Message {
	var out []llm.Message
	for i := 0; i < 6; i++ {
		out = append(out,
			llm.Message{Role: llm.User, Content: runes(400)},
			llm.Message{Role: llm.Assistant, Content: runes(400)})
	}
	return out
}

func TestCompactionReplacesOldTurns(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("LE RÉSUMÉ"), text("ok")}, window(1000))
	seed(t, h, "s_1", longHistory()...)
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	sum := reqs[0]
	if len(sum.Tools) != 0 || len(sum.Messages) != 2 || sum.Messages[0].Role != llm.System ||
		!strings.HasPrefix(sum.Messages[0].Content, "Résume fidèlement la conversation suivante") ||
		sum.Messages[1].Role != llm.User || !strings.Contains(sum.Messages[1].Content, "user : ") ||
		!strings.Contains(sum.Messages[1].Content, "assistant : ") {
		t.Fatalf("requête de résumé %s", mustJSON(sum))
	}
	msgs := reqs[1].Messages
	if len(reqs[1].Tools) == 0 {
		t.Fatal("requête normale sans outils")
	}
	if msgs[0].Role != llm.System || msgs[0].Content != "Tu es un agent de test." {
		t.Fatalf("premier message %+v", msgs[0])
	}
	if msgs[1].Role != llm.User || msgs[1].Content != "Résumé de la conversation précédente :\n\nLE RÉSUMÉ" {
		t.Fatalf("message de résumé %+v", msgs[1])
	}
	if last := msgs[len(msgs)-1]; last.Role != llm.User || last.Content != "suite" {
		t.Fatalf("dernier message %+v", last)
	}
	// Queue : dernier échange (200 jetons) + "suite".
	if len(msgs) != 5 {
		t.Fatalf("%d messages : %s", len(msgs), mustJSON(msgs))
	}
	cc := ofType(evs, event.ContextCompacted)
	if len(cc) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	p := decode[event.ContextCompactedPayload](t, cc[0])
	if p.Replaced != 10 || p.Summary != "LE RÉSUMÉ" || p.TokensAfter >= p.TokensBefore || p.TokensBefore <= 800 {
		t.Fatalf("ContextCompacted %+v", p)
	}
	if !strings.Contains(string(cc[0].Payload), `"tokens_before"`) {
		t.Fatalf("payload %s", cc[0].Payload)
	}
	// La requête de résumé ne produit ni delta ni message d'assistant.
	if n := len(ofType(evs, event.AssistantDelta)); n != 1 {
		t.Fatalf("%d AssistantDelta", n)
	}
	if n := len(ofType(evs, event.AssistantMessage)); n != 1 {
		t.Fatalf("%d AssistantMessage", n)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}

	// Le tour suivant repart de l'historique compacté.
	if _, err := h.submit(t, "s_1", "encore", nil); err != nil {
		t.Fatal(err)
	}
	if got := h.prov.reqs()[2].Messages[1].Content; !strings.HasPrefix(got, "Résumé de la conversation précédente") {
		t.Fatalf("tour suivant : %q", got)
	}
}

func TestCompactionKeepsToolPairs(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("R"), text("ok")}, window(1000))
	c1 := llm.ToolCall{ID: "c1", Name: "read_file", Args: json.RawMessage(`{"path":"a"}`)}
	c2 := llm.ToolCall{ID: "c2", Name: "read_file", Args: json.RawMessage(`{"path":"b"}`)}
	seed(t, h, "s_1",
		llm.Message{Role: llm.User, Content: runes(1200)},
		llm.Message{Role: llm.Assistant, Content: runes(1200)},
		llm.Message{Role: llm.User, Content: "go"},
		llm.Message{Role: llm.Assistant, ToolCalls: []llm.ToolCall{c1, c2}},
		llm.Message{Role: llm.Tool, Content: runes(800), ToolCallID: "c1"},
		llm.Message{Role: llm.Tool, Content: runes(400), ToolCallID: "c2"},
		llm.Message{Role: llm.Assistant, Content: "fini"})
	if _, err := h.submit(t, "s_1", "suite", nil); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	msgs := reqs[1].Messages
	if msgs[2].Role == llm.Tool {
		t.Fatalf("la queue commence par un message tool : %s", mustJSON(msgs))
	}
	open := map[string]bool{}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			open[c.ID] = true
		}
		if m.Role == llm.Tool && !open[m.ToolCallID] {
			t.Fatalf("résultat %s sans appel : %s", m.ToolCallID, mustJSON(msgs))
		}
	}
	want := []llm.Message{
		{Role: llm.System, Content: "Tu es un agent de test."},
		{Role: llm.User, Content: "Résumé de la conversation précédente :\n\nR"},
		{Role: llm.User, Content: "suite"},
	}
	if mustJSON(msgs) != mustJSON(want) {
		t.Fatalf("messages %s", mustJSON(msgs))
	}
	// Les appels d'outils figurent dans le texte résumé.
	if s := reqs[0].Messages[1].Content; !strings.Contains(s, "read_file") || !strings.Contains(s, `{"path":"b"}`) {
		t.Fatalf("texte à résumer sans appels d'outils : %q", s)
	}
}

func TestCompactionCannotFit(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, window(1000))
	evs, err := h.submit(t, "s_1", runes(4000), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 0 {
		t.Fatalf("%d requêtes", n)
	}
	e := ofType(evs, event.Error)
	if len(e) != 1 || decode[event.ErrorPayload](t, e[0]).Code != "context_too_large" {
		t.Fatalf("events %v", types(evs))
	}
	if tc := lastTurn(t, evs); tc.Reason != "context_too_large" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

// forcedSetup : sous le seuil (1000 + 309 < 1600) : seule une erreur
// ErrContextLength du fournisseur déclenche la compaction.
func forcedSetup(t *testing.T, script [][]llm.Chunk) *harness {
	h := newHarness(t, euInfo, script, window(2000))
	seed(t, h, "s_1",
		llm.Message{Role: llm.User, Content: runes(1000)},
		llm.Message{Role: llm.Assistant, Content: runes(1000)},
		llm.Message{Role: llm.User, Content: runes(1000)},
		llm.Message{Role: llm.Assistant, Content: runes(1000)})
	return h
}

func TestContextLengthForcesCompaction(t *testing.T) {
	h := forcedSetup(t, [][]llm.Chunk{
		{{Done: true, Err: provider.ErrContextLength}},
		text("R"),
		text("ok"),
	})
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 3 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	if len(reqs[1].Tools) != 0 || len(reqs[1].Messages) != 2 {
		t.Fatalf("requête de résumé %s", mustJSON(reqs[1]))
	}
	// Budget de queue : 0.3 × min(2000, 1316 rejetés) = 394 jetons ; le
	// second échange (500) n'y tient pas, seul "suite" est conservé.
	if m := reqs[2].Messages; len(m) != 3 || m[1].Content != "Résumé de la conversation précédente :\n\nR" {
		t.Fatalf("nouvelle tentative %s", mustJSON(m))
	}
	cc := ofType(evs, event.ContextCompacted)
	if len(cc) != 1 || decode[event.ContextCompactedPayload](t, cc[0]).Replaced != 4 {
		t.Fatalf("events %v", types(evs))
	}
	if len(ofType(evs, event.Error)) != 0 {
		t.Fatalf("events %v", types(evs))
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestContextLengthTwiceIsProviderError(t *testing.T) {
	h := forcedSetup(t, [][]llm.Chunk{
		{{Done: true, Err: provider.ErrContextLength}},
		text("R"),
		{{Done: true, Err: provider.ErrContextLength}},
	})
	evs, err := h.submit(t, "s_1", "suite", nil)
	if !errors.Is(err, provider.ErrContextLength) {
		t.Fatalf("err %v", err)
	}
	if n := len(h.prov.reqs()); n != 3 {
		t.Fatalf("%d requêtes", n)
	}
	e := ofType(evs, event.Error)
	if len(e) != 1 || decode[event.ErrorPayload](t, e[0]).Code != "provider" {
		t.Fatalf("events %v", types(evs))
	}
}

func TestCompactionGoesThroughOutbound(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("R"), text("ok")}, window(1000))
	hist := longHistory()
	hist[0].Content = "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf " + hist[0].Content
	seed(t, h, "s_1", hist...)
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 || len(ofType(evs, event.ContextCompacted)) != 1 {
		t.Fatalf("%d requêtes, events %v", len(reqs), types(evs))
	}
	if strings.Contains(mustJSON(reqs[0]), "Zx8kq") {
		t.Fatal("la requête de résumé contient le secret")
	}
	ra := ofType(evs, event.RedactionApplied)
	if len(ra) == 0 || decode[event.RedactionAppliedPayload](t, ra[0]).Counts["SECRET"] != 1 {
		t.Fatalf("RedactionApplied absent : %v", types(evs))
	}
}

func TestCompactionJurisdictionRefused(t *testing.T) {
	h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("R"), text("ok")}, window(1000))
	seed(t, h, "s_1", longHistory()...)
	evs, err := h.submit(t, "s_1", "suite", func(event.PermissionRequestedPayload) Reply {
		return Reply{Allow: false}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 0 {
		t.Fatalf("%d requêtes", n)
	}
	if len(ofType(evs, event.PermissionRequested)) != 1 || len(ofType(evs, event.ContextCompacted)) != 0 {
		t.Fatalf("events %v", types(evs))
	}
	if tc := lastTurn(t, evs); tc.Reason != "blocked" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestRestoreReplaysCompaction(t *testing.T) {
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.UserMessage, event.UserMessagePayload{Text: "u1"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "a1"}).
		add(event.UserMessage, event.UserMessagePayload{Text: "u2"}).
		add(event.ContextCompacted, event.ContextCompactedPayload{Summary: "R", Replaced: 2, TokensBefore: 9, TokensAfter: 3}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "a2"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "u3", nil); err != nil {
		t.Fatal(err)
	}
	want := []llm.Message{
		{Role: llm.System, Content: "Tu es un agent de test."},
		{Role: llm.User, Content: "Résumé de la conversation précédente :\n\nR"},
		{Role: llm.User, Content: "u2"},
		{Role: llm.Assistant, Content: "a2"},
		{Role: llm.User, Content: "u3"},
	}
	if got := h.prov.reqs()[0].Messages; mustJSON(got) != mustJSON(want) {
		t.Fatalf("messages\n%s\nattendu\n%s", mustJSON(got), mustJSON(want))
	}
}

func TestRestoreInvalidCompaction(t *testing.T) {
	for _, n := range []int{0, 2, -1} {
		l := &evLog{t: t, sid: "s_1"}
		l.add(event.UserMessage, event.UserMessagePayload{Text: "u1"}).
			add(event.ContextCompacted, event.ContextCompactedPayload{Summary: "R", Replaced: n})
		h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
		if err := h.eng.Restore("s_1", l.evs); err == nil {
			t.Fatalf("Replaced %d accepté", n)
		}
	}
}

func TestRestoreTwiceEmptyLog(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.Restore("s_1", nil); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("second Restore : %v", err)
	}
}

// funcProvider répond selon la requête reçue et enregistre chaque requête.
type funcProvider struct {
	info llm.ProviderInfo
	f    func(llm.Request) []llm.Chunk

	mu       sync.Mutex
	requests []llm.Request
}

func (p *funcProvider) Info() llm.ProviderInfo { return p.info }

func (p *funcProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	chunks := p.f(req)
	ch := make(chan llm.Chunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (p *funcProvider) reqs() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.Request(nil), p.requests...)
}

func TestLongTurnNoRepeatedCompaction(t *testing.T) {
	n := 0
	fp := &funcProvider{info: euInfo, f: func(req llm.Request) []llm.Chunk {
		if len(req.Tools) == 0 {
			return text("R")
		}
		n++
		return call(fmt.Sprintf("c%d", n), "read_file", `{"path":"gros.txt"}`)
	}}
	h := newHarness(t, euInfo, nil, window(1000), func(d *Deps) { d.Provider = fp })
	if err := os.WriteFile(filepath.Join(h.root, "gros.txt"), []byte(runes(400)), 0o600); err != nil {
		t.Fatal(err)
	}
	seed(t, h, "s_1", longHistory()...)
	evs, err := h.submit(t, "s_1", "lis gros.txt en boucle", nil)
	if err != nil {
		t.Fatal(err)
	}
	cc := ofType(evs, event.ContextCompacted)
	if len(cc) == 0 {
		t.Fatalf("aucune compaction : %v", types(evs))
	}
	for _, e := range cc {
		p := decode[event.ContextCompactedPayload](t, e)
		if p.TokensAfter >= p.TokensBefore || p.Replaced <= 1 {
			t.Fatalf("compaction sans gain %+v (x%d)", p, len(cc))
		}
	}
	summaries := 0
	for _, r := range fp.reqs() {
		if len(r.Tools) == 0 {
			summaries++
		}
	}
	if summaries != len(cc) {
		t.Fatalf("%d requêtes de résumé pour %d compactions", summaries, len(cc))
	}
	if tc := lastTurn(t, evs); tc.Reason != "context_too_large" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	// Aucune requête normale ne dépasse le seuil.
	for i, r := range fp.reqs() {
		if len(r.Tools) > 0 && estimate(r.Messages, r.Tools) > 800 {
			t.Fatalf("requête %d au-dessus du seuil : %d", i, estimate(r.Messages, r.Tools))
		}
	}
}

func TestForcedCompactionUsesRejectedSize(t *testing.T) {
	fp := &funcProvider{info: euInfo, f: func(req llm.Request) []llm.Chunk {
		// Fenêtre réelle du modèle : 1000 jetons, bien en deçà des 32000
		// configurés.
		if estimate(req.Messages, req.Tools) > 1000 {
			return []llm.Chunk{{Done: true, Err: provider.ErrContextLength}}
		}
		if len(req.Tools) == 0 {
			return text("R")
		}
		return text("ok")
	}}
	h := newHarness(t, euInfo, nil, window(32000), func(d *Deps) { d.Provider = fp })
	seed(t, h, "s_1", longHistory()...)
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := fp.reqs()
	if len(reqs) != 3 || len(reqs[1].Tools) != 0 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	if len(ofType(evs, event.ContextCompacted)) != 1 || len(ofType(evs, event.Error)) != 0 {
		t.Fatalf("events %v", types(evs))
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestSummaryMasksToolArgs(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("R"), text("ok")}, window(1000))
	hist := longHistory()
	hist = append([]llm.Message{
		{Role: llm.User, Content: "connecte-toi"},
		// Le second mot de passe, avec une espace, échappe au masquage du
		// texte brut : seul RedactJSON sur les arguments le masque.
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "login", Args: json.RawMessage(`{"password":"hunter2hunter2"}`)},
			{ID: "c2", Name: "login", Args: json.RawMessage(`{"password": "hunter2 hunter2"}`)},
		}},
		{Role: llm.Tool, Content: "ok", ToolCallID: "c1"},
		{Role: llm.Tool, Content: "ok", ToolCallID: "c2"},
	}, hist...)
	seed(t, h, "s_1", hist...)
	if _, err := h.submit(t, "s_1", "suite", nil); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 || len(reqs[0].Tools) != 0 || !strings.Contains(reqs[0].Messages[1].Content, "login") {
		t.Fatalf("requêtes %s", mustJSON(reqs))
	}
	if j := mustJSON(reqs[0]); strings.Contains(j, "hunter2hunter2") || strings.Contains(j, "hunter2 hunter2") {
		t.Fatal("la requête de résumé contient le mot de passe")
	}
}
