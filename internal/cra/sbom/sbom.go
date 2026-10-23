// Package sbom lit les SBOM CycloneDX JSON et analyse les package-URL (purl).
package sbom

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Component est un composant logiciel d'un SBOM.
type Component struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl"`
	BOMRef  string `json:"bom_ref,omitempty"`
}

// BOM est un SBOM CycloneDX aplati.
type BOM struct {
	SpecVersion, Generator string
	Timestamp              time.Time
	Components             []Component
}

type rawComponent struct {
	BOMRef     string         `json:"bom-ref"`
	Name       string         `json:"name"`
	Version    string         `json:"version"`
	PURL       string         `json:"purl"`
	Components []rawComponent `json:"components"`
}

type rawTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type rawBOM struct {
	BOMFormat   string `json:"bomFormat"`
	SpecVersion string `json:"specVersion"`
	Metadata    struct {
		Timestamp string          `json:"timestamp"`
		Tools     json.RawMessage `json:"tools"`
	} `json:"metadata"`
	Components []rawComponent `json:"components"`
}

// Read lit un SBOM CycloneDX JSON 1.4 à 1.6.
func Read(path string) (BOM, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BOM{}, fmt.Errorf("sbom: %s: %w", path, err)
	}
	var raw rawBOM
	if err := json.Unmarshal(data, &raw); err != nil {
		return BOM{}, fmt.Errorf("sbom: %s: %w", path, err)
	}
	if raw.BOMFormat != "CycloneDX" {
		return BOM{}, fmt.Errorf("sbom: %s: not a CycloneDX document", path)
	}
	switch raw.SpecVersion {
	case "1.4", "1.5", "1.6":
	default:
		return BOM{}, fmt.Errorf("sbom: %s: unsupported CycloneDX version %s", path, raw.SpecVersion)
	}
	b := BOM{SpecVersion: raw.SpecVersion, Generator: generator(raw.Metadata.Tools)}
	// Horodatage illisible : ignoré (valeur zéro).
	if t, err := time.Parse(time.RFC3339, raw.Metadata.Timestamp); err == nil {
		b.Timestamp = t.UTC()
	}
	b.Components = flatten(nil, raw.Components)
	return b, nil
}

// flatten aplatit les composants imbriqués en profondeur d'abord, parent avant enfants.
func flatten(out []Component, in []rawComponent) []Component {
	for _, c := range in {
		out = append(out, Component{Name: c.Name, Version: c.Version, PURL: c.PURL, BOMRef: c.BOMRef})
		out = flatten(out, c.Components)
	}
	return out
}

// generator extrait le premier outil : tableau (1.4) ou objet components/services (1.5+).
func generator(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var list []rawTool
	if json.Unmarshal(raw, &list) != nil {
		var obj struct {
			Components []rawTool `json:"components"`
			Services   []rawTool `json:"services"`
		}
		if json.Unmarshal(raw, &obj) != nil {
			return ""
		}
		list = append(obj.Components, obj.Services...)
	}
	if len(list) == 0 {
		return ""
	}
	t := list[0]
	if t.Version == "" {
		return t.Name
	}
	return t.Name + " " + t.Version
}
