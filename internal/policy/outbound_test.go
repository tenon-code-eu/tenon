package policy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codeberg.org/tenon/tenon/internal/llm"
)

func newOut(t *testing.T) *Outbound {
	t.Helper()
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewOutbound(r)
}

func TestOutboundNonEUWithoutConsent(t *testing.T) {
	o := newOut(t)
	_, _, err := o.Check(llm.ProviderInfo{ID: "x", Jurisdiction: llm.NonEU}, []llm.Message{{Role: llm.User, Content: "hi"}})
	if !errors.Is(err, ErrJurisdiction) {
		t.Fatalf("err = %v", err)
	}
}

func TestOutboundNonEUAfterConsent(t *testing.T) {
	o := newOut(t)
	o.Consent("x")
	out, _, err := o.Check(llm.ProviderInfo{ID: "x", Jurisdiction: llm.NonEU}, []llm.Message{{Role: llm.User, Content: "hi"}})
	if err != nil || len(out) != 1 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if _, _, err := o.Check(llm.ProviderInfo{ID: "y", Jurisdiction: llm.NonEU}, nil); !errors.Is(err, ErrJurisdiction) {
		t.Fatal("consent must be per provider")
	}
}

func TestOutboundEUAndLocalPass(t *testing.T) {
	o := newOut(t)
	for _, j := range []llm.Jurisdiction{llm.EU, llm.Local} {
		if _, _, err := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: j}, nil); err != nil {
			t.Errorf("%s: %v", j, err)
		}
	}
}

func TestOutboundUnknownJurisdictionFailsClosed(t *testing.T) {
	o := newOut(t)
	if _, _, err := o.Check(llm.ProviderInfo{ID: "p"}, nil); !errors.Is(err, ErrJurisdiction) {
		t.Fatalf("err = %v", err)
	}
}

func TestOutboundRedactsContentAndArgs(t *testing.T) {
	o := newOut(t)
	orig := []llm.Message{
		{Role: llm.User, Content: "mail jean@exemple.fr"},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "sh", Args: json.RawMessage(`{"cmd":"echo jean@exemple.fr"}`)}}},
	}
	out, counts, err := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: llm.EU}, orig)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Content != "mail [EMAIL_1]" {
		t.Errorf("content = %q", out[0].Content)
	}
	if string(out[1].ToolCalls[0].Args) != `{"cmd":"echo [EMAIL_1]"}` {
		t.Errorf("args = %s", out[1].ToolCalls[0].Args)
	}
	if counts["EMAIL"] != 2 {
		t.Errorf("counts = %v", counts)
	}
	if orig[0].Content != "mail jean@exemple.fr" || string(orig[1].ToolCalls[0].Args) != `{"cmd":"echo jean@exemple.fr"}` {
		t.Error("original mutated")
	}
}

func TestOutboundPrivateKeyInArgsJSON(t *testing.T) {
	o := newOut(t)
	key := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\nxyz\n-----END RSA PRIVATE KEY-----"
	raw, _ := json.Marshal(map[string]string{"content": key})
	if !strings.Contains(string(raw), `\n`) {
		t.Fatal("setup")
	}
	msgs := []llm.Message{{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{Name: "write", Args: raw}}}}
	out, _, err := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: llm.EU}, msgs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out[0].ToolCalls[0].Args)
	if strings.Contains(got, "MIIabc") || strings.Contains(got, "PRIVATE") || !json.Valid([]byte(got)) {
		t.Fatalf("args = %s", got)
	}
}

func TestOutboundTruncatedPrivateKeyInArgs(t *testing.T) {
	o := newOut(t)
	raw := json.RawMessage(`{"content":"-----BEGIN PRIVATE KEY-----\nMIIabcDEF123\nxyz"}`)
	out, _, _ := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: llm.EU}, []llm.Message{{ToolCalls: []llm.ToolCall{{Args: raw}}}})
	got := string(out[0].ToolCalls[0].Args)
	if strings.Contains(got, "MIIabc") || !json.Valid([]byte(got)) {
		t.Fatalf("args = %s", got)
	}
}

func TestOutboundKeepsArgsValidJSON(t *testing.T) {
	o := newOut(t)
	raw := json.RawMessage(`{"cmd":"export TOKEN=Zx8kq2LmNp4Rt6Vw\"; ls"}`)
	out, _, _ := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: llm.EU}, []llm.Message{{ToolCalls: []llm.ToolCall{{Args: raw}}}})
	got := out[0].ToolCalls[0].Args
	if !json.Valid(got) || strings.Contains(string(got), "Zx8kq2LmNp4Rt6Vw") {
		t.Fatalf("args = %s", got)
	}
}

func TestOutboundFallbackMasksKeyedValue(t *testing.T) {
	o := newOut(t)
	raw := json.RawMessage(`{"password":"hunter2hunter2","cmd":"export TOKEN=Zx8kq2LmNp4Rt6Vw\"; ls"}`)
	out, _, _ := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: llm.EU}, []llm.Message{{ToolCalls: []llm.ToolCall{{Args: raw}}}})
	got := string(out[0].ToolCalls[0].Args)
	if !json.Valid([]byte(got)) || strings.Contains(got, "hunter2") || strings.Contains(got, "Zx8kq2") {
		t.Fatalf("args = %s", got)
	}
}

func TestOutboundWalkMasksKeyedValue(t *testing.T) {
	o := newOut(t)
	counts := map[string]int{}
	v := o.walk(map[string]any{"password": "correcthorse", "note": "hello"}, counts)
	m := v.(map[string]any)
	if m["password"] != "[SECRET_1]" || m["note"] != "hello" || counts["SECRET"] != 1 {
		t.Fatalf("got %v %v", m, counts)
	}
}

func TestOutboundEscapedQuotedKeyInArgs(t *testing.T) {
	o := newOut(t)
	raw := json.RawMessage(`{"content":"{\"api_key\": \"Zx8kq2LmNp4Rt6Vw9YbC\"}"}`)
	out, _, _ := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: llm.EU}, []llm.Message{{ToolCalls: []llm.ToolCall{{Args: raw}}}})
	got := string(out[0].ToolCalls[0].Args)
	if !json.Valid([]byte(got)) || strings.Contains(got, "Zx8kq2") {
		t.Fatalf("args = %s", got)
	}
}

func TestOutboundMasksEscapedNewlineLines(t *testing.T) {
	o := newOut(t)
	for _, raw := range []string{
		`{"path":"a.env","content":"API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf\n"}`,
		`{"path":"a.yml","content":"x: 1\napi_key = Zx8kq2LmNp4Rt6Vw9YbC1dEf\ny: 2"}`,
	} {
		msgs := []llm.Message{{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{Name: "write_file", Args: json.RawMessage(raw)}}}}
		out, counts, err := o.Check(llm.ProviderInfo{ID: "p", Jurisdiction: llm.EU}, msgs)
		if err != nil {
			t.Fatal(err)
		}
		got := string(out[0].ToolCalls[0].Args)
		if strings.Contains(got, "Zx8kq") || !json.Valid([]byte(got)) || counts["SECRET"] != 1 {
			t.Fatalf("%s -> %s %v", raw, got, counts)
		}
	}
}
