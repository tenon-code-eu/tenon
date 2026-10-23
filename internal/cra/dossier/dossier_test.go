package dossier

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const goodProduct = `name = "P"
version = "1.0"
manufacturer = "M"
security_contact = "s@x.eu"
support_end = 2031-12-31
category = "default"
`

func TestLoadProduct(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root); !errors.Is(err, ErrNoDossier) {
		t.Fatalf("want ErrNoDossier, got %v", err)
	}
	write(t, root, "cra/product.toml", goodProduct)
	d, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if d.Product.Name != "P" || d.Product.Category != "default" {
		t.Fatalf("%+v", d.Product)
	}
	if want := time.Date(2031, 12, 31, 0, 0, 0, 0, time.UTC); !d.Product.SupportEnd.Equal(want) || d.Product.SupportEnd.Location() != time.UTC {
		t.Fatalf("support_end %v", d.Product.SupportEnd)
	}
	if len(d.Product.SBOMs) != 1 || d.Product.SBOMs[0] != "cra/sbom*.cdx.json" {
		t.Fatalf("sboms %v", d.Product.SBOMs)
	}
	for _, f := range []string{"name", "version", "manufacturer", "security_contact", "support_end", "category"} {
		var keep []string
		for _, l := range strings.Split(goodProduct, "\n") {
			if !strings.HasPrefix(l, f+" ") {
				keep = append(keep, l)
			}
		}
		write(t, root, "cra/product.toml", strings.Join(keep, "\n"))
		_, err := Load(root)
		if err == nil || err.Error() != "cra: product.toml: "+f+" required" {
			t.Errorf("%s: %v", f, err)
		}
	}
	write(t, root, "cra/product.toml", strings.Replace(goodProduct, `"default"`, `"huge"`, 1))
	_, err = Load(root)
	if err == nil || err.Error() != "cra: product.toml: category must be default, important-1, important-2 or critical" {
		t.Fatal(err)
	}
	// support_end en date-heure avec fuseau accepté.
	write(t, root, "cra/product.toml", strings.Replace(goodProduct, "2031-12-31", "2031-12-31T10:00:00+02:00", 1))
	if d, err = Load(root); err != nil || d.Product.SupportEnd.Day() != 31 {
		t.Fatal(err, d.Product.SupportEnd)
	}
}

func TestLoadVEX(t *testing.T) {
	root := t.TempDir()
	if v, err := LoadVEX(filepath.Join(root, "none.json")); err != nil || len(v) != 0 {
		t.Fatal(v, err)
	}
	p := filepath.Join(root, "vex.json")
	write(t, root, "vex.json", `{"vulnerabilities":[{"id":"CVE-1","analysis":{"state":"not_affected"},"affects":[{"ref":"pkg:npm/a"}]}]}`)
	_, err := LoadVEX(p)
	if err == nil || err.Error() != "cra: vex: CVE-1: not_affected requires a justification" {
		t.Fatal(err)
	}
	write(t, root, "vex.json", `{"vulnerabilities":[
	 {"id":"CVE-2","analysis":{"state":"in_triage"},"affects":[{"ref":"pkg:npm/a"},{"ref":"b"}]},
	 {"id":"CVE-3","analysis":{"state":"not_affected","justification":"code_not_reachable"},"affects":[{"ref":"c"}]}]}`)
	v, err := LoadVEX(p)
	if err != nil || len(v) != 3 {
		t.Fatal(v, err)
	}
	if v[0].State != "under_investigation" || v[2].Justification != "code_not_reachable" {
		t.Fatalf("%+v", v)
	}
}

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	x, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func find(ds []Deadline, step string) Deadline {
	for _, d := range ds {
		if d.Step == step {
			return d
		}
	}
	return Deadline{}
}

