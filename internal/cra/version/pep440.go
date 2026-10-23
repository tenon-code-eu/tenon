package version

import (
	"fmt"
	"regexp"
	"strings"
)

type pep440Comparator struct{}

// PEP440 compare selon PEP 440 (epoch, release, pré-release, post, dev).
// Le label local (`+cu117`) est ignoré (comportement pip-audit) : les plages
// OSV ne le portent pas et les SBOM réels en contiennent.
var PEP440 Comparator = pep440Comparator{}

var pep440Re = regexp.MustCompile(`^(?:(\d+)!)?(\d+(?:\.\d+)*)` +
	`(?:[-_.]?(alpha|a|beta|b|preview|pre|c|rc)[-_.]?(\d+)?)?` +
	`(?:-(\d+)|[-_.]?(post|rev|r)[-_.]?(\d+)?)?` +
	`(?:[-_.]?(dev)[-_.]?(\d+)?)?` +
	`(\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?$`)

type pep440 struct {
	epoch   string
	release []string // zéros finaux retirés
	preRank int      // -1 : avant toute pré-release (dev seul), 0 a, 1 b, 2 rc, 3 : aucune
	preNum  string
	post    string // "" : pas de post
	hasPost bool
	dev     string
	hasDev  bool
}

func parsePEP440(s string) (pep440, error) {
	var v pep440
	n := strings.ToLower(strings.TrimSpace(s))
	n = strings.TrimPrefix(n, "v")
	m := pep440Re.FindStringSubmatch(n)
	if m == nil {
		return v, fmt.Errorf("pep440: %q: version invalide", s)
	}
	v.epoch = m[1]
	if v.epoch == "" {
		v.epoch = "0"
	}
	rel := strings.Split(m[2], ".")
	for len(rel) > 1 && strings.TrimLeft(rel[len(rel)-1], "0") == "" {
		rel = rel[:len(rel)-1]
	}
	v.release = rel

	hasPre := m[3] != ""
	hasPost := m[5] != "" || m[6] != ""
	hasDev := m[8] != ""
	v.hasPost, v.hasDev = hasPost, hasDev
	switch m[3] {
	case "alpha", "a":
		v.preRank = 0
	case "beta", "b":
		v.preRank = 1
	case "c", "rc", "pre", "preview":
		v.preRank = 2
	}
	v.preNum = numOrZero(m[4])
	switch {
	case !hasPre && !hasPost && hasDev:
		v.preRank, v.preNum = -1, "0" // 1.0.dev1 avant 1.0a1
	case !hasPre:
		v.preRank, v.preNum = 3, "0"
	}
	if hasPost {
		if m[5] != "" {
			v.post = m[5]
		} else {
			v.post = numOrZero(m[7])
		}
	}
	if hasDev {
		v.dev = numOrZero(m[9])
	}
	return v, nil
}

func numOrZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func (pep440Comparator) Compare(a, b string) (int, error) {
	x, err := parsePEP440(a)
	if err != nil {
		return 0, err
	}
	y, err := parsePEP440(b)
	if err != nil {
		return 0, err
	}
	if c := cmpDigits(x.epoch, y.epoch); c != 0 {
		return c, nil
	}
	for i := 0; i < len(x.release) && i < len(y.release); i++ {
		if c := cmpDigits(x.release[i], y.release[i]); c != 0 {
			return c, nil
		}
	}
	if len(x.release) != len(y.release) {
		if len(x.release) < len(y.release) {
			return -1, nil
		}
		return 1, nil
	}
	if x.preRank != y.preRank {
		if x.preRank < y.preRank {
			return -1, nil
		}
		return 1, nil
	}
	if c := cmpDigits(x.preNum, y.preNum); c != 0 {
		return c, nil
	}
	// Pas de post < post.
	if x.hasPost != y.hasPost {
		if !x.hasPost {
			return -1, nil
		}
		return 1, nil
	}
	if x.hasPost {
		if c := cmpDigits(x.post, y.post); c != 0 {
			return c, nil
		}
	}
	// dev avant non-dev.
	if x.hasDev != y.hasDev {
		if x.hasDev {
			return -1, nil
		}
		return 1, nil
	}
	if x.hasDev {
		return cmpDigits(x.dev, y.dev), nil
	}
	return 0, nil
}
