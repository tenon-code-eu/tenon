package dossier

import "time"

// Étapes et statuts des échéances.
const (
	StepEarlyWarning = "early-warning"
	StepNotification = "notification"
	StepFinal        = "final"

	StatusDue     = "due"
	StatusOverdue = "overdue"
	StatusDone    = "done"
	StatusUnknown = "unknown"
)

// Deadline est l'échéance d'une étape de signalement.
type Deadline struct {
	Step   string
	Due    *time.Time
	Done   *time.Time
	Status string
}

// Deadlines calcule les échéances d'un signalement à la date now. Fonction
// pure, en UTC : 24 h et 72 h sont des durées exactes (un changement d'heure
// ne les décale pas) ; le mois calendaire est ajouté à la date-heure UTC.
func Deadlines(r Report, now time.Time) []Deadline {
	var early, notif, final *time.Time
	if r.AwareAt != nil {
		a := r.AwareAt.UTC()
		e, n := a.Add(24*time.Hour), a.Add(72*time.Hour)
		early, notif = &e, &n
	}
	switch r.Kind {
	case "incident":
		if r.NotificationAt != nil {
			f := addMonthClamped(r.NotificationAt.UTC())
			final = &f
		}
	default:
		if r.FixAvailableAt != nil {
			f := r.FixAvailableAt.UTC().AddDate(0, 0, 14)
			final = &f
		}
	}
	mk := func(step string, due, done *time.Time) Deadline {
		d := Deadline{Step: step, Due: due, Done: done}
		switch {
		case done != nil:
			d.Status = StatusDone
		case due == nil:
			d.Status = StatusUnknown
		case now.After(*due):
			d.Status = StatusOverdue
		default:
			d.Status = StatusDue
		}
		return d
	}
	return []Deadline{
		mk(StepEarlyWarning, early, r.EarlyWarningAt),
		mk(StepNotification, notif, r.NotificationAt),
		mk(StepFinal, final, r.FinalAt),
	}
}

// addMonthClamped ajoute un mois calendaire en ramenant le jour au dernier
// jour du mois cible (31 janvier donne fin février).
func addMonthClamped(t time.Time) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}
