package gitmark

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/session"
)

// isolateGit coupe git de la configuration de l'utilisateur et du système
// (core.hooksPath global, signature…) pour ce test et ses sous-processus.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
}

// newRepo crée un dépôt git vide et renvoie sa racine (liens résolus).
func newRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	gitRun(t, dir, nil, "init", "-q")
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// gitRun exécute git dans dir avec env ajouté à l'environnement courant.
func gitRun(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v : %v\n%s", args, err, errb.String())
	}
	return out.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeSession décrit une session écrite avec internal/session.
type fakeSession struct {
	id, root string
	provider llm.ProviderInfo
	results  []event.ToolResultPayload
}

func writeSession(t *testing.T, dir string, s fakeSession) {
	t.Helper()
	w, err := session.Open(dir, s.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	seq := uint64(0)
	ts := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	add := func(typ event.Type, p any) {
		seq++
		e, err := event.New(s.id, seq, ts.Add(time.Duration(seq)*time.Second), typ, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	add(event.SessionStarted, event.SessionStartedPayload{Provider: s.provider, ProjectRoot: s.root})
	add(event.UserMessage, event.UserMessagePayload{Text: "écris"})
	for _, r := range s.results {
		add(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: r.CallID, Tool: callTool(r)})
		add(event.ToolResult, r)
	}
	add(event.TurnCompleted, event.TurnCompletedPayload{Iterations: 1, Reason: "done"})
}

// callTool : un ToolResult de test sans Tool garde le nom de l'outil dans le
// CallID ("write_file:1"), retrouvé par l'appel ToolCallRequested.
func callTool(r event.ToolResultPayload) string {
	if r.Tool != "" {
		return r.Tool
	}
	name, _, _ := strings.Cut(r.CallID, ":")
	return name
}

var mistral = llm.ProviderInfo{ID: "mistral", Name: "Mistral AI", Model: "devstral", Country: "FR"}

func TestAgentWritesFromSessions(t *testing.T) {
	repo := newRepo(t)
	other := t.TempDir()
	sdir := filepath.Join(t.TempDir(), "sessions")
	writeSession(t, sdir, fakeSession{id: "s_000000000001", root: repo, provider: mistral, results: []event.ToolResultPayload{
		{CallID: "c1", Tool: "write_file", Path: filepath.Join(repo, "a.go"), HashAfter: "h1"},
		{CallID: "c2", Tool: "edit_file", Path: filepath.Join(repo, "b.go"), HashAfter: "h2"},
		{CallID: "c3", Tool: "write_file", Path: filepath.Join(repo, "err.go"), IsError: true},
		{CallID: "c4", Tool: "read_file", Path: filepath.Join(repo, "read.go")},
		{CallID: "c5", Tool: "write_file", Path: ""},
		{CallID: "write_file:6", Path: filepath.Join(repo, "c.go"), HashAfter: "h3"},
		{CallID: "c7", Tool: "write_file", Path: filepath.Join(repo, "denied.go"), IsError: true, Denied: "user"},
	}})
	writeSession(t, sdir, fakeSession{id: "s_000000000002", root: other, provider: mistral, results: []event.ToolResultPayload{
		{CallID: "c1", Tool: "write_file", Path: filepath.Join(repo, "z.go")},
	}})

	ws, err := AgentWrites(sdir, repo)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range ws {
		got = append(got, filepath.Base(w.Path))
		if w.SessionID != "s_000000000001" || w.Model != "devstral" || w.Provider != "Mistral AI" || w.Country != "FR" || w.TS.IsZero() {
			t.Fatalf("écriture : %+v", w)
		}
	}
	slices.Sort(got)
	if want := []string{"a.go", "b.go", "c.go"}; !slices.Equal(got, want) {
		t.Fatalf("écritures : %v, attendu %v", got, want)
	}

	// Dossier absent : aucune écriture, aucune erreur.
	if ws, err := AgentWrites(filepath.Join(t.TempDir(), "absent"), repo); err != nil || len(ws) != 0 {
		t.Fatalf("dossier absent : %v %v", ws, err)
	}
}

func TestTrailersMatchStaged(t *testing.T) {
	repo := newRepo(t)
	writes := []Write{{Path: filepath.Join(repo, "a.go"), SessionID: "s_000000000001", Model: "devstral", Provider: "Mistral AI", Country: "FR"}}
	got := Trailers([]string{"a.go", "b.go"}, repo, writes)
	want := []string{"AI-Assisted-By: tenon (devstral, Mistral AI, FR)", "AI-Session: s_000000000001"}
	if !slices.Equal(got, want) {
		t.Fatalf("trailers : %q, attendu %q", got, want)
	}
	if got := Trailers([]string{"b.go"}, repo, writes); len(got) != 0 {
		t.Fatalf("aucune correspondance attendue : %q", got)
	}
}

func TestTrailersMultipleSessions(t *testing.T) {
	repo := newRepo(t)
	p := func(n string) string { return filepath.Join(repo, n) }
	writes := []Write{
		{Path: p("a.go"), SessionID: "s_00000000000b", Model: "devstral", Provider: "Mistral AI", Country: "FR"},
		{Path: p("b.go"), SessionID: "s_00000000000a", Model: "devstral", Provider: "Mistral AI", Country: "FR"},
		{Path: p("a.go"), SessionID: "s_00000000000a", Model: "devstral", Provider: "Mistral AI", Country: "FR"},
		{Path: p("c.go"), SessionID: "s_00000000000c", Model: "qwen", Provider: "Local", Country: ""},
		{Path: p("d.go"), SessionID: "s_00000000000d", Model: "x", Provider: "Y", Country: "DE"},
	}
	got := Trailers([]string{"c.go", "b.go", "a.go"}, repo, writes)
	want := []string{
		"AI-Assisted-By: tenon (devstral, Mistral AI, FR)",
		"AI-Assisted-By: tenon (qwen, Local, —)",
		"AI-Session: s_00000000000a",
		"AI-Session: s_00000000000b",
		"AI-Session: s_00000000000c",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("trailers :\n%q\nattendu\n%q", got, want)
	}
}

func TestTrailersResolveSymlinks(t *testing.T) {
	repo := newRepo(t)
	link := filepath.Join(t.TempDir(), "lien")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	writes := []Write{{Path: filepath.Join(link, "a.go"), SessionID: "s_000000000001", Model: "m", Provider: "P", Country: "FR"}}
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n")
	if got := Trailers([]string{"a.go"}, repo, writes); len(got) != 2 {
		t.Fatalf("lien non résolu : %q", got)
	}
}

func TestNoMatchNoChange(t *testing.T) {
	repo := newRepo(t)
	sdir := filepath.Join(t.TempDir(), "sessions")
	writeSession(t, sdir, fakeSession{id: "s_000000000001", root: repo, provider: mistral, results: []event.ToolResultPayload{
		{CallID: "c1", Tool: "write_file", Path: filepath.Join(repo, "a.go")},
	}})
	writeFile(t, filepath.Join(repo, "b.go"), "package b\n")
	gitRun(t, repo, nil, "add", "b.go")
	msg := filepath.Join(t.TempDir(), "MSG")
	orig := "titre\n\ncorps sans fin de ligne"
	writeFile(t, msg, orig)
	if err := Mark(msg, repo, sdir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(msg)
	if string(b) != orig {
		t.Fatalf("message modifié : %q", b)
	}
}

func TestMarkAddsTrailersOnce(t *testing.T) {
	repo := newRepo(t)
	sdir := filepath.Join(t.TempDir(), "sessions")
	writeSession(t, sdir, fakeSession{id: "s_000000000001", root: repo, provider: mistral, results: []event.ToolResultPayload{
		{CallID: "c1", Tool: "write_file", Path: filepath.Join(repo, "sous", "a.go")},
	}})
	writeFile(t, filepath.Join(repo, "sous", "a.go"), "package a\n")
	gitRun(t, repo, nil, "add", ".")
	msg := filepath.Join(t.TempDir(), "MSG")
	writeFile(t, msg, "titre\n")
	// Appelé depuis un sous-dossier du dépôt.
	for range 2 {
		if err := Mark(msg, filepath.Join(repo, "sous"), sdir); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(msg)
	want := "titre\n\nAI-Assisted-By: tenon (devstral, Mistral AI, FR)\nAI-Session: s_000000000001\n"
	if string(b) != want {
		t.Fatalf("message :\n%q\nattendu\n%q", b, want)
	}
}

func TestOddPaths(t *testing.T) {
	repo := newRepo(t)
	names := []string{"avec espace.go", `guil"lemet.go`, "retour\nligne.go", "é accent.go"}
	evil := llm.ProviderInfo{Name: "Evil\nSigned-off-by: pirate", Model: ":modèle\x07\r\nAI-Session: s_ffffffffffff", Country: "FR"}
	var results []event.ToolResultPayload
	for i, n := range names {
		writeFile(t, filepath.Join(repo, n), "x\n")
		results = append(results, event.ToolResultPayload{CallID: string(rune('a' + i)), Tool: "write_file", Path: filepath.Join(repo, n)})
	}
	sdir := filepath.Join(t.TempDir(), "sessions")
	writeSession(t, sdir, fakeSession{id: "s_000000000001", root: repo, provider: evil, results: results})
	gitRun(t, repo, nil, "add", ".")

	msg := filepath.Join(t.TempDir(), "MSG")
	writeFile(t, msg, "titre\n")
	if err := Mark(msg, repo, sdir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(msg)
	want := "titre\n\nAI-Assisted-By: tenon (modèleAI-Session: s_ffffffffffff, EvilSigned-off-by: pirate, FR)\nAI-Session: s_000000000001\n"
	if string(b) != want {
		t.Fatalf("message :\n%q\nattendu\n%q", b, want)
	}
	for _, n := range names {
		if strings.Contains(string(b), n) {
			t.Fatalf("chemin dans le message : %q", n)
		}
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"devstral":        "devstral",
		"  ::a:b ":        "a:b",
		"a\nb\rc\td\x00e": "abcde",
		"x‮y":             "xy",
	} {
		if got := sanitize(in); got != want {
			t.Fatalf("sanitize(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestInstallHook(t *testing.T) {
	repo := newRepo(t)
	path, err := InstallHook(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, ".git", "hooks", "prepare-commit-msg"); path != want {
		t.Fatalf("chemin %q, attendu %q", path, want)
	}
	checkHook(t, path)

	// core.hooksPath respecté (relatif à la racine du dépôt).
	repo2 := newRepo(t)
	gitRun(t, repo2, nil, "config", "core.hooksPath", "mes-hooks")
	path, err = InstallHook(repo2, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo2, "mes-hooks", "prepare-commit-msg"); path != want {
		t.Fatalf("chemin %q, attendu %q", path, want)
	}
	checkHook(t, path)

	// Depuis un sous-dossier : même hook, déjà identique.
	sub := filepath.Join(repo2, "sous")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := InstallHook(sub, false); err != nil || p != path {
		t.Fatalf("sous-dossier : %q %v", p, err)
	}
}

func checkHook(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "#!/bin/sh\n" +
		"# tenon : marquage du code produit par l'agent (ne bloque jamais le commit)\n" +
		"case \"$2\" in merge) exit 0 ;; esac\n" +
		"command -v tenon >/dev/null 2>&1 || exit 0\n" +
		"tenon git mark \"$1\" >/dev/null 2>&1 || true\n" +
		"exit 0\n"
	if string(b) != want || HookScript != want {
		t.Fatalf("contenu du hook :\n%s", b)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestInstallKeepsExistingHook(t *testing.T) {
	repo := newRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "prepare-commit-msg")
	writeFile(t, hook, "#!/bin/sh\necho mien\n")

	if _, err := InstallHook(repo, false); !errors.Is(err, ErrHookExists) {
		t.Fatalf("erreur attendue ErrHookExists : %v", err)
	}
	if b, _ := os.ReadFile(hook); string(b) != "#!/bin/sh\necho mien\n" {
		t.Fatalf("hook existant modifié : %q", b)
	}

	writeFile(t, hook+".bak", "ancien bak\n")
	if _, err := InstallHook(repo, true); err != nil {
		t.Fatal(err)
	}
	checkHook(t, hook)
	if b, _ := os.ReadFile(hook + ".bak"); string(b) != "#!/bin/sh\necho mien\n" {
		t.Fatalf(".bak : %q", b)
	}

	// Hook identique : succès sans modification (le .bak reste celui d'avant).
	if _, err := InstallHook(repo, false); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHook(repo, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(hook + ".bak"); string(b) != "#!/bin/sh\necho mien\n" {
		t.Fatalf(".bak écrasé par un hook identique : %q", b)
	}
	checkHook(t, hook)
}

func TestHookNeverBlocks(t *testing.T) {
	repo := newRepo(t)
	if _, err := InstallHook(repo, false); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Dir(gitPath)
	if _, err := os.Stat(filepath.Join(gitDir, "tenon")); err == nil {
		t.Skipf("un tenon est installé à côté de git (%s)", gitDir)
	}

	commit := func(bin, name, msg string) {
		t.Helper()
		writeFile(t, filepath.Join(repo, name), name+"\n")
		path := []string{"PATH=" + bin + string(os.PathListSeparator) + gitDir}
		gitRun(t, repo, path, "add", name)
		gitRun(t, repo, path, "commit", "-q", "-m", msg)
		if got := gitRun(t, repo, nil, "log", "-1", "--format=%B"); got != msg+"\n\n" {
			t.Fatalf("message du commit : %q", got)
		}
	}

	// PATH sans tenon.
	commit(t.TempDir(), "a.txt", "sans tenon")

	// tenon factice qui échoue bruyamment.
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "tenon"), "#!/bin/sh\necho panne >&2\necho panne\nexit 42\n")
	if err := os.Chmod(filepath.Join(bin, "tenon"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit(bin, "b.txt", "tenon en panne")
}
