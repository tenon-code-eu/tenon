package compliance

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/cra/dossier"
	"github.com/tenon-code-eu/tenon/internal/event"
)

// CRAReportStatus résume un signalement CRA : dernière étape journalisée et
// retard calculé à la date de l'export (Overdue n'a aucun effet sur le code de sortie).
type CRAReportStatus struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Project      string   `json:"project"`
	Step         string   `json:"step"`
	At           string   `json:"at"`
	Overdue      bool     `json:"overdue"`
	OverdueSteps []string `json:"overdue_steps"`
	// LateSteps : étapes faites après leur échéance.
	LateSteps []string `json:"late_steps"`
	// DeadlinesUnknown : aucun événement open dans le journal (échéances incalculables).
	DeadlinesUnknown bool `json:"deadlines_unknown"`
}

// CRA est la section « Cyber Resilience Act » : dernier scan de chaque
// racine de projet (ouvertes, à vérifier, non vérifiés et conflits VEX
// cumulés, date la plus récente) et signalements. Omise sans événement CRA.
type CRA struct {
	LastScan  string            `json:"last_scan"`
	Open      int               `json:"open"`
	ToVerify  int               `json:"to_verify"`
	Unchecked int               `json:"unchecked"`
	Conflicts int               `json:"conflicts"`
	Reports   []CRAReportStatus `json:"reports"`
}

// craSessions détecte les sessions courtes « cra » (sans fournisseur) : leur
// SessionStarted, où qu'il soit dans le journal, porte kind « cra » ; à
// défaut (SessionStarted illisible ou hors lecture), toutes leurs lignes de
// la plage sont des événements CRA.
func craSessions(ds Dataset) map[string]bool {
	out := map[string]bool{}
	other := map[string]bool{}
	for _, l := range ds.Lines {
		if l.Type == event.CRAScan || l.Type == event.CRAReport {
			out[l.Session] = true
		} else {
			other[l.Session] = true
		}
	}
	for sid := range other {
		delete(out, sid)
	}
	for sid, si := range ds.Sessions {
		if si.Started != nil {
			out[sid] = si.Started.Kind == "cra"
		}
	}
	return out
}

// isCRASession indique une session « cra » : hors des statistiques de conversation.
func isCRASession(ds Dataset, sid string) bool { return ds.cra[sid] }

// sessionRoot renvoie la racine de projet d'une session (vide si inconnue).
func sessionRoot(ds Dataset, sid string) string {
	if st := ds.Sessions[sid].Started; st != nil {
		return st.ProjectRoot
	}
	return ""
}

type craKey struct{ root, id string }

type craReport struct {
	r    dossier.Report
	kind string
	last CRAReportStatus
}

