// Package dossier lit et écrit le dossier CRA d'un dépôt : cra/product.toml,
// VEX CycloneDX et signalements, ainsi que le calcul des échéances de signalement.
package dossier

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultSBOMGlob est le motif des SBOM quand product.toml n'en liste pas.
const DefaultSBOMGlob = "cra/sbom*.cdx.json"

// ErrNoDossier signale l'absence du répertoire cra/.
var ErrNoDossier = errors.New("cra: no dossier (cra/ directory not found)")

// Product décrit le produit couvert par le dossier.
type Product struct {
	Name, Version, Manufacturer, SecurityContact, Category string
	SupportEnd                                             time.Time
	SBOMs                                                  []string
}

// Report est un signalement (vulnérabilité exploitée ou incident grave).
type Report struct {
	ID, Kind, Summary                                                string
	AwareAt, FixAvailableAt, EarlyWarningAt, NotificationAt, FinalAt *time.Time
}

// Dossier est le dossier CRA chargé.
type Dossier struct {
	Root    string
	Product Product
	VEX     []VEXEntry
	Reports []Report
}

var categories = map[string]bool{"default": true, "important-1": true, "important-2": true, "critical": true}

type rawProduct struct {
	Name            string   `toml:"name"`
	Version         string   `toml:"version"`
	Manufacturer    string   `toml:"manufacturer"`
	SecurityContact string   `toml:"security_contact"`
	SupportEnd      any      `toml:"support_end"`
	Category        string   `toml:"category"`
	SBOMs           []string `toml:"sboms"`
}

// Load charge le dossier de <root>/cra. Un cra/ absent renvoie ErrNoDossier.
func Load(root string) (Dossier, error) {
	dir := filepath.Join(root, "cra")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Dossier{}, ErrNoDossier
	}
	p, err := loadProduct(filepath.Join(dir, "product.toml"))
	if err != nil {
		return Dossier{}, err
	}
	vex, err := LoadVEX(filepath.Join(dir, "vex.cdx.json"))
	if err != nil {
		return Dossier{}, err
	}
	reports, err := loadReports(filepath.Join(dir, "reports"))
	if err != nil {
		return Dossier{}, err
	}
	return Dossier{Root: root, Product: p, VEX: vex, Reports: reports}, nil
}

func loadProduct(path string) (Product, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Product{}, errors.New("cra: product.toml: not found (run tenon cra init)")
		}
		return Product{}, fmt.Errorf("cra: product.toml: %w", err)
	}
	var raw rawProduct
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return Product{}, fmt.Errorf("cra: product.toml: %w", err)
	}
	for _, f := range []struct {
		key string
		val string
	}{
		{"name", raw.Name}, {"version", raw.Version}, {"manufacturer", raw.Manufacturer},
		{"security_contact", raw.SecurityContact},
	} {
		if strings.TrimSpace(f.val) == "" {
			return Product{}, fmt.Errorf("cra: product.toml: %s required", f.key)
		}
	}
	se, isTime := raw.SupportEnd.(time.Time)
	if !md.IsDefined("support_end") {
		return Product{}, errors.New("cra: product.toml: support_end required")
	}
	if !isTime {
		return Product{}, errors.New("cra: product.toml: support_end must be a date (YYYY-MM-DD)")
	}
	if raw.Category == "" {
		return Product{}, errors.New("cra: product.toml: category required")
	}
	if !categories[raw.Category] {
		return Product{}, errors.New("cra: product.toml: category must be default, important-1, important-2 or critical")
	}
	// support_end est une date : date locale TOML ou date-heure, ramenée à minuit UTC.
	if isLocalTOML(se) {
		se = time.Date(se.Year(), se.Month(), se.Day(), 0, 0, 0, 0, time.UTC)
	} else {
		u := se.UTC()
		se = time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	}
	sboms := raw.SBOMs
	if len(sboms) == 0 {
		sboms = []string{DefaultSBOMGlob}
	}
	return Product{
		Name: raw.Name, Version: raw.Version, Manufacturer: raw.Manufacturer,
		SecurityContact: raw.SecurityContact, Category: raw.Category,
		SupportEnd: se, SBOMs: sboms,
	}, nil
}

// isLocalTOML indique une valeur TOML sans fuseau (date, date-heure ou heure
// locales) : BurntSushi/toml les décode dans un fuseau fictif nommé (à condition de décoder vers `any` : la voie
// TextUnmarshaler de time.Time perd le nom du fuseau).
func isLocalTOML(t time.Time) bool {
	switch t.Location().String() {
	case "date-local", "datetime-local", "time-local":
		return true
	}
	return false
}

// SBOMPaths développe les motifs `sboms` (relatifs à la racine) en fichiers.
func (d Dossier) SBOMPaths() ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, pat := range d.Product.SBOMs {
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(d.Root, filepath.FromSlash(pat))
		}
		m, err := filepath.Glob(pat)
		if err != nil {
			return nil, fmt.Errorf("cra: product.toml: sboms: %w", err)
		}
		for _, f := range m {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// Incomplete liste les points qui rendent le dossier incomplet à la date now
// (ce n'est pas une erreur de chargement).
func (d Dossier) Incomplete(now time.Time) []string {
	var out []string
	end := d.Product.SupportEnd
	if !end.IsZero() {
		n := now.UTC()
		today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
		if today.After(end) {
			out = append(out, "support period ended on "+end.Format("2006-01-02"))
		}
	}
	return out
}
