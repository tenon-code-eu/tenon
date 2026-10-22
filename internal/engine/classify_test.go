package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/classify"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/routing"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

const (
	refusedMsg  = "classified content requires a local provider (routing.local)"
	withheldMsg = "[classified content withheld: no local provider]"
	paieContent = "nom;salaire\nDupont;4200\n"
	docContent  = "# Note\n\nDIFFUSION\nRESTREINTE\n\nContenu du document.\n"
)

// withClassify active les règles de classification (rh/**) et installe un
// routeur, avec un fournisseur local si local est vrai : localView sur le
// même fournisseur factice, pour que h.prov enregistre toutes les requêtes.
func withClassify(local bool) func(*Deps) {
	return func(d *Deps) {
		d.Classify = classify.Rules{Paths: []string{"rh/**"}}
		cur := routing.Candidate{ID: d.Provider.Info().ID, Provider: d.Provider}
		var lc *routing.Candidate
		if local {
			lc = &routing.Candidate{ID: "local", Provider: localView{d.Provider}}
		}
		d.Router = routing.New(cur, lc, nil)
	}
}

// localView présente un fournisseur comme local (identifiant « local »).
type localView struct{ provider.Provider }

func (v localView) Info() llm.ProviderInfo {
	info := v.Provider.Info()
	info.ID, info.Jurisdiction = "local", llm.Local
	return info
}

// newClassifyHarness : outils fichiers et shell, projet avec rh/paie.csv
// (chemin classé) et doc.md (marquage).
func newClassifyHarness(t *testing.T, local bool, script [][]llm.Chunk, opts ...func(*Deps)) *harness {
	t.Helper()
	h := newHarness(t, euInfo, script, append([]func(*Deps){withShell, withClassify(local)}, opts...)...)
	writeProjectFiles(t, h.root)
	return h
}

