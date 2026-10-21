package compliance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/tsp"
	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
)

var (
	since = time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	until = time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)
)

func TestLoadPeriodBounds(t *testing.T) {
	um := func(seq uint64, ts time.Time) event.Event {
		return ev(t, "s1", seq, ts, event.UserMessage, event.UserMessagePayload{Text: "x"})
	}
	dir := writeLog(t, audit.Full,
		started(t, "s1", since.Add(-48*time.Hour), "/p"),
		um(2, since.Add(-time.Nanosecond)),
		um(3, since),
		um(4, until.Add(24*time.Hour-time.Millisecond)),
		um(5, until.Add(24*time.Hour)),
	)
	ds, err := Load(dir, Filter{Since: since, Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Lines) != 2 || ds.Lines[0].Seq != 3 || ds.Lines[1].Seq != 4 {
		t.Fatalf("lines = %+v", ds.Lines)
	}
	if !ds.Chain.Intact || ds.Chain.Lines != 5 || ds.Level != LevelFull {
		t.Fatalf("chain %+v level %s", ds.Chain, ds.Level)
	}
}

func TestLoadProjectScope(t *testing.T) {
	d := since.Add(time.Hour)
	dir := writeLog(t, audit.Full,
		started(t, "a", d, "/proj/a"),
		started(t, "b", d.Add(time.Second), "/proj/b"),
		ev(t, "a", 2, d.Add(2*time.Second), event.UserMessage, event.UserMessagePayload{Text: "x"}),
		ev(t, "b", 2, d.Add(3*time.Second), event.UserMessage, event.UserMessagePayload{Text: "x"}),
	)
	f := Filter{Since: since, Until: until, Project: "/proj/a/"}
	ds, err := Load(dir, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Lines) != 2 {
		t.Fatalf("lines = %d", len(ds.Lines))
	}
	for _, l := range ds.Lines {
		if l.Session != "a" {
			t.Fatalf("session %s", l.Session)
		}
	}
	f.Project, f.AllProjects = "", true
	if ds, _ = Load(dir, f, nil); len(ds.Lines) != 4 {
		t.Fatalf("all projects lines = %d", len(ds.Lines))
	}
	f.AllProjects, f.Session = false, "b"
	if ds, _ = Load(dir, f, nil); len(ds.Lines) != 2 || ds.Lines[0].Session != "b" {
		t.Fatalf("session lines = %+v", ds.Lines)
	}
}

func TestLoadSessionStartedBeforePeriod(t *testing.T) {
	dir := writeLog(t, audit.Full,
		started(t, "s", since.Add(-72*time.Hour), "/proj"),
		ev(t, "s", 2, since.Add(time.Hour), event.UserMessage, event.UserMessagePayload{Text: "x"}),
	)
	ds, err := Load(dir, Filter{Since: since, Until: until, Project: "/proj"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Lines) != 1 {
		t.Fatalf("lines = %d", len(ds.Lines))
	}
	si := ds.Sessions["s"]
	if si.Started == nil || si.Started.ProjectRoot != "/proj" || si.Started.Provider.ID != "p" {
		t.Fatalf("started = %+v", si.Started)
	}
}

func TestLoadMetadataLevel(t *testing.T) {
	d := since.Add(time.Hour)
	dir := writeLog(t, audit.Metadata,
		started(t, "s", d, "/proj"),
		ev(t, "s", 2, d.Add(time.Second), event.UserMessage, event.UserMessagePayload{Text: "x"}),
	)
	ds, err := Load(dir, Filter{Since: since, Until: until, Project: "/proj"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Lines) != 0 || ds.ExcludedMetadataSessions != 1 || ds.Level != LevelNone {
		t.Fatalf("lines %d excluded %d level %s", len(ds.Lines), ds.ExcludedMetadataSessions, ds.Level)
	}
	if ds.Sessions["s"].Started != nil {
		t.Fatal("Started must be nil at metadata level")
	}
	ds, err = Load(dir, Filter{Since: since, Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Lines) != 2 || ds.Level != LevelMetadata || ds.ExcludedMetadataSessions != 0 {
		t.Fatalf("lines %d level %s excluded %d", len(ds.Lines), ds.Level, ds.ExcludedMetadataSessions)
	}
}

func TestLoadMixedLevel(t *testing.T) {
	d := since.Add(time.Hour)
	dir := writeLog(t, audit.Full, started(t, "s", d, "/proj"))
	w, err := audit.Open(dir, audit.Metadata, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ev(t, "s", 2, d.Add(time.Second), event.UserMessage, event.UserMessagePayload{Text: "x"})); err != nil {
		t.Fatal(err)
	}
	w.Close()
	ds, err := Load(dir, Filter{Since: since, Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Level != LevelMixed {
		t.Fatalf("level = %s", ds.Level)
	}
}

func TestLoadEmpty(t *testing.T) {
	dir := writeLog(t, audit.Full)
	ds, err := Load(dir, Filter{Since: since, Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Level != LevelNone || len(ds.Lines) != 0 {
		t.Fatalf("level %s lines %d", ds.Level, len(ds.Lines))
	}
}

func TestLoadMissingDir(t *testing.T) {
	ds, err := Load(filepath.Join(t.TempDir(), "absent"), Filter{Since: since, Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Chain{Intact: true, Lines: 0, FinalHash: "sha256:0"}
	if ds.Chain != want || len(ds.Lines) != 0 || ds.Level != LevelNone || len(ds.Unreadable) != 0 {
		t.Fatalf("dataset = %+v", ds)
	}
}

func TestLoadUnreadable(t *testing.T) {
	for name, bad := range map[string]string{"garbage": "not json", "empty object": "{}"} {
		t.Run(name, func(t *testing.T) {
			d := since.Add(time.Hour)
			dir := writeLog(t, audit.Full, started(t, "s", d, "/proj"))
			files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
			if len(files) != 1 {
				t.Fatalf("files = %v", files)
			}
			f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString(bad + "\n")
			f.Close()
			// Même hors périmètre projet, la ligne illisible est signalée.
			ds, err := Load(dir, Filter{Since: since, Until: until, Project: "/other"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(ds.Unreadable) != 1 || ds.Unreadable[0].Line != 2 || ds.Unreadable[0].File != filepath.Base(files[0]) && ds.Unreadable[0].File != files[0] {
				t.Fatalf("unreadable = %+v", ds.Unreadable)
			}
			if ds.Chain.Intact || ds.Chain.Error == "" || ds.Chain.FinalHash != "" {
				t.Fatalf("chain = %+v", ds.Chain)
			}
		})
	}
}

func TestIsMetadataPayload(t *testing.T) {
	h := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		p    string
		want bool
	}{
		{`{"sha256":"` + h + `","size":12}`, true},
		{`{"size":0,"sha256":"` + h + `"}`, true},
		{`{"sha256":"` + h + `","size":12,"x":1}`, false},
		{`{"sha256":"` + h + `"}`, false},
		{`{"size":12}`, false},
		{`{"sha256":"` + h + `","size":"12"}`, false},
		{`{"sha256":"ABCDEF","size":1}`, false},
		{`{"sha256":"` + h[:63] + `","size":1}`, false},
		{`{"text":"x"}`, false},
		{`null`, false},
		{`[]`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := IsMetadataPayload(json.RawMessage(c.p)); got != c.want {
			t.Errorf("%s = %v, want %v", c.p, got, c.want)
		}
	}
}

// Une ligne ajoutée entre la vérification et la lecture n'est pas rapportée :
// le rapport décrit exactement les lignes vérifiées.
func TestLoadStopsAtVerifiedLines(t *testing.T) {
	d := since.Add(time.Hour)
	dir := writeLog(t, audit.Full,
		started(t, "s", d, "/proj"),
		ev(t, "s", 2, d.Add(time.Second), event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}),
	)
	old := afterVerify
	t.Cleanup(func() { afterVerify = old })
	afterVerify = func() {
		w, err := audit.Open(dir, audit.Full, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Write(ev(t, "s", 3, d.Add(2*time.Second), event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	ds, err := Load(dir, Filter{Since: since, Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ds.Chain.Intact || ds.Chain.Lines != 2 || len(ds.Lines) != 2 {
		t.Fatalf("chain %+v lines %d", ds.Chain, len(ds.Lines))
	}
}

// anchorHead range un jeton valide sur la tête de la chaîne de dir.
func anchorHead(t *testing.T, ca *tsptest.CA, dir string) {
	t.Helper()
	head, err := audit.Head(dir)
	if err != nil || head == "" {
		t.Fatalf("head %q: %v", head, err)
	}
	d, err := audit.Digest(head)
	if err != nil {
		t.Fatal(err)
	}
	tok := ca.Token(t, d, nil, tsptest.Options{})
	info, err := tsp.ParseToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.SaveToken(dir, info, tok); err != nil {
		t.Fatal(err)
	}
}

func TestLoadTimestamps(t *testing.T) {
	d := since.Add(time.Hour)
	dir := writeLog(t, audit.Full,
		started(t, "s1", d, "/p"),
		ev(t, "s1", 2, d.Add(time.Second), event.UserMessage, event.UserMessagePayload{Text: "x"}),
	)
	ca := tsptest.NewCA(t)
	f := Filter{Since: since, Until: until, AllProjects: true}
	ds, err := Load(dir, f, ca.Pool)
	if err != nil || ds.Timestamps.Count != 0 {
		t.Fatalf("sans jeton : %+v %v", ds.Timestamps, err)
	}
	anchorHead(t, ca, dir)
	ds, err = Load(dir, f, ca.Pool)
	if err != nil || ds.Timestamps.Count != 1 || ds.Timestamps.Valid != 1 || len(ds.Timestamps.Errors) != 0 || ds.Timestamps.LinesAfter != 0 {
		t.Fatalf("jeton valide : %+v %v", ds.Timestamps, err)
	}
	// Racines différentes : le jeton est invalide.
	if ds, _ = Load(dir, f, tsptest.NewCA(t).Pool); ds.Timestamps.Valid != 0 || len(ds.Timestamps.Errors) != 1 {
		t.Fatalf("autre CA : %+v", ds.Timestamps)
	}
	// Jeton orphelin : sa tête n'est pas dans la chaîne.
	orphan := tsptest.Options{}
	tok := ca.Token(t, make([]byte, 32), nil, orphan)
	info, err := tsp.ParseToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.SaveToken(dir, info, tok); err != nil {
		t.Fatal(err)
	}
	ds, err = Load(dir, f, ca.Pool)
	if err != nil || ds.Timestamps.Count != 2 || ds.Timestamps.Valid != 1 || len(ds.Timestamps.Errors) != 1 {
		t.Fatalf("orphelin : %+v %v", ds.Timestamps, err)
	}
}
