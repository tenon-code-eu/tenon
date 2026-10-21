// Package compliance construit le rapport de conformité à partir du journal d'audit.
package compliance

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/session"
)

// Niveaux d'audit détectés sur les lignes retenues.
const (
	LevelFull     = "full"
	LevelMetadata = "metadata"
	LevelMixed    = "mixed"
	LevelNone     = "none"
)

// Filter borne le périmètre du rapport. Since et Until sont des minuits UTC ;
// Until est le dernier jour inclus.
type Filter struct {
	Since, Until time.Time
	Project      string
	AllProjects  bool
	Session      string
}

// Chain résume la vérification de la chaîne de hachage.
type Chain struct {
	Intact    bool
	Error     string
	Lines     int
	FinalHash string
}

// Unreadable désigne une ligne du journal qui n'a pas pu être lue.
type Unreadable struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Error string `json:"error"`
}

// SessionInfo regroupe ce que le journal dit d'une session.
type SessionInfo struct {
	// Started est le premier SessionStarted (nil au niveau metadata ou s'il est absent).
	Started *event.SessionStartedPayload
}

// Dataset est le journal vérifié et filtré.
type Dataset struct {
	Filter                   Filter
	Chain                    Chain
	Lines                    []audit.Line
	Unreadable               []Unreadable
	Sessions                 map[string]SessionInfo
	Level                    string
	ExcludedMetadataSessions int
	// Timestamps résume les jetons RFC 3161 ; calculé seulement si la chaîne
	// est intacte (zéro sinon).
	Timestamps audit.TimestampSummary
}

// errVerifiedEnd arrête la lecture après la dernière ligne vérifiée.
var errVerifiedEnd = errors.New("end of verified lines")

// afterVerify est appelée entre la vérification et la lecture ; remplacée
// dans les tests pour simuler un ajout concurrent au journal.
var afterVerify = func() {}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsMetadataPayload indique si p est une charge réduite du niveau metadata :
// un objet avec exactement les clés sha256 (64 hex minuscules) et size (nombre).
func IsMetadataPayload(p json.RawMessage) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(p, &m) != nil || len(m) != 2 {
		return false
	}
	var sum string
	if json.Unmarshal(m["sha256"], &sum) != nil || !sha256Hex.MatchString(sum) {
		return false
	}
	var size float64
	raw, ok := m["size"]
	if !ok {
		return false
	}
	return json.Unmarshal(raw, &size) == nil
}

// Load vérifie le journal puis le filtre. Il ne modifie ni ne verrouille rien :
// seuls audit.Verify, audit.VerifyTimestamps et audit.ReadLines sont appelés.
// roots : racines de confiance des jetons (nil : racines système). Erreur
// seulement si la lecture échoue.
func Load(dir string, f Filter, roots *x509.CertPool) (Dataset, error) {
	ds := Dataset{Filter: f, Sessions: map[string]SessionInfo{}, Level: LevelNone}

	final, n, verr := audit.Verify(dir)
	ds.Chain = Chain{Intact: verr == nil, Lines: n, FinalHash: final}
	if verr != nil {
		ds.Chain.Error = verr.Error()
		ds.Chain.FinalHash = ""
	}
	if verr == nil {
		ts, err := audit.VerifyTimestamps(dir, roots)
		if err != nil {
			return Dataset{}, err
		}
		ds.Timestamps = ts
	}
	afterVerify()

	end := f.Until.AddDate(0, 0, 1)
	metaStart := map[string]bool{} // session dont le premier SessionStarted est réduit
	seen := map[string]bool{}      // session dont le premier SessionStarted est vu
	var cands []audit.Line

	// Chaîne intacte : la lecture s'arrête après les ds.Chain.Lines lignes
	// vérifiées (ReadLines et Verify parcourent les fichiers dans le même
	// ordre) ; une ligne ajoutée entre-temps n'est pas rapportée.
	delivered := 0
	err := audit.ReadLines(dir, func(file string, lineNo int, l audit.Line, err error) error {
		if ds.Chain.Intact && delivered >= ds.Chain.Lines {
			return errVerifiedEnd
		}
		delivered++
		if err != nil {
			ds.Unreadable = append(ds.Unreadable, Unreadable{File: file, Line: lineNo, Error: err.Error()})
			return nil
		}
		ts, perr := time.Parse(time.RFC3339Nano, l.TS)
		if perr != nil {
			ds.Unreadable = append(ds.Unreadable, Unreadable{File: file, Line: lineNo, Error: "ts: " + perr.Error()})
			return nil
		}
		if _, ok := ds.Sessions[l.Session]; !ok {
			ds.Sessions[l.Session] = SessionInfo{}
		}
		if l.Type == event.SessionStarted && !seen[l.Session] {
			seen[l.Session] = true
			if IsMetadataPayload(l.Payload) {
				metaStart[l.Session] = true
			} else {
				var p event.SessionStartedPayload
				if json.Unmarshal(l.Payload, &p) == nil {
					ds.Sessions[l.Session] = SessionInfo{Started: &p}
				}
			}
		}
		t := ts.UTC()
		if t.Before(f.Since) || !t.Before(end) {
			return nil
		}
		if f.Session != "" && l.Session != f.Session {
			return nil
		}
		cands = append(cands, l)
		return nil
	})
	if err != nil && !errors.Is(err, errVerifiedEnd) {
		return Dataset{}, err
	}

	excluded := map[string]bool{}
	for _, l := range cands {
		if !keep(f, ds.Sessions[l.Session], l.Session, metaStart, excluded) {
			continue
		}
		ds.Lines = append(ds.Lines, l)
	}
	ds.ExcludedMetadataSessions = len(excluded)

	var full, meta bool
	for _, l := range ds.Lines {
		if IsMetadataPayload(l.Payload) {
			meta = true
		} else {
			full = true
		}
	}
	switch {
	case full && meta:
		ds.Level = LevelMixed
	case meta:
		ds.Level = LevelMetadata
	case full:
		ds.Level = LevelFull
	}
	return ds, nil
}

// keep applique le périmètre projet/session à une ligne candidate.
func keep(f Filter, si SessionInfo, sid string, metaStart, excluded map[string]bool) bool {
	if f.Session != "" || f.AllProjects {
		return true
	}
	if si.Started == nil {
		if metaStart[sid] {
			excluded[sid] = true
		}
		return false
	}
	return session.SameRoot(si.Started.ProjectRoot, f.Project)
}
