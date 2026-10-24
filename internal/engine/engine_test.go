package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

var update = flag.Bool("update", false, "régénère les fichiers golden")

var euInfo = llm.ProviderInfo{
	ID: "mistral", Name: "Mistral AI", BaseURL: "https://api.mistral.ai/v1",
	Model: "mistral-large-latest", Country: "FR", Jurisdiction: llm.EU,
}

var nonEUInfo = llm.ProviderInfo{
	ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1",
	Model: "gpt-4o", Country: "US", Jurisdiction: llm.NonEU,
}

// memSink enregistre les événements ; failOn fait échouer l'écriture d'un type.
type memSink struct {
	mu     sync.Mutex
	events []event.Event
	failOn event.Type
}

func (s *memSink) Write(e event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn != "" && e.Type == s.failOn {
		return errors.New("disque plein")
	}
	s.events = append(s.events, e)
	return nil
}

type harness struct {
	eng  *Engine
	prov *fakeProvider
	root string
	sink *memSink
	sub  <-chan event.Event
}

func newHarness(t *testing.T, info llm.ProviderInfo, script [][]llm.Chunk, opts ...func(*Deps)) *harness {
	t.Helper()
	root := t.TempDir()
	perms, err := policy.NewPermissions(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	red, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	prov := &fakeProvider{info: info, script: script}
	sink := &memSink{}
	var tick int
	var clockMu sync.Mutex
	t0 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	d := Deps{
		Provider:      prov,
		Tools:         tools.NewRegistry(tools.FS(root, 30000)...),
		Permissions:   perms,
		Outbound:      policy.NewOutbound(red),
		Sinks:         []event.Sink{sink},
		MaxIterations: 50,
		SystemPrompt:  "Tu es un agent de test.",
		ProjectRoot:   root,
		Now: func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			tick++
			return t0.Add(time.Duration(tick) * time.Second)
		},
	}
	for _, o := range opts {
		o(&d)
	}
	eng := New(d)
	return &harness{eng: eng, prov: prov, root: root, sink: sink, sub: eng.Subscribe()}
}

// submit lance un tour et collecte ses événements. respond est appelé depuis
// le goroutine lecteur pour chaque PermissionRequested.
func (h *harness) submit(t *testing.T, sid, input string, respond func(event.PermissionRequestedPayload) Reply) ([]event.Event, error) {
	t.Helper()
	var (
		evs  []event.Event
		done = make(chan struct{})
		out  = make(chan []event.Event)
	)
	handle := func(ev event.Event) {
		evs = append(evs, ev)
		if ev.Type == event.PermissionRequested {
			var p event.PermissionRequestedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Errorf("payload: %v", err)
				return
			}
			r := Reply{}
			if respond != nil {
				r = respond(p)
			}
			if err := h.eng.Respond(p.PermissionID, r); err != nil {
				t.Errorf("Respond: %v", err)
			}
		}
	}
	go func() {
		for {
			select {
			case ev := <-h.sub:
				handle(ev)
			case <-done:
				for {
					select {
					case ev := <-h.sub:
						handle(ev)
					default:
						out <- evs
						return
					}
				}
			}
		}
	}()
	err := h.eng.Submit(context.Background(), sid, input)
	close(done)
	return <-out, err
}

