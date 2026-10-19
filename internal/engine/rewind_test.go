package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/checkpoint"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// snapshotTo copie l'ancien contenu dans le magasin (câblage de cmd/tenon).
func snapshotTo(s *checkpoint.Store) func(string, []byte) error {
	return func(_ string, old []byte) error {
		_, err := s.Save(old)
		return err
	}
}

// newRewindHarness : harnais avec magasin de copies, outils fichiers
// (copie avant écriture) et shell.
func newRewindHarness(t *testing.T, script [][]llm.Chunk, opts ...func(*Deps)) (*harness, *checkpoint.Store) {
	t.Helper()
	store, err := checkpoint.Open(filepath.Join(t.TempDir(), "checkpoints"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, euInfo, script, append([]func(*Deps){func(d *Deps) {
		d.Checkpoints = store
		d.Tools = tools.NewRegistry(append(
			tools.FS(d.ProjectRoot, 30000, tools.WithSnapshot(snapshotTo(store))),
			tools.Shell(d.ProjectRoot, 5*time.Second, 30000))...)
	}}, opts...)...)
	return h, store
}

func allowAll(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s présent : %v", path, err)
	}
}

func writeCall(id, path, content string) []llm.Chunk {
	return call(id, "write_file", mustJSON(map[string]string{"path": path, "content": content}))
}

func historyOf(h *harness, sid string) string {
	s := h.eng.session(sid)
	return mustJSON(s.history)
}

func statuses(files []event.RewoundFile) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		out[filepath.Base(f.Path)] = f.Status
	}
	return out
}

func planStatuses(p checkpoint.Plan) map[string]string {
	out := map[string]string{}
	for _, f := range p.Files {
		out[filepath.Base(f.Path)] = f.Action + "/" + f.Status
	}
	return out
}

func lastRewound(t *testing.T, h *harness) event.RewoundPayload {
	t.Helper()
	rw := ofType(h.sink.events, event.Rewound)
	if len(rw) == 0 {
		t.Fatalf("aucun Rewound : %v", types(h.sink.events))
	}
	return decode[event.RewoundPayload](t, rw[len(rw)-1])
}

func TestRewindCodeRestoresAndDeletes(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		writeCall("c1", "a.txt", "v2"),
		writeCall("c2", "b.txt", "new"),
		text("done"),
		text("ok"),
	})
	a, b := filepath.Join(h.root, "a.txt"), filepath.Join(h.root, "b.txt")
	writeFile(t, a, "v1")
	if _, err := h.submit(t, "s_1", "first prompt\nsecond line", allowAll); err != nil {
		t.Fatal(err)
	}
	pts := h.eng.RewindPoints("s_1")
	if len(pts) != 1 || pts[0].Excerpt != "first prompt" || !pts[0].Conversation {
		t.Fatalf("points %+v", pts)
	}
	plan, err := h.eng.RewindPlan("s_1", pts[0].Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got := planStatuses(plan); got["a.txt"] != "restore/ok" || got["b.txt"] != "delete/ok" || len(got) != 2 {
		t.Fatalf("plan %v", got)
	}
	hist := historyOf(h, "s_1")

	res, err := h.eng.Rewind(context.Background(), "s_1", pts[0].Seq, RewindCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, a); got != "v1" {
		t.Fatalf("a.txt = %q", got)
	}
	absent(t, b)
	if got := statuses(res.Files); got["a.txt"] != "restored" || got["b.txt"] != "deleted" {
		t.Fatalf("statuts %v", got)
	}
	note := `[tenon] The user restored files to their state before: "first prompt"`
	if res.Note != note || res.Prompt != "" {
		t.Fatalf("résultat %+v", res)
	}
	if historyOf(h, "s_1") != hist {
		t.Fatal("historique modifié par un rewind code")
	}
	rp := lastRewound(t, h)
	if rp.ToSeq != pts[0].Seq || rp.Mode != "code" || rp.Note != note || len(rp.Files) != 2 {
		t.Fatalf("Rewound %+v", rp)
	}
	for _, f := range rp.Files {
		if filepath.Base(f.Path) == "a.txt" && (f.Hash == "" || !strings.HasPrefix(f.Hash, "sha256:")) {
			t.Fatalf("hash restauré %+v", f)
		}
	}
	if last := h.sink.events[len(h.sink.events)-1]; last.Type != event.Rewound {
		t.Fatalf("dernier événement %s", last.Type)
	}

	// La note est jointe au prochain message, une seule fois.
	if _, err := h.submit(t, "s_1", "next", allowAll); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "after", allowAll); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	msgs := reqs[len(reqs)-1].Messages
	n := 0
	for _, m := range msgs {
		if m.Role == llm.User && strings.Contains(m.Content, note) {
			n++
			if m.Content != withHookContext("next", note) {
				t.Fatalf("message %q", m.Content)
			}
		}
	}
	if n != 1 {
		t.Fatalf("note présente %d fois : %s", n, mustJSON(msgs))
	}
	ums := ofType(h.sink.events, event.UserMessage)
	if hc := decode[event.UserMessagePayload](t, ums[1]).HookContext; hc != note {
		t.Fatalf("hook_context %q", hc)
	}
}

