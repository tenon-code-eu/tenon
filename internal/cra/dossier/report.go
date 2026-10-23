package dossier

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

var reportIDRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(\d+)$`)

// rawReport sert à l'écriture ; la lecture décode les dates vers `any`
// (voir isLocalTOML).
type rawReport struct {
	Kind           string     `toml:"kind"`
	Summary        string     `toml:"summary"`
	AwareAt        *time.Time `toml:"aware_at,omitempty"`
	FixAvailableAt *time.Time `toml:"fix_available_at,omitempty"`
	EarlyWarningAt *time.Time `toml:"early_warning_at,omitempty"`
	NotificationAt *time.Time `toml:"notification_at,omitempty"`
	FinalAt        *time.Time `toml:"final_at,omitempty"`
}

type readReport struct {
	Kind           string `toml:"kind"`
	Summary        string `toml:"summary"`
	AwareAt        any    `toml:"aware_at"`
	FixAvailableAt any    `toml:"fix_available_at"`
	EarlyWarningAt any    `toml:"early_warning_at"`
	NotificationAt any    `toml:"notification_at"`
	FinalAt        any    `toml:"final_at"`
}

func loadReports(dir string) ([]Report, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cra: reports: %w", err)
	}
	var out []Report
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		r, err := loadReport(filepath.Join(dir, e.Name()), strings.TrimSuffix(e.Name(), ".toml"))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}

// idLess ordonne par date puis par numéro (numérique : -2 avant -10).
func idLess(a, b string) bool {
	ma, mb := reportIDRe.FindStringSubmatch(a), reportIDRe.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return a < b
	}
	if ma[1] != mb[1] {
		return ma[1] < mb[1]
	}
	na, _ := strconv.Atoi(ma[2])
	nb, _ := strconv.Atoi(mb[2])
	return na < nb
}

func loadReport(path, id string) (Report, error) {
	prefix := "cra: reports/" + id + ".toml: "
	if !reportIDRe.MatchString(id) {
		return Report{}, fmt.Errorf("%sinvalid report id (want YYYY-MM-DD-<n>)", prefix)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, fmt.Errorf("%s%w", prefix, err)
	}
	var raw readReport
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return Report{}, fmt.Errorf("%s%w", prefix, err)
	}
	if raw.Kind != "vulnerability" && raw.Kind != "incident" {
		return Report{}, fmt.Errorf("%skind must be vulnerability or incident", prefix)
	}
	if raw.AwareAt == nil {
		return Report{}, fmt.Errorf("%saware_at required", prefix)
	}
	r := Report{ID: id, Kind: raw.Kind, Summary: raw.Summary}
	for _, f := range []struct {
		key string
		in  any
		out **time.Time
	}{
		{"aware_at", raw.AwareAt, &r.AwareAt},
		{"fix_available_at", raw.FixAvailableAt, &r.FixAvailableAt},
		{"early_warning_at", raw.EarlyWarningAt, &r.EarlyWarningAt},
		{"notification_at", raw.NotificationAt, &r.NotificationAt},
		{"final_at", raw.FinalAt, &r.FinalAt},
	} {
		if f.in == nil {
			continue
		}
		t, ok := f.in.(time.Time)
		if !ok {
			return Report{}, fmt.Errorf("%s%s must be an RFC 3339 date-time", prefix, f.key)
		}
		// Une date-heure sans fuseau est refusée : les échéances se calculent en UTC.
		if isLocalTOML(t) {
			return Report{}, fmt.Errorf("%s%s must include a time zone", prefix, f.key)
		}
		u := t.UTC()
		*f.out = &u
	}
	return r, nil
}

// NewReportID renvoie le prochain identifiant libre AAAA-MM-JJ-<n> (date UTC de now).
func NewReportID(root string, now time.Time) string {
	day := now.UTC().Format("2006-01-02")
	max := 0
	ents, _ := os.ReadDir(filepath.Join(root, "cra", "reports"))
	for _, e := range ents {
		m := reportIDRe.FindStringSubmatch(strings.TrimSuffix(e.Name(), ".toml"))
		if m == nil || m[1] != day {
			continue
		}
		if n, _ := strconv.Atoi(m[2]); n > max {
			max = n
		}
	}
	return fmt.Sprintf("%s-%d", day, max+1)
}

// SaveReport crée cra/reports/<id>.toml (dates en UTC) et échoue si l'id existe.
func SaveReport(root string, r Report) error { return writeReport(root, r, true) }

// UpdateReport remplace un signalement existant (report mark).
func UpdateReport(root string, r Report) error { return writeReport(root, r, false) }

func writeReport(root string, r Report, exclusive bool) error {
	if !reportIDRe.MatchString(r.ID) {
		return fmt.Errorf("cra: invalid report id %q", r.ID)
	}
	dir := filepath.Join(root, "cra", "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cra: %w", err)
	}
	utc := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		u := t.UTC()
		return &u
	}
	raw := rawReport{
		Kind: r.Kind, Summary: r.Summary, AwareAt: utc(r.AwareAt), FixAvailableAt: utc(r.FixAvailableAt),
		EarlyWarningAt: utc(r.EarlyWarningAt), NotificationAt: utc(r.NotificationAt), FinalAt: utc(r.FinalAt),
	}
	tmp, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return fmt.Errorf("cra: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := toml.NewEncoder(tmp).Encode(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cra: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cra: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("cra: %w", err)
	}
	dst := filepath.Join(dir, r.ID+".toml")
	if exclusive {
		// Lien dur : atomique et refuse d'écraser un fichier existant.
		if err := os.Link(tmp.Name(), dst); err != nil {
			if errors.Is(err, os.ErrExist) {
				return fmt.Errorf("cra: report %s already exists", r.ID)
			}
			return fmt.Errorf("cra: %w", err)
		}
		return nil
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("cra: %w", err)
	}
	return nil
}
