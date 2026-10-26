package compliance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func memStart(b *logb, sid string) {
	b.add(sid, event.SessionStarted, event.SessionStartedPayload{Kind: "memory", ProjectRoot: "/p"})
}

func TestExportMemory(t *testing.T) {
	b := newLog(t)
	b.start("s1", provEU)
	b.add("s1", event.MemoryWritten, event.MemoryWrittenPayload{Scope: "project", ID: "build-cmd", SHA256: "aa", Author: "model"})
	b.add("s1", event.MemoryWritten, event.MemoryWrittenPayload{Scope: "user", ID: "token-note", SHA256: "bb", Author: "user", Sensitive: true, Redacted: true})
	b.add("s1", event.MemoryRead, event.MemoryReadPayload{Scope: "project", ID: "build-cmd"})
	b.add("s1", event.MemoryRead, event.MemoryReadPayload{Scope: "user", ID: "token-note", Sensitive: true})
	b.add("s1", event.MemoryRead, event.MemoryReadPayload{Scope: "user", ID: "token-note", Sensitive: true})
	b.add("s1", event.MemoryForgotten, event.MemoryForgottenPayload{Scope: "project", ID: "old", By: "model"})
	b.add("s1", event.MemoryTrusted, event.MemoryTrustedPayload{ID: "build-cmd", SHA256: "aa"})
	// Sessions courtes « memory » : hors conversations.
	memStart(b, "m1")
	b.add("m1", event.MemoryForgotten, event.MemoryForgottenPayload{Scope: "user", ID: "stale", By: "expiry"})
	b.add("m1", event.MemoryForgotten, event.MemoryForgottenPayload{Scope: "user", ID: "x", By: "user"})
	b.add("m1", event.MemoryForgotten, event.MemoryForgottenPayload{Scope: "global", ID: "y", By: "user"})
	// Charges brutes portant des clés title et content (journal écrit par une
	// autre version, ou à la main) : elles ne passent jamais dans le rapport.
	b.add("s1", event.MemoryWritten, json.RawMessage(`{"scope":"user","id":"n1","sha256":"cc","author":"model","title":"ZQX-TITLE-7731","content":"ZQX-CONTENT-9942"}`))
	b.add("s1", event.MemoryRead, json.RawMessage(`{"scope":"user","id":"n1","sensitive":false,"title":"ZQX-TITLE-7731","content":"ZQX-CONTENT-9942"}`))
	r := Build(b.dataset(), opts())

	if r.Memory == nil {
		t.Fatal("section memory absente")
	}
	want := &Memory{
		Writes: []MemoryWrite{
			{Scope: "project", ID: "build-cmd", Author: "model"},
			{Scope: "user", ID: "token-note", Author: "user", Sensitive: true, Redacted: true},
			{Scope: "user", ID: "n1", Author: "model"},
		},
		SensitiveReads: 2,
		Forgotten:      MemoryForgotten{User: 2, Model: 1, Expiry: 1},
		Trusted:        1,
	}
	if !reflect.DeepEqual(r.Memory, want) {
		t.Fatalf("memory = %+v", r.Memory)
	}
	if r.Summary.Sessions != 1 || r.Summary.UnknownProviderSessions != 0 || len(r.Providers) != 1 {
		t.Fatalf("stats %+v providers %+v", r.Summary, r.Providers)
	}
	js, _ := json.Marshal(r.Memory)
	if !strings.Contains(string(js), `"sensitive_reads":2`) || !strings.Contains(string(js), `"forgotten":{"user":2,"model":1,"expiry":1}`) {
		t.Fatalf("JSON : %s", js)
	}
	// Ni titre ni contenu : ni le JSON complet ni le HTML ne portent les
	// valeurs des clés title et content, ni l'empreinte.
	all, _ := json.Marshal(r)
	fr, en := render(t, r, i18n.FR), render(t, r, i18n.EN)
	for _, bad := range []string{"ZQX-TITLE-7731", "ZQX-CONTENT-9942", `"sha256":"aa"`} {
		for name, out := range map[string]string{"JSON": string(all), "HTML FR": fr, "HTML EN": en} {
			if strings.Contains(out, bad) {
				t.Fatalf("%q dans le %s", bad, name)
			}
		}
	}
	if !strings.Contains(fr, "<dt>Mémoire</dt>") || !strings.Contains(fr, "écritures : 3,") {
		t.Fatalf("FR : %s", fr)
	}
	if !strings.Contains(en, "<dt>Memory</dt>") || !strings.Contains(en, "writes: 3,") {
		t.Fatalf("EN : %s", en)
	}
}

// Un compte de 1 se lit sans faute d'accord (« writes: 1 », pas « 1 writes »).
func TestExportMemoryCountsSingular(t *testing.T) {
	b := newLog(t)
	b.start("s1", provEU)
	b.add("s1", event.MemoryWritten, event.MemoryWrittenPayload{Scope: "user", ID: "a", SHA256: "aa", Author: "user"})
	r := Build(b.dataset(), opts())
	fr, en := render(t, r, i18n.FR), render(t, r, i18n.EN)
	if !strings.Contains(en, "writes: 1, sensitive reads: 0") || strings.Contains(en, "1 writes") {
		t.Fatalf("EN : %s", en)
	}
	if !strings.Contains(fr, "écritures : 1, lectures sensibles : 0") || strings.Contains(fr, "1 écritures") {
		t.Fatalf("FR : %s", fr)
	}
}

func TestExportMemoryAbsent(t *testing.T) {
	b := newLog(t)
	b.start("s1", provEU)
	r := Build(b.dataset(), opts())
	if r.Memory != nil {
		t.Fatalf("section présente : %+v", r.Memory)
	}
	js, _ := json.Marshal(r)
	if strings.Contains(string(js), `"memory"`) {
		t.Fatal("clé memory dans le JSON")
	}
	if out := render(t, r, i18n.EN); strings.Contains(out, "<dt>Memory</dt>") {
		t.Fatal("section rendue sans événement")
	}
}
