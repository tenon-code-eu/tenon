package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/tenon-code-eu/tenon/internal/ui/tui"
)

const (
	tuiWidth   = 100
	tuiHeight  = 30
	tuiTimeout = 10 * time.Second
)

// steppingClock renvoie une horloge qui avance d'une seconde à chaque
// lecture : le délai d'armement des acceptations est toujours écoulé, sans
// attente réelle.
func steppingClock() func() time.Time {
	var mu sync.Mutex
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t = t.Add(time.Second)
		return t
	}
}

// tuiRun est le résultat d'un run() en mode interactif simulé.
type tuiRun struct {
	code   int
	stderr string
	// view est la dernière vue de la TUI ; started indique qu'elle a été lancée.
	view    string
	started bool
	// opts sont les options reçues par la TUI.
	opts tui.Options
}

// runTUI exécute la CLI avec un terminal simulé de 100×30 : la TUI lancée par
// run() est remplacée par un programme teatest piloté par drive.
func (e *env) runTUI(drive func(tm *teatest.TestModel), args ...string) tuiRun {
	e.t.Helper()
	var res tuiRun
	prev := startTUI
	startTUI = func(ctx context.Context, eng tui.Engine, opts tui.Options, in io.Reader, out io.Writer) error {
		res.started = true
		res.opts = opts
		opts.Now = steppingClock()
		tm := teatest.NewTestModel(e.t, tui.New(eng, opts), teatest.WithInitialTermSize(tuiWidth, tuiHeight))
		drive(tm)
		res.view = tm.FinalModel(e.t, teatest.WithFinalTimeout(tuiTimeout)).View()
		return nil
	}
	defer func() { startTUI = prev }()
	var out, errb bytes.Buffer
	res.code = run(context.Background(), args, strings.NewReader(""), &out, &errb,
		func(k string) string { return e.vars[k] }, e.wd, func() bool { return true })
	res.stderr = errb.String()
	return res
}

// typeText envoie s rune par rune (teatest.Type découpe en octets).
func typeText(tm *teatest.TestModel, s string) {
	for _, r := range s {
		tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func pressEnter(tm *teatest.TestModel) { tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) }

// waitText attend que la sortie de la TUI contienne s.
func waitText(t *testing.T, tm *teatest.TestModel, s string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(s)) },
		teatest.WithDuration(tuiTimeout), teatest.WithCheckInterval(10*time.Millisecond))
}

