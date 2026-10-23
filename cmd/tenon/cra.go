package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/cra/dossier"
	"github.com/tenon-code-eu/tenon/internal/cra/osv"
	"github.com/tenon-code-eu/tenon/internal/cra/report"
	"github.com/tenon-code-eu/tenon/internal/cra/sbom"
	"github.com/tenon-code-eu/tenon/internal/cra/scan"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/profile"
)

// Codes de sortie propres à « tenon cra » : 7 pour une catégorie listée par
// scan --fail-on non nulle, ou une échéance dépassée (status).
const craExitFinding = 7

// craFailOn : catégories acceptées par scan --fail-on.
var craFailOn = []string{scan.StatusOpen, scan.StatusToVerify, "unchecked", "conflicts"}

// parseFailOn lit la liste séparée par des virgules de --fail-on ; vide : aucune.
func parseFailOn(v string) (map[string]bool, error) {
	out := map[string]bool{}
	if v == "" {
		return out, nil
	}
	bad := errors.New("cra: --fail-on must be a comma-separated list of " + strings.Join(craFailOn, ", "))
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		ok := false
		for _, c := range craFailOn {
			ok = ok || p == c
		}
		if !ok {
			return nil, bad
		}
		out[p] = true
	}
	return out, nil
}

// failOnHit indique qu'une catégorie listée est non nulle.
func failOnHit(cats map[string]bool, res scan.Result) bool {
	return (cats[scan.StatusOpen] && res.Open > 0) || (cats[scan.StatusToVerify] && res.ToVerify > 0) ||
		(cats["unchecked"] && res.Unchecked > 0) || (cats["conflicts"] && res.Conflicts > 0)
}

// scanPayload résume un scan pour l'événement CRAScan.
func scanPayload(res scan.Result, dbDate time.Time) event.CRAScanPayload {
	p := event.CRAScanPayload{
		SBOMs: res.SBOMs, Components: res.Components, Open: res.Open, ToVerify: res.ToVerify,
		NotAffected: res.NotAffected, Fixed: res.Fixed, Unchecked: res.Unchecked, Conflicts: res.Conflicts,
	}
	if !dbDate.IsZero() {
		p.DBDate = dbDate.UTC().Format(time.RFC3339)
	}
	return p
}

// craOldDB : au-delà, la base OSV locale fait l'objet d'un avis.
const craOldDB = 7 * 24 * time.Hour

// craHTTPClient construit le client de téléchargement OSV ; remplacé dans les
// tests (certificat du miroir de test).
var craHTTPClient = osv.Client

// craOSVDir est la base OSV locale sous le dossier de données.
func craOSVDir(getenv func(string) string) string {
	return filepath.Join(config.DataDir(getenv), "cra", "osv")
}

// craEnv regroupe ce dont les sous-commandes ont besoin.
type craEnv struct {
	ctx        context.Context
	lang       i18n.Lang
	stdout     io.Writer
	stderr     io.Writer
	getenv     func(string) string
	wd         string
	globalPath string
}

// fail écrit l'erreur (déjà préfixée « cra: » pour les erreurs du domaine) et renvoie 1.
func (c craEnv) fail(err error) int {
	msg := err.Error()
	if !strings.HasPrefix(msg, "cra:") {
		msg = "cra: " + msg
	}
	fmt.Fprintln(c.stderr, msg)
	return 1
}

// craCommand exécute « tenon cra <sous-commande> ».
func craCommand(ctx context.Context, lang i18n.Lang, args []string, stdout, stderr io.Writer, getenv func(string) string, wd, globalPath string) int {
	c := craEnv{ctx: ctx, lang: lang, stdout: stdout, stderr: stderr, getenv: getenv, wd: wd, globalPath: globalPath}
	if len(args) == 0 {
		fmt.Fprintln(stderr, i18n.T(lang, "cra.usage"))
		return 1
	}
	switch {
	case args[0] == "init":
		return c.init(args[1:])
	case args[0] == "db" && len(args) >= 2 && args[1] == "update":
		return c.dbUpdate(args[2:])
	case args[0] == "scan":
		return c.scan(args[1:])
	case args[0] == "status":
		return c.status(args[1:])
	case args[0] == "report" && len(args) >= 2 && args[1] == "open":
		return c.reportOpen(args[2:])
	case args[0] == "report" && len(args) >= 2 && args[1] == "mark":
		return c.reportMark(args[2:])
	case args[0] == "export":
		return c.export(args[1:])
	}
	fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", args[0]))
	fmt.Fprintln(stderr, i18n.T(lang, "cra.usage"))
	return 1
}