func TestDeadlinesVulnerability(t *testing.T) {
	a := ts(t, "2026-10-23T09:00:00+02:00")
	r := Report{Kind: "vulnerability", AwareAt: &a}
	now := ts(t, "2026-10-23T10:00:00Z")
	ds := Deadlines(r, now)
	if got := find(ds, "early-warning").Due; !got.Equal(ts(t, "2026-10-24T07:00:00Z")) || got.Location() != time.UTC {
		t.Fatal(got)
	}
	if got := find(ds, "notification").Due; !got.Equal(ts(t, "2026-10-26T07:00:00Z")) {
		t.Fatal(got)
	}
	if f := find(ds, "final"); f.Due != nil || f.Status != "unknown" {
		t.Fatalf("%+v", f)
	}
	fx := ts(t, "2026-10-30T12:00:00Z")
	r.FixAvailableAt = &fx
	if f := find(Deadlines(r, now), "final"); f.Due == nil || !f.Due.Equal(ts(t, "2026-11-13T12:00:00Z")) || f.Status != "due" {
		t.Fatalf("%+v", f)
	}
}

func TestDeadlinesIncident(t *testing.T) {
	a := ts(t, "2027-01-01T00:00:00Z")
	n := ts(t, "2027-01-31T10:30:00Z")
	r := Report{Kind: "incident", AwareAt: &a, NotificationAt: &n}
	if f := find(Deadlines(r, a), "final"); f.Due == nil || !f.Due.Equal(ts(t, "2027-02-28T10:30:00Z")) {
		t.Fatalf("%+v", f)
	}
	n = ts(t, "2026-10-23T08:00:00Z")
	if f := find(Deadlines(r, a), "final"); !f.Due.Equal(ts(t, "2026-11-23T08:00:00Z")) {
		t.Fatalf("%+v", f)
	}
	// Bissextile.
	n = ts(t, "2028-01-31T00:00:00Z")
	if f := find(Deadlines(r, a), "final"); !f.Due.Equal(ts(t, "2028-02-29T00:00:00Z")) {
		t.Fatalf("%+v", f)
	}
}

func TestDeadlinesOverdueDone(t *testing.T) {
	a := ts(t, "2026-10-23T09:00:00Z")
	e := ts(t, "2026-10-23T20:00:00Z")
	r := Report{Kind: "vulnerability", AwareAt: &a, EarlyWarningAt: &e}
	ds := Deadlines(r, ts(t, "2026-10-27T00:00:00Z"))
	if s := find(ds, "early-warning").Status; s != "done" {
		t.Fatal(s)
	}
	if s := find(ds, "notification").Status; s != "overdue" {
		t.Fatal(s)
	}
	ds = Deadlines(r, ts(t, "2026-10-25T00:00:00Z"))
	if s := find(ds, "notification").Status; s != "due" {
		t.Fatal(s)
	}
}

func TestDeadlinesDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 2026-10-24 12:00 Paris (CEST), la veille du passage à l'heure d'hiver.
	a := time.Date(2026, 10, 24, 12, 0, 0, 0, loc)
	r := Report{Kind: "vulnerability", AwareAt: &a}
	ds := Deadlines(r, a)
	if got := find(ds, "early-warning").Due; got.Sub(a) != 24*time.Hour {
		t.Fatal(got.Sub(a))
	}
	if got := find(ds, "notification").Due; got.Sub(a) != 72*time.Hour {
		t.Fatal(got.Sub(a))
	}
}

func TestNewReportID(t *testing.T) {
	root := t.TempDir()
	now := ts(t, "2026-10-23T23:30:00Z")
	if id := NewReportID(root, now); id != "2026-10-23-1" {
		t.Fatal(id)
	}
	a := now
	if err := SaveReport(root, Report{ID: "2026-10-23-1", Kind: "vulnerability", Summary: "s", AwareAt: &a}); err != nil {
		t.Fatal(err)
	}
	if id := NewReportID(root, now); id != "2026-10-23-2" {
		t.Fatal(id)
	}
}

