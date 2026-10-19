package checkpoint

import (
	"errors"
	"testing"
)

type fsFake map[string]FileState

func (f fsFake) cur(p string) FileState { return f[p] }

func file(h string) FileState { return FileState{Exists: true, Hash: h} }

func hasAll(string) bool { return true }

func basePoints() []Point {
	return []Point{{Seq: 1, Excerpt: "a", HistoryLen: 0, Conversation: true}, {Seq: 4, Excerpt: "c", HistoryLen: 2}, {Seq: 10, Excerpt: "b", HistoryLen: 4}}
}

func one(t *testing.T, st State, to uint64, cur fsFake, has func(string) bool) FilePlan {
	t.Helper()
	p, err := BuildPlan(st, to, cur.cur, has)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 1 {
		t.Fatalf("files = %+v", p.Files)
	}
	return p.Files[0]
}

func TestBuildPlan(t *testing.T) {
	tests := []struct {
		name   string
		writes []Write
		cur    fsFake
		has    func(string) bool
		action string
		status string
		target string
		expect string
	}{
		{"created -> delete ok", []Write{{Seq: 5, Path: "/f", Before: "", After: "h1"}}, fsFake{"/f": file("h1")}, hasAll, "delete", "ok", "", "h1"},
		{"created then user-deleted -> noop", []Write{{Seq: 5, Path: "/f", Before: "", After: "h1"}}, fsFake{}, hasAll, "delete", "noop", "", "h1"},
		{"modified once -> restore", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1"}}, fsFake{"/f": file("h1")}, hasAll, "restore", "ok", "h0", "h1"},
		{"modified twice -> first before", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1"}, {Seq: 6, Path: "/f", Before: "h1", After: "h2"}}, fsFake{"/f": file("h2")}, hasAll, "restore", "ok", "h0", "h2"},
		{"user changed -> conflict", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1"}}, fsFake{"/f": file("hX")}, hasAll, "restore", "conflict", "h0", "h1"},
		{"symlink -> conflict", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1"}}, fsFake{"/f": {Exists: true, Symlink: true, Hash: "h1"}}, hasAll, "restore", "conflict", "h0", "h1"},
		{"user deleted modified -> conflict", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1"}}, fsFake{}, hasAll, "restore", "conflict", "h0", "h1"},
		{"missing copy -> unrestorable", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1"}}, fsFake{"/f": file("h1")}, func(string) bool { return false }, "restore", "unrestorable", "h0", "h1"},
		{"no checkpoint -> unrestorable", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1", NoCheckpoint: true}}, fsFake{"/f": file("h1")}, hasAll, "restore", "unrestorable", "h0", "h1"},
		{"delete never unrestorable", []Write{{Seq: 5, Path: "/f", Before: "", After: "h1", NoCheckpoint: true}}, fsFake{"/f": file("h1")}, func(string) bool { return false }, "delete", "ok", "", "h1"},
		{"already at target -> noop", []Write{{Seq: 5, Path: "/f", Before: "h0", After: "h1"}}, fsFake{"/f": file("h0")}, hasAll, "restore", "noop", "h0", "h1"},
		{"writes before point ignored", []Write{{Seq: 3, Path: "/f", Before: "h0", After: "h1"}, {Seq: 5, Path: "/f", Before: "h1", After: "h2"}}, fsFake{"/f": file("h2")}, hasAll, "restore", "ok", "h1", "h2"},
		{"earlier no_checkpoint ignored", []Write{{Seq: 3, Path: "/f", Before: "h0", After: "h1", NoCheckpoint: true}, {Seq: 5, Path: "/f", Before: "h1", After: "h2"}}, fsFake{"/f": file("h2")}, hasAll, "restore", "ok", "h1", "h2"},
		{"recorded rewind deletion then user recreate -> conflict", []Write{{Seq: 5, Path: "/f", Before: "", After: "h1"}, {Seq: 11, Path: "/f", Before: "h1", After: ""}}, fsFake{"/f": file("h9")}, hasAll, "delete", "conflict", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := State{Points: basePoints(), Writes: tc.writes}
			f := one(t, st, 4, tc.cur, tc.has)
			if f.Action != tc.action || f.Status != tc.status || f.Target != tc.target || f.Expected != tc.expect {
				t.Fatalf("got %+v", f)
			}
		})
	}
}

func TestBuildPlanRewindThenNewWrite(t *testing.T) {
	// Point 1; écriture 5 (h0->h1); rewind enregistré 6 (h1->h0); point 10; écriture 12 (h0->h2).
	st := State{Points: basePoints(), Writes: []Write{
		{Seq: 5, Path: "/f", Before: "h0", After: "h1"},
		{Seq: 6, Path: "/f", Before: "h1", After: "h0"},
		{Seq: 12, Path: "/f", Before: "h0", After: "h2"},
	}}
	f := one(t, st, 10, fsFake{"/f": file("h2")}, hasAll)
	if f.Action != "restore" || f.Status != "ok" || f.Target != "h0" || f.Expected != "h2" {
		t.Fatalf("got %+v", f)
	}
	// Plan vers le point 1 : cible = Before de la 1re écriture, attendu = dernière.
	f = one(t, st, 4, fsFake{"/f": file("h2")}, hasAll)
	if f.Target != "h0" || f.Expected != "h2" || f.Status != "ok" {
		t.Fatalf("got %+v", f)
	}
}

func TestBuildPlanMisc(t *testing.T) {
	st := State{
		Points: basePoints(),
		Writes: []Write{
			{Seq: 5, Path: "/b", Before: "", After: "h1"},
			{Seq: 6, Path: "/a", Before: "h0", After: "h2"},
		},
		Untracked: []Untracked{{Seq: 1, Tool: "bash"}, {Seq: 7, Tool: "bash"}, {Seq: 8, Tool: "mcp"}},
	}
	cur := fsFake{"/a": file("h2"), "/b": file("h1")}
	p, err := BuildPlan(st, 1, cur.cur, hasAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 2 || p.Files[0].Path != "/a" || p.Files[1].Path != "/b" {
		t.Fatalf("files = %+v", p.Files)
	}
	if p.UntrackedCalls != 2 {
		t.Fatalf("untracked = %d", p.UntrackedCalls)
	}
	if !p.ConversationAvailable || p.Point.Seq != 1 {
		t.Fatalf("plan = %+v", p)
	}
	p, _ = BuildPlan(st, 10, cur.cur, hasAll)
	if p.ConversationAvailable || len(p.Files) != 0 || p.UntrackedCalls != 0 {
		t.Fatalf("plan = %+v", p)
	}
	if _, err := BuildPlan(st, 99, cur.cur, hasAll); !errors.Is(err, ErrUnknownPoint) {
		t.Fatalf("err = %v", err)
	}
}