// parseInterleaved analyse les options où qu'elles soient parmi les
// arguments positionnels (« report mark <id> <step> --at … »).
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		rest := fs.Args()
		// Après « -- », tout est positionnel.
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// flags crée le jeu d'options d'une sous-commande.
func (c craEnv) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("tenon cra "+name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	return fs
}

// parse analyse args ; ok=false avec le code de sortie (0 pour -h).
func (c craEnv) parse(fs *flag.FlagSet, args []string, maxPos int) ([]string, int, bool) {
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, 0, false
		}
		return nil, 1, false
	}
	if len(pos) > maxPos {
		fmt.Fprintln(c.stderr, i18n.T(c.lang, "err.unexpected_args", pos[maxPos]))
		return nil, 1, false
	}
	return pos, 0, true
}

// productTemplate est le modèle commenté de cra/product.toml.
const productTemplate = `# CRA product description (tenon cra). Fill in every field, then run: tenon cra status
# Product covered by this dossier.
name = ""
version = ""
# Manufacturer (legal entity) and security contact (e-mail or URL) for vulnerability reports.
manufacturer = ""
security_contact = ""
# End of the support period (YYYY-MM-DD): uncomment and set.
# support_end = 2031-12-31
# Category: default, important-1, important-2 or critical.
category = "default"
# SBOM files (CycloneDX JSON 1.4-1.6), relative to the repository root.
# sboms = ["cra/sbom*.cdx.json"]
`

// init crée cra/product.toml (modèle) et cra/reports/ ; refuse si product.toml existe.
func (c craEnv) init(args []string) int {
	fs := c.flags("init")
	if _, code, ok := c.parse(fs, args, 0); !ok {
		return code
	}
	path := filepath.Join(c.wd, "cra", "product.toml")
	if _, err := os.Lstat(path); err == nil {
		return c.fail(errors.New("cra: cra/product.toml already exists"))
	}
	if err := os.MkdirAll(filepath.Join(c.wd, "cra", "reports"), 0o755); err != nil {
		return c.fail(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return c.fail(errors.New("cra: cra/product.toml already exists"))
		}
		return c.fail(err)
	}
	if _, err := f.WriteString(productTemplate); err != nil {
		_ = f.Close()
		return c.fail(err)
	}
	if err := f.Close(); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.init_done"))
	return 0
}

// stringList est une option répétable (et séparée par des virgules).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

// dbUpdate télécharge les archives OSV des écosystèmes demandés, ou de ceux
// des SBOM du dossier.
func (c craEnv) dbUpdate(args []string) int {
	fs := c.flags("db update")
	mirror := fs.String("mirror", "", i18n.T(c.lang, "flag.cra_mirror"))
	var ecos stringList
	fs.Var(&ecos, "ecosystem", i18n.T(c.lang, "flag.cra_ecosystem"))
	if _, code, ok := c.parse(fs, args, 0); !ok {
		return code
	}
	s, err := loadSetup(c.lang, c.getenv, c.wd, c.globalPath)
	if err != nil {
		fmt.Fprintln(c.stderr, i18n.T(c.lang, "err.prefix", err))
		return 1
	}
	red := redactText(s.redactor)
	base := *mirror
	if base == "" {
		base = s.cfg.CRA.OSVMirror
	}
	if base == "" {
		base = osv.DefaultBase
	}
	// strict-local : seul un miroir local est permis (le défaut public est refusé).
	if s.cfg.Profile == profile.StrictLocal {
		u, err := url.Parse(base)
		if err != nil || !profile.IsLocalHost(u.Hostname()) {
			return c.fail(errors.New("cra: profile strict-local requires a local osv mirror"))
		}
	}
	if err := osv.CheckBase(base); err != nil {
		return c.fail(err)
	}
	list := []string(ecos)
	if len(list) == 0 {
		if list, err = c.sbomEcosystems(); err != nil {
			return c.fail(err)
		}
	}
	dir := craOSVDir(c.getenv)
	before := corruptIndexes(dir)
	strict := s.cfg.Profile == profile.StrictLocal
	client := craHTTPClient(strict, strict)
	uerr := osv.Update(c.ctx, client, base, list, dir, time.Now)
	// Un index illisible a été mis de côté : avis, même si la suite échoue.
	for _, f := range sortedKeys(corruptIndexes(dir)) {
		if !before[f] {
			fmt.Fprintln(c.stderr, i18n.T(c.lang, "cra.index_corrupt", f))
		}
	}
	if uerr != nil {
		fmt.Fprintln(c.stderr, red(uerr.Error()))
		return 1
	}
	fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.db_updated", strings.Join(list, ", ")))
	return 0
}