func types(evs []event.Event) []event.Type {
	out := make([]event.Type, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

func ofType(evs []event.Event, typ event.Type) []event.Event {
	var out []event.Event
	for _, e := range evs {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func decode[T any](t *testing.T, e event.Event) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(e.Payload, &v); err != nil {
		t.Fatalf("decode %s: %v", e.Type, err)
	}
	return v
}

func lastTurn(t *testing.T, evs []event.Event) event.TurnCompletedPayload {
	t.Helper()
	tc := ofType(evs, event.TurnCompleted)
	if len(tc) != 1 {
		t.Fatalf("TurnCompleted x%d, events %v", len(tc), types(evs))
	}
	return decode[event.TurnCompletedPayload](t, tc[0])
}

func text(s ...string) []llm.Chunk {
	var out []llm.Chunk
	for _, x := range s {
		out = append(out, llm.Chunk{Text: x})
	}
	return append(out, llm.Chunk{Done: true})
}

func call(id, name, args string) []llm.Chunk {
	return []llm.Chunk{{Done: true, ToolCalls: []llm.ToolCall{{ID: id, Name: name, Args: json.RawMessage(args)}}}}
}

func calls(cs ...llm.ToolCall) []llm.Chunk {
	return []llm.Chunk{{Done: true, ToolCalls: cs}}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestTextOnlyTurn(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("Bon", "jour !")}, func(d *Deps) {
		d.ProjectRoot = "/home/dev/projet"
	})
	evs, err := h.submit(t, "s_1", "Salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []event.Type{event.SessionStarted, event.UserMessage, event.AssistantDelta,
		event.AssistantDelta, event.Usage, event.AssistantMessage, event.TurnCompleted}
	if got := types(evs); !equalTypes(got, want) {
		t.Fatalf("types %v, want %v", got, want)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" || tc.Iterations != 1 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	var buf bytes.Buffer
	for _, e := range evs {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	golden := filepath.Join("testdata", "text_turn.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want2, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want2) {
		t.Fatalf("golden différent :\n%s\nattendu :\n%s", buf.Bytes(), want2)
	}
	// L'audit reçoit exactement les mêmes événements, dans le même ordre.
	if len(h.sink.events) != len(evs) {
		t.Fatalf("sink %d événements, abonnés %d", len(h.sink.events), len(evs))
	}
	// La requête porte le prompt système, le message utilisateur et les outils.
	r := h.prov.reqs()[0]
	if r.Model != euInfo.Model || len(r.Tools) == 0 || r.Messages[0].Role != llm.System || r.Messages[1].Content != "Salut" {
		t.Fatalf("requête %+v", r)
	}
}

func equalTypes(a, b []event.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestToolCallAllowedRead(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"README.md"}`),
		text("Le fichier contient bonjour"),
	})
	if err := os.WriteFile(filepath.Join(h.root, "README.md"), []byte("bonjour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "lis le README", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ofType(evs, event.PermissionRequested)) != 0 {
		t.Fatal("lecture dans le projet : aucune permission attendue")
	}
	res := ofType(evs, event.ToolResult)
	if len(res) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	p := decode[event.ToolResultPayload](t, res[0])
	if p.IsError || !strings.Contains(p.Content, "bonjour") || p.CallID != "c1" {
		t.Fatalf("ToolResult %+v", p)
	}
	// ToolCallRequested précède le résultat.
	tcr := ofType(evs, event.ToolCallRequested)
	if len(tcr) != 1 || tcr[0].Seq > res[0].Seq {
		t.Fatalf("ToolCallRequested %v", types(evs))
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	var found bool
	for _, m := range reqs[1].Messages {
		if m.Role == llm.Tool && m.ToolCallID == "c1" && strings.Contains(m.Content, "bonjour") {
			found = true
		}
	}
	if !found {
		t.Fatalf("2e requête sans message tool : %+v", reqs[1].Messages)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" || tc.Iterations != 2 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestToolCallAskDeniedByReply(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"out.txt","content":"x"}`),
		text("ok"),
	})
	evs, err := h.submit(t, "s_1", "écris", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: false} })
	if err != nil {
		t.Fatal(err)
	}
	pr := ofType(evs, event.PermissionRequested)
	if len(pr) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	pp := decode[event.PermissionRequestedPayload](t, pr[0])
	if pp.Kind != "tool" || pp.Tool != "write_file" || pp.PermissionID != "s_1-p1" || len(pp.Paths) != 1 {
		t.Fatalf("PermissionRequested %+v", pp)
	}
	rs := ofType(evs, event.PermissionResolved)
	if len(rs) != 1 || decode[event.PermissionResolvedPayload](t, rs[0]).Allowed {
		t.Fatalf("PermissionResolved %v", types(evs))
	}
	if _, err := os.Stat(filepath.Join(h.root, "out.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fichier créé malgré le refus : %v", err)
	}
	p := decode[event.ToolResultPayload](t, ofType(evs, event.ToolResult)[0])
	if !p.IsError || !strings.Contains(p.Content, "denied by the user") || p.Denied != "user" {
		t.Fatalf("ToolResult %+v", p)
	}
	if err := h.eng.Respond("s_1-p1", Reply{Allow: true}); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("Respond après résolution : %v", err)
	}
}

