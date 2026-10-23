package compliance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

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
		{ID: "2026-01-10-1", Kind: "vulnerability", Project: "/p", Step: "early-warning", At: "2026-01-10T10:00:00Z", Overdue: true, OverdueSteps: []string{"notification"}},
		{ID: "2026-01-12-1", Kind: "incident", Project: "/p", Step: "open", At: "2026-01-12T23:00:00Z", OverdueSteps: []string{}},
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
		{i18n.FR, "dernier scan 2026-01-11T10:00:00Z, 3 ouvertes, 1 à vérifier; 2026-01-10-1 (vulnerability) : early-warning, en retard (notification)"},
		{i18n.EN, "last scan 2026-01-11T10:00:00Z, 3 open, 1 to verify; 2026-01-10-1 (vulnerability): early-warning, overdue (notification)"},
	} {
		r := richReport(false)
		r.CRA = &CRA{LastScan: "2026-01-11T10:00:00Z", Open: 3, ToVerify: 1, Reports: []CRAReportStatus{
			{ID: "2026-01-10-1", Kind: "vulnerability", Step: "early-warning", Overdue: true, OverdueSteps: []string{"notification"}},
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
