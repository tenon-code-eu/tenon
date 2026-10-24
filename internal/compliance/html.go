package compliance

import (
	"bytes"
	_ "embed"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

//go:embed report.html.tmpl
var reportTmpl string

// baseTmpl est analysé une seule fois ; les fonctions réelles, liées à la
// langue, remplacent ces bouchons sur un clone à chaque rendu. Aucune
// fonction ne renvoie de template.HTML : tout passe par l'échappement.
var baseTmpl = template.Must(template.New("report").Funcs(funcs(i18n.EN)).Parse(reportTmpl))

// htmlData est la donnée passée au gabarit.
type htmlData struct {
	R    Report
	ID   string
	Lang string
}

// funcs renvoie les fonctions du gabarit pour une langue.
func funcs(lang i18n.Lang) template.FuncMap {
	return template.FuncMap{
		// t traduit une clé i18n.
		"t": func(key string, args ...any) string { return i18n.T(lang, key, args...) },
		// tk traduit prefix+value, ou rend value telle quelle si la clé manque.
		"tk": func(prefix, value string) string {
			if s := i18n.T(lang, prefix+value); !strings.HasPrefix(s, "!") {
				return s
			}
			return value
		},
		// routing résume le routage d'une session en une ligne.
		"routing": func(s SessionRouting) string {
			provs := strings.Join(s.Providers, ", ")
			out := i18n.T(lang, "report.routing_plain", s.Session, provs, s.Fallbacks)
			if s.Sensitive {
				out = i18n.T(lang, "report.routing_sensitive", s.Session, s.Reason, provs, s.Fallbacks)
			}
			if s.Violation {
				out += ", " + i18n.T(lang, "report.routing_violation")
			}
			return out
		},
		// tokens résume la section tokens : total, par session, énergie.
		"tokens": func(t Tokens) string {
			line := func(c TokenCounts) string {
				return i18n.T(lang, "report.tokens_counts", c.Input, c.Output, c.Cached, int(math.Round(c.EstimatedShare*100)), c.Saved)
			}
			parts := []string{line(t.Total)}
			for _, s := range t.Sessions {
				parts = append(parts, s.Session+" : "+line(s.TokenCounts))
			}
			for _, p := range t.Providers {
				parts = append(parts, p.ID+" "+p.Model+" : "+line(p.TokenCounts))
			}
			out := strings.Join(parts, "; ")
			if t.Energy != nil {
				// Résultats en 3 chiffres significatifs (jamais « 0,00 » pour
				// une valeur non nulle), facteurs tels que déclarés.
				dec := func(s string) string {
					if lang == i18n.FR {
						s = strings.Replace(s, ".", ",", 1)
					}
					return s
				}
				sig := func(f float64) string { return dec(sig3(f)) }
				factor := func(f float64) string { return dec(strconv.FormatFloat(f, 'f', -1, 64)) }
				e := t.Energy
				out += " — " + i18n.T(lang, "report.tokens_energy", sig(e.Wh), sig(e.GCO2e), factor(e.WhPerMTok), factor(e.GCO2PerKWh))
			}
			return out
		},
		// cra résume la section CRA en une ligne.
		"cra": func(c CRA) string {
			parts := []string{}
			if c.LastScan != "" {
				parts = append(parts, i18n.T(lang, "report.cra_scan", c.LastScan, c.Open, c.ToVerify, c.Unchecked, c.Conflicts))
			}
			for _, s := range c.Reports {
				line := i18n.T(lang, "report.cra_report", s.ID, s.Kind, s.Step)
				if s.DeadlinesUnknown {
					line += ", " + i18n.T(lang, "report.cra_unknown")
				}
				if s.Overdue {
					line += ", " + i18n.T(lang, "report.cra_overdue") + " (" + strings.Join(s.OverdueSteps, ", ") + ")"
				}
				if len(s.LateSteps) > 0 {
					line += ", " + i18n.T(lang, "report.cra_late") + " (" + strings.Join(s.LateSteps, ", ") + ")"
				}
				parts = append(parts, line)
			}
			return strings.Join(parts, "; ")
		},
		"date": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" },
		"day":  func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	}
}

// RenderHTML rend le rapport en une page HTML autonome et imprimable.
func RenderHTML(r Report, reportID string, lang i18n.Lang) ([]byte, error) {
	tmpl, err := baseTmpl.Clone()
	if err != nil {
		return nil, err
	}
	tmpl.Funcs(funcs(lang))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, htmlData{R: r, ID: reportID, Lang: string(lang)}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sig3 écrit f avec 3 chiffres significatifs, sans exposant.
func sig3(f float64) string {
	if f == 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	d := max(0, 2-int(math.Floor(math.Log10(math.Abs(f)))))
	return strconv.FormatFloat(f, 'f', d, 64)
}