func TestRememberForSession(t *testing.T) {
	w := `{"path":"out.txt","content":"x"}`
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", w),
		call("c2", "write_file", w),
		text("fini"),
	})
	evs, err := h.submit(t, "s_1", "écris deux fois", func(event.PermissionRequestedPayload) Reply {
		return Reply{Allow: true, Remember: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.PermissionRequested)); n != 1 {
		t.Fatalf("%d PermissionRequested, events %v", n, types(evs))
	}
	res := ofType(evs, event.ToolResult)
	if len(res) != 2 {
		t.Fatalf("events %v", types(evs))
	}
	for _, r := range res {
		if p := decode[event.ToolResultPayload](t, r); p.IsError || p.HashAfter == "" {
			t.Fatalf("ToolResult %+v", p)
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, "out.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestRememberIsPerSession(t *testing.T) {
	w := `{"path":"out.txt","content":"x"}`
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", w), text("fini"),
		call("c2", "write_file", w), text("fini"),
	})
	allow := func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true, Remember: true} }
	if _, err := h.submit(t, "s_1", "a", allow); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_2", "b", allow)
	if err != nil {
		t.Fatal(err)
	}
	pr := ofType(evs, event.PermissionRequested)
	if len(pr) != 1 || decode[event.PermissionRequestedPayload](t, pr[0]).PermissionID != "s_2-p1" {
		t.Fatalf("session 2 : %v", types(evs))
	}
}

func TestUnknownToolAndMalformedArgs(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		calls(
			llm.ToolCall{ID: "c1", Name: "rm_rf", Args: json.RawMessage(`{}`)},
			llm.ToolCall{ID: "c2", Name: "read_file", Args: json.RawMessage(`{"path":`)},
		),
		text("désolé"),
	})
	evs, err := h.submit(t, "s_1", "fais n'importe quoi", nil)
	if err != nil {
		t.Fatal(err)
	}
	res := ofType(evs, event.ToolResult)
	if len(res) != 2 {
		t.Fatalf("events %v", types(evs))
	}
	p1 := decode[event.ToolResultPayload](t, res[0])
	p2 := decode[event.ToolResultPayload](t, res[1])
	if !p1.IsError || p1.Content != "unknown tool: rm_rf" {
		t.Fatalf("ToolResult 1 %+v", p1)
	}
	if !p2.IsError || !strings.HasPrefix(p2.Content, "invalid arguments: ") || strings.Count(p2.Content, "invalid arguments") != 1 {
		t.Fatalf("ToolResult 2 %+v", p2)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	if len(h.prov.reqs()) != 2 {
		t.Fatal("le modèle doit recevoir les erreurs")
	}
}

func TestMaxIterations(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{call("c1", "glob", `{"pattern":"*"}`)}, func(d *Deps) {
		d.MaxIterations = 3
	})
	evs, err := h.submit(t, "s_1", "boucle", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 3 {
		t.Fatalf("%d requêtes", n)
	}
	if tc := lastTurn(t, evs); tc.Reason != "max_iterations" || tc.Iterations != 3 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestAuditFailureFailsClosed(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"out.txt","content":"x"}`),
		text("ok"),
	})
	h.sink.failOn = event.ToolCallRequested
	evs, err := h.submit(t, "s_1", "écris", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} })
	if !errors.Is(err, ErrAuditFailure) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.root, "out.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("outil exécuté malgré l'échec d'audit")
	}
	last := evs[len(evs)-1]
	if last.Type != event.Error || decode[event.ErrorPayload](t, last).Code != "audit_failure" {
		t.Fatalf("events %v", types(evs))
	}
	if len(ofType(evs, event.ToolCallRequested)) != 0 || len(ofType(evs, event.ToolResult)) != 0 {
		t.Fatalf("événement non audité diffusé : %v", types(evs))
	}
	for _, e := range h.sink.events {
		if e.Type == event.Error {
			t.Fatal("l'erreur d'audit ne doit pas être réécrite dans le sink")
		}
	}
	// Toute soumission ultérieure échoue immédiatement.
	h.sink.failOn = ""
	n := len(h.prov.reqs())
	evs2, err := h.submit(t, "s_2", "encore", nil)
	if !errors.Is(err, ErrAuditFailure) || len(evs2) != 0 || len(h.prov.reqs()) != n {
		t.Fatalf("2e Submit : err %v, events %v", err, types(evs2))
	}
}

func TestNonEUConsentAsked(t *testing.T) {
	h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("hello")})
	evs, err := h.submit(t, "s_1", "salut", func(p event.PermissionRequestedPayload) Reply {
		if p.Kind != "jurisdiction" || p.Provider == nil || p.Provider.ID != "openai" {
			t.Errorf("PermissionRequested %+v", p)
		}
		return Reply{Allow: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []event.Type{event.SessionStarted, event.UserMessage, event.PermissionRequested,
		event.PermissionResolved, event.JurisdictionWarning, event.AssistantDelta,
		event.Usage, event.AssistantMessage, event.TurnCompleted}
	if got := types(evs); !equalTypes(got, want) {
		t.Fatalf("types %v, want %v", got, want)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	// Second tour : ni nouvelle demande ni nouvel avertissement.
	evs, err = h.submit(t, "s_1", "encore", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ofType(evs, event.PermissionRequested))+len(ofType(evs, event.JurisdictionWarning)) != 0 {
		t.Fatalf("2e tour %v", types(evs))
	}
}

func TestNonEURefused(t *testing.T) {
	h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("hello")})
	evs, err := h.submit(t, "s_1", "salut", func(event.PermissionRequestedPayload) Reply { return Reply{} })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 0 {
		t.Fatalf("%d requêtes envoyées", n)
	}
	if tc := lastTurn(t, evs); tc.Reason != "blocked" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestSecretRedactedBeforeProvider(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"x.txt"}`),
		text("vu"),
	})
	evs, err := h.submit(t, "s_1", "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	for i, r := range reqs {
		if strings.Contains(mustJSON(r), "Zx8kq") {
			t.Fatalf("requête %d contient le secret", i)
		}
	}
	ra := ofType(evs, event.RedactionApplied)
	if len(ra) == 0 || decode[event.RedactionAppliedPayload](t, ra[0]).Counts["SECRET"] != 1 {
		t.Fatalf("RedactionApplied absent : %v", types(evs))
	}
	// L'historique en mémoire reste brut (masquage à chaque envoi).
	if um := decode[event.UserMessagePayload](t, ofType(evs, event.UserMessage)[0]); !strings.Contains(um.Text, "Zx8kq") {
		t.Fatalf("UserMessage %+v", um)
	}
}

