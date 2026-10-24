package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tenon-code-eu/tenon/internal/compliance"
	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// exportNow donne la date du jour de « tenon audit export » ; remplacée dans les tests.
var exportNow = time.Now

// dateLayout est le format des dates de --since et --until.
const dateLayout = "2006-01-02"

// errFileExists signale un fichier de sortie déjà présent.
type errFileExists struct{ path string }

func (e errFileExists) Error() string { return e.path }

// auditExport écrit le rapport de conformité (HTML et JSON) tiré du journal
// d'audit. Codes : 0 succès, 1 erreur d'usage ou d'écriture, 2 rapport écrit
// mais chaîne d'audit cassée ou jeton d'horodatage invalide ou orphelin. Le journal n'est ni modifié ni verrouillé.
func auditExport(args []string, langFlag string, stdout, stderr io.Writer, getenv func(string) string, wd, globalPath string) int {
	hl, _ := uiLang(langFlag, getenv, wd, globalPath)
	flags := flag.NewFlagSet("tenon audit export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	since := flags.String("since", "", i18n.T(hl, "flag.export_since"))
	until := flags.String("until", "", i18n.T(hl, "flag.export_until"))
	project := flags.String("project", "", i18n.T(hl, "flag.export_project"))
	allProjects := flags.Bool("all-projects", false, i18n.T(hl, "flag.export_all_projects"))
	sessionID := flags.String("session", "", i18n.T(hl, "flag.export_session"))
	details := flags.Bool("details", false, i18n.T(hl, "flag.export_details"))
	tsaCA := flags.String("tsa-ca", "", i18n.T(hl, "flag.tsa_ca"))
	out := flags.String("out", "", i18n.T(hl, "flag.export_out"))
	langOpt := flags.String("lang", langFlag, i18n.T(hl, "flag.lang"))
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	lang, ok := uiLang(*langOpt, getenv, wd, globalPath)
	if !ok {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.bad_lang", *langOpt)))
		return 1
	}
	usageErr := func(key string, a ...any) int {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, key, a...)))
		return 1
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", flags.Arg(0)))
		return 1
	}

	// Période : dates UTC, bornes incluses ; défaut : les 30 derniers jours.
	now := exportNow().UTC()
	untilDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if *until != "" {
		d, err := time.Parse(dateLayout, *until)
		if err != nil {
			return usageErr("err.bad_date", *until)
		}
		untilDay = d
	}
	sinceDay := untilDay.AddDate(0, 0, -29)
	if *since != "" {
		d, err := time.Parse(dateLayout, *since)
		if err != nil {
			return usageErr("err.bad_date", *since)
		}
		sinceDay = d
	}
	if sinceDay.After(untilDay) {
		return usageErr("err.since_after_until")
	}

	// Périmètre : --project, --all-projects et --session s'excluent.
	scopes := 0
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "project" || f.Name == "all-projects" || f.Name == "session" {
			scopes++
		}
	})
	if scopes > 1 {
		return usageErr("err.scope_exclusive")
	}
	filter := compliance.Filter{Since: sinceDay, Until: untilDay, AllProjects: *allProjects, Session: *sessionID}
	if !filter.AllProjects && filter.Session == "" {
		filter.Project = wd
		if *project != "" {
			filter.Project = resolveFrom(wd, *project)
		}
	}
	outDir := wd
	if *out != "" {
		outDir = resolveFrom(wd, *out)
	}

	st, err := loadSetup(lang, getenv, wd, globalPath)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	dir := auditDir(config.DataDir(getenv))
	roots, err := verifyRoots(lang, *tsaCA, dir, getenv, wd, globalPath, stderr)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	ds, err := compliance.Load(dir, filter, roots)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	rep := compliance.Build(ds, compliance.Options{
		Redact:     redactText(st.redactor),
		RedactJSON: func(s string) string { out, _ := st.redactor.RedactJSON(s); return out },
		Details:    *details,
		Generator:  "tenon " + version,
		Now:        now,
		WhPerMTok:  st.cfg.Usage.WhPerMTok,
		GCO2PerKWh: st.cfg.Usage.GCO2PerKWh,
	})
	id, err := compliance.ReportID(rep)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	htmlBytes, err := compliance.RenderHTML(rep, id, lang)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	sum := sha256.Sum256(htmlBytes)
	jsonBytes, err := compliance.RenderJSON(rep, id, "sha256:"+hex.EncodeToString(sum[:]))
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}

	// Le dossier de sortie peut contenir des données sensibles : 0700.
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	base := filepath.Join(outDir, "tenon-audit-"+sinceDay.Format(dateLayout)+"_"+untilDay.Format(dateLayout))
	htmlPath, jsonPath := base+".html", base+".json"
	if err := writeExport(htmlPath, htmlBytes, jsonPath, jsonBytes); err != nil {
		var fe errFileExists
		if errors.As(err, &fe) {
			return usageErr("err.file_exists", fe.path)
		}
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	fmt.Fprintln(stdout, i18n.T(lang, "audit.export.ok", htmlPath, jsonPath))
	if !rep.Integrity.Intact {
		// Integrity.Error est déjà masquée et nettoyée des caractères de contrôle.
		fmt.Fprintln(stderr, i18n.T(lang, "audit.export.broken", rep.Integrity.Error))
		return 2
	}
	if n := len(rep.Integrity.Timestamps.Errors); n > 0 {
		fmt.Fprintln(stderr, i18n.T(lang, "audit.export.timestamps_invalid", n))
		return 2
	}
	if rep.RoutingViolation() {
		fmt.Fprintln(stderr, i18n.T(lang, "audit.export.routing_violation"))
		return 2
	}
	return 0
}

// resolveFrom rend p absolu, relativement à wd.
func resolveFrom(wd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(wd, p)
}

// writeExport écrit les deux fichiers en 0600, sans jamais en remplacer un :
// si l'un existe, rien n'est écrit ; si le JSON échoue, le HTML est retiré.
func writeExport(htmlPath string, htmlBytes []byte, jsonPath string, jsonBytes []byte) error {
	for _, p := range []string{htmlPath, jsonPath} {
		if _, err := os.Lstat(p); err == nil {
			return errFileExists{p}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := writeNew(htmlPath, htmlBytes); err != nil {
		return err
	}
	if err := writeNew(jsonPath, jsonBytes); err != nil {
		_ = os.Remove(htmlPath)
		return err
	}
	return nil
}

// writeNew crée path (O_EXCL, 0600 quel que soit le umask) et y écrit b ; en
// cas d'échec après création, le fichier est retiré.
func writeNew(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return errFileExists{path}
	}
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(0o600)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