// corruptIndexes liste les index.json mis de côté (index.json.corrupt-*).
func corruptIndexes(dir string) map[string]bool {
	out := map[string]bool{}
	m, _ := filepath.Glob(filepath.Join(dir, "index.json.corrupt-*"))
	for _, f := range m {
		out[filepath.Base(f)] = true
	}
	return out
}

// sortedKeys renvoie les clés de m triées.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sbomEcosystems renvoie les écosystèmes OSV des composants des SBOM du dossier.
func (c craEnv) sbomEcosystems() ([]string, error) {
	d, err := dossier.Load(c.wd)
	if errors.Is(err, dossier.ErrNoDossier) {
		return nil, errors.New("cra: no dossier to read ecosystems from (use --ecosystem)")
	}
	if err != nil {
		return nil, err
	}
	boms, err := readBOMs(d)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, b := range boms {
		for _, comp := range b.Components {
			p, err := sbom.ParsePURL(comp.PURL)
			if err != nil {
				continue
			}
			if eco, ok := p.Ecosystem(); ok {
				set[eco] = true
			}
		}
	}
	if len(set) == 0 {
		return nil, errors.New("cra: no ecosystem found in the SBOMs (use --ecosystem)")
	}
	return sortedKeys(set), nil
}

// readBOMs lit les SBOM du dossier.
func readBOMs(d dossier.Dossier) ([]sbom.BOM, error) {
	paths, err := d.SBOMPaths()
	if err != nil {
		return nil, err
	}
	boms := make([]sbom.BOM, 0, len(paths))
	for _, p := range paths {
		b, err := sbom.Read(p)
		if err != nil {
			return nil, err
		}
		boms = append(boms, b)
	}
	return boms, nil
}

// openDB ouvre la base OSV locale ; absente : message « run tenon cra db update ».
func openDB(getenv func(string) string) (*osv.DB, error) {
	db, err := osv.Open(craOSVDir(getenv))
	if errors.Is(err, os.ErrNotExist) {
		return nil, &scan.NoDBError{}
	}
	return db, err
}

// scan rapproche les SBOM de la base et applique le VEX.
func (c craEnv) scan(args []string) int {
	fs := c.flags("scan")
	failOn := fs.String("fail-on", "", i18n.T(c.lang, "flag.cra_fail_on"))
	asJSON := fs.Bool("json", false, i18n.T(c.lang, "flag.cra_json"))
	if _, code, ok := c.parse(fs, args, 0); !ok {
		return code
	}
	cats, err := parseFailOn(*failOn)
	if err != nil {
		return c.fail(err)
	}
	s, err := loadSetup(c.lang, c.getenv, c.wd, c.globalPath)
	if err != nil {
		fmt.Fprintln(c.stderr, i18n.T(c.lang, "err.prefix", err))
		return 1
	}
	d, err := dossier.Load(c.wd)
	if err != nil {
		return c.fail(err)
	}
	boms, err := readBOMs(d)
	if err != nil {
		return c.fail(err)
	}
	db, err := openDB(c.getenv)
	if err != nil {
		return c.fail(err)
	}
	res, err := scan.Run(d, boms, db)
	if err != nil {
		return c.fail(err)
	}
	dbDate := res.DBDate
	if dbDate.IsZero() {
		dbDate, _ = db.OldestDate()
	}
	if age := time.Since(dbDate); !dbDate.IsZero() && age > craOldDB {
		fmt.Fprintf(c.stderr, "cra: vulnerability database is %d days old\n", int(age.Hours()/24))
	}
	// Composants non vérifiés ou conflits VEX : un résultat « sans ouverte »
	// n'est pas un résultat propre (le code de sortie ne change pas).
	if res.Unchecked > 0 || res.Conflicts > 0 {
		fmt.Fprintln(c.stderr, i18n.T(c.lang, "cra.scan_attention", res.Unchecked, res.Conflicts))
	}
	if *asJSON {
		if res.Findings == nil {
			res.Findings = []scan.Finding{}
		}
		if res.UncheckedComponents == nil {
			res.UncheckedComponents = []sbom.Component{}
		}
		enc := json.NewEncoder(c.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return c.fail(err)
		}
	} else {
		c.printScan(res, dbDate)
	}
	if err := craJournal(s, c.getenv, c.wd, event.CRAScan, scanPayload(res, dbDate)); err != nil {
		return c.fail(err)
	}
	if failOnHit(cats, res) {
		return craExitFinding
	}
	return 0
}