func TestReportRoundTripAndZone(t *testing.T) {
	root := t.TempDir()
	write(t, root, "cra/product.toml", goodProduct)
	a := ts(t, "2026-10-23T09:00:00+02:00")
	f := ts(t, "2026-10-25T00:00:00Z")
	if err := SaveReport(root, Report{ID: "2026-10-23-1", Kind: "vulnerability", Summary: "s", AwareAt: &a, FixAvailableAt: &f}); err != nil {
		t.Fatal(err)
	}
	d, err := Load(root)
	if err != nil || len(d.Reports) != 1 {
		t.Fatal(d.Reports, err)
	}
	r := d.Reports[0]
	if !r.AwareAt.Equal(a) || r.FixAvailableAt == nil || r.NotificationAt != nil || r.Summary != "s" {
		t.Fatalf("%+v", r)
	}
	write(t, root, "cra/reports/2026-10-24-1.toml", "kind = \"incident\"\naware_at = 2026-10-24T09:00:00\n")
	_, err = Load(root)
	if err == nil || err.Error() != "cra: reports/2026-10-24-1.toml: aware_at must include a time zone" {
		t.Fatal(err)
	}
}

func TestSupportEndPast(t *testing.T) {
	root := t.TempDir()
	write(t, root, "cra/product.toml", strings.Replace(goodProduct, "2031-12-31", "2020-06-30", 1))
	d, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	now := ts(t, "2026-10-23T00:00:00Z")
	got := d.Incomplete(now)
	if len(got) != 1 || got[0] != "support period ended on 2020-06-30" {
		t.Fatal(got)
	}
	if got := d.Incomplete(ts(t, "2020-06-30T23:00:00Z")); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestVEXStatesMapped(t *testing.T) {
	root := t.TempDir()
	write(t, root, "v.json", `{"vulnerabilities":[
	 {"id":"A","analysis":{"state":"false_positive","justification":"code_not_present"},"affects":[{"ref":"x"}]},
	 {"id":"B","analysis":{"state":"resolved_with_pedigree"},"affects":[{"ref":"x"}]}]}`)
	v, err := LoadVEX(filepath.Join(root, "v.json"))
	if err != nil || v[0].State != "not_affected" || v[1].State != "fixed" {
		t.Fatal(v, err)
	}
	write(t, root, "w.json", `{"vulnerabilities":[{"id":"A","analysis":{"state":"false_positive"},"affects":[{"ref":"x"}]}]}`)
	if _, err := LoadVEX(filepath.Join(root, "w.json")); err == nil || err.Error() != "cra: vex: A: not_affected requires a justification" {
		t.Fatal(err)
	}
}

func TestNoSBOMFound(t *testing.T) {
	root := t.TempDir()
	write(t, root, "cra/product.toml", goodProduct)
	d, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SBOMPaths(); !errors.Is(err, ErrNoDefaultSBOM) || err.Error() != "cra: no SBOM found (cra/sbom*.cdx.json)" {
		t.Fatal(err)
	}
	write(t, root, "cra/sbom.cdx.json", "{}")
	if p, err := d.SBOMPaths(); err != nil || len(p) != 1 {
		t.Fatal(p, err)
	}
}

func TestSaveReportExclusive(t *testing.T) {
	root := t.TempDir()
	a := ts(t, "2026-10-23T09:00:00Z")
	r := Report{ID: "2026-10-23-1", Kind: "incident", AwareAt: &a}
	if err := SaveReport(root, r); err != nil {
		t.Fatal(err)
	}
	if err := SaveReport(root, r); err == nil {
		t.Fatal("second SaveReport must fail")
	}
	n := ts(t, "2026-10-24T09:00:00Z")
	r.NotificationAt = &n
	if err := UpdateReport(root, r); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReport(t *testing.T) {
	root := t.TempDir()
	write(t, root, "cra/reports/2026-10-21-1.toml", "kind = \"incident\"\nsummary = \"x\"\naware_at = 2026-10-21T08:00:00+02:00\n")
	r, err := LoadReport(root, "2026-10-21-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "2026-10-21-1" || r.Kind != "incident" || r.AwareAt == nil || !r.AwareAt.Equal(time.Date(2026, 10, 21, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", r)
	}
	for _, id := range []string{"2026-10-21-2", "../x", "bogus"} {
		if _, err := LoadReport(root, id); err == nil || err.Error() != "cra: report "+id+" not found" {
			t.Errorf("%s : %v", id, err)
		}
	}
}

// TestSBOMEntryMissing : chaque entrée de sboms doit trouver au moins un
// fichier ; un SBOM listé absent n'est jamais ignoré en silence.
func TestSBOMEntryMissing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "cra/product.toml", goodProduct+`sboms = ["cra/go.cdx.json", "cra/npm.cdx.json"]`+"\n")
	write(t, root, "cra/go.cdx.json", "{}")
	d, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.SBOMPaths()
	var miss *SBOMMissingError
	if !errors.As(err, &miss) || miss.Entry != "cra/npm.cdx.json" || errors.Is(err, ErrNoDefaultSBOM) || err.Error() != "cra: no SBOM found (cra/npm.cdx.json)" {
		t.Fatalf("SBOM listé absent : %v", err)
	}
	// Le motif par défaut listé explicitement reste une entrée explicite.
	write(t, root, "cra/product.toml", goodProduct+`sboms = ["cra/sbom*.cdx.json"]`+"\n")
	if d2, err := Load(root); err != nil {
		t.Fatal(err)
	} else if _, err := d2.SBOMPaths(); !errors.As(err, &miss) || errors.Is(err, ErrNoDefaultSBOM) {
		t.Fatalf("motif explicite : %v", err)
	}
	write(t, root, "cra/npm.cdx.json", "{}")
	if p, err := d.SBOMPaths(); err != nil || len(p) != 2 {
		t.Fatal(p, err)
	}
}

