package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// scripted rejoue une réponse par appel (la dernière une fois le script épuisé).
type scripted struct {
	info   llm.ProviderInfo
	script [][]llm.Chunk

	mu sync.Mutex
	n  int
}

func (s *scripted) Info() llm.ProviderInfo { return s.info }

func (s *scripted) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Chunk, error) {
	s.mu.Lock()
	i := s.n
	s.n++
	s.mu.Unlock()
	if i >= len(s.script) {
		i = len(s.script) - 1
	}
	ch := make(chan llm.Chunk, len(s.script[i]))
	for _, c := range s.script[i] {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (s *scripted) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func text(s string) []llm.Chunk { return []llm.Chunk{{Text: s}, {Done: true}} }

func call(name, args string) []llm.Chunk {
	return []llm.Chunk{{Done: true, ToolCalls: []llm.ToolCall{{ID: "c1", Name: name, Args: json.RawMessage(args)}}}}
}

func newEngine(t *testing.T, j llm.Jurisdiction, script ...[]llm.Chunk) (*engine.Engine, *scripted, string) {
	t.Helper()
	return newEngineRules(t, j, nil, script...)
}

func newEngineRules(t *testing.T, j llm.Jurisdiction, rules []policy.Rule, script ...[]llm.Chunk) (*engine.Engine, *scripted, string) {
	t.Helper()
	root := t.TempDir()
	perm, err := policy.NewPermissions(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	r, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &scripted{
		info:   llm.ProviderInfo{ID: "t", Name: "Test SA", Model: "m-1", Country: "FR", Jurisdiction: j},
		script: script,
	}
	eng := engine.New(engine.Deps{
		Provider:    p,
		Tools:       tools.NewRegistry(tools.FS(root, 1000)...),
		Permissions: perm,
		Outbound:    policy.NewOutbound(r),
		ProjectRoot: root,
	})
	return eng, p, root
}

func TestTextOutputFinalAnswerAndBanner(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("Bonjour"))
	var out, errb bytes.Buffer
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "salut"}, Options{Output: Text}, &out, &errb)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "Bonjour" {
		t.Fatalf("stdout=%q", out.String())
	}
	if !strings.Contains(errb.String(), "tenon · m-1 · Test SA · FR · juridiction UE") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestPermissionDeniedExit3(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, call("write_file", `{"path":"x.txt","content":"a"}`), text("fini"))
	var out, errb bytes.Buffer
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "écris"}, Options{Output: Text}, &out, &errb)
	if code != 3 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "refus") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestJurisdictionDeniedExit3(t *testing.T) {
	eng, p, _ := newEngine(t, llm.NonEU, text("non"))
	var out, errb bytes.Buffer
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "salut"}, Options{Output: Text}, &out, &errb)
	if code != 3 || p.calls() != 0 {
		t.Fatalf("code=%d calls=%d stderr=%s", code, p.calls(), errb.String())
	}
	if !strings.Contains(errb.String(), "hors UE") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestJSONOutputOneEventPerLine(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("Bonjour"))
	var out, errb bytes.Buffer
	if code := Run(context.Background(), eng, "s_1", engine.Input{Text: "salut"}, Options{Output: JSON}, &out, &errb); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	sc := bufio.NewScanner(&out)
	var types []event.Type
	for sc.Scan() {
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("ligne %q : %v", sc.Text(), err)
		}
		types = append(types, e.Type)
	}
	if len(types) < 3 || types[0] != event.SessionStarted || types[len(types)-1] != event.TurnCompleted {
		t.Fatalf("types=%v", types)
	}
}

type failing struct{}

func (failing) Info() llm.ProviderInfo {
	return llm.ProviderInfo{ID: "t", Model: "m", Jurisdiction: llm.EU}
}
func (failing) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, context.DeadlineExceeded
}

func TestSubmitErrorExit1(t *testing.T) {
	root := t.TempDir()
	perm, _ := policy.NewPermissions(root, nil)
	r, _ := policy.NewRedactor(nil)
	eng := engine.New(engine.Deps{Provider: failing{}, Permissions: perm, Outbound: policy.NewOutbound(r), ProjectRoot: root})
	var out, errb bytes.Buffer
	if code := Run(context.Background(), eng, "s_1", engine.Input{Text: "salut"}, Options{Output: Text}, &out, &errb); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if errb.Len() == 0 {
		t.Fatal("stderr vide")
	}
}