// printScan affiche le résultat : résumé, ouvertes, à vérifier, conflits VEX, composants non vérifiés.
func (c craEnv) printScan(res scan.Result, dbDate time.Time) {
	date := "-"
	if !dbDate.IsZero() {
		date = dbDate.UTC().Format("2006-01-02")
	}
	fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.scan_summary", res.SBOMs, res.Components, res.Open, res.ToVerify, res.NotAffected, res.Fixed, res.Conflicts, res.Unchecked, date))
	line := func(f scan.Finding) string {
		return stripControl(f.ID + " " + f.Component.PURL + " " + f.Summary)
	}
	for _, f := range res.Findings {
		if f.Status == scan.StatusOpen {
			fmt.Fprintln(c.stdout, line(f))
		}
	}
	for _, f := range res.Findings {
		if f.Status == scan.StatusToVerify {
			fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.to_verify", line(f)))
		}
	}
	for _, f := range res.Findings {
		if f.Conflict {
			fmt.Fprintln(c.stdout, stripControl(f.ConflictMessage()))
		}
	}
	for _, comp := range res.UncheckedComponents {
		fmt.Fprintln(c.stdout, stripControl(fmt.Sprintf("unchecked: %s@%s (%s)", comp.Name, comp.Version, uncheckedReason(comp))))
	}
}

// uncheckedReason explique pourquoi un composant n'a pas été vérifié.
func uncheckedReason(comp sbom.Component) string {
	if comp.PURL == "" {
		return "no purl"
	}
	p, err := sbom.ParsePURL(comp.PURL)
	if err != nil {
		return "invalid purl"
	}
	return "unsupported type " + p.Type
}