// craSection construit la section à partir des événements CRAScan et
// CRAReport des sessions « cra ». Les marques portent leur propre heure ;
// l'événement open porte aware_at. Une plage qui ne contient pas l'événement
// open laisse les échéances inconnues (donc jamais en retard).
func craSection(ds Dataset, o Options) *CRA {
	type scanState struct {
		ts                         time.Time
		open, tv, unchecked, confl int
	}
	scans := map[string]scanState{}
	reports := map[craKey]*craReport{}
	var order []craKey
	for _, l := range ds.Lines {
		if l.Type != event.CRAScan || IsMetadataPayload(l.Payload) || !isCRASession(ds, l.Session) {
			continue
		}
		var p event.CRAScanPayload
		if !decode(l, &p) {
			continue
		}
		root := sessionRoot(ds, l.Session)
		if t := lineTS(l); !t.Before(scans[root].ts) {
			scans[root] = scanState{ts: t, open: p.Open, tv: p.ToVerify, unchecked: p.Unchecked, confl: p.Conflicts}
		}
	}
	// Signalements inscrits dans la période exportée.
	inPeriod := map[craKey]bool{}
	for _, l := range ds.Lines {
		if l.Type != event.CRAReport || IsMetadataPayload(l.Payload) || !isCRASession(ds, l.Session) {
			continue
		}
		var p event.CRAReportPayload
		if decode(l, &p) {
			inPeriod[craKey{sessionRoot(ds, l.Session), p.ID}] = true
		}
	}
	// Les échéances viennent de tout le journal, période ignorée.
	for _, l := range ds.CRALines {
		if IsMetadataPayload(l.Payload) || !isCRASession(ds, l.Session) {
			continue
		}
		var p event.CRAReportPayload
		if !decode(l, &p) {
			continue
		}
		at, err := time.Parse(time.RFC3339, p.At)
		if err != nil {
			continue
		}
		at = at.UTC()
		k := craKey{sessionRoot(ds, l.Session), p.ID}
		cr := reports[k]
		if cr == nil {
			cr = &craReport{r: dossier.Report{ID: p.ID, Kind: p.Kind}}
			reports[k] = cr
			order = append(order, k)
		}
		// Le genre est connu de l'événement open ; une marque seule le reprend de sa charge.
		if p.Step == "open" || cr.kind == "" {
			cr.kind = p.Kind
			cr.r.Kind = p.Kind
		}
		switch p.Step {
		case "open":
			cr.r.AwareAt = &at
		case "early-warning":
			cr.r.EarlyWarningAt = &at
		case "notification":
			cr.r.NotificationAt = &at
		case "final":
			cr.r.FinalAt = &at
		case "fix-available":
			cr.r.FixAvailableAt = &at
		}
		cr.last = CRAReportStatus{Step: p.Step, At: at.Format(time.RFC3339)}
	}
	// Listé : un événement dans la période, ou pas encore clos.
	for k, cr := range reports {
		if !inPeriod[k] && cr.r.FinalAt != nil {
			delete(reports, k)
		}
	}
	order = keysKept(order, reports)
	if len(scans) == 0 && len(reports) == 0 {
		return nil
	}
	c := &CRA{Reports: []CRAReportStatus{}}
	var latest time.Time
	for _, s := range scans {
		c.Open += s.open
		c.ToVerify += s.tv
		c.Unchecked += s.unchecked
		c.Conflicts += s.confl
		if s.ts.After(latest) {
			latest = s.ts
		}
	}
	if !latest.IsZero() {
		c.LastScan = latest.Format(time.RFC3339)
	}
	for _, k := range order {
		cr := reports[k]
		st := cr.last
		st.ID, st.Kind, st.Project = o.clean(k.id), o.clean(cr.r.Kind), o.clean(k.root)
		st.OverdueSteps = []string{}
		st.LateSteps = []string{}
		// Sans événement open, aucune échéance n'est calculable : jamais présenté comme propre.
		st.DeadlinesUnknown = cr.r.AwareAt == nil
		for _, d := range dossier.Deadlines(cr.r, o.Now) {
			if d.Status == dossier.StatusOverdue {
				st.Overdue = true
				st.OverdueSteps = append(st.OverdueSteps, d.Step)
			}
			if d.Status == dossier.StatusDoneLate {
				st.LateSteps = append(st.LateSteps, d.Step)
			}
		}
		c.Reports = append(c.Reports, st)
	}
	sort.Slice(c.Reports, func(i, j int) bool {
		a, b := c.Reports[i], c.Reports[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return reportIDLess(a.ID, b.ID)
	})
	return c
}

// reportIDLess ordonne AAAA-MM-JJ-<n> par date puis par numéro (numérique).
func reportIDLess(a, b string) bool {
	ia, ib := strings.LastIndex(a, "-"), strings.LastIndex(b, "-")
	if ia < 0 || ib < 0 || a[:ia] != b[:ib] {
		return a < b
	}
	na, ea := strconv.Atoi(a[ia+1:])
	nb, eb := strconv.Atoi(b[ib+1:])
	if ea != nil || eb != nil {
		return a < b
	}
	return na < nb
}

// keysKept filtre order sur les clés restantes de reports.
func keysKept(order []craKey, reports map[craKey]*craReport) []craKey {
	out := order[:0]
	for _, k := range order {
		if _, ok := reports[k]; ok {
			out = append(out, k)
		}
	}
	return out
}