func writeProjectFiles(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "rh"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, c := range map[string]string{"rh/paie.csv": paieContent, "doc.md": docContent, "libre.txt": "rien ici\n"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func classified(t *testing.T, evs []event.Event) []event.SessionClassifiedPayload {
	t.Helper()
	var out []event.SessionClassifiedPayload
	for _, e := range ofType(evs, event.SessionClassified) {
		out = append(out, decode[event.SessionClassifiedPayload](t, e))
	}
	return out
}

func (h *harness) sensitive(t *testing.T, sid string) bool {
	t.Helper()
	h.eng.mu.Lock()
	s, ok := h.eng.sessions[sid]
	h.eng.mu.Unlock()
	if !ok {
		t.Fatalf("session %s inconnue", sid)
	}
	return s.isSensitive()
}

// historyHas indique si un message de l'historique de session contient sub.
func (h *harness) historyHas(t *testing.T, sid, sub string) bool {
	t.Helper()
	h.eng.mu.Lock()
	s := h.eng.sessions[sid]
	h.eng.mu.Unlock()
	for _, m := range s.history {
		if strings.Contains(m.Content, sub) {
			return true
		}
	}
	return false
}

// requestsHave indique si une requête envoyée au fournisseur contient sub.
func (h *harness) requestsHave(sub string) bool {
	for _, r := range h.prov.reqs() {
		for _, m := range r.Messages {
			if strings.Contains(m.Content, sub) {
				return true
			}
		}
	}
	return false
}

func assertRefused(t *testing.T, evs []event.Event, tool string) {
	t.Helper()
	tr := toolResultOf(t, evs, tool)
	if tr.Denied != "classification" || tr.Content != refusedMsg || !tr.IsError {
		t.Fatalf("ToolResult %+v", tr)
	}
	if n := len(ofType(evs, event.SessionClassified)); n != 0 {
		t.Fatalf("%d SessionClassified sans local", n)
	}
}

func assertWithheld(t *testing.T, h *harness, evs []event.Event, tool, leaked string) {
	t.Helper()
	tr := toolResultOf(t, evs, tool)
	if tr.Content != withheldMsg {
		t.Fatalf("ToolResult %+v", tr)
	}
	if n := len(ofType(evs, event.SessionClassified)); n != 0 {
		t.Fatalf("%d SessionClassified sans local", n)
	}
	if h.sensitive(t, "s_1") {
		t.Fatal("session sensible sans local")
	}
	if h.historyHas(t, "s_1", leaked) || h.requestsHave(leaked) {
		t.Fatalf("contenu classé %q dans l'historique ou une requête", leaked)
	}
}

func TestReadClassifiedPath(t *testing.T) {
	script := [][]llm.Chunk{
		call("c1", "read_file", `{"path":"rh/paie.csv"}`),
		call("c2", "read_file", `{"path":"./rh/../rh/paie.csv"}`),
		text("ok"),
	}
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script)
		evs, err := h.submit(t, "s_1", "lis la paie", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "path", Source: "tool", CallID: "c1", Path: "rh/paie.csv"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
		if !h.sensitive(t, "s_1") {
			t.Fatal("session non sensible")
		}
		// Émis avant l'exécution de l'outil.
		var iSC, iTR int
		for i, e := range evs {
			switch {
			case e.Type == event.SessionClassified:
				iSC = i
			case e.Type == event.ToolResult && iTR == 0:
				iTR = i
			}
		}
		if iSC > iTR {
			t.Fatalf("SessionClassified après ToolResult : %v", types(evs))
		}
		if tr := toolResultOf(t, evs, "read_file"); !strings.Contains(tr.Content, "Dupont") {
			t.Fatalf("ToolResult %+v", tr)
		}
		// Second tour : jamais réémis.
		h.prov.script = append(h.prov.script, call("c3", "read_file", `{"path":"rh/paie.csv"}`), text("ok"))
		evs, err = h.submit(t, "s_1", "encore", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(ofType(evs, event.SessionClassified)); n != 0 {
			t.Fatalf("%d SessionClassified au second tour", n)
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script)
		evs, err := h.submit(t, "s_1", "lis la paie", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ofType(evs, event.ToolResult) {
			tr := decode[event.ToolResultPayload](t, e)
			if tr.Denied != "classification" || tr.Content != refusedMsg {
				t.Fatalf("ToolResult %+v", tr)
			}
		}
		assertRefused(t, evs, "read_file")
		if h.historyHas(t, "s_1", "Dupont") || h.requestsHave("Dupont") {
			t.Fatal("contenu lu")
		}
		if h.sensitive(t, "s_1") {
			t.Fatal("session sensible sans local")
		}
	})
}

func TestWriteClassifiedPath(t *testing.T) {
	script := [][]llm.Chunk{call("c1", "write_file", `{"path":"rh/nouveau.csv","content":"x"}`), text("ok")}
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script)
		evs, err := h.submit(t, "s_1", "écris", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		if len(sc) != 1 || sc[0].Reason != "path" || sc[0].Path != "rh/nouveau.csv" {
			t.Fatalf("SessionClassified %+v", sc)
		}
		if !exists(filepath.Join(h.root, "rh", "nouveau.csv")) {
			t.Fatal("fichier non écrit")
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script)
		evs, err := h.submit(t, "s_1", "écris", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, evs, "write_file")
		if exists(filepath.Join(h.root, "rh", "nouveau.csv")) {
			t.Fatal("fichier écrit")
		}
	})
	t.Run("marked content no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"note.txt","content":"Spécial  France"}`), text("ok"),
		})
		evs, err := h.submit(t, "s_1", "écris", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, evs, "write_file")
		if exists(filepath.Join(h.root, "note.txt")) {
			t.Fatal("fichier écrit")
		}
	})
}