func TestIncompleteStream(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{{
		{Text: "Je commen"},
		{Done: true, Err: provider.ErrIncomplete},
	}})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	am := ofType(evs, event.AssistantMessage)
	if len(am) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.AssistantMessagePayload](t, am[0]); !p.Incomplete || p.Text != "Je commen" {
		t.Fatalf("AssistantMessage %+v", p)
	}
	if tc := lastTurn(t, evs); tc.Reason != "incomplete" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestProviderError(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{{{Done: true, Err: provider.ErrAuth}}})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err %v", err)
	}
	e := ofType(evs, event.Error)
	if len(e) != 1 || decode[event.ErrorPayload](t, e[0]).Code != "provider" {
		t.Fatalf("events %v", types(evs))
	}
}

func TestWriteWithRedactionMarkerRefused(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"conf.env.txt","content":"KEY=[SECRET_1]\n"}`),
		text("ok"),
	})
	evs, err := h.submit(t, "s_1", "écris la conf", func(event.PermissionRequestedPayload) Reply {
		return Reply{Allow: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.root, "conf.env.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fichier écrit avec un marqueur de masquage")
	}
	if len(ofType(evs, event.PermissionRequested)) != 0 {
		t.Fatalf("permission demandée avant le refus : %v", types(evs))
	}
	p := decode[event.ToolResultPayload](t, ofType(evs, event.ToolResult)[0])
	want := "redacted content: cannot write a redaction marker ([SECRET_1])"
	if !p.IsError || p.Content != want || p.Denied != "policy" {
		t.Fatalf("ToolResult %+v", p)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestPermissionWaitEndsOnCancel(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{call("c1", "write_file", `{"path":"out.txt","content":"x"}`)})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for ev := range h.sub {
			if ev.Type == event.PermissionRequested {
				cancel()
				return
			}
		}
	}()
	err := h.eng.Submit(ctx, "s_1", "écris")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if err := h.eng.Respond("s_1-p1", Reply{Allow: true}); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("Respond après annulation : %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.root, "out.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fichier écrit après annulation")
	}
}

func TestWriteWithEscapedRedactionMarkerRefused(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"conf.txt","content":"KEY=[SECRET_1]"}`),
		text("ok"),
	})
	evs, err := h.submit(t, "s_1", "écris la conf", func(event.PermissionRequestedPayload) Reply {
		return Reply{Allow: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.root, "conf.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fichier écrit avec un marqueur de masquage échappé")
	}
	if p := decode[event.ToolResultPayload](t, ofType(evs, event.ToolResult)[0]); !p.IsError || !strings.Contains(p.Content, "[SECRET_1]") {
		t.Fatalf("ToolResult %+v", p)
	}
}