// craJournal écrit une session courte dans le journal d'audit :
// SessionStarted{kind:"cra"} puis l'événement CRA, au niveau et avec le
// masquage configurés.
func craJournal(s setup, getenv func(string) string, wd string, typ event.Type, payload any) error {
	red := s.redactor
	redactJSON := func(v string) string { out, _ := red.RedactJSON(v); return out }
	w, err := audit.Open(auditDir(config.DataDir(getenv)), audit.Level(s.cfg.Audit.Level), redactJSON)
	if err != nil {
		return fmt.Errorf("cra: audit log: %w", err)
	}
	defer func() { _ = w.Close() }()
	sid, err := newSessionID()
	if err != nil {
		return fmt.Errorf("cra: audit log: %w", err)
	}
	now := time.Now()
	start := event.SessionStartedPayload{Kind: "cra", ProjectRoot: wd}
	if s.cfg.Profile != profile.None {
		start.Profile = string(s.cfg.Profile)
	}
	for i, ev := range []struct {
		typ     event.Type
		payload any
	}{{event.SessionStarted, start}, {typ, payload}} {
		e, err := event.New(sid, uint64(i+1), now, ev.typ, ev.payload)
		if err != nil {
			return fmt.Errorf("cra: audit log: %w", err)
		}
		if err := w.Write(e); err != nil {
			return fmt.Errorf("cra: audit log: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("cra: audit log: %w", err)
	}
	return nil
}

// parseZoned lit une date-heure RFC 3339 dont le fuseau est obligatoire.
func parseZoned(flagName, v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("cra: --%s must be RFC 3339 with a time zone", flagName)
	}
	return t.UTC(), nil
}

// reportOpen crée un signalement et affiche ses échéances.
func (c craEnv) reportOpen(args []string) int {
	fs := c.flags("report open")
	kind := fs.String("kind", "", i18n.T(c.lang, "flag.cra_kind"))
	summary := fs.String("summary", "", i18n.T(c.lang, "flag.cra_summary"))
	awareAt := fs.String("aware-at", "", i18n.T(c.lang, "flag.cra_aware_at"))
	if _, code, ok := c.parse(fs, args, 0); !ok {
		return code
	}
	if *kind != "vulnerability" && *kind != "incident" {
		return c.fail(errors.New("cra: --kind must be vulnerability or incident"))
	}
	if strings.TrimSpace(*summary) == "" {
		return c.fail(errors.New("cra: --summary required"))
	}
	now := time.Now().UTC()
	aware := now.Truncate(time.Second)
	if *awareAt != "" {
		t, err := parseZoned("aware-at", *awareAt)
		if err != nil {
			return c.fail(err)
		}
		aware = t
	}
	if st, err := os.Stat(filepath.Join(c.wd, "cra")); err != nil || !st.IsDir() {
		return c.fail(dossier.ErrNoDossier)
	}
	s, err := loadSetup(c.lang, c.getenv, c.wd, c.globalPath)
	if err != nil {
		fmt.Fprintln(c.stderr, i18n.T(c.lang, "err.prefix", err))
		return 1
	}
	r := dossier.Report{ID: dossier.NewReportID(c.wd, now), Kind: *kind, Summary: *summary, AwareAt: &aware}
	if err := dossier.SaveReport(c.wd, r); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.report_created", r.ID))
	c.printDeadlines(r, now)
	return c.journalReport(s, r, "open", aware)
}

// craSteps : étapes acceptées par report mark.
var craSteps = map[string]bool{"early-warning": true, "notification": true, "final": true, "fix-available": true}

// reportMark enregistre la date d'une étape d'un signalement existant.
func (c craEnv) reportMark(args []string) int {
	fs := c.flags("report mark")
	at := fs.String("at", "", i18n.T(c.lang, "flag.cra_at"))
	pos, code, ok := c.parse(fs, args, 2)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		fmt.Fprintln(c.stderr, i18n.T(c.lang, "cra.usage"))
		return 1
	}
	id, step := pos[0], pos[1]
	if !craSteps[step] {
		return c.fail(errors.New("cra: step must be early-warning, notification, final or fix-available"))
	}
	now := time.Now().UTC()
	when := now.Truncate(time.Second)
	if *at != "" {
		t, err := parseZoned("at", *at)
		if err != nil {
			return c.fail(err)
		}
		when = t
	}
	r, err := dossier.LoadReport(c.wd, id)
	if err != nil {
		return c.fail(err)
	}
	switch step {
	case "early-warning":
		r.EarlyWarningAt = &when
	case "notification":
		r.NotificationAt = &when
	case "final":
		r.FinalAt = &when
	case "fix-available":
		if r.Kind != "vulnerability" {
			return c.fail(errors.New("cra: fix-available applies only to vulnerability reports"))
		}
		r.FixAvailableAt = &when
	}
	s, err := loadSetup(c.lang, c.getenv, c.wd, c.globalPath)
	if err != nil {
		fmt.Fprintln(c.stderr, i18n.T(c.lang, "err.prefix", err))
		return 1
	}
	if err := dossier.UpdateReport(c.wd, r); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.report_marked", r.ID, step, when.Format(time.RFC3339)))
	c.printDeadlines(r, now)
	return c.journalReport(s, r, step, when)
}

// journalReport écrit l'événement CRAReport ; un échec donne le code 1, le
// signalement enregistré est conservé.
func (c craEnv) journalReport(s setup, r dossier.Report, step string, at time.Time) int {
	p := event.CRAReportPayload{ID: r.ID, Kind: r.Kind, Step: step, At: at.UTC().Format(time.RFC3339)}
	if err := craJournal(s, c.getenv, c.wd, event.CRAReport, p); err != nil {
		return c.fail(err)
	}
	return 0
}

