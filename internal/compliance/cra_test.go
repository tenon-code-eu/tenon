package compliance

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// craStart ouvre une session « cra » sur la racine donnée.
func craStart(b *logb, sid, root string) {
	b.add(sid, event.SessionStarted, event.SessionStartedPayload{Kind: "cra", ProjectRoot: root})
}

func TestExportCRASection(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	craStart(b, "c1", "/p")
	b.add("c1", event.CRAScan, event.CRAScanPayload{SBOMs: 1, Components: 4, Open: 2, ToVerify: 1})
	craStart(b, "c2", "/p")
	scanAt := b.add("c2", event.CRAScan, event.CRAScanPayload{SBOMs: 1, Components: 4, Open: 3, ToVerify: 0})
	// Signalement 1 : alerte précoce faite, notification en retard (due 2026-01-11 00:00Z).
	craStart(b, "c3", "/p")
	b.add("c3", event.CRAReport, event.CRAReportPayload{ID: "2026-01-10-1", Kind: "vulnerability", Step: "open", At: "2026-01-10T00:00:00Z"})
	craStart(b, "c4", "/p")
	b.add("c4", event.CRAReport, event.CRAReportPayload{ID: "2026-01-10-1", Kind: "vulnerability", Step: "early-warning", At: "2026-01-10T10:00:00Z"})
	// Signalement 2 : incident ouvert à l'instant, rien de dépassé.
	craStart(b, "c5", "/p")
	b.add("c5", event.CRAReport, event.CRAReportPayload{ID: "2026-01-12-1", Kind: "incident", Step: "open", At: "2026-01-12T23:00:00Z"})
	r := Build(b.dataset(), opts())

	if r.CRA == nil {
		t.Fatal("section CRA absente")
	}
	if r.CRA.LastScan != scanAt.UTC().Format("2006-01-02T15:04:05Z") || r.CRA.Open != 3 || r.CRA.ToVerify != 0 {
		t.Fatalf("scan %+v", r.CRA)
	}
	want := []CRAReportStatus{
		{ID: "2026-01-10-1", Kind: "vulnerability", Project: "/p", Step: "early-warning", At: "2026-01-10T10:00:00Z", Overdue: true, OverdueSteps: []string{"notification"}, LateSteps: []string{}},
		{ID: "2026-01-12-1", Kind: "incident", Project: "/p", Step: "open", At: "2026-01-12T23:00:00Z", OverdueSteps: []string{}, LateSteps: []string{}},
	}
	if !reflect.DeepEqual(r.CRA.Reports, want) {
		t.Fatalf("reports %+v", r.CRA.Reports)
	}
	// Les sessions « cra » ne comptent pas dans les statistiques de conversation.
	if r.Summary.Sessions != 1 || r.Summary.UnknownProviderSessions != 0 || len(r.Providers) != 1 {
		t.Fatalf("stats %+v providers %+v", r.Summary, r.Providers)
	}
	out, _ := json.Marshal(r)
	if !strings.Contains(string(out), `"cra":{"last_scan":"`) {
		t.Fatalf("JSON : %s", out)
	}
}

func TestExportCRATimelineExcludesCRASessions(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	craStart(b, "c1", "/p")
	b.add("c1", event.CRAScan, event.CRAScanPayload{})
	o := opts()
	o.Details = true
	r := Build(b.dataset(), o)
	if len(r.Timeline) != 1 || r.Timeline[0].Session != "s" {
		t.Fatalf("timeline %+v", r.Timeline)
	}
}

func TestExportCRAPerProjectRoot(t *testing.T) {
	b := newLog(t)
	craStart(b, "c1", "/a")
	b.add("c1", event.CRAScan, event.CRAScanPayload{Open: 2, ToVerify: 1})
	craStart(b, "c2", "/b")
	b.add("c2", event.CRAScan, event.CRAScanPayload{Open: 4, ToVerify: 3})
	craStart(b, "c3", "/a")
	b.add("c3", event.CRAScan, event.CRAScanPayload{Open: 1, ToVerify: 0})
	r := Build(b.dataset(), opts())
	if r.CRA == nil || r.CRA.Open != 5 || r.CRA.ToVerify != 3 {
		t.Fatalf("%+v", r.CRA)
	}
}