func TestCancelledToolCallsKeepHistoryValid(t *testing.T) {
	w := `{"path":"out.txt","content":"x"}`
	h := newHarness(t, euInfo, [][]llm.Chunk{
		calls(
			llm.ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(w)},
			llm.ToolCall{ID: "c2", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)},
		),
		text("ok"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var evs []event.Event
	done := make(chan struct{})
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for {
			select {
			case ev := <-h.sub:
				evs = append(evs, ev)
				if ev.Type == event.PermissionRequested {
					cancel()
				}
			case <-done:
				for {
					select {
					case ev := <-h.sub:
						evs = append(evs, ev)
					default:
						return
					}
				}
			}
		}
	}()
	err := h.eng.Submit(ctx, "s_1", "écris")
	close(done)
	<-collected
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if tc := lastTurn(t, evs); tc.Reason != "cancelled" || tc.Iterations != 1 {
		t.Fatalf("TurnCompleted %+v", tc)
	}

	// La session reste utilisable : chaque tool_call a sa réponse.
	if _, err := h.submit(t, "s_1", "encore", nil); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	answered := map[string]bool{}
	var ids []string
	for _, m := range reqs[1].Messages {
		if m.Role == llm.Tool {
			answered[m.ToolCallID] = true
		}
		if m.Role == llm.Assistant {
			for _, c := range m.ToolCalls {
				ids = append(ids, c.ID)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("tool_calls %v", ids)
	}
	for _, id := range ids {
		if !answered[id] {
			t.Fatalf("tool_call %s sans réponse : %+v", id, reqs[1].Messages)
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, "out.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fichier écrit après annulation")
	}
}

func TestToolCallDeniedByPolicyRule(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"out.txt","content":"x"}`),
		call("c2", "read_file", `{"path":"absent.txt"}`),
		text("ok"),
	}, func(d *Deps) {
		perms, err := policy.NewPermissions(d.ProjectRoot, []policy.Rule{{Tool: "write_file", Decision: policy.Deny}})
		if err != nil {
			t.Fatal(err)
		}
		d.Permissions = perms
	})
	evs, err := h.submit(t, "s_1", "écris", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ofType(evs, event.PermissionRequested)) != 0 {
		t.Fatalf("permission demandée : %v", types(evs))
	}
	if _, err := os.Stat(filepath.Join(h.root, "out.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fichier créé malgré la règle deny : %v", err)
	}
	rs := ofType(evs, event.ToolResult)
	if len(rs) != 2 {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.ToolResultPayload](t, rs[0]); !p.IsError || p.Denied != "policy" {
		t.Fatalf("ToolResult deny %+v", p)
	}
	// Une erreur d'outil ordinaire n'est pas un refus.
	if p := decode[event.ToolResultPayload](t, rs[1]); !p.IsError || p.Denied != "" {
		t.Fatalf("ToolResult erreur %+v", p)
	}
	if !strings.Contains(string(rs[1].Payload), `"is_error":true`) || strings.Contains(string(rs[1].Payload), `"denied"`) {
		t.Fatalf("payload %s", rs[1].Payload)
	}
}

func TestEngineDefaultIsFrench(t *testing.T) {
	if DefaultSystemPrompt != i18n.SystemPrompt(i18n.FR) {
		t.Fatal("DefaultSystemPrompt doit égaler le prompt FR")
	}
}

func TestSubmitInputRecordsSkill(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	in := Input{Text: "T", Typed: "/review", Skill: "review", SkillSHA256: "ab"}
	if err := h.eng.SubmitInput(context.Background(), "s_1", in); err != nil {
		t.Fatal(err)
	}
	msgs := h.prov.reqs()[0].Messages
	if last := msgs[len(msgs)-1]; last.Role != llm.User || last.Content != "T" {
		t.Fatalf("dernier message %+v, attendu user \"T\"", last)
	}
	var got []event.Event
	for _, e := range h.sink.events {
		if e.Type == event.UserMessage {
			got = append(got, e)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d UserMessage, attendu 1", len(got))
	}
	p := decode[event.UserMessagePayload](t, got[0])
	want := event.UserMessagePayload{Text: "T", Typed: "/review", Skill: "review", SkillSHA256: "ab"}
	if p != want {
		t.Fatalf("payload %+v, attendu %+v", p, want)
	}
}

func TestPlainSubmitPayloadHasNoSkillKeys(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Submit(context.Background(), "s_1", "bonjour"); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, e := range h.sink.events {
		if e.Type != event.UserMessage {
			continue
		}
		n++
		raw := string(e.Payload)
		for _, k := range []string{`"typed"`, `"skill"`, `"skill_sha256"`} {
			if strings.Contains(raw, k) {
				t.Fatalf("payload %s contient %s", raw, k)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d UserMessage", n)
	}
}