// printDeadlines affiche les échéances d'un signalement ; true si l'une est dépassée.
func (c craEnv) printDeadlines(r dossier.Report, now time.Time) bool {
	overdue := false
	for _, d := range dossier.Deadlines(r, now) {
		var l string
		switch d.Status {
		case dossier.StatusDone:
			l = i18n.T(c.lang, "cra.deadline_done", d.Step, d.Done.UTC().Format(time.RFC3339))
		case dossier.StatusDoneLate:
			l = i18n.T(c.lang, "cra.deadline_done_late", d.Step, d.Done.UTC().Format(time.RFC3339), d.Due.UTC().Format(time.RFC3339))
		case dossier.StatusOverdue:
			overdue = true
			l = i18n.T(c.lang, "cra.deadline_overdue", d.Step, d.Due.UTC().Format(time.RFC3339))
		case dossier.StatusDue:
			l = i18n.T(c.lang, "cra.deadline_due", d.Step, d.Due.UTC().Format(time.RFC3339))
		default:
			l = i18n.T(c.lang, "cra.deadline_unknown", d.Step)
		}
		fmt.Fprintln(c.stdout, "  "+l)
	}
	return overdue
}

// status affiche la complétude du dossier, le dernier scan et les échéances.
// Codes : 0 ; 7 si une échéance est dépassée ; 1 si le dossier ne se charge
// pas ou qu'un SBOM listé est absent ou invalide (l'emporte sur 7).
func (c craEnv) status(args []string) int {
	fs := c.flags("status")
	if _, code, ok := c.parse(fs, args, 0); !ok {
		return code
	}
	d, err := dossier.Load(c.wd)
	if err != nil {
		return c.fail(err)
	}
	now := time.Now()
	p := d.Product
	fmt.Fprintln(c.stdout, stripControl(i18n.T(c.lang, "cra.status_product", p.Name, p.Version, p.Category)))
	// Un SBOM listé absent ou invalide rend le dossier invalide : code 1 à la
	// fin. Sans `sboms`, un SBOM pas encore produit rend seulement le dossier incomplet.
	sbomBad := false
	if paths, err := d.SBOMPaths(); err != nil {
		sbomBad = !errors.Is(err, dossier.ErrNoDefaultSBOM)
		fmt.Fprintln(c.stdout, stripControl(i18n.T(c.lang, "cra.status_sbom_missing", err)))
	} else {
		valid := 0
		for _, path := range paths {
			if _, err := sbom.Read(path); err != nil {
				fmt.Fprintln(c.stdout, stripControl(i18n.T(c.lang, "cra.status_sbom_invalid", err)))
				continue
			}
			valid++
		}
		if valid == len(paths) {
			fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.status_sbom_ok", valid))
		} else {
			sbomBad = true
		}
	}
	if _, err := os.Stat(filepath.Join(c.wd, "cra", "vex.cdx.json")); err != nil {
		fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.status_vex_missing"))
	} else {
		fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.status_vex_ok", len(d.VEX)))
	}
	for _, item := range d.Incomplete(now) {
		fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.status_incomplete", item))
	}
	fmt.Fprintln(c.stdout, c.lastScanLine())
	if len(d.Reports) == 0 {
		fmt.Fprintln(c.stdout, i18n.T(c.lang, "cra.status_reports_none"))
	}
	overdue := false
	for _, r := range d.Reports {
		fmt.Fprintln(c.stdout, stripControl(i18n.T(c.lang, "cra.status_report", r.ID, r.Kind, r.Summary)))
		if c.printDeadlines(r, now) {
			overdue = true
		}
	}
	if sbomBad {
		return 1
	}
	if overdue {
		return craExitFinding
	}
	return 0
}

// lastScanLine décrit le dernier CRAScan du journal pour ce projet. Au niveau
// d'audit metadata, les charges sont des empreintes : le projet ne peut être
// vérifié (« projet inconnu ») et seule la date est connue.
func (c craEnv) lastScanLine() string {
	type sess struct{ readable, match bool }
	sessions := map[string]sess{}
	var last *audit.Line
	lastReadable := false
	_ = audit.ReadLines(auditDir(config.DataDir(c.getenv)), func(_ string, _ int, l audit.Line, err error) error {
		if err != nil {
			return nil
		}
		switch l.Type {
		case event.SessionStarted:
			var p event.SessionStartedPayload
			var probe map[string]json.RawMessage
			readable := json.Unmarshal(l.Payload, &probe) == nil && probe["sha256"] == nil && json.Unmarshal(l.Payload, &p) == nil
			sessions[l.Session] = sess{readable: readable, match: !readable || (p.Kind == "cra" && p.ProjectRoot == c.wd)}
		case event.CRAScan:
			if s, ok := sessions[l.Session]; ok && s.match {
				l := l
				last = &l
				lastReadable = s.readable
			}
		}
		return nil
	})
	if last == nil {
		return i18n.T(c.lang, "cra.status_scan_never")
	}
	date := last.TS
	if t, err := time.Parse(time.RFC3339Nano, last.TS); err == nil {
		date = t.UTC().Format(time.RFC3339)
	}
	if !lastReadable {
		return i18n.T(c.lang, "cra.status_scan_unknown_project", date)
	}
	var probe map[string]json.RawMessage
	var p event.CRAScanPayload
	if json.Unmarshal(last.Payload, &probe) != nil || probe["open"] == nil || json.Unmarshal(last.Payload, &p) != nil {
		return i18n.T(c.lang, "cra.status_scan_date", date)
	}
	return i18n.T(c.lang, "cra.status_scan", date, p.Open, p.ToVerify)
}