// quitTUI tape /quit puis Entrée jusqu'à la sortie du programme : Entrée est
// ignorée tant que le tour en cours n'a pas rendu la main.
func quitTUI(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	typeText(tm, "/quit")
	fin := make(chan struct{})
	go func() { tm.GetProgram().Wait(); close(fin) }()
	deadline := time.After(tuiTimeout)
	for {
		pressEnter(tm)
		select {
		case <-fin:
			return
		case <-deadline:
			t.Fatal("la TUI ne s'est pas arrêtée après /quit")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestNoTTYRefused(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	for _, lang := range []string{"fr", "en"} {
		code, out, errb := e.run("--lang", lang)
		want := "l'interface interactive demande un terminal"
		if lang == "en" {
			want = "the interactive interface needs a terminal"
		}
		if code != 1 || out != "" || !strings.Contains(errb, want) {
			t.Fatalf("%s : code=%d stdout=%q stderr=%q", lang, code, out, errb)
		}
	}
	if len(f.requests()) != 0 {
		t.Fatalf("%d requêtes", len(f.requests()))
	}
	if _, err := os.Stat(e.dataDir); !os.IsNotExist(err) {
		t.Fatalf("données créées avant le refus : %v", err)
	}
}

func TestOutputRequiresPrompt(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	r := e.runTUI(func(tm *teatest.TestModel) { t.Error("TUI lancée") }, "--output", "json")
	if r.code != 1 || r.started || !strings.Contains(r.stderr, "--output ne s'utilise qu'avec -p") {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	// Même refus sans terminal : --output est vérifié d'abord.
	code, _, errb := e.run("--output", "text")
	if code != 1 || !strings.Contains(errb, "--output ne s'utilise qu'avec -p") {
		t.Fatalf("sans terminal : code=%d stderr=%q", code, errb)
	}
	if len(f.requests()) != 0 {
		t.Fatalf("%d requêtes", len(f.requests()))
	}
	if _, err := os.Stat(e.dataDir); !os.IsNotExist(err) {
		t.Fatalf("données créées avant le refus : %v", err)
	}
}

func TestTUIConversation(t *testing.T) {
	f := newFakeLLM(t, sseText("Bonjour, je suis le faux serveur"))
	e := newEnv(t, f, "eu")
	r := e.runTUI(func(tm *teatest.TestModel) {
		typeText(tm, "bonjour")
		pressEnter(tm)
		waitText(t, tm, "je suis le faux serveur")
		quitTUI(t, tm)
	})
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	for _, want := range []string{"Bonjour, je suis le faux serveur", "juridiction UE", "› bonjour"} {
		if !strings.Contains(r.view, want) {
			t.Errorf("vue finale sans %q :\n%s", want, r.view)
		}
	}
	reqs := f.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0], "bonjour") {
		t.Fatalf("requêtes : %q", reqs)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	if !strings.Contains(r.view, strings.TrimSuffix(ents[0].Name(), ".jsonl")) {
		t.Errorf("identifiant de session absent de la barre d'état :\n%s", r.view)
	}
	// Le cadre de bienvenue est un affichage local : version transmise, rien
	// dans la session ni dans l'audit.
	if r.opts.Version != version {
		t.Errorf("version transmise %q, attendu %q", r.opts.Version, version)
	}
	for _, want := range []string{"████████", "Harness open-source"} {
		if !strings.Contains(r.view, want) {
			t.Errorf("vue finale sans %q :\n%s", want, r.view)
		}
	}
	b, err := os.ReadFile(filepath.Join(e.sessionsDir(), ents[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, where := range []string{string(b), e.auditContents()} {
		if strings.Contains(where, "████") || strings.Contains(where, "Harness open-source") {
			t.Errorf("cadre de bienvenue persisté :\n%s", where)
		}
	}
}

func TestTUIPermissionFlow(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		written   bool
		want      string
	}{
		{"oui", "o", true, "[ok]"},
		{"non", "n", false, "[refusé]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"salut\n"}`), sseText("terminé"))
			e := newEnv(t, f, "eu")
			r := e.runTUI(func(tm *teatest.TestModel) {
				typeText(tm, "écris x.txt")
				pressEnter(tm)
				waitText(t, tm, "Autoriser cette action ?")
				typeText(tm, tc.key)
				waitText(t, tm, "terminé")
				quitTUI(t, tm)
			})
			if r.code != 0 {
				t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
			}
			b, err := os.ReadFile(filepath.Join(e.wd, "x.txt"))
			switch {
			case tc.written && (err != nil || string(b) != "salut\n"):
				t.Fatalf("fichier : %q %v", b, err)
			case !tc.written && !os.IsNotExist(err):
				t.Fatalf("fichier écrit malgré le refus : %v", err)
			}
			if !strings.Contains(r.view, "write_file") || !strings.Contains(r.view, tc.want) {
				t.Errorf("vue finale sans %q :\n%s", tc.want, r.view)
			}
		})
	}
}

func TestTUIResume(t *testing.T) {
	f := newFakeLLM(t, sseText("enchanté"), sseText("Vous êtes Ada"))
	e := newEnv(t, f, "eu")
	if code, out, errb := e.run("-p", "je m'appelle Ada"); code != 0 {
		t.Fatalf("run 1 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	r := e.runTUI(func(tm *teatest.TestModel) {
		typeText(tm, "comment je m'appelle ?")
		pressEnter(tm)
		waitText(t, tm, "Vous êtes Ada")
		quitTUI(t, tm)
	}, "--resume", "last")
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if !strings.Contains(r.view, "(reprise)") {
		t.Errorf("bannière sans reprise :\n%s", r.view)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "je m'appelle Ada") || !strings.Contains(reqs[1], "comment je m'appelle ?") {
		t.Fatalf("requêtes : %q", reqs)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
}

func TestTUIAuditVerifies(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"salut\n"}`), sseText("terminé"))
	e := newEnv(t, f, "eu")
	r := e.runTUI(func(tm *teatest.TestModel) {
		typeText(tm, "écris x.txt")
		pressEnter(tm)
		waitText(t, tm, "Autoriser cette action ?")
		typeText(tm, "o")
		waitText(t, tm, "terminé")
		quitTUI(t, tm)
	})
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	code, out, errb := e.run("audit", "verify")
	if code != 0 || !strings.HasPrefix(out, "OK : ") {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(e.auditContents(), "write_file") {
		t.Error("appel write_file absent de l'audit")
	}
}

// Une TUI qui rend ErrTurnNotFinished donne un message traduit et le code 1.
func TestTUITurnNotFinished(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	prev := startTUI
	startTUI = func(context.Context, tui.Engine, tui.Options, io.Reader, io.Writer) error {
		return tui.ErrTurnNotFinished
	}
	defer func() { startTUI = prev }()
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"--lang", "en"}, strings.NewReader(""), &out, &errb,
		func(k string) string { return e.vars[k] }, e.wd, func() bool { return true })
	if code != 1 || errb.String() != "tenon: the current turn did not finish before exit\n" {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}

// --allow-non-eu donne le consentement d'avance ; sans lui, la TUI pose la
// question de juridiction.
func TestTUIJurisdiction(t *testing.T) {
	f := newFakeLLM(t, sseText("réponse distante"))
	e := newEnv(t, f, "non-eu")
	r := e.runTUI(func(tm *teatest.TestModel) {
		typeText(tm, "salut")
		pressEnter(tm)
		waitText(t, tm, "envoyer la conversation à")
		typeText(tm, "n")
		quitTUI(t, tm)
	})
	if r.code != 0 || len(f.requests()) != 0 {
		t.Fatalf("refus : code=%d requêtes=%d stderr=%q", r.code, len(f.requests()), r.stderr)
	}
	r = e.runTUI(func(tm *teatest.TestModel) {
		typeText(tm, "salut")
		pressEnter(tm)
		waitText(t, tm, "réponse distante")
		quitTUI(t, tm)
	}, "--allow-non-eu")
	if r.code != 0 || len(f.requests()) != 1 {
		t.Fatalf("consentement : code=%d requêtes=%d stderr=%q", r.code, len(f.requests()), r.stderr)
	}
}

// Le vrai tui.Run reçoit l'entrée et la sortie standard de run() : /quit
// rend la main avec le code 0, sans requête au fournisseur.
func TestTUIRealRunQuits(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	var out, errb bytes.Buffer
	code := run(context.Background(), nil, strings.NewReader("/quit\r"), &out, &errb,
		func(k string) string { return e.vars[k] }, e.wd, func() bool { return true })
	if code != 0 || errb.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "juridiction UE") {
		t.Fatalf("barre d'état absente de la sortie : %q", out.String())
	}
	if len(f.requests()) != 0 {
		t.Fatalf("%d requêtes", len(f.requests()))
	}
}

// La TUI reçoit la fonction de masquage du texte du mode non interactif.
func TestTUIRedactWired(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	r := e.runTUI(func(tm *teatest.TestModel) { quitTUI(t, tm) })
	if r.code != 0 || r.opts.Redact == nil {
		t.Fatalf("code=%d Redact=%v stderr=%q", r.code, r.opts.Redact != nil, r.stderr)
	}
	if got := r.opts.Redact("MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"); strings.Contains(got, "Zx8kq") {
		t.Fatalf("Redact ne masque pas : %q", got)
	}
}

// syncBuffer est un bytes.Buffer utilisable depuis plusieurs goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Le vrai tui.Run, lancé par run() : un message contenant un secret
// déclenche une demande d'écriture ; Ctrl+C deux fois la refuse, annule le
// tour et quitte avec le code 0. Le journal d'audit reste vérifiable, le
// secret n'apparaît ni dans l'audit, ni dans la session, ni dans les
// requêtes, et le fichier n'est pas écrit.
func TestTUIRealRunCancelMidPrompt(t *testing.T) {
	const secret = "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"salut\n"}`), sseText("terminé"))
	e := newEnv(t, f, "eu")
	inR, inW := io.Pipe()
	defer inW.Close()
	var out, errb syncBuffer
	codec := make(chan int, 1)
	go func() {
		codec <- run(context.Background(), nil, inR, &out, &errb,
			func(k string) string { return e.vars[k] }, e.wd, func() bool { return true })
	}()
	write := func(s string) {
		t.Helper()
		if _, err := inW.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	waitOut := func(s string) {
		t.Helper()
		deadline := time.After(tuiTimeout)
		for !strings.Contains(out.String(), s) {
			select {
			case <-deadline:
				t.Fatalf("sortie sans %q :\n%s", s, out.String())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitOut("juridiction UE")
	write("écris x.txt " + secret)
	write("\r")
	waitOut("Autoriser cette action ?")
	write("\x03")
	write("\x03")
	var code int
	select {
	case code = <-codec:
	case <-time.After(tuiTimeout):
		t.Fatal("la TUI ne s'est pas arrêtée après Ctrl+C deux fois")
	}
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(e.wd, "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("fichier écrit malgré le refus : %v", err)
	}
	if c, o, eb := e.run("audit", "verify"); c != 0 || !strings.HasPrefix(o, "OK : ") {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", c, o, eb)
	}
	a := e.auditContents()
	if !strings.Contains(a, "write_file") || strings.Contains(a, "Zx8kq") {
		t.Fatalf("audit : write_file absent ou secret présent :\n%s", a)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	b, err := os.ReadFile(filepath.Join(e.sessionsDir(), ents[0].Name()))
	if err != nil || strings.Contains(string(b), "Zx8kq") || !strings.Contains(string(b), "[SECRET_1]") {
		t.Fatalf("session : %v\n%s", err, b)
	}
	// La demande a été refusée, pas acceptée puis annulée.
	if !strings.Contains(string(b), `"allowed":false`) || strings.Contains(string(b), `"allowed":true`) {
		t.Fatalf("session sans refus de la demande :\n%s", b)
	}
	reqs := f.requests()
	if len(reqs) == 0 {
		t.Fatal("aucune requête")
	}
	for _, r := range reqs {
		if strings.Contains(r, "Zx8kq") {
			t.Fatalf("secret envoyé : %s", r)
		}
	}
}

// Le mode non interactif n'affiche jamais le cadre de bienvenue.
func TestHeadlessNoWelcome(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	code, out, errb := e.run("-p", "bonjour")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	for _, s := range []string{out, errb} {
		if strings.Contains(s, "████") || strings.Contains(s, "Harness open-source") || strings.Contains(s, "⬢ TENON") {
			t.Errorf("cadre de bienvenue en mode -p :\n%s", s)
		}
	}
}

// [ui] theme et animation arrivent dans les options de la TUI.
func TestTUIThemeOptions(t *testing.T) {
	quit := func(tm *teatest.TestModel) {
		tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
		tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	}
	e := newEnvExtra(t, newFakeLLM(t, sseText("x")), "eu", "[ui]\ntheme = \"gruvbox\"\nanimation = false\n")
	res := e.runTUI(quit, "--lang", "en")
	if res.opts.Theme != "gruvbox" || res.opts.Animation {
		t.Fatalf("options %+v", res.opts)
	}
	e = newEnv(t, newFakeLLM(t, sseText("x")), "eu")
	res = e.runTUI(quit, "--lang", "en")
	if res.opts.Theme != "tenon" || !res.opts.Animation {
		t.Fatalf("défauts %+v", res.opts)
	}
}