func TestReadMarkedFile(t *testing.T) {
	script := [][]llm.Chunk{call("c1", "read_file", `{"path":"doc.md"}`), text("ok")}
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script)
		evs, err := h.submit(t, "s_1", "lis", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "marker", Source: "tool", CallID: "c1", Marker: "DIFFUSION RESTREINTE"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
		if tr := toolResultOf(t, evs, "read_file"); !strings.Contains(tr.Content, "Contenu du document") {
			t.Fatalf("ToolResult %+v", tr)
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script)
		evs, err := h.submit(t, "s_1", "lis", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "read_file", "Contenu du document")
		if tr := toolResultOf(t, evs, "read_file"); tr.Denied != "" {
			t.Fatalf("ToolResult %+v", tr)
		}
		// Le tour continue : le modèle reçoit le contenu retenu.
		if !h.requestsHave(withheldMsg) {
			t.Fatal("contenu retenu absent de la requête suivante")
		}
	})
	t.Run("partial no local", func(t *testing.T) {
		// Extrait d'un document marqué, sans la ligne du marquage.
		h := newClassifyHarness(t, false, [][]llm.Chunk{
			call("c1", "read_file", `{"path":"doc.md","offset":6,"limit":1}`), text("ok"),
		})
		evs, err := h.submit(t, "s_1", "lis", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "read_file", "Contenu du document")
	})
}

func TestShellClassified(t *testing.T) {
	t.Run("path no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{
			call("c1", "shell", `{"command":"touch ran.txt; cat rh/paie.csv"}`), text("ok"),
		})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, evs, "shell")
		if exists(filepath.Join(h.root, "ran.txt")) {
			t.Fatal("commande exécutée")
		}
	})
	t.Run("path local", func(t *testing.T) {
		h := newClassifyHarness(t, true, [][]llm.Chunk{
			call("c1", "shell", `{"command":"cat rh/paie.csv"}`), text("ok"),
		})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "path", Source: "shell", CallID: "c1", Path: "rh/paie.csv"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
	t.Run("marker no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{
			call("c1", "shell", `{"command":"cat doc.md"}`), text("ok"),
		})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "shell", "Contenu du document")
	})
	t.Run("marker local", func(t *testing.T) {
		h := newClassifyHarness(t, true, [][]llm.Chunk{
			call("c1", "shell", `{"command":"cat doc.md"}`), text("ok"),
		})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "marker", Source: "shell", CallID: "c1", Marker: "DIFFUSION RESTREINTE"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
}

func TestGrepClassified(t *testing.T) {
	t.Run("path local", func(t *testing.T) {
		h := newClassifyHarness(t, true, [][]llm.Chunk{call("c1", "grep", `{"pattern":"Dupont"}`), text("ok")})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "path", Source: "tool", CallID: "c1", Path: "rh/paie.csv"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
	t.Run("path no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{call("c1", "grep", `{"pattern":"Dupont"}`), text("ok")})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "grep", "4200")
	})
	t.Run("marker no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{call("c1", "grep", `{"pattern":"RESTREINTE|DIFFUSION"}`), text("ok")})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "grep", "RESTREINTE")
	})
	t.Run("unclassified", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{call("c1", "grep", `{"pattern":"rien"}`), text("ok")})
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		if tr := toolResultOf(t, evs, "grep"); !strings.Contains(tr.Content, "libre.txt:1:rien ici") {
			t.Fatalf("ToolResult %+v", tr)
		}
	})
}

func TestGlobNeverTaints(t *testing.T) {
	for _, local := range []bool{true, false} {
		h := newClassifyHarness(t, local, [][]llm.Chunk{call("c1", "glob", `{"pattern":"**/*"}`), text("ok")})
		if err := os.WriteFile(filepath.Join(h.root, "DIFFUSION RESTREINTE.txt"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		tr := toolResultOf(t, evs, "glob")
		if !strings.Contains(tr.Content, "rh/paie.csv") || !strings.Contains(tr.Content, "DIFFUSION RESTREINTE.txt") || tr.Denied != "" {
			t.Fatalf("local=%v ToolResult %+v", local, tr)
		}
		if n := len(ofType(evs, event.SessionClassified)); n != 0 || h.sensitive(t, "s_1") {
			t.Fatalf("local=%v : glob a classé la session", local)
		}
	}
}

// mcpTool est un outil MCP factice (préfixe de serveur) au résultat fixe.
type mcpTool struct{ content string }

func (mcpTool) Name() string            { return "mcp__docs__fetch" }
func (mcpTool) Description() string     { return "outil MCP factice" }
func (mcpTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (mcpTool) Risk() tools.Risk        { return tools.Read }
func (mcpTool) Target(json.RawMessage) (tools.Target, error) {
	return tools.Target{}, nil
}
func (m mcpTool) Run(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Content: m.content}, nil
}

func TestMCPResultMarker(t *testing.T) {
	withMCP := func(d *Deps) {
		d.Tools = tools.NewRegistry(mcpTool{content: "Rapport — Restreint UE — budget 12 M€"})
	}
	script := [][]llm.Chunk{call("c1", "mcp__docs__fetch", `{}`), text("ok")}
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script, withMCP)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "marker", Source: "mcp", CallID: "c1", Marker: "RESTREINT UE"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script, withMCP)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "mcp__docs__fetch", "budget")
	})
}

