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

// ErrNoDefaultSBOM : `sboms` absent et le motif par défaut ne trouve aucun
// fichier. Le SBOM n'est pas encore produit : dossier incomplet pour status
// et export, erreur pour scan et db update.
var ErrNoDefaultSBOM = errors.New("cra: no SBOM found (" + DefaultSBOMGlob + ")")

// SBOMMissingError : une entrée explicite de `sboms` ne désigne aucun
// fichier. Le dossier est invalide.
type SBOMMissingError struct{ Entry string }

func (e *SBOMMissingError) Error() string { return "cra: no SBOM found (" + e.Entry + ")" }

// Product décrit le produit couvert par le dossier.
// Les balises JSON (snake_case) forment le schéma du dossier exporté.
type Product struct {
	Name            string    `json:"name"`
	Version         string    `json:"version"`
	Manufacturer    string    `json:"manufacturer"`
	SecurityContact string    `json:"security_contact"`
	Category        string    `json:"category"`
	SupportEnd      time.Time `json:"support_end"`
	SBOMs           []string  `json:"sboms"`
	// SBOMsDefault : `sboms` absent de product.toml (motif par défaut).
	SBOMsDefault bool `json:"-"`
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
	if err := unknownKey(md, "cra: product.toml: "); err != nil {
		return Product{}, err
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
	sboms, def := raw.SBOMs, false
	if len(sboms) == 0 {
		sboms, def = []string{DefaultSBOMGlob}, true
	}
	return Product{
		Name: raw.Name, Version: raw.Version, Manufacturer: raw.Manufacturer,
		SecurityContact: raw.SecurityContact, Category: raw.Category,
		SupportEnd: se, SBOMs: sboms, SBOMsDefault: def,
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

// unknownKey refuse la première clé non décodée (faute de frappe ou champ
// ajouté à la main) : un fichier lu en partie ne doit jamais être réécrit.
func unknownKey(md toml.MetaData, prefix string) error {
	if u := md.Undecoded(); len(u) > 0 {
		return fmt.Errorf("%sunknown key %s", prefix, u[0].String())
	}
	return nil
}

// SBOMPaths développe les motifs `sboms` (relatifs à la racine) en fichiers.
// Chaque entrée explicite doit désigner au moins un fichier, sinon
// *SBOMMissingError : un SBOM listé absent n'est jamais ignoré en silence.
// Sans `sboms`, un motif par défaut vide donne ErrNoDefaultSBOM.
func (d Dossier) SBOMPaths() ([]string, error) {
	entries := d.Product.SBOMs
	if len(entries) == 0 || d.Product.SBOMsDefault {
		m, err := filepath.Glob(filepath.Join(d.Root, filepath.FromSlash(DefaultSBOMGlob)))
		if err != nil || len(m) == 0 {
			return nil, ErrNoDefaultSBOM
		}
		return m, nil
	}
	var out []string
	seen := map[string]bool{}
	for _, entry := range entries {
		pat := entry
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(d.Root, filepath.FromSlash(pat))
		}
		m, err := filepath.Glob(pat)
		if err != nil {
			return nil, fmt.Errorf("cra: product.toml: sboms: %w", err)
		}
		if len(m) == 0 {
			return nil, &SBOMMissingError{Entry: entry}
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