func TestJurisdictionLabel(t *testing.T) {
	for j, want := range map[llm.Jurisdiction]string{llm.EU: "UE", llm.NonEU: "hors UE", llm.Local: "locale"} {
		if got := JurisdictionLabel(i18n.FR, j); got != want {
			t.Fatalf("%s : %q", j, got)
		}
	}
	for j, want := range map[llm.Jurisdiction]string{llm.EU: "EU", llm.NonEU: "non-EU", llm.Local: "local"} {
		if got := JurisdictionLabel(i18n.EN, j); got != want {
			t.Fatalf("%s : %q", j, got)
		}
	}
}

func TestPolicyDenyExit3(t *testing.T) {
	eng, _, root := newEngineRules(t, llm.EU, []policy.Rule{{Tool: "write_file", Decision: policy.Deny}},
		call("write_file", `{"path":"x.txt","content":"a"}`), text("fini"))
	var out, errb bytes.Buffer
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "écris"}, Options{Output: Text}, &out, &errb)
	if code != 3 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "politique") {
		t.Fatalf("stderr=%q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("fichier écrit : %v", err)
	}
}

func TestToolErrorIsNotRefusal(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, call("read_file", `{"path":"absent.txt"}`), text("fini"))
	var out, errb bytes.Buffer
	if code := Run(context.Background(), eng, "s_1", engine.Input{Text: "lis"}, Options{Output: Text}, &out, &errb); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}

func TestJSONOutputRedacted(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("ok"))
	var out, errb bytes.Buffer
	redact := func(s string) string { return strings.ReplaceAll(s, "SECRET", "[X]") }
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "mon SECRET"}, Options{Output: JSON, Redact: redact}, &out, &errb)
	if code != 0 || strings.Contains(out.String(), "SECRET") || !strings.Contains(out.String(), "[X]") {
		t.Fatalf("code=%d stdout=%s", code, out.String())
	}
}

