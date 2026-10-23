// Package report produit le dossier technique CRA exportable (HTML et JSON).
package report

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/cra/dossier"
	"github.com/tenon-code-eu/tenon/internal/cra/sbom"
	"github.com/tenon-code-eu/tenon/internal/cra/scan"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// BOMSummary résume un SBOM du dossier.
type BOMSummary struct {
	File        string    `json:"file"`
	SpecVersion string    `json:"spec_version"`
	Generator   string    `json:"generator"`
	Timestamp   time.Time `json:"timestamp"`
	Components  int       `json:"components"`
}

// DeadlineView est l'échéance d'une étape de signalement.
type DeadlineView struct {
	Step   string     `json:"step"`
	Due    *time.Time `json:"due"`
	Done   *time.Time `json:"done"`
	Status string     `json:"status"`
}

// ReportView est un signalement avec ses échéances à la date d'export.
type ReportView struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Summary        string         `json:"summary"`
	AwareAt        *time.Time     `json:"aware_at"`
	FixAvailableAt *time.Time     `json:"fix_available_at"`
	EarlyWarningAt *time.Time     `json:"early_warning_at"`
	NotificationAt *time.Time     `json:"notification_at"`
	FinalAt        *time.Time     `json:"final_at"`
	Deadlines      []DeadlineView `json:"deadlines"`
}

// Doc est une partie narrative (cra/docs/*.md), rendue en texte échappé.
type Doc struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Dossier est le modèle pur du dossier technique ; ses balises JSON forment le schéma.
type Dossier struct {
	Product     dossier.Product `json:"product"`
	BOMs        []BOMSummary    `json:"boms"`
	Scan        *scan.Result    `json:"scan"`
	Reports     []ReportView    `json:"reports"`
	Docs        []Doc           `json:"docs"`
	GeneratedAt time.Time       `json:"generated_at"`
}

// maxDocSize borne la taille d'une partie narrative.
const maxDocSize = 1 << 20

// Build assemble le dossier : résumés des SBOM, échéances calculées à now,
// parties narratives de cra/docs/*.md (triées par nom). res peut être nil
// (export sans scan).
func Build(d dossier.Dossier, res *scan.Result, now time.Time) (Dossier, error) {
	out := Dossier{
		Product: d.Product, Scan: res, GeneratedAt: now.UTC(),
		BOMs: []BOMSummary{}, Reports: []ReportView{}, Docs: []Doc{},
	}
	if res != nil {
		if res.Findings == nil {
			res.Findings = []scan.Finding{}
		}
		if res.UncheckedComponents == nil {
			res.UncheckedComponents = []sbom.Component{}
		}
	}
	// Sans SBOM, le dossier s'exporte quand même (liste vide).
	if paths, err := d.SBOMPaths(); err == nil {
		for _, p := range paths {
			b, err := sbom.Read(p)
			if err != nil {
				return Dossier{}, err
			}
			rel, rerr := filepath.Rel(d.Root, p)
			if rerr != nil {
				rel = p
			}
			out.BOMs = append(out.BOMs, BOMSummary{
				File: filepath.ToSlash(rel), SpecVersion: b.SpecVersion, Generator: b.Generator,
				Timestamp: b.Timestamp.UTC(), Components: len(b.Components),
			})
		}
	}
	for _, r := range d.Reports {
		v := ReportView{
			ID: r.ID, Kind: r.Kind, Summary: r.Summary, AwareAt: r.AwareAt, FixAvailableAt: r.FixAvailableAt,
			EarlyWarningAt: r.EarlyWarningAt, NotificationAt: r.NotificationAt, FinalAt: r.FinalAt,
			Deadlines: []DeadlineView{},
		}
		for _, dl := range dossier.Deadlines(r, now) {
			v.Deadlines = append(v.Deadlines, DeadlineView{Step: dl.Step, Due: dl.Due, Done: dl.Done, Status: dl.Status})
		}
		out.Reports = append(out.Reports, v)
	}
	docs, err := readDocs(filepath.Join(d.Root, "cra", "docs"))
	if err != nil {
		return Dossier{}, err
	}
	out.Docs = docs
	return out, nil
}

// readDocs lit les fichiers .md réguliers du dossier (liens symboliques
// ignorés), triés par nom ; un dossier absent donne une liste vide.
func readDocs(dir string) ([]Doc, error) {
	docs := []Doc{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return docs, nil
		}
		return nil, fmt.Errorf("cra: docs: %w", err)
	}
	for _, e := range ents {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("cra: docs: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(f, maxDocSize+1))
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("cra: docs: %s: %w", e.Name(), err)
		}
		if len(data) > maxDocSize {
			return nil, fmt.Errorf("cra: docs: %s: larger than 1 MiB", e.Name())
		}
		docs = append(docs, Doc{Name: e.Name(), Content: string(data)})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}

//go:embed report.html.tmpl
var tmplText string

var baseTmpl = template.Must(template.New("dossier").Funcs(funcs(i18n.EN)).Parse(tmplText))

// funcs renvoie les fonctions du gabarit pour une langue ; aucune ne renvoie
// de template.HTML : tout passe par l'échappement.
func funcs(lang i18n.Lang) template.FuncMap {
	return template.FuncMap{
		"t": func(key string, args ...any) string { return i18n.T(lang, key, args...) },
		// tk traduit prefix+value, ou rend value telle quelle si la clé manque.
		"tk": func(prefix, value string) string {
			if s := i18n.T(lang, prefix+value); !strings.HasPrefix(s, "!") {
				return s
			}
			return value
		},
		"date": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" },
		"day":  func(t time.Time) string { return t.UTC().Format("2006-01-02") },
		"dayz": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.UTC().Format("2006-01-02")
		},
		"opt": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return t.UTC().Format("2006-01-02 15:04") + " UTC"
		},
	}
}

type htmlData struct {
	D    Dossier
	Lang string
}

// Write écrit cra-dossier.json et cra-dossier.html dans dir (créé en 0700,
// fichiers en 0600).
func Write(dir string, d Dossier, lang i18n.Lang) error {
	j, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	j = append(j, '\n')
	tmpl, err := baseTmpl.Clone()
	if err != nil {
		return err
	}
	tmpl.Funcs(funcs(lang))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, htmlData{D: d, Lang: string(lang)}); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{"cra-dossier.json", j}, {"cra-dossier.html", buf.Bytes()}} {
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, f.data, 0o600); err != nil {
			return err
		}
		// WriteFile ne change pas le mode d'un fichier existant.
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
	}
	return nil
}
