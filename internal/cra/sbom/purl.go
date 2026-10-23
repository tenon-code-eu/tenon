package sbom

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// PURL est un package-URL décodé ; qualifiers et subpath sont ignorés.
type PURL struct {
	Type, Namespace, Name, Version string
}

// ParsePURL analyse « pkg:type/namespace/name@version?qualifiers#subpath ».
func ParsePURL(s string) (PURL, error) {
	rest := s
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i]
	}
	if len(rest) < 4 || !strings.EqualFold(rest[:4], "pkg:") {
		return PURL{}, fmt.Errorf("purl: %q: missing pkg: scheme", s)
	}
	rest = strings.TrimLeft(rest[4:], "/")
	typ, path, ok := strings.Cut(rest, "/")
	if !ok || typ == "" {
		return PURL{}, fmt.Errorf("purl: %q: missing type or name", s)
	}
	// La version suit le dernier « @ » situé après le dernier « / » (un @scope npm reste dans le chemin).
	version := ""
	if i := strings.LastIndexByte(path, '@'); i > strings.LastIndexByte(path, '/') {
		version, path = path[i+1:], path[:i]
	}
	segs := strings.Split(path, "/")
	dec := make([]string, 0, len(segs))
	for _, sg := range segs {
		if sg == "" {
			continue
		}
		d, err := url.PathUnescape(sg)
		if err != nil {
			return PURL{}, fmt.Errorf("purl: %q: %w", s, err)
		}
		dec = append(dec, d)
	}
	if len(dec) == 0 {
		return PURL{}, errors.New("purl: " + s + ": missing name")
	}
	version, err := url.PathUnescape(version)
	if err != nil {
		return PURL{}, fmt.Errorf("purl: %q: %w", s, err)
	}
	return PURL{
		Type:      strings.ToLower(typ),
		Namespace: strings.Join(dec[:len(dec)-1], "/"),
		Name:      dec[len(dec)-1],
		Version:   version,
	}, nil
}

var ecosystems = map[string]string{
	"golang": "Go", "npm": "npm", "cargo": "crates.io", "pypi": "PyPI",
	"maven": "Maven", "nuget": "NuGet", "gem": "RubyGems", "composer": "Packagist",
}

// Ecosystem renvoie l'écosystème OSV du type purl.
func (p PURL) Ecosystem() (string, bool) {
	e, ok := ecosystems[p.Type]
	return e, ok
}

var pypiSep = regexp.MustCompile(`[-_.]+`)

// OSVName renvoie le nom du paquet tel que l'indexe OSV.
func (p PURL) OSVName() string {
	switch p.Type {
	case "maven":
		if p.Namespace == "" {
			return p.Name
		}
		return p.Namespace + ":" + p.Name
	case "pypi":
		// Normalisation PEP 503.
		return pypiSep.ReplaceAllString(strings.ToLower(p.Name), "-")
	}
	if p.Namespace == "" {
		return p.Name
	}
	return p.Namespace + "/" + p.Name
}