func TestRewindConflictKeptOrOverwritten(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{writeCall("c1", "a.txt", "v2"), text("done")})
	a := filepath.Join(h.root, "a.txt")
	writeFile(t, a, "v1")
	if _, err := h.submit(t, "s_1", "go", allowAll); err != nil {
		t.Fatal(err)
	}
	writeFile(t, a, "user edit")
	seq := h.eng.RewindPoints("s_1")[0].Seq
	plan, err := h.eng.RewindPlan("s_1", seq)
	if err != nil {
		t.Fatal(err)
	}
	if got := planStatuses(plan); got["a.txt"] != "restore/conflict" {
		t.Fatalf("plan %v", got)
	}
	res, err := h.eng.Rewind(context.Background(), "s_1", seq, RewindCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(res.Files)["a.txt"]; got != "kept" {
		t.Fatalf("statut %q", got)
	}
	if got := readFile(t, a); got != "user edit" {
		t.Fatalf("fichier modifié sans accord : %q", got)
	}
	res, err = h.eng.Rewind(context.Background(), "s_1", seq, RewindCode, []string{a})
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(res.Files)["a.txt"]; got != "restored" {
		t.Fatalf("statut %q", got)
	}
	if got := readFile(t, a); got != "v1" {
		t.Fatalf("a.txt = %q", got)
	}
}

func TestRewindConflictSymlinkNeverFollowed(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{writeCall("c1", "a.txt", "v2"), text("done")})
	a := filepath.Join(h.root, "a.txt")
	writeFile(t, a, "v1")
	if _, err := h.submit(t, "s_1", "go", allowAll); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other.txt")
	writeFile(t, other, "precious")
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, a); err != nil {
		t.Fatal(err)
	}
	seq := h.eng.RewindPoints("s_1")[0].Seq
	res, err := h.eng.Rewind(context.Background(), "s_1", seq, RewindCode, []string{a})
	if err != nil {
		t.Fatal(err)
	}
	f := res.Files[0]
	if f.Status != "failed" || f.Error != "path became a symbolic link" {
		t.Fatalf("fichier %+v", f)
	}
	if got := readFile(t, other); got != "precious" {
		t.Fatalf("cible du lien modifiée : %q", got)
	}
	if fi, err := os.Lstat(a); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("lien remplacé : %v", err)
	}
}

func TestRewindConversation(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		text("hello"),
		writeCall("c1", "a.txt", "v2"),
		text("done"),
		text("ok"),
	})
	a := filepath.Join(h.root, "a.txt")
	writeFile(t, a, "v1")
	if _, err := h.submit(t, "s_1", "one", allowAll); err != nil {
		t.Fatal(err)
	}
	hist := historyOf(h, "s_1")
	if _, err := h.submit(t, "s_1", "two\nmore", allowAll); err != nil {
		t.Fatal(err)
	}
	pts := h.eng.RewindPoints("s_1")
	if len(pts) != 2 || pts[0].Excerpt != "two" || pts[1].Excerpt != "one" {
		t.Fatalf("points %+v", pts)
	}
	res, err := h.eng.Rewind(context.Background(), "s_1", pts[0].Seq, RewindConversation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Prompt != "two\nmore" || res.Note != "" || len(res.Files) != 0 {
		t.Fatalf("résultat %+v", res)
	}
	if historyOf(h, "s_1") != hist {
		t.Fatalf("historique %s\nattendu %s", historyOf(h, "s_1"), hist)
	}
	if got := readFile(t, a); got != "v2" {
		t.Fatalf("fichier touché : %q", got)
	}
	if rp := lastRewound(t, h); rp.Mode != "conversation" || rp.Note != "" {
		t.Fatalf("Rewound %+v", rp)
	}
	// Le tour suivant repart de l'historique tronqué, sans note.
	if _, err := h.submit(t, "s_1", "three", allowAll); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	msgs := reqs[len(reqs)-1].Messages
	if len(msgs) != 4 || msgs[3].Content != "three" {
		t.Fatalf("requête %s", mustJSON(msgs))
	}
}

