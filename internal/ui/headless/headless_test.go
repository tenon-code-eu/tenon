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

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/policy"
	"codeberg.org/tenon/tenon/internal/tools"
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
	code := Run(context.Background(), eng, "s_1", "salut", Options{Output: Text}, &out, &errb)
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
	code := Run(context.Background(), eng, "s_1", "écris", Options{Output: Text}, &out, &errb)
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
	code := Run(context.Background(), eng, "s_1", "salut", Options{Output: Text}, &out, &errb)
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
	if code := Run(context.Background(), eng, "s_1", "salut", Options{Output: JSON}, &out, &errb); code != 0 {
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
	if code := Run(context.Background(), eng, "s_1", "salut", Options{Output: Text}, &out, &errb); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if errb.Len() == 0 {
		t.Fatal("stderr vide")
	}
}

func TestJurisdictionLabel(t *testing.T) {
	for j, want := range map[llm.Jurisdiction]string{llm.EU: "UE", llm.NonEU: "hors UE", llm.Local: "locale"} {
		if got := jurisdictionLabel(j); got != want {
			t.Fatalf("%s : %q", j, got)
		}
	}
}

func TestPolicyDenyExit3(t *testing.T) {
	eng, _, root := newEngineRules(t, llm.EU, []policy.Rule{{Tool: "write_file", Decision: policy.Deny}},
		call("write_file", `{"path":"x.txt","content":"a"}`), text("fini"))
	var out, errb bytes.Buffer
	code := Run(context.Background(), eng, "s_1", "écris", Options{Output: Text}, &out, &errb)
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
	if code := Run(context.Background(), eng, "s_1", "lis", Options{Output: Text}, &out, &errb); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}

func TestJSONOutputRedacted(t *testing.T) {
	eng, _, _ := newEngine(t, llm.EU, text("ok"))
	var out, errb bytes.Buffer
	redact := func(s string) string { return strings.ReplaceAll(s, "SECRET", "[X]") }
	code := Run(context.Background(), eng, "s_1", "mon SECRET", Options{Output: JSON, Redact: redact}, &out, &errb)
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
	code := Run(context.Background(), eng, "s_1", "mon SECRET", Options{Output: JSON, Redact: broken}, &out, &errb)
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