func TestUserMessageMarker(t *testing.T) {
	const msg = "résume ce texte : diffusion   restreinte, budget 12 M€"
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, [][]llm.Chunk{text("ok")})
		evs, err := h.submit(t, "s_1", msg, nil)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "marker", Source: "user", Marker: "DIFFUSION RESTREINTE"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
		if len(h.prov.reqs()) != 1 {
			t.Fatalf("%d requêtes", len(h.prov.reqs()))
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{text("ok")})
		evs, err := h.submit(t, "s_1", msg, nil)
		if err != nil {
			t.Fatal(err)
		}
		errs := ofType(evs, event.Error)
		if len(errs) != 1 || decode[event.ErrorPayload](t, errs[0]).Code != "classification" {
			t.Fatalf("events %v", types(evs))
		}
		if tc := lastTurn(t, evs); tc.Reason != "blocked" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
		if len(h.prov.reqs()) != 0 {
			t.Fatal("fournisseur appelé")
		}
		if h.historyHas(t, "s_1", "budget") {
			t.Fatal("message classé dans l'historique")
		}
		for _, e := range h.sink.events {
			if strings.Contains(string(e.Payload), "budget") {
				t.Fatalf("message classé journalisé : %s", e.Type)
			}
		}
		if h.sensitive(t, "s_1") {
			t.Fatal("session sensible sans local")
		}
	})
}