func TestRewindBoth(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		text("hello"),
		writeCall("c1", "a.txt", "v2"),
		text("done"),
	})
	a := filepath.Join(h.root, "a.txt")
	writeFile(t, a, "v1")
	if _, err := h.submit(t, "s_1", "one", allowAll); err != nil {
		t.Fatal(err)
	}
	hist := historyOf(h, "s_1")
	if _, err := h.submit(t, "s_1", "two", allowAll); err != nil {
		t.Fatal(err)
	}
	seq := h.eng.RewindPoints("s_1")[0].Seq
	res, err := h.eng.Rewind(context.Background(), "s_1", seq, RewindBoth, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Prompt != "two" || res.Note != "" || statuses(res.Files)["a.txt"] != "restored" {
		t.Fatalf("résultat %+v", res)
	}
	if got := readFile(t, a); got != "v1" {
		t.Fatalf("a.txt = %q", got)
	}
	if historyOf(h, "s_1") != hist {
		t.Fatal("historique non tronqué")
	}
}

func TestRewindConversationAfterCompaction(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{text("LE RÉSUMÉ"), text("ok")}, window(1000))
	seed(t, h, "s_1", longHistory()...)
	if _, err := h.submit(t, "s_1", "suite", allowAll); err != nil {
		t.Fatal(err)
	}
	pts := h.eng.RewindPoints("s_1")
	if len(pts) < 2 {
		t.Fatalf("points %+v", pts)
	}
	for _, p := range pts {
		if p.Conversation {
			t.Fatalf("point rembobinable avant la compaction : %+v", p)
		}
	}
	for _, mode := range []RewindMode{RewindConversation, RewindBoth} {
		if _, err := h.eng.Rewind(context.Background(), "s_1", pts[0].Seq, mode, nil); !errors.Is(err, ErrConversationUnavailable) {
			t.Fatalf("%s : %v", mode, err)
		}
	}
	if n := len(ofType(h.sink.events, event.Rewound)); n != 0 {
		t.Fatalf("%d Rewound après un refus", n)
	}
	// Le code reste rembobinable.
	if _, err := h.eng.Rewind(context.Background(), "s_1", pts[0].Seq, RewindCode, nil); err != nil {
		t.Fatal(err)
	}
	// Un point postérieur à la compaction est rembobinable.
	if _, err := h.submit(t, "s_1", "encore", allowAll); err != nil {
		t.Fatal(err)
	}
	p := h.eng.RewindPoints("s_1")[0]
	plan, err := h.eng.RewindPlan("s_1", p.Seq)
	if err != nil || !plan.ConversationAvailable || !p.Conversation {
		t.Fatalf("plan %+v, %v", plan, err)
	}
}