func TestExportCRAAbsent(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	r := Build(b.dataset(), opts())
	out, _ := json.Marshal(r)
	if r.CRA != nil || strings.Contains(string(out), `"cra"`) {
		t.Fatalf("cra %+v / %s", r.CRA, out)
	}
}

func TestExportCRAHTML(t *testing.T) {
	for _, c := range []struct {
		l   i18n.Lang
		row string
	}{
		{i18n.FR, "dernier scan 2026-01-11T10:00:00Z, 3 ouvertes, 1 à vérifier, 40 non vérifiés, 2 conflits VEX; 2026-01-10-1 (vulnerability) : early-warning, en retard (notification), fait en retard (early-warning)"},
		{i18n.EN, "last scan 2026-01-11T10:00:00Z, 3 open, 1 to verify, 40 unchecked, 2 VEX conflicts; 2026-01-10-1 (vulnerability): early-warning, overdue (notification), done late (early-warning)"},
	} {
		r := richReport(false)
		r.CRA = &CRA{LastScan: "2026-01-11T10:00:00Z", Open: 3, ToVerify: 1, Unchecked: 40, Conflicts: 2, Reports: []CRAReportStatus{
			{ID: "2026-01-10-1", Kind: "vulnerability", Step: "early-warning", Overdue: true, OverdueSteps: []string{"notification"}, LateSteps: []string{"early-warning"}},
		}}
		out := render(t, r, c.l)
		if !strings.Contains(out, "<dt>CRA</dt><dd>"+c.row+"</dd>") {
			t.Errorf("%s : ligne CRA absente", c.l)
		}
		if out = render(t, richReport(false), c.l); strings.Contains(out, "<dt>CRA</dt>") {
			t.Errorf("%s : ligne CRA sans données", c.l)
		}
	}
}