func TestJSONOutputRedactionFailsClosed(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("ok"))
	var out, errb bytes.Buffer
	broken := func(s string) string {
		if strings.Contains(s, "SECRET") {
			return `{"text":"` // JSON invalide
		}
		return s
	}
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "mon SECRET"}, Options{Output: JSON, Redact: broken}, &out, &errb)
	if code != 0 || strings.Contains(out.String(), "SECRET") {
		t.Fatalf("code=%d stdout=%s", code, out.String())
	}
	found := false
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var e struct {
			Type    event.Type         `json:"type"`
			Payload event.ErrorPayload `json:"payload"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("ligne %q : %v", sc.Text(), err)
		}
		if e.Type == event.Error && e.Payload.Code == "redaction" {
			found = true
		}
	}
	if !found {
		t.Fatalf("aucune erreur de masquage : %s", out.String())
	}
}

func TestTextOutputFinalAnswerRedacted(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("voici SECRET"))
	var out, errb bytes.Buffer
	redact := func(s string) string { return strings.ReplaceAll(s, "SECRET", "[X]") }
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "salut"}, Options{Output: Text, Redact: redact}, &out, &errb)
	if code != 0 || strings.Contains(out.String(), "SECRET") || !strings.Contains(out.String(), "voici [X]") {
		t.Fatalf("code=%d stdout=%q", code, out.String())
	}
	// RedactText, s'il est fourni, s'applique au texte à la place de Redact.
	eng, _, _ = newEngine(t, llm.EU, text("voici SECRET"))
	out.Reset()
	textOnly := func(s string) string { return strings.ReplaceAll(s, "SECRET", "[T]") }
	Run(context.Background(), eng, "s_1", engine.Input{Text: "salut"}, Options{Output: Text, Redact: redact, RedactText: textOnly}, &out, &errb)
	if !strings.Contains(out.String(), "voici [T]") {
		t.Fatalf("stdout=%q", out.String())
	}
}

func TestEnglishOutput(t *testing.T) {
	eng, _, _ := newEngine(t, llm.NonEU, text("non"))
	var out, errb bytes.Buffer
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "hi"}, Options{Output: Text, Lang: i18n.EN}, &out, &errb)
	if code != 3 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	for _, want := range []string{
		"tenon · m-1 · Test SA · FR · jurisdiction non-EU\n",
		"refused (non-interactive mode): sending to Test SA (jurisdiction non-EU); rerun with --allow-non-eu to accept\n",
		"turn blocked: sending to the provider was refused\n",
	} {
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("stderr=%q, attendu %q", errb.String(), want)
		}
	}
	if strings.Contains(errb.String(), "juridiction") {
		t.Fatalf("stderr en français : %q", errb.String())
	}
}

// emitted rend un événement à partir de sa charge utile.
func emitted(t *testing.T, typ event.Type, payload any) event.Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return event.Event{Type: typ, Session: "s_1", Payload: b}
}

func TestInstructionsAndCompactionNotices(t *testing.T) {
	for lang, want := range map[i18n.Lang][]string{
		i18n.FR: {"instructions : AGENTS.md, TENON.md\n", "contexte compacté : 4 messages résumés\n"},
		i18n.EN: {"instructions: AGENTS.md, TENON.md\n", "context compacted: 4 messages summarised\n"},
	} {
		var out, errb bytes.Buffer
		r := &renderer{out: Text, lang: lang, redact: func(s string) string { return s },
			redactText: func(s string) string { return s }, stdout: &out, stderr: &errb}
		r.handle(emitted(t, event.SessionStarted, event.SessionStartedPayload{
			Provider:     llm.ProviderInfo{ID: "t", Model: "m", Jurisdiction: llm.EU},
			Instructions: []string{"AGENTS.md", "TENON.md"},
		}))
		r.handle(emitted(t, event.ContextCompacted, event.ContextCompactedPayload{Summary: "s", Replaced: 4}))
		for _, w := range want {
			if !strings.Contains(errb.String(), w) {
				t.Fatalf("%s : stderr=%q, attendu %q", lang, errb.String(), w)
			}
		}
	}
	// Sans fichier d'instructions, aucune ligne « instructions ».
	var out, errb bytes.Buffer
	r := &renderer{out: Text, lang: i18n.FR, redact: func(s string) string { return s },
		redactText: func(s string) string { return s }, stdout: &out, stderr: &errb}
	r.handle(emitted(t, event.SessionStarted, event.SessionStartedPayload{
		Provider: llm.ProviderInfo{ID: "t", Model: "m", Jurisdiction: llm.EU},
	}))
	if strings.Contains(errb.String(), "instructions") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestDefaultLangIsFrench(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("ok"))
	var out, errb bytes.Buffer
	Run(context.Background(), eng, "s_1", engine.Input{Text: "salut"}, Options{Output: Text}, &out, &errb)
	if !strings.Contains(errb.String(), "juridiction UE") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

// Un tour qui n'a pas pu tourner (contexte trop volumineux) est une erreur :
// code 1, aucune requête.
func TestContextTooLargeExit1(t *testing.T) {
	root := t.TempDir()
	perm, err := policy.NewPermissions(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &scripted{info: llm.ProviderInfo{ID: "t", Model: "m-1", Jurisdiction: llm.EU}, script: [][]llm.Chunk{text("ok")}}
	eng := engine.New(engine.Deps{
		Provider: p, Permissions: perm, Outbound: policy.NewOutbound(r),
		ProjectRoot: root, ContextWindow: 100,
	})
	for _, out := range []Output{Text, JSON} {
		var stdout, errb bytes.Buffer
		code := Run(context.Background(), eng, "s_"+string(out), engine.Input{Text: strings.Repeat("mot ", 400)}, Options{Output: out}, &stdout, &errb)
		if code != 1 || p.calls() != 0 {
			t.Fatalf("%s : code=%d appels=%d stderr=%q", out, code, p.calls(), errb.String())
		}
	}
}

// Error{context_too_large} est rendu depuis le catalogue, pas depuis le
// message (français) de l'événement.
func TestContextTooLargeMessageLocalised(t *testing.T) {
	var out, errb bytes.Buffer
	r := &renderer{out: Text, lang: i18n.EN, redact: func(s string) string { return s },
		redactText: func(s string) string { return s }, stdout: &out, stderr: &errb}
	r.handle(emitted(t, event.Error, event.ErrorPayload{
		Code: "context_too_large", Message: "contexte trop volumineux : impossible de le compacter sous la fenêtre du modèle",
	}))
	if errb.String() != "error (context_too_large): context too large: it cannot be compacted below the model's window\n" {
		t.Fatalf("stderr=%q", errb.String())
	}
}

// Le refus est décrit partie par partie masquée : une règle ancrée en fin de
// ligne ne reconnaîtrait pas un secret suivi des chemins.
func TestRefusalDescriptionRedactedPerPart(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("x"))
	red, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	mask := func(s string) string { out, _ := red.Redact(s); return out }
	var out, errb bytes.Buffer
	r := &renderer{eng: eng, out: Text, lang: i18n.FR, redact: mask, redactText: mask, stdout: &out, stderr: &errb}
	payload, _ := json.Marshal(event.PermissionRequestedPayload{
		PermissionID: "p1", Kind: "tool", Tool: "grep", Command: "api_key = Zx8kq2LmNp4Rt6Vw9YbC1dEf", Paths: []string{"src"},
	})
	r.handle(event.Event{Type: event.PermissionRequested, Payload: payload})
	if s := errb.String(); strings.Contains(s, "Zx8kq") || !strings.Contains(s, "[SECRET_") {
		t.Fatalf("stderr=%q", s)
	}
}

func TestHeadlessServerDownNotice(t *testing.T) {
	for lang, want := range map[i18n.Lang]string{
		i18n.FR: "serveur MCP gh indisponible : connexion refusée\n",
		i18n.EN: "MCP server gh unavailable: connexion refusée\n",
	} {
		var out, errb bytes.Buffer
		r := &renderer{out: Text, lang: lang, redact: func(s string) string { return s },
			redactText: func(s string) string { return s }, stdout: &out, stderr: &errb}
		r.handle(emitted(t, event.McpServerDown, event.McpServerDownPayload{Server: "gh", Error: "connexion refusée"}))
		if errb.String() != want {
			t.Fatalf("%s : stderr=%q, attendu %q", lang, errb.String(), want)
		}
	}
	// Texte masqué partie par partie et nettoyé des caractères de contrôle.
	red, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	mask := func(s string) string { out, _ := red.Redact(s); return out }
	var out, errb bytes.Buffer
	r := &renderer{out: Text, lang: i18n.FR, redact: mask, redactText: mask, stdout: &out, stderr: &errb}
	r.handle(emitted(t, event.McpServerDown, event.McpServerDownPayload{
		Server: "gh", Error: "\x1b[31mapi_key = Zx8kq2LmNp4Rt6Vw9YbC1dEf\nsuite\u202e",
	}))
	s := errb.String()
	if strings.Contains(s, "Zx8kq") || !strings.Contains(s, "[SECRET_") || strings.ContainsAny(s, "\x1b\u202e") ||
		strings.Count(s, "\n") != 1 {
		t.Fatalf("stderr=%q", s)
	}
}

func TestBannerListsMCP(t *testing.T) {
	servers := []event.MCPServerInfo{{ID: "gh", Jurisdiction: llm.NonEU, Country: "US"}, {ID: "docs", Jurisdiction: llm.EU, Country: "FR"}}
	for lang, want := range map[i18n.Lang]string{
		i18n.FR: "MCP : gh (hors UE), docs (UE)\n",
		i18n.EN: "MCP: gh (non-EU), docs (EU)\n",
	} {
		var out, errb bytes.Buffer
		r := &renderer{out: Text, lang: lang, redact: func(s string) string { return s },
			redactText: func(s string) string { return s }, stdout: &out, stderr: &errb}
		r.handle(emitted(t, event.SessionStarted, event.SessionStartedPayload{
			Provider: llm.ProviderInfo{ID: "t", Model: "m", Jurisdiction: llm.EU}, MCP: servers,
		}))
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("%s : stderr=%q, attendu %q", lang, errb.String(), want)
		}
	}
	// Sans serveur MCP actif, pas de ligne.
	var out, errb bytes.Buffer
	r := &renderer{out: Text, lang: i18n.FR, redact: func(s string) string { return s },
		redactText: func(s string) string { return s }, stdout: &out, stderr: &errb}
	r.handle(emitted(t, event.SessionStarted, event.SessionStartedPayload{Provider: llm.ProviderInfo{ID: "t", Model: "m", Jurisdiction: llm.EU}}))
	if strings.Contains(errb.String(), "MCP") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestTextIgnoresSubagentMessages(t *testing.T) {
	render := func(msgs ...event.AssistantMessagePayload) string {
		var out bytes.Buffer
		r := &renderer{
			out: Text, stdout: &out, stderr: &bytes.Buffer{},
			redact: func(s string) string { return s }, redactText: func(s string) string { return s },
		}
		for i, m := range msgs {
			ev, err := event.New("s_1", uint64(i+1), time.Now(), event.AssistantMessage, m)
			if err != nil {
				t.Fatal(err)
			}
			r.handle(ev)
		}
		if r.final != "" {
			out.WriteString(r.final + "\n")
		}
		return out.String()
	}
	sub := event.AssistantMessagePayload{Text: "sub text", ParentCallID: "call_1"}
	main := event.AssistantMessagePayload{Text: "main"}
	if got := render(sub, main); got != "main\n" {
		t.Fatalf("stdout = %q", got)
	}
	if got := render(main, sub); got != "main\n" {
		t.Fatalf("sous-agent après le principal : %q", got)
	}
	if got := render(sub); strings.Contains(got, "sub text") {
		t.Fatalf("stdout = %q", got)
	}
}

// feed passe les événements au renderer et renvoie (refused, stderr).
func feed(t *testing.T, lang i18n.Lang, evs ...any) (bool, string) {
	t.Helper()
	var errb bytes.Buffer
	r := &renderer{hookLabels: map[string]string{}, out: Text, lang: lang,
		redact:     func(s string) string { return s },
		redactText: func(s string) string { return strings.ReplaceAll(s, "SECRET", "[X]") },
		stdout:     &bytes.Buffer{}, stderr: &errb}
	for i := 0; i < len(evs); i += 2 {
		ev, err := event.New("s", uint64(i), time.Now(), evs[i].(event.Type), evs[i+1])
		if err != nil {
			t.Fatal(err)
		}
		r.handle(ev)
	}
	return r.refused, errb.String()
}

func TestHookEventsText(t *testing.T) {
	_, got := feed(t, i18n.EN,
		event.HookStarted, event.HookStartedPayload{RunID: "r1", HookID: "tenon:Stop:0.0", Type: "command", Command: "/usr/bin/lint --x"},
		event.HookCompleted, event.HookCompletedPayload{RunID: "r1", HookID: "tenon:Stop:0.0", Status: "error", Error: "boom\x1b[31m\nSECRET"},
		event.HookStarted, event.HookStartedPayload{RunID: "r2", HookID: "tenon:Stop:0.1", Type: "prompt"},
		event.HookCompleted, event.HookCompletedPayload{RunID: "r2", HookID: "tenon:Stop:0.1", Status: "timeout", Error: "late"},
		event.HookCompleted, event.HookCompletedPayload{RunID: "r3", HookID: "tenon:PreToolUse:0.0", Status: "blocked", Reason: "no\u202e way", SystemMessage: "heads up", StopReason: "enough"},
	)
	for _, want := range []string{
		"hook lint (error): boom [31m [X]",
		"hook prompt (timeout): late",
		"hook tenon:PreToolUse:0.0 blocked: no  way",
		"hook: heads up",
		"hook stopped the turn: enough",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q absent : %q", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b\u202e") || strings.Contains(got, "SECRET") {
		t.Fatalf("texte non nettoyé : %q", got)
	}
}

func TestHookTurnReasons(t *testing.T) {
	refused, got := feed(t, i18n.EN, event.TurnCompleted, event.TurnCompletedPayload{Reason: "blocked_by_hook"})
	if !refused || !strings.Contains(got, "prompt blocked by a hook") {
		t.Fatalf("refused=%v %q", refused, got)
	}
	refused, got = feed(t, i18n.EN, event.TurnCompleted, event.TurnCompletedPayload{Reason: "stopped_by_hook"})
	if refused || !strings.Contains(got, "turn ended: stopped_by_hook") {
		t.Fatalf("refused=%v %q", refused, got)
	}
	refused, _ = feed(t, i18n.EN, event.ToolResult, event.ToolResultPayload{Denied: "hook"})
	if !refused {
		t.Fatal("denied hook non refusé")
	}
}

// strictRun rejoue des événements dans un renderer et applique exitCode.
func strictRun(t *testing.T, out Output, strict bool, evs ...any) (int, string, string) {
	t.Helper()
	var outb, errb bytes.Buffer
	r := &renderer{hookLabels: map[string]string{}, out: out, lang: i18n.EN,
		redact:     func(s string) string { return s },
		redactText: func(s string) string { return s },
		stdout:     &outb, stderr: &errb}
	for i := 0; i < len(evs); i += 2 {
		ev, err := event.New("s", uint64(i), time.Now(), evs[i].(event.Type), evs[i+1])
		if err != nil {
			t.Fatal(err)
		}
		r.handle(ev)
	}
	return r.exitCode(strict), outb.String(), errb.String()
}

func TestStrictReasons(t *testing.T) {
	for _, reason := range []string{"max_iterations", "incomplete", "stopped_by_hook", "cancelled"} {
		code, _, errb := strictRun(t, Text, true, event.TurnCompleted, event.TurnCompletedPayload{Reason: reason})
		if code != 4 || !strings.Contains(errb, "strict: turn ended with "+reason) {
			t.Fatalf("%s strict: code=%d %q", reason, code, errb)
		}
		code, _, errb = strictRun(t, Text, false, event.TurnCompleted, event.TurnCompletedPayload{Reason: reason})
		if code != 0 || strings.Contains(errb, "strict:") || !strings.Contains(errb, "turn ended: "+reason) {
			t.Fatalf("%s non strict: code=%d %q", reason, code, errb)
		}
	}
	if code, _, errb := strictRun(t, Text, true, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}); code != 0 || strings.Contains(errb, "strict:") {
		t.Fatalf("done: code=%d %q", code, errb)
	}
}

func TestStrictRefusedWins(t *testing.T) {
	code, _, _ := strictRun(t, Text, true,
		event.ToolResult, event.ToolResultPayload{Denied: "hook"},
		event.TurnCompleted, event.TurnCompletedPayload{Reason: "incomplete"})
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
	code, _, _ = strictRun(t, Text, true,
		event.TurnCompleted, event.TurnCompletedPayload{Reason: "context_too_large"})
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
}

func TestStrictJSON(t *testing.T) {
	code, out, errb := strictRun(t, JSON, true,
		event.AssistantMessage, event.AssistantMessagePayload{Text: "x"},
		event.TurnCompleted, event.TurnCompletedPayload{Reason: "incomplete"})
	if code != 4 || strings.Contains(errb, "strict:") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("stdout vide")
	}
	for _, l := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("ligne non JSON %q : %v", l, err)
		}
	}
}

// recShell est un faux outil shell qui compte ses exécutions.
type recShell struct {
	mu  sync.Mutex
	ran int
}

func (s *recShell) Name() string            { return "shell" }
func (s *recShell) Description() string     { return "faux shell" }
func (s *recShell) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *recShell) Risk() tools.Risk        { return tools.Exec }
func (s *recShell) Target(args json.RawMessage) (tools.Target, error) {
	var a struct {
		Command string `json:"command"`
	}
	err := json.Unmarshal(args, &a)
	return tools.Target{Command: a.Command}, err
}
func (s *recShell) Run(context.Context, json.RawMessage) (tools.Result, error) {
	s.mu.Lock()
	s.ran++
	s.mu.Unlock()
	return tools.Result{Content: "ok"}, nil
}

// Review Focus 5 : une règle ask avec network ne s'ouvre jamais sans réponse
// humaine ; headless refuse la demande comme d'habitude (code 3).
func TestHeadlessAskNetworkRule(t *testing.T) {
	root := t.TempDir()
	perm, err := policy.NewPermissions(root, []policy.Rule{
		{Tool: "shell", Match: "curl *", Decision: policy.Ask, Network: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	sh := &recShell{}
	eng := engine.New(engine.Deps{
		Provider: &scripted{
			info:   llm.ProviderInfo{ID: "t", Name: "Test SA", Model: "m-1", Country: "FR", Jurisdiction: llm.EU},
			script: [][]llm.Chunk{call("shell", `{"command":"curl https://example.eu"}`), text("fini")},
		},
		Tools:          tools.NewRegistry(sh),
		Permissions:    perm,
		Outbound:       policy.NewOutbound(r),
		ProjectRoot:    root,
		ShellSandboxed: true,
	})
	var out, errb bytes.Buffer
	code := Run(context.Background(), eng, "s_1", engine.Input{Text: "télécharge"}, Options{Output: JSON}, &out, &errb)
	if code != 3 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if sh.ran != 0 {
		t.Fatalf("shell exécuté %d fois", sh.ran)
	}
	sc := bufio.NewScanner(&out)
	resolved := 0
	for sc.Scan() {
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("ligne %q : %v", sc.Text(), err)
		}
		if e.Type != event.PermissionResolved {
			continue
		}
		resolved++
		var p event.PermissionResolvedPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Allowed || p.Network {
			t.Fatalf("PermissionResolved %+v", p)
		}
	}
	if resolved != 1 {
		t.Fatalf("%d PermissionResolved, attendu 1", resolved)
	}
}

func tokensRenderer(lang i18n.Lang) (*renderer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return &renderer{out: Text, lang: lang, redact: func(s string) string { return s },
		redactText: func(s string) string { return s }, stdout: &out, stderr: &errb}, &errb
}

func TestTokensLineAtTurnEnd(t *testing.T) {
	r, errb := tokensRenderer(i18n.FR)
	r.sessionTokens = 1000 // reprise : total d'avant ce tour
	r.handle(emitted(t, event.Usage, event.UsagePayload{Purpose: "turn", Input: 100, Output: 20, Cached: 40}))
	r.handle(emitted(t, event.Usage, event.UsagePayload{Purpose: "compaction", Input: 10, Output: 5, Cached: 0}))
	r.handle(emitted(t, event.TurnCompleted, event.TurnCompletedPayload{Iterations: 1, Reason: "done"}))
	want := "tokens: in 110 · out 25 · cached 40 · session 1135\n"
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("stderr=%q, attendu %q", errb.String(), want)
	}
	// Tour suivant : sommes remises à zéro, session cumulée.
	r.handle(emitted(t, event.Usage, event.UsagePayload{Purpose: "turn", Input: 1, Output: 2}))
	r.handle(emitted(t, event.TurnCompleted, event.TurnCompletedPayload{Iterations: 1, Reason: "done"}))
	if !strings.Contains(errb.String(), "tokens: in 1 · out 2 · cached 0 · session 1138\n") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestNoTokensLineWithoutUsageOrInJSON(t *testing.T) {
	r, errb := tokensRenderer(i18n.EN)
	r.handle(emitted(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}))
	if strings.Contains(errb.String(), "tokens:") {
		t.Fatalf("stderr=%q", errb.String())
	}
	var out, jerr bytes.Buffer
	j := &renderer{out: JSON, lang: i18n.EN, redact: func(s string) string { return s },
		redactText: func(s string) string { return s }, stdout: &out, stderr: &jerr}
	j.handle(emitted(t, event.Usage, event.UsagePayload{Input: 1, Output: 2}))
	j.handle(emitted(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}))
	if strings.Contains(jerr.String(), "tokens:") || strings.Count(out.String(), "\n") != 2 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), jerr.String())
	}
}

func TestBudgetWarningNotice(t *testing.T) {
	r, errb := tokensRenderer(i18n.EN)
	r.handle(emitted(t, event.BudgetWarning, event.BudgetWarningPayload{Used: 800, Max: 1000}))
	if !strings.Contains(errb.String(), "budget: 800 of 1000 tokens used (80 %)\n") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestBudgetExceededStrictExit4(t *testing.T) {
	for _, strict := range []bool{true, false} {
		r, errb := tokensRenderer(i18n.EN)
		r.handle(emitted(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "budget_exceeded"}))
		code := r.exitCode(strict)
		if want := map[bool]int{true: 4, false: 0}[strict]; code != want {
			t.Fatalf("strict=%v code=%d", strict, code)
		}
		if strict && !strings.Contains(errb.String(), "strict: turn ended with budget_exceeded") {
			t.Fatalf("stderr=%q", errb.String())
		}
	}
}
