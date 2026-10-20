package compliance

import (
	"bytes"
	_ "embed"
	"html/template"
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