func TestHookRewriteToClassified(t *testing.T) {
	upd := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"file_path":"rh/paie.csv"}}}`
	script := [][]llm.Chunk{call("c1", "read_file", `{"path":"libre.txt"}`), text("ok")}
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script, withHooks(t, hk{"PreToolUse", "Read", jsonHook(t, upd)}))
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, evs, "read_file")
		if h.requestsHave("Dupont") {
			t.Fatal("contenu lu")
		}
	})
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script, withHooks(t, hk{"PreToolUse", "Read", jsonHook(t, upd)}))
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		if len(sc) != 1 || sc[0].Path != "rh/paie.csv" {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
}

func TestSymlinkClassified(t *testing.T) {
	script := [][]llm.Chunk{
		call("c1", "read_file", `{"path":"lien.csv"}`),
		call("c2", "read_file", `{"path":"dossier/paie.csv"}`),
		text("ok"),
	}
	link := func(t *testing.T, root string) {
		t.Helper()
		if err := os.Symlink(filepath.Join(root, "rh", "paie.csv"), filepath.Join(root, "lien.csv")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("rh", filepath.Join(root, "dossier")); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script)
		link(t, h.root)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		trs := ofType(evs, event.ToolResult)
		if len(trs) != 2 {
			t.Fatalf("events %v", types(evs))
		}
		for _, e := range trs {
			if tr := decode[event.ToolResultPayload](t, e); tr.Denied != "classification" || tr.Content != refusedMsg {
				t.Fatalf("ToolResult %+v", tr)
			}
		}
		if h.requestsHave("Dupont") {
			t.Fatal("contenu lu")
		}
	})
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script)
		link(t, h.root)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		if len(sc) != 1 || sc[0].Reason != "path" || sc[0].Path != "lien.csv" {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
}

func TestResumeSensitive(t *testing.T) {
	journal := func(t *testing.T) []event.Event {
		l := &evLog{t: t, sid: "s_1"}
		l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
			add(event.UserMessage, event.UserMessagePayload{Text: "lis la paie"}).
			add(event.SessionClassified, event.SessionClassifiedPayload{Reason: "path", Source: "tool", CallID: "c1", Path: "rh/paie.csv"}).
			add(event.AssistantMessage, event.AssistantMessagePayload{Text: "fait"}).
			add(event.TurnCompleted, event.TurnCompletedPayload{Iterations: 1, Reason: "done"})
		return l.evs
	}
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, [][]llm.Chunk{text("ok")})
		if err := h.eng.Restore("s_1", journal(t)); err != nil {
			t.Fatal(err)
		}
		if !h.sensitive(t, "s_1") {
			t.Fatal("session reprise non sensible")
		}
		evs, err := h.submit(t, "s_1", "et ensuite ?", nil)
		if err != nil {
			t.Fatal(err)
		}
		errs := ofType(evs, event.Error)
		if len(errs) != 1 || decode[event.ErrorPayload](t, errs[0]).Code != "classification" {
			t.Fatalf("events %v", types(evs))
		}
		if tc := lastTurn(t, evs); tc.Reason != "blocked" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
		if len(h.prov.reqs()) != 0 {
			t.Fatal("fournisseur appelé")
		}
		if n := len(ofType(evs, event.SessionClassified)); n != 0 {
			t.Fatalf("%d SessionClassified réémis", n)
		}
	})
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, [][]llm.Chunk{call("c2", "read_file", `{"path":"doc.md"}`), text("ok")})
		if err := h.eng.Restore("s_1", journal(t)); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "et ensuite ?", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		if len(h.prov.reqs()) != 2 {
			t.Fatalf("%d requêtes", len(h.prov.reqs()))
		}
		if n := len(ofType(evs, event.SessionClassified)); n != 0 {
			t.Fatalf("%d SessionClassified réémis", n)
		}
	})
}

func TestSubAgentTaintsParent(t *testing.T) {
	script := [][]llm.Chunk{
		call("p1", "agent", `{"description":"paie","prompt":"lis rh/paie.csv"}`),
		call("s1", "read_file", `{"path":"rh/paie.csv"}`),
		text("résumé du sous-agent"),
		text("fin"),
	}
	t.Run("local", func(t *testing.T) {
		h := newAgentEngine(t, script, nil, withClassify(true))
		writeProjectFiles(t, h.root)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "path", Source: "tool", CallID: "s1", Path: "rh/paie.csv"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
		if !h.sensitive(t, "s_1") {
			t.Fatal("session parente non sensible")
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newAgentEngine(t, script, nil, withClassify(false))
		writeProjectFiles(t, h.root)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, evs, "read_file")
		if h.requestsHave("Dupont") {
			t.Fatal("contenu lu par le sous-agent")
		}
	})
}

func TestGrepMarkedFileOtherLine(t *testing.T) {
	// La ligne trouvée ne contient pas le marquage ; le fichier, si.
	script := [][]llm.Chunk{call("c1", "grep", `{"pattern":"Contenu"}`), text("ok")}
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		want := event.SessionClassifiedPayload{Reason: "marker", Source: "tool", CallID: "c1", Marker: "DIFFUSION RESTREINTE"}
		if len(sc) != 1 || sc[0] != want {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script)
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "grep", "Contenu du document")
	})
}

func TestLargeMarkedFile(t *testing.T) {
	big := func(t *testing.T, root string) {
		t.Helper()
		var b strings.Builder
		b.WriteString("Entête du rapport\n")
		line := strings.Repeat("x", 99) + "\n"
		for b.Len() < 3<<20 {
			b.WriteString(line)
		}
		b.WriteString("Mention : diffusion\n   restreinte\n")
		if err := os.WriteFile(filepath.Join(root, "gros.txt"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, tool, args string
	}{
		{"read", "read_file", `{"path":"gros.txt","limit":1}`},
		{"grep", "grep", `{"pattern":"Entête"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newClassifyHarness(t, false, [][]llm.Chunk{call("c1", c.tool, c.args), text("ok")})
			big(t, h.root)
			evs, err := h.submit(t, "s_1", "go", allowAll)
			if err != nil {
				t.Fatal(err)
			}
			assertWithheld(t, h, evs, c.tool, "Entête")
		})
	}
}