// export écrit le dossier technique (HTML et JSON). Le scan est joint (et
// inscrit au journal, CRAScan) quand la base OSV est disponible ; sinon
// l'export se fait sans scan, avec un avis. Sans `sboms`, un SBOM pas encore
// produit donne aussi un export sans scan ; un SBOM listé absent ou invalide
// rend le dossier invalide : code 1. La configuration n'est lue que pour
// journaliser un scan.
func (c craEnv) export(args []string) int {
	fs := c.flags("export")
	out := fs.String("out", "", i18n.T(c.lang, "flag.export_out"))
	langOpt := fs.String("lang", "", i18n.T(c.lang, "flag.lang"))
	if _, code, ok := c.parse(fs, args, 0); !ok {
		return code
	}
	lang := c.lang
	if *langOpt != "" {
		l, ok := uiLang(*langOpt, c.getenv, c.wd, c.globalPath)
		if !ok {
			fmt.Fprintln(c.stderr, i18n.T(c.lang, "err.prefix", i18n.T(c.lang, "err.bad_lang", *langOpt)))
			return 1
		}
		lang = l
	}
	d, err := dossier.Load(c.wd)
	if err != nil {
		return c.fail(err)
	}
	var res *scan.Result
	notice := "cra: export without vulnerability scan (run tenon cra db update)"
	boms, err := readBOMs(d)
	switch {
	case errors.Is(err, dossier.ErrNoDefaultSBOM):
		notice = "cra: export without vulnerability scan (no SBOM found)"
	case err != nil:
		return c.fail(err)
	default:
		db, err := openDB(c.getenv)
		if err == nil {
			var r scan.Result
			r, err = scan.Run(d, boms, db)
			res = &r
		}
		var noDB *scan.NoDBError
		if errors.As(err, &noDB) {
			res, err = nil, nil
		}
		if err != nil {
			return c.fail(err)
		}
		if res != nil {
			s, err := loadSetup(c.lang, c.getenv, c.wd, c.globalPath)
			if err != nil {
				fmt.Fprintln(c.stderr, i18n.T(c.lang, "err.prefix", err))
				return 1
			}
			dbDate := res.DBDate
			if dbDate.IsZero() {
				dbDate, _ = db.OldestDate()
			}
			if err := craJournal(s, c.getenv, c.wd, event.CRAScan, scanPayload(*res, dbDate)); err != nil {
				return c.fail(err)
			}
		}
	}
	if res == nil {
		fmt.Fprintln(c.stderr, notice)
	}
	now := exportNow().UTC()
	dos, err := report.Build(d, res, now)
	if err != nil {
		return c.fail(err)
	}
	outDir := c.wd
	if *out != "" {
		outDir = resolveFrom(c.wd, *out)
	}
	if err := report.Write(outDir, dos, lang); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.stdout, i18n.T(lang, "cra.export_ok", filepath.Join(outDir, "cra-dossier.html"), filepath.Join(outDir, "cra-dossier.json")))
	return 0
}

// craConfigLine renvoie la ligne « config check » du CRA.
func craConfigLine(lang i18n.Lang, getenv func(string) string, wd string) string {
	state := "ok"
	if _, err := dossier.Load(wd); errors.Is(err, dossier.ErrNoDossier) {
		state = "absent"
	} else if err != nil {
		state = "invalid"
	}
	date := "absent"
	if db, err := osv.Open(craOSVDir(getenv)); err == nil {
		if t, ok := db.OldestDate(); ok {
			date = t.UTC().Format("2006-01-02")
		}
	}
	return i18n.T(lang, "config.cra", state, date)
}
