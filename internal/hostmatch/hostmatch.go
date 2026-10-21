// Package hostmatch porte la correspondance d'hôtes partagée par la politique
// d'organisation et la liste blanche de fournisseurs : hôte exact ou joker
// strict "*.x.eu" (sous-domaines uniquement, jamais "x.eu").
package hostmatch

import (
	"errors"
	"net/url"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/profile"
)

// List est une liste blanche d'hôtes ; Local admet en plus les hôtes locaux.
type List struct {
	Name  string
	Hosts []string
	Local bool
}

// Allows indique si l'hôte est admis par la liste ; un hôte vide est refusé.
func (l List) Allows(host string) bool {
	if host == "" {
		return false
	}
	return Match(l.Hosts, host) || (l.Local && profile.IsLocalHost(host))
}

// HostOf renvoie l'hôte de l'URL en minuscules, sans port ; "" en cas d'erreur.
func HostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// CheckEntry valide une entrée de liste ; l'appelant enrobe l'erreur.
func CheckEntry(h string) error {
	if h == "" {
		return errors.New("empty host")
	}
	if strings.ContainsAny(h, "/:") {
		return errors.New("scheme, path or port not allowed")
	}
	rest := h
	if strings.HasPrefix(h, "*.") {
		rest = h[2:]
		if len(strings.Split(rest, ".")) < 2 {
			return errors.New("wildcard needs at least two labels")
		}
	}
	if strings.Contains(rest, "*") {
		return errors.New("wildcard only allowed as leading \"*.\"")
	}
	for _, l := range strings.Split(rest, ".") {
		if l == "" {
			return errors.New("empty label")
		}
	}
	return nil
}

// Match applique la règle exacte / joker strict.
func Match(entries []string, host string) bool {
	host = strings.ToLower(host)
	if host == "" {
		return false
	}
	for _, e := range entries {
		e = strings.ToLower(e)
		if rest, ok := strings.CutPrefix(e, "*."); ok {
			if strings.HasSuffix(host, "."+rest) {
				return true
			}
			continue
		}
		if host == e {
			return true
		}
	}
	return false
}