func TestStopHookFeedbackMarker(t *testing.T) {
	stop := func(t *testing.T) func(*Deps) {
		flag := filepath.Join(t.TempDir(), "flag")
		return withHooks(t, hk{"Stop", "", cmd(hookScript(t, "cat >/dev/null\n"+
			onceFlag(flag, `echo '{"decision":"block","reason":"voir note EU RESTRICTED budget"}'`)))})
	}
	script := [][]llm.Chunk{text("first"), text("second")}
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script, stop(t))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		if len(sc) != 1 || sc[0].Reason != "marker" || sc[0].Source != "tool" || sc[0].Marker != "EU RESTRICTED" {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script, stop(t))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(ofType(evs, event.SessionClassified)); n != 0 {
			t.Fatalf("%d SessionClassified", n)
		}
		if h.historyHas(t, "s_1", "budget") || h.requestsHave("budget") {
			t.Fatal("retour de hook classé dans l'historique")
		}
		if !h.historyHas(t, "s_1", stopFeedback+withheldMsg) {
			t.Fatal("retour retenu absent")
		}
	})
}

func TestCompactHookContextMarker(t *testing.T) {
	run := func(t *testing.T, local bool) (*harness, []event.Event) {
		hs := withHooks(t, hk{"SessionStart", "compact", cmd(hookScript(t, "cat >/dev/null\ncat <<'EOF'\n"+
			ctxOut("SessionStart", "note SPECIAL FRANCE budget")+"\nEOF"))})
		h := newClassifyHarness(t, local, [][]llm.Chunk{text("LE RÉSUMÉ"), text("ok")}, window(1000), hs)
		l := &evLog{t: t, sid: "s_1"}
		for _, m := range longHistory() {
			if m.Role == llm.User {
				l.add(event.UserMessage, event.UserMessagePayload{Text: m.Content})
			} else {
				l.add(event.AssistantMessage, event.AssistantMessagePayload{Text: m.Content})
			}
		}
		if err := h.eng.Restore("s_1", l.evs); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "suite", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(ofType(evs, event.ContextCompacted)) != 1 {
			t.Fatalf("events %v", types(evs))
		}
		return h, evs
	}
	t.Run("local", func(t *testing.T) {
		_, evs := run(t, true)
		sc := classified(t, evs)
		if len(sc) != 1 || sc[0].Reason != "marker" || sc[0].Marker != "SPÉCIAL FRANCE" {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
	t.Run("no local", func(t *testing.T) {
		h, evs := run(t, false)
		if h.historyHas(t, "s_1", "budget") || h.requestsHave("budget") {
			t.Fatal("contexte classé dans l'historique")
		}
		p := decode[event.ContextCompactedPayload](t, ofType(evs, event.ContextCompacted)[0])
		if p.HookContext != withheldMsg {
			t.Fatalf("ContextCompacted %+v", p)
		}
	})
}

func TestBlockedToolReasonMarker(t *testing.T) {
	block := `{"decision":"block","reason":"interdit : diffusion restreinte, budget 12"}`
	script := [][]llm.Chunk{call("c1", "read_file", `{"path":"libre.txt"}`), text("ok")}
	t.Run("local", func(t *testing.T) {
		h := newClassifyHarness(t, true, script, withHooks(t, hk{"PreToolUse", "Read", jsonHook(t, block)}))
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		sc := classified(t, evs)
		if len(sc) != 1 || sc[0].Reason != "marker" || sc[0].CallID != "c1" {
			t.Fatalf("SessionClassified %+v", sc)
		}
	})
	t.Run("no local", func(t *testing.T) {
		h := newClassifyHarness(t, false, script, withHooks(t, hk{"PreToolUse", "Read", jsonHook(t, block)}))
		evs, err := h.submit(t, "s_1", "go", allowAll)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, h, evs, "read_file", "budget")
	})
}
