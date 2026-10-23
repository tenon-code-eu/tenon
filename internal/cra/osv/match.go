package osv

import (
	"errors"
	"sort"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/cra/sbom"
	"github.com/tenon-code-eu/tenon/internal/cra/version"
)

// États d'un constat.
const (
	StateAffected = "affected"
	StateToVerify = "to_verify"
)

// Finding est une vulnérabilité OSV rapprochée d'un composant.
type Finding struct {
	ID        string
	Aliases   []string
	Summary   string
	Component sbom.Component
	State     string
}

// verdict d'un affected : rang croissant de gravité pour la fusion.
type verdict int

const (
	notAffected verdict = iota
	toVerify
	isAffected
)

// Match renvoie les vulnérabilités qui concernent le composant, une par ID.
// Un composant sans purl exploitable, ou d'un écosystème hors correspondance,
// n'est pas rapproché (nil, nil) : il est signalé à part par l'appelant.
// Un écosystème absent de la base renvoie ErrNoArchive ; une archive
// illisible, une erreur nommant l'écosystème. Jamais de « aucune
// vulnérabilité » silencieux.
func (db *DB) Match(c sbom.Component) ([]Finding, error) {
	p, eco, ok := parse(c)
	if !ok {
		return nil, nil
	}
	ix, err := db.load(eco)
	if err != nil {
		return nil, err
	}
	ver := p.Version
	if ver == "" {
		ver = c.Version
	}
	cmp, hasCmp := version.For(eco)
	name := p.OSVName()
	// OSV indexe stdlib et toolchain Go sans le préfixe « go » (go1.22.1 → 1.22.1).
	if eco == "Go" && (name == "stdlib" || name == "toolchain") {
		ver = strings.TrimPrefix(ver, "go")
	}
	var out []Finding
	for _, e := range ix.byName[name] {
		v := notAffected
		for _, a := range e.Affected {
			if a.Package.Ecosystem != eco || normName(eco, a.Package.Name) != name {
				continue
			}
			if r := evalAffected(a, ver, cmp, hasCmp); r > v {
				v = r
			}
		}
		if v == notAffected {
			continue
		}
		f := Finding{ID: e.ID, Summary: e.Summary, Component: c, State: StateAffected}
		if v == toVerify {
			f.State = StateToVerify
		}
		f.Aliases = append([]string(nil), e.Aliases...)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// MatchAll rapproche tous les composants ; un composant présent plusieurs fois
// dans le SBOM (même purl hors qualificatifs, même version) n'est rapporté
// qu'une fois par vulnérabilité, avec sa première occurrence.
func (db *DB) MatchAll(cs []sbom.Component) ([]Finding, error) {
	seen := map[string]bool{}
	var out []Finding
	for _, c := range cs {
		p, _, ok := parse(c)
		if !ok {
			continue
		}
		ver := p.Version
		if ver == "" {
			ver = c.Version
		}
		key := p.Type + "/" + p.Namespace + "/" + p.Name + "@" + ver
		if seen[key] {
			continue
		}
		seen[key] = true
		fs, err := db.Match(c)
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
	}
	return out, nil
}

// parse décode le purl du composant et son écosystème OSV.
func parse(c sbom.Component) (sbom.PURL, string, bool) {
	if c.PURL == "" {
		return sbom.PURL{}, "", false
	}
	p, err := sbom.ParsePURL(c.PURL)
	if err != nil {
		return sbom.PURL{}, "", false
	}
	eco, ok := p.Ecosystem()
	if !ok {
		return sbom.PURL{}, "", false
	}
	return p, eco, true
}

// evalAffected évalue un affected OSV pour une version : liste `versions`,
// puis plages SEMVER/ECOSYSTEM (GIT ignorées). Tout doute donne toVerify.
func evalAffected(a affected, ver string, cmp version.Comparator, hasCmp bool) verdict {
	var ranges []osvRange
	for _, r := range a.Ranges {
		if r.Type == "SEMVER" || r.Type == "ECOSYSTEM" {
			ranges = append(ranges, r)
		}
	}
	// Ni liste ni plage évaluable : affecté de façon inconnue.
	if len(a.Versions) == 0 && len(ranges) == 0 {
		return toVerify
	}
	if ver == "" {
		return toVerify
	}
	if listed(a.Versions, ver, cmp, hasCmp) {
		return isAffected
	}
	// OSV : l'ensemble affecté est l'union de `versions` et des plages. Une
	// liste `versions` sans plage évaluable est complète : une version non
	// listée n'est pas affectée. to_verify ne vaut que pour des plages
	// SEMVER/ECOSYSTEM que le comparateur ne sait pas évaluer.
	if len(ranges) == 0 {
		return notAffected
	}
	if !hasCmp {
		return toVerify
	}
	res := notAffected
	for _, r := range ranges {
		in, err := inRange(r.Events, ver, cmp)
		switch {
		case err != nil:
			res = toVerify
		case in:
			return isAffected
		}
	}
	return res
}

// listed teste l'appartenance à la liste `versions` : égalité exacte (au
// préfixe `v` près pour Go), ou égalité selon le comparateur.
func listed(versions []string, ver string, cmp version.Comparator, hasCmp bool) bool {
	bare := strings.TrimPrefix(ver, "v")
	for _, lv := range versions {
		if lv == ver || strings.TrimPrefix(lv, "v") == bare {
			return true
		}
		if hasCmp {
			if c, err := cmp.Compare(ver, lv); err == nil && c == 0 {
				return true
			}
		}
	}
	return false
}

// inRange trie les événements par version (« introduced: 0 » en tête, tri
// stable pour les égalités) comme l'algorithme de référence OSV, puis les
// parcourt : introduced ouvre, fixed ferme à partir de sa version,
// last_affected ferme après sa version ; limit est ignoré. Une plage sans
// événement ou sans introduced est mal formée : erreur, donc à vérifier.
func inRange(events []event, ver string, cmp version.Comparator) (bool, error) {
	type ev struct {
		kind byte // 'i', 'f' ou 'l'
		v    string
	}
	var evs []ev
	hasIntro := false
	for _, e := range events {
		switch {
		case e.Introduced != "":
			evs = append(evs, ev{'i', e.Introduced})
			hasIntro = true
		case e.Fixed != "":
			evs = append(evs, ev{'f', e.Fixed})
		case e.LastAffected != "":
			evs = append(evs, ev{'l', e.LastAffected})
		}
	}
	if !hasIntro {
		return false, errMalformedRange
	}
	// Tri par insertion (stable) ; la moindre erreur de comparaison l'emporte.
	less := func(a, b ev) (bool, error) {
		if b.kind == 'i' && b.v == "0" {
			return false, nil
		}
		if a.kind == 'i' && a.v == "0" {
			return true, nil
		}
		c, err := cmp.Compare(a.v, b.v)
		return c < 0, err
	}
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0; j-- {
			l, err := less(evs[j], evs[j-1])
			if err != nil {
				return false, err
			}
			if !l {
				break
			}
			evs[j], evs[j-1] = evs[j-1], evs[j]
		}
	}
	in := false
	for _, e := range evs {
		if e.kind == 'i' && e.v == "0" {
			in = true
			continue
		}
		c, err := cmp.Compare(ver, e.v)
		if err != nil {
			return false, err
		}
		switch e.kind {
		case 'i':
			if c >= 0 {
				in = true
			}
		case 'f':
			if c >= 0 {
				in = false
			}
		case 'l':
			if c > 0 {
				in = false
			}
		}
	}
	return in, nil
}

var errMalformedRange = errors.New("osv: range without introduced event")