func TestRewindTurnInProgress(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		text("hello"),
		writeCall("c1", "a.txt", "v2"),
		text("done"),
	})
	if _, err := h.submit(t, "s_1", "one", allowAll); err != nil {
		t.Fatal(err)
	}
	seq := h.eng.RewindPoints("s_1")[0].Seq
	var rewindErr, planErr error
	var pts []checkpoint.Point
	if _, err := h.submit(t, "s_1", "two", func(event.PermissionRequestedPayload) Reply {
		// Le tour attend la réponse : il tient la session.
		_, rewindErr = h.eng.Rewind(context.Background(), "s_1", seq, RewindCode, nil)
		_, planErr = h.eng.RewindPlan("s_1", seq)
		pts = h.eng.RewindPoints("s_1")
		return Reply{Allow: true}
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(rewindErr, ErrTurnInProgress) || rewindErr.Error() != "a turn is in progress" {
		t.Fatalf("Rewind : %v", rewindErr)
	}
	if !errors.Is(planErr, ErrTurnInProgress) {
		t.Fatalf("RewindPlan : %v", planErr)
	}
	if len(pts) != 2 {
		t.Fatalf("points pendant le tour %+v", pts)
	}
}

func TestRewindPartialFailure(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		calls(
			llm.ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt","content":"v2"}`)},
			llm.ToolCall{ID: "c2", Name: "write_file", Args: json.RawMessage(`{"path":"sub/c.txt","content":"v2"}`)},
		),
		text("done"),
	})
	a, c := filepath.Join(h.root, "a.txt"), filepath.Join(h.root, "sub", "c.txt")
	writeFile(t, a, "v1")
	writeFile(t, c, "v1")
	if _, err := h.submit(t, "s_1", "go", allowAll); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Dir(c)
	if err := os.Chmod(sub, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
	seq := h.eng.RewindPoints("s_1")[0].Seq
	res, err := h.eng.Rewind(context.Background(), "s_1", seq, RewindCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	var failed event.RewoundFile
	for _, f := range res.Files {
		if filepath.Base(f.Path) == "c.txt" {
			failed = f
		}
	}
	if failed.Status != "failed" || failed.Error == "" || failed.Hash != "" {
		t.Fatalf("c.txt %+v", failed)
	}
	if got := statuses(res.Files)["a.txt"]; got != "restored" {
		t.Fatalf("a.txt %q", got)
	}
	if readFile(t, a) != "v1" || readFile(t, c) != "v2" {
		t.Fatal("contenus inattendus")
	}
}

func TestWriteRefusedWhenCheckpointFails(t *testing.T) {
	var dir string
	h, _ := newRewindHarness(t, [][]llm.Chunk{writeCall("c1", "a.txt", "v2"), text("done")}, func(d *Deps) {
		dir = filepath.Join(t.TempDir(), "ro")
		store, err := checkpoint.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		d.Checkpoints = store
		d.Tools = tools.NewRegistry(tools.FS(d.ProjectRoot, 30000, tools.WithSnapshot(snapshotTo(store)))...)
	})
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	a := filepath.Join(h.root, "a.txt")
	writeFile(t, a, "v1")
	evs, err := h.submit(t, "s_1", "go", allowAll)
	if err != nil {
		t.Fatal(err)
	}
	r := toolResultOf(t, evs, "write_file")
	if !r.IsError || !strings.Contains(r.Content, "cannot save checkpoint") {
		t.Fatalf("ToolResult %+v", r)
	}
	if got := readFile(t, a); got != "v1" {
		t.Fatalf("a.txt écrit sans copie : %q", got)
	}
}

// fakeMCP est un outil MCP factice de risque donné.
type fakeMCP struct {
	name string
	risk tools.Risk
}

func (f fakeMCP) Name() string                                 { return f.name }
func (f fakeMCP) Description() string                          { return "fake" }
func (f fakeMCP) Schema() json.RawMessage                      { return json.RawMessage(`{"type":"object"}`) }
func (f fakeMCP) Risk() tools.Risk                             { return f.risk }
func (f fakeMCP) Target(json.RawMessage) (tools.Target, error) { return tools.Target{}, nil }
func (f fakeMCP) Run(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Content: "done"}, nil
}

