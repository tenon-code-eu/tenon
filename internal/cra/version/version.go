// Package version compare des versions de composants selon les règles de leur
// écosystème (semver, PEP 440), pour évaluer les plages de versions OSV.
package version

import "strings"

// Comparator compare deux versions : -1 si a < b, 0 si égales, 1 si a > b.
// Une version invalide pour l'écosystème renvoie une erreur, jamais un
// résultat arbitraire (un faux résultat masquerait une vulnérabilité).
type Comparator interface {
	Compare(a, b string) (int, error)
}

// For renvoie le comparateur d'un écosystème OSV ; false s'il n'y en a pas
// (seule la liste explicite `versions` d'OSV s'applique alors).
func For(ecosystem string) (Comparator, bool) {
	switch ecosystem {
	case "Go", "npm", "crates.io":
		return Semver, true
	case "PyPI":
		return PEP440, true
	}
	return nil, false
}

// cmpDigits compare deux suites de chiffres décimales de taille arbitraire
// (pas de débordement d'entier).
func cmpDigits(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	switch {
	case len(a) != len(b):
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