// loadFrom charge le journal sur une période qui commence un jour après les événements.
func loadLater(t *testing.T, b *logb) Dataset {
	t.Helper()
	dir := writeLog(t, audit.Full, b.evs...)
	ds, err := Load(dir, Filter{Since: since.AddDate(0, 0, 1), Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestExportCRADeadlinesIgnorePeriod(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	craStart(b, "c1", "/p")
	b.add("c1", event.CRAReport, event.CRAReportPayload{ID: "2026-01-08-1", Kind: "vulnerability", Step: "open", At: "2026-01-08T00:00:00Z"})
	ds := loadLater(t, b)
	if len(ds.Lines) != 0 {
		t.Fatalf("lignes dans la période : %d", len(ds.Lines))
	}
	r := Build(ds, opts())
	if r.CRA == nil || len(r.CRA.Reports) != 1 {
		t.Fatalf("cra %+v", r.CRA)
	}
	s := r.CRA.Reports[0]
	if !s.Overdue || s.DeadlinesUnknown || !reflect.DeepEqual(s.OverdueSteps, []string{"early-warning", "notification"}) {
		t.Fatalf("report %+v", s)
	}
	if r.Summary.Sessions != 0 || r.Summary.UnknownProviderSessions != 0 {
		t.Fatalf("stats %+v", r.Summary)
	}
}

func TestExportCRAFinalReportOutOfPeriodNotListed(t *testing.T) {
	b := newLog(t)
	craStart(b, "c1", "/p")
	b.add("c1", event.CRAReport, event.CRAReportPayload{ID: "2026-01-08-1", Kind: "incident", Step: "open", At: "2026-01-08T00:00:00Z"})
	b.add("c1", event.CRAReport, event.CRAReportPayload{ID: "2026-01-08-1", Kind: "incident", Step: "final", At: "2026-01-09T00:00:00Z"})
	if r := Build(loadLater(t, b), opts()); r.CRA != nil {
		t.Fatalf("rapport clos hors période listé : %+v", r.CRA)
	}
}

func TestExportCRADeadlinesUnknownWithoutOpen(t *testing.T) {
	b := newLog(t)
	craStart(b, "c1", "/p")
	b.add("c1", event.CRAReport, event.CRAReportPayload{ID: "2026-01-08-1", Kind: "vulnerability", Step: "early-warning", At: "2026-01-09T00:00:00Z"})
	r := Build(b.dataset(), opts())
	if r.CRA == nil || !r.CRA.Reports[0].DeadlinesUnknown || r.CRA.Reports[0].Overdue {
		t.Fatalf("%+v", r.CRA)
	}
	for _, c := range []struct {
		l    i18n.Lang
		want string
	}{{i18n.EN, "deadlines unknown (no open event)"}, {i18n.FR, "échéances inconnues (ouverture absente)"}} {
		rr := richReport(false)
		rr.CRA = r.CRA
		out := render(t, rr, c.l)
		if !strings.Contains(out, c.want) || strings.Contains(out, "!report.") {
			t.Errorf("%s : marqueur %q absent ou clé manquante", c.l, c.want)
		}
	}
}

// Toutes les clés report.cra_* du gabarit existent dans les deux langues.
func TestCRAKeysResolve(t *testing.T) {
	src, err := os.ReadFile("html.go")
	if err != nil {
		t.Fatal(err)
	}
	keys := regexp.MustCompile(`"(report\.(?:label\.)?cra[a-z_]*)"`).FindAllSubmatch(src, -1)
	if len(keys) == 0 {
		t.Fatal("aucune clé trouvée")
	}
	for _, k := range keys {
		for _, l := range []i18n.Lang{i18n.EN, i18n.FR} {
			if got := i18n.T(l, string(k[1])); strings.HasPrefix(got, "!") {
				t.Errorf("%s : clé %s manquante", l, k[1])
			}
		}
	}
}

func TestExportCRASessionStartedBeforePeriod(t *testing.T) {
	b := newLog(t)
	craStart(b, "c1", "/p")
	b.add("c1", event.CRAScan, event.CRAScanPayload{Open: 2})
	// Décaler le scan dans la période : l'événement tardif seul est dans la plage.
	dir := writeLog(t, audit.Full, b.evs...)
	ds, err := Load(dir, Filter{Since: since, Until: until, AllProjects: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ds.Lines = ds.Lines[1:] // plus de SessionStarted dans la plage
	ds.Sessions["c1"] = SessionInfo{}
	r := Build(ds, opts())
	if r.Summary.Sessions != 0 || r.Summary.UnknownProviderSessions != 0 || r.CRA == nil || r.CRA.Open != 2 {
		t.Fatalf("%+v %+v", r.Summary, r.CRA)
	}
}

// TestExportCRAUncheckedConflicts : composants non vérifiés et conflits VEX
// du dernier scan de chaque racine, cumulés ; un scan « 0 ouverte » avec 40
// composants non vérifiés n'a pas l'air propre.
func TestExportCRAUncheckedConflicts(t *testing.T) {
	b := newLog(t)
	craStart(b, "c1", "/a")
	b.add("c1", event.CRAScan, event.CRAScanPayload{Open: 0, Unchecked: 7, Conflicts: 3})
	craStart(b, "c2", "/a")
	b.add("c2", event.CRAScan, event.CRAScanPayload{Open: 0, Unchecked: 40, Conflicts: 1})
	craStart(b, "c3", "/b")
	b.add("c3", event.CRAScan, event.CRAScanPayload{Unchecked: 2})
	r := Build(b.dataset(), opts())
	if r.CRA == nil || r.CRA.Unchecked != 42 || r.CRA.Conflicts != 1 {
		t.Fatalf("%+v", r.CRA)
	}
	out, _ := json.Marshal(r.CRA)
	if !strings.Contains(string(out), `"unchecked":42`) || !strings.Contains(string(out), `"conflicts":1`) {
		t.Fatalf("JSON : %s", out)
	}
}

// TestExportCRADoneLate : une étape faite après son échéance est listée.
func TestExportCRADoneLate(t *testing.T) {
	b := newLog(t)
	craStart(b, "c1", "/p")
	b.add("c1", event.CRAReport, event.CRAReportPayload{ID: "2026-01-10-1", Kind: "vulnerability", Step: "open", At: "2026-01-10T00:00:00Z"})
	craStart(b, "c2", "/p")
	b.add("c2", event.CRAReport, event.CRAReportPayload{ID: "2026-01-10-1", Kind: "vulnerability", Step: "early-warning", At: "2026-01-11T10:00:00Z"})
	r := Build(b.dataset(), opts())
	if r.CRA == nil || len(r.CRA.Reports) != 1 {
		t.Fatalf("%+v", r.CRA)
	}
	if got := r.CRA.Reports[0].LateSteps; !reflect.DeepEqual(got, []string{"early-warning"}) {
		t.Fatalf("late %v", got)
	}
}