func TestUntrackedCounted(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		text("hello"),
		calls(
			llm.ToolCall{ID: "c1", Name: "shell", Args: json.RawMessage(`{"command":"true"}`)},
			llm.ToolCall{ID: "c2", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)},
			llm.ToolCall{ID: "c3", Name: "mcp__srv__get", Args: json.RawMessage(`{}`)},
			llm.ToolCall{ID: "c4", Name: "mcp__srv__put", Args: json.RawMessage(`{}`)},
		),
		text("done"),
	}, func(d *Deps) {
		d.Tools = d.Tools.With(fakeMCP{"mcp__srv__get", tools.Read}, fakeMCP{"mcp__srv__put", tools.Write})
	})
	writeFile(t, filepath.Join(h.root, "a.txt"), "v1")
	if _, err := h.submit(t, "s_1", "one", allowAll); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "two", allowAll)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"shell": true, "read_file": false, "mcp__srv__get": false, "mcp__srv__put": true}
	for tool, u := range want {
		r := toolResultOf(t, evs, tool)
		if r.Untracked != u {
			t.Fatalf("%s : untracked %v, résultat %+v", tool, r.Untracked, r)
		}
	}
	pts := h.eng.RewindPoints("s_1")
	plan, err := h.eng.RewindPlan("s_1", pts[0].Seq)
	if err != nil || plan.UntrackedCalls != 2 {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	plan, err = h.eng.RewindPlan("s_1", pts[1].Seq)
	if err != nil || plan.UntrackedCalls != 2 {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	res, err := h.eng.Rewind(context.Background(), "s_1", pts[0].Seq, RewindCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	if rp := lastRewound(t, h); rp.UntrackedCalls != 2 {
		t.Fatalf("Rewound %+v", rp)
	}
}

func TestUntrackedNotCountedWhenDenied(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		text("hello"),
		call("c1", "shell", `{"command":"true"}`),
		text("done"),
	})
	if _, err := h.submit(t, "s_1", "one", allowAll); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "two", func(event.PermissionRequestedPayload) Reply { return Reply{} })
	if err != nil {
		t.Fatal(err)
	}
	if r := toolResultOf(t, evs, "shell"); r.Untracked {
		t.Fatalf("appel refusé compté : %+v", r)
	}
}

func TestSubagentWritesRewound(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		writeCall("s1", "a.txt", "sub"),
		text("sub done"),
		text("done"),
	}, func(d *Deps) {
		cat, _ := agents.Discover(i18n.EN, []agents.Dir{{Path: t.TempDir(), Source: agents.Project}})
		d.Agents = cat
		d.Lang = i18n.EN
	})
	a := filepath.Join(h.root, "a.txt")
	writeFile(t, a, "hi\n")
	if _, err := h.submit(t, "s_1", "go", allowAll); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, a); got != "sub" {
		t.Fatalf("a.txt = %q", got)
	}
	seq := h.eng.RewindPoints("s_1")[0].Seq
	res, err := h.eng.Rewind(context.Background(), "s_1", seq, RewindCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if statuses(res.Files)["a.txt"] != "restored" || readFile(t, a) != "hi\n" {
		t.Fatalf("résultat %+v", res)
	}
}

func TestRewindNoStoreUnrestorable(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{
		calls(
			llm.ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt","content":"v2"}`)},
			llm.ToolCall{ID: "c2", Name: "write_file", Args: json.RawMessage(`{"path":"b.txt","content":"new"}`)},
		),
		text("done"),
	}, func(d *Deps) { d.Checkpoints = nil })
	a, b := filepath.Join(h.root, "a.txt"), filepath.Join(h.root, "b.txt")
	writeFile(t, a, "v1")
	if _, err := h.submit(t, "s_1", "go", allowAll); err != nil {
		t.Fatal(err)
	}
	seq := h.eng.RewindPoints("s_1")[0].Seq
	res, err := h.eng.Rewind(context.Background(), "s_1", seq, RewindCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(res.Files); got["a.txt"] != "skipped" || got["b.txt"] != "deleted" {
		t.Fatalf("statuts %v", got)
	}
	if readFile(t, a) != "v2" {
		t.Fatal("a.txt modifié")
	}
	absent(t, b)
}

func TestRewindUnknownPointAndMode(t *testing.T) {
	h, _ := newRewindHarness(t, [][]llm.Chunk{text("hello")})
	if _, err := h.submit(t, "s_1", "one", allowAll); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.RewindPlan("s_1", 999); !errors.Is(err, checkpoint.ErrUnknownPoint) {
		t.Fatalf("RewindPlan : %v", err)
	}
	if _, err := h.eng.Rewind(context.Background(), "s_1", 999, RewindCode, nil); !errors.Is(err, checkpoint.ErrUnknownPoint) {
		t.Fatalf("Rewind : %v", err)
	}
	seq := h.eng.RewindPoints("s_1")[0].Seq
	if _, err := h.eng.Rewind(context.Background(), "s_1", seq, RewindMode("all"), nil); err == nil {
		t.Fatal("mode inconnu accepté")
	}
	if pts := h.eng.RewindPoints("inconnue"); pts != nil {
		t.Fatalf("points %+v", pts)
	}
	if _, err := h.eng.RewindPlan("inconnue", 1); !errors.Is(err, checkpoint.ErrUnknownPoint) {
		t.Fatalf("RewindPlan session inconnue : %v", err)
	}
}
