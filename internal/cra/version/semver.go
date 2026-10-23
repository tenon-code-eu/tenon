package version

import (
	"fmt"
	"strings"
)

type semverComparator struct{}

// Semver compare selon semver 2.0 §11. Préfixe `v` accepté, métadonnées de
// build ignorées, `1.2` lu comme `1.2.0`. Les pseudo-versions Go
// (vX.Y.Z-0.yyyymmddhhmmss-abcdefabcdef) sont des pré-releases semver : la
// précédence ordinaire les classe correctement.
var Semver Comparator = semverComparator{}

type semver struct {
	core [3]string
	pre  []string
}

func parseSemver(s string) (semver, error) {
	var v semver
	orig := s
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if s[i+1:] == "" {
			return v, fmt.Errorf("semver: %q: métadonnées de build vides", orig)
		}
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("semver: %q: trop de segments", orig)
	}
	for i := range v.core {
		v.core[i] = "0"
	}
	for i, p := range parts {
		if !isDigits(p) {
			return v, fmt.Errorf("semver: %q: segment %q invalide", orig, p)
		}
		v.core[i] = p
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return v, fmt.Errorf("semver: %q: identifiant de pré-release vide", orig)
			}
			for i := 0; i < len(id); i++ {
				c := id[i]
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
					return v, fmt.Errorf("semver: %q: caractère invalide dans la pré-release", orig)
				}
			}
		}
	}
	return v, nil
}

func (semverComparator) Compare(a, b string) (int, error) {
	x, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	y, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := range x.core {
		if c := cmpDigits(x.core[i], y.core[i]); c != 0 {
			return c, nil
		}
	}
	// Une version sans pré-release est supérieure à celle qui en a une.
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0, nil
	case len(x.pre) == 0:
		return 1, nil
	case len(y.pre) == 0:
		return -1, nil
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		p, q := x.pre[i], y.pre[i]
		pn, qn := isDigits(p), isDigits(q)
		var c int
		switch {
		case pn && qn:
			c = cmpDigits(p, q)
		case pn:
			c = -1 // numérique < alphanumérique
		case qn:
			c = 1
		default:
			c = strings.Compare(p, q)
		}
		if c != 0 {
			return c, nil
		}
	}
	// Tous les champs communs égaux : le plus grand nombre de champs gagne.
	switch {
	case len(x.pre) < len(y.pre):
		return -1, nil
	case len(x.pre) > len(y.pre):
		return 1, nil
	}
	return 0, nil
}