// TestUnknownKeys : une clé inconnue de product.toml ou d'un signalement est
// refusée (report mark ne réécrit jamais un fichier qu'il n'a pas lu en entier).
func TestUnknownKeys(t *testing.T) {
	root := t.TempDir()
	write(t, root, "cra/product.toml", goodProduct+"owner = \"x\"\n")
	if _, err := Load(root); err == nil || err.Error() != "cra: product.toml: unknown key owner" {
		t.Fatalf("product.toml : %v", err)
	}
	write(t, root, "cra/product.toml", goodProduct+"[extra]\nnote = \"x\"\n")
	if _, err := Load(root); err == nil || !strings.HasPrefix(err.Error(), "cra: product.toml: unknown key extra") {
		t.Fatalf("product.toml table : %v", err)
	}
	write(t, root, "cra/product.toml", goodProduct)
	write(t, root, "cra/reports/2026-10-21-1.toml", "kind = \"incident\"\nsummary = \"x\"\naware_at = 2026-10-21T08:00:00Z\nnotes = \"keep me\"\n")
	if _, err := LoadReport(root, "2026-10-21-1"); err == nil || err.Error() != "cra: reports/2026-10-21-1.toml: unknown key notes" {
		t.Fatalf("signalement : %v", err)
	}
	if _, err := Load(root); err == nil || err.Error() != "cra: reports/2026-10-21-1.toml: unknown key notes" {
		t.Fatalf("Load : %v", err)
	}
}

// TestDeadlinesDoneLate : une étape faite après son échéance est done_late.
func TestDeadlinesDoneLate(t *testing.T) {
	a := ts(t, "2026-10-20T09:00:00Z")
	early := ts(t, "2026-10-21T10:00:00Z") // échéance 2026-10-21T09:00:00Z
	notif := ts(t, "2026-10-22T09:00:00Z") // avant l'échéance (2026-10-23T09:00:00Z)
	r := Report{ID: "2026-10-20-1", Kind: "vulnerability", AwareAt: &a, EarlyWarningAt: &early, NotificationAt: &notif}
	ds := Deadlines(r, ts(t, "2026-10-30T00:00:00Z"))
	if ds[0].Status != StatusDoneLate || ds[1].Status != StatusDone || ds[2].Status != StatusUnknown {
		t.Fatalf("%+v", ds)
	}
	// Faite à l'heure exacte de l'échéance : à temps.
	onTime := ts(t, "2026-10-21T09:00:00Z")
	r.EarlyWarningAt = &onTime
	if ds := Deadlines(r, ts(t, "2026-10-30T00:00:00Z")); ds[0].Status != StatusDone {
		t.Fatalf("%+v", ds[0])
	}
}
