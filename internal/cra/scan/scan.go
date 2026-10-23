// Package scan rapproche les composants des SBOM de la base OSV et applique le VEX du dossier.
package scan

import (
	"fmt"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/cra/dossier"
	"github.com/tenon-code-eu/tenon/internal/cra/osv"
	"github.com/tenon-code-eu/tenon/internal/cra/sbom"
)

// Statuts d'un constat après application du VEX.
const (
	StatusOpen        = "open"
	StatusNotAffected = "not_affected"
	StatusFixed       = "fixed"
	StatusToVerify    = "to_verify"
)

// Finding est un constat OSV avec son statut VEX.
type Finding struct {
	osv.Finding
	Status string
	// Conflict : le VEX dit « fixed » alors qu'OSV classe la version exacte du SBOM comme affectée.
	Conflict bool
}

// ConflictMessage décrit le conflit VEX/OSV pour l'affichage.
func (f Finding) ConflictMessage() string {
	return "fixed per VEX but " + f.Component.PURL + " is listed as affected by " + f.ID
}

// Result est le résultat d'un scan.
type Result struct {
	SBOMs, Components                  int
	Findings                           []Finding
	Open, ToVerify, NotAffected, Fixed int
	DBDate                             time.Time
	Conflicts, Unchecked               int
	// UncheckedComponents : composants sans purl exploitable ou de type non pris en charge.
	UncheckedComponents []sbom.Component
}

// NoDBError signale qu'aucune archive OSV n'existe pour un écosystème des SBOM :
// ses composants ne peuvent pas être vérifiés.
type NoDBError struct{ Ecosystem string }

func (e *NoDBError) Error() string {
	if e.Ecosystem == "" {
		return "cra: no vulnerability database (run tenon cra db update)"
	}
	return "cra: no vulnerability database for " + e.Ecosystem + " (run tenon cra db update)"
}

// Unwrap permet errors.Is(err, osv.ErrNoArchive).
func (e *NoDBError) Unwrap() error { return osv.ErrNoArchive }

// Run apparie les composants de tous les SBOM à la base et applique le VEX du dossier.
// Un écosystème sans archive est une erreur (*NoDBError), jamais un résultat « propre ».
func Run(d dossier.Dossier, boms []sbom.BOM, db *osv.DB) (Result, error) {
	res := Result{SBOMs: len(boms)}
	var comps []sbom.Component
	for _, b := range boms {
		comps = append(comps, b.Components...)
	}
	res.Components = len(comps)
	if db == nil {
		return res, &NoDBError{}
	}
	// Écosystèmes utilisés : tous doivent avoir une archive.
	var oldest time.Time
	seen := map[string]bool{}
	for _, c := range comps {
		p, err := sbom.ParsePURL(c.PURL)
		if err != nil {
			res.Unchecked++
			res.UncheckedComponents = append(res.UncheckedComponents, c)
			continue
		}
		eco, ok := p.Ecosystem()
		if !ok {
			res.Unchecked++
			res.UncheckedComponents = append(res.UncheckedComponents, c)
			continue
		}
		if seen[eco] {
			continue
		}
		seen[eco] = true
		date, has := db.Date(eco)
		if !has {
			return res, &NoDBError{Ecosystem: eco}
		}
		if oldest.IsZero() || date.Before(oldest) {
			oldest = date
		}
	}
	res.DBDate = oldest
	fs, err := db.MatchAll(comps)
	if err != nil {
		return res, err
	}
	for _, f := range fs {
		st, vexState := statusFor(f, d.VEX)
		sf := Finding{Finding: f, Status: st}
		if vexState == dossier.VEXFixed && f.State == osv.StateAffected {
			sf.Conflict = true
			res.Conflicts++
		}
		switch sf.Status {
		case StatusOpen:
			res.Open++
		case StatusToVerify:
			res.ToVerify++
		case StatusNotAffected:
			res.NotAffected++
		case StatusFixed:
			res.Fixed++
		}
		res.Findings = append(res.Findings, sf)
	}
	return res, nil
}

// statusFor choisit l'entrée VEX la plus précise (ref versionnée ou bom-ref
// avant ref sans version) et en déduit le statut.
func statusFor(f osv.Finding, vex []dossier.VEXEntry) (string, string) {
	best, bestScore := "", 0
	for _, e := range vex {
		if !idMatches(e.ID, f) {
			continue
		}
		score := refScore(e.Ref, f.Component)
		// À score égal, l'état le plus prudent l'emporte (pas l'ordre du fichier).
		if score > bestScore || (score == bestScore && score > 0 && caution(e.State) > caution(best)) {
			best, bestScore = e.State, score
		}
	}
	switch best {
	case dossier.VEXNotAffected:
		return StatusNotAffected, best
	case dossier.VEXFixed:
		return StatusFixed, best
	case dossier.VEXAffected, dossier.VEXUnderInvestigation:
		return StatusOpen, best
	}
	if f.State == osv.StateToVerify {
		return StatusToVerify, best
	}
	return StatusOpen, best
}

// caution classe les états VEX : affected/under_investigation > fixed > not_affected.
func caution(state string) int {
	switch state {
	case dossier.VEXAffected, dossier.VEXUnderInvestigation:
		return 3
	case dossier.VEXFixed:
		return 2
	case dossier.VEXNotAffected:
		return 1
	}
	return 0
}

func idMatches(id string, f osv.Finding) bool {
	if id == f.ID {
		return true
	}
	for _, a := range f.Aliases {
		if id == a {
			return true
		}
	}
	return false
}

// refScore : 0 sans correspondance, 1 purl sans version, 2 purl versionné ou bom-ref.
func refScore(ref string, c sbom.Component) int {
	if ref == "" {
		return 0
	}
	if c.BOMRef != "" && ref == c.BOMRef {
		return 2
	}
	if !strings.HasPrefix(strings.ToLower(ref), "pkg:") || c.PURL == "" {
		return 0
	}
	rp, err := sbom.ParsePURL(ref)
	if err != nil {
		return 0
	}
	cp, err := sbom.ParsePURL(c.PURL)
	if err != nil {
		return 0
	}
	if rp.Type != cp.Type || rp.OSVName() != cp.OSVName() {
		return 0
	}
	if rp.Version == "" {
		return 1
	}
	cv := cp.Version
	if cv == "" {
		cv = c.Version
	}
	if rp.Version == cv {
		return 2
	}
	return 0
}

// String aide au débogage des tests.
func (f Finding) String() string { return fmt.Sprintf("%s:%s", f.ID, f.Status) }
