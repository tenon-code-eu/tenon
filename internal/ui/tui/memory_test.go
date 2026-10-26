package tui

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/memory"
)

var regexpDate = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

func memStore(t *testing.T) *memory.Store {
	t.Helper()
	return memory.Open(memory.Options{Root: t.TempDir(), DataDir: t.TempDir(), Project: true})
}

func TestRememberScopes(t *testing.T) {
	for _, c := range []struct {
		in    string
		scope memory.Scope
		text  string
	}{
		{"/remember build passe par make", memory.User, "build passe par make"},
		{"/remember --project build passe par make", memory.Project, "build passe par make"},
		{"/remember --global a b", memory.Global, "a b"},
		{"/remember x --project", memory.User, "x --project"},
	} {
		f := newFake()
		h := newHarness(t, f, Options{Lang: i18n.EN, Memory: memStore(t)})
		typeText(h, c.in)
		enter(h)
		if len(f.remembered) != 1 || f.remembered[0].scope != c.scope || f.remembered[0].text != c.text {
			t.Fatalf("%q : %+v", c.in, f.remembered)
		}
		if n := notices(h); !strings.Contains(n, "memory: saved "+string(c.scope)+"/fake-id") || !strings.Contains(n, "memory: note one") {
			t.Fatalf("avis %q", n)
		}
	}
}

func TestRememberUsageAndDisabled(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN, Memory: memStore(t)})
	typeText(h, "/remember --project   ")
	enter(h)
	if len(f.remembered) != 0 || !strings.Contains(notices(h), "usage: /remember") {
		t.Fatalf("%+v %q", f.remembered, notices(h))
	}
	for _, by := range []string{"policy", "config"} {
		h := newHarness(t, newFake(), Options{Lang: i18n.EN, MemoryDisabledBy: by})
		typeText(h, "/remember abc")
		enter(h)
		typeText(h, "/memory")
		enter(h)
		if n := notices(h); strings.Count(n, "memory: disabled by "+by) != 2 {
			t.Fatalf("avis %q", n)
		}
	}
	f2 := newFake()
	f2.rememberErr = errors.New("memory: entry too large (5000 bytes, max 4096)")
	h = newHarness(t, f2, Options{Lang: i18n.EN, Memory: memStore(t)})
	typeText(h, "/remember abc")
	enter(h)
	if !strings.Contains(notices(h), "entry too large") {
		t.Fatalf("avis %q", notices(h))
	}
}

func TestMemoryList(t *testing.T) {
	st := memStore(t)
	if _, err := st.Put(memory.Entry{Scope: memory.User, ID: "a", Title: "build make", Body: "b", Author: "user"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(memory.Entry{Scope: memory.Global, ID: "s", Title: "secret title", Body: "b", Author: "user", Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(memory.Entry{Scope: memory.Project, ID: "p", Title: "proj", Body: "b", Author: "user"}); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, Memory: st})
	typeText(h, "/memory")
	enter(h)
	n := notices(h)
	if strings.Contains(n, "secret title") || strings.Contains(n, "\x1b") {
		t.Fatalf("avis %q", n)
	}
	for _, want := range []string{"[global] s  (sensitive)  ", "[project] p  proj  —", "[user] a  build"} {
		if !strings.Contains(n, want) {
			t.Fatalf("manque %q dans %q", want, n)
		}
	}
	if !regexpDate.MatchString(n) {
		t.Fatalf("échéance absente %q", n)
	}
	h2 := newHarness(t, newFake(), Options{Lang: i18n.FR, Memory: memStore(t)})
	typeText(h2, "/memory")
	enter(h2)
	if n := notices(h2); !strings.Contains(n, "aucune entrée") {
		t.Fatalf("avis %q", n)
	}
}

func rewindMemory(t *testing.T, lang i18n.Lang, memSeq uint64) string {
	f := rwFake()
	h := newHarness(t, f, Options{Lang: lang, ProjectRoot: "/p"})
	if memSeq > 0 {
		e, err := event.New("s", memSeq, time.Now(), event.MemoryWritten, event.MemoryWrittenPayload{Scope: "user", ID: "a", SHA256: "ff", Author: "model"})
		if err != nil {
			t.Fatal(err)
		}
		f.ch <- e
		h.until("événement lu", func() bool { return len(h.m.memSeqs) == 1 })
	}
	typeText(h, "/rewind")
	enter(h)
	enter(h) // point #2 (seq 5)
	return ansi.Strip(h.m.View())
}

func TestRewindMemoryNotice(t *testing.T) {
	if v := rewindMemory(t, i18n.EN, 7); !strings.Contains(v, "memory entries are not rewound") {
		t.Fatalf("vue %q", v)
	}
	if v := rewindMemory(t, i18n.FR, 7); !strings.Contains(v, "les entrées de mémoire ne sont pas restaurées") {
		t.Fatalf("vue %q", v)
	}
	// Écrite avant le point : hors de la plage restaurée.
	if v := rewindMemory(t, i18n.EN, 3); strings.Contains(v, "memory entries are not rewound") {
		t.Fatalf("avis hors plage %q", v)
	}
	if v := rewindMemory(t, i18n.EN, 0); strings.Contains(v, "memory entries are not rewound") {
		t.Fatalf("avis sans écriture %q", v)
	}
}

func TestMemoryWriteDiffInPanel(t *testing.T) {
	st := memStore(t)
	e, err := st.Put(memory.Entry{Scope: memory.User, ID: "a", Title: "t", Body: "un\nancien\n", Author: "user"})
	if err != nil {
		t.Fatal(err)
	}
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN, Memory: st, Redact: func(s string) string { return strings.ReplaceAll(s, "SECRET", "[REDACTED]") }})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	args, _ := json.Marshal(map[string]string{"scope": "user", "id": "a", "title": "t", "content": "un\nnouveau SECRET\n"})
	askPerm(t, h, f, event.PermissionRequestedPayload{
		PermissionID: "m1", Kind: "tool", Tool: "memory_write", Args: args, Paths: []string{st.Path(e.Scope, e.ID)},
	})
	v := h.m.View()
	for _, want := range []string{"- ancien", "+ nouveau [REDACTED]"} {
		if !strings.Contains(v, want) {
			t.Fatalf("vue sans %q : %q", want, v)
		}
	}
	if strings.Contains(v, "SECRET") || strings.Contains(v, "title:") {
		t.Fatalf("fuite %q", v)
	}
	// Nouvelle entrée : tout est ajouté.
	args2, _ := json.Marshal(map[string]string{"content": "a\nb\n"})
	f2 := newFake()
	h2 := newHarness(t, f2, Options{Lang: i18n.EN})
	h2.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	askPerm(t, h2, f2, event.PermissionRequestedPayload{
		PermissionID: "m2", Kind: "tool", Tool: "memory_write", Args: args2, Paths: []string{filepath.Join(t.TempDir(), "none.md")},
	})
	if v := h2.m.View(); !strings.Contains(v, "+ a") || !strings.Contains(v, "+ b") {
		t.Fatalf("vue %q", v)
	}
}
