// Package policy contient le moteur de permissions : chaque appel d'outil y
// est vérifié avant exécution.
package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"codeberg.org/tenon/tenon/internal/tools"
)

// Decision est le verdict du moteur pour un appel.
type Decision int

const (
	Allow Decision = iota
	Ask
	Deny
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	}
	return "unknown"
}

// ParseDecision accepte exactement "allow", "ask" ou "deny".
func ParseDecision(s string) (Decision, error) {
	switch s {
	case "allow":
		return Allow, nil
	case "ask":
		return Ask, nil
	case "deny":
		return Deny, nil
	}
	return Ask, fmt.Errorf("décision inconnue %q (attendu : allow, ask ou deny)", s)
}

// Rule associe un outil ("*" = tous) et un motif à une décision.
type Rule struct {
	Tool, Match string
	Decision    Decision
}

// Call décrit un appel d'outil à vérifier.
type Call struct {
	Tool   string
	Risk   tools.Risk
	Target tools.Target
}

// Permissions évalue les appels selon les règles et le confinement au projet.
type Permissions struct {
	root    string // racine résolue (EvalSymlinks)
	rawRoot string // racine telle que fournie, nettoyée
	rules   []Rule
}

// NewPermissions résout root via EvalSymlinks.
func NewPermissions(root string, rules []Rule) (*Permissions, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("racine du projet : %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("racine du projet : %w", err)
	}
	return &Permissions{root: resolved, rawRoot: abs, rules: append([]Rule(nil), rules...)}, nil
}

type pathInfo struct {
	given    string // chemin absolu nettoyé
	resolved string // chemin après résolution des liens
	inside   bool   // false si hors racine ou résolution impossible
}

// Check applique l'algorithme : deny, confinement/fichiers sensibles, allow, ask, défaut.
func (p *Permissions) Check(c Call) Decision {
	infos := make([]pathInfo, len(c.Target.Paths))
	for i, raw := range c.Target.Paths {
		infos[i] = p.inspect(raw)
	}

	// matches : deny (fold=true) compare sans casse et sur toutes les formes ;
	// allow/ask comparent la forme résolue, sensible à la casse. Deny et ask
	// correspondent si UN chemin correspond ; allow exige TOUS les chemins.
	matches := func(r Rule, fold bool) bool {
		if r.Tool != "*" && r.Tool != c.Tool {
			return false
		}
		if r.Match == "" {
			return true
		}
		if c.Target.Command != "" {
			if r.Decision == Allow && chainsBeyond(r.Match, c.Target.Command) {
				return false // allow ne couvre jamais une commande enchaînée
			}
			return matchWildcard(r.Match, c.Target.Command)
		}
		pat := r.Match
		if fold {
			pat = strings.ToLower(pat)
		}
		pathMatches := func(in pathInfo) bool {
			for _, cand := range p.candidates(in, fold) {
				if fold {
					cand = strings.ToLower(cand)
				}
				if matchWildcard(pat, cand) {
					return true
				}
			}
			return false
		}
		if fold || r.Decision == Ask { // deny et ask : UN chemin suffit ; allow : TOUS
			for _, in := range infos {
				if pathMatches(in) {
					return true
				}
			}
			return false
		}
		if len(infos) == 0 {
			return false
		}
		for _, in := range infos {
			if !pathMatches(in) {
				return false
			}
		}
		return true
	}

	// deny : aussi sur la forme lexicale, pour ne pas être contourné par un lien.
	for _, r := range p.rules {
		if r.Decision == Deny && matches(r, true) {
			return Deny
		}
	}

	for _, in := range infos {
		if !in.inside || isSensitive(in.given) || isSensitive(in.resolved) {
			return Ask
		}
	}

	for _, r := range p.rules {
		if r.Decision == Allow && matches(r, false) {
			return Allow
		}
	}
	for _, r := range p.rules {
		if r.Decision == Ask && matches(r, false) {
			return Ask
		}
	}

	if c.Risk == tools.Read {
		return Allow
	}
	return Ask
}

func (p *Permissions) inspect(raw string) pathInfo {
	given := raw
	if !filepath.IsAbs(given) {
		given = filepath.Join(p.root, given)
	}
	given = filepath.Clean(given)
	resolved, ok := resolvePath(given)
	if !ok {
		return pathInfo{given: given, resolved: given}
	}
	_, in := relInside(p.root, resolved)
	return pathInfo{given: given, resolved: resolved, inside: in}
}

// candidates renvoie les formes comparées aux motifs : relative à la racine si
// dans le projet, absolue sinon.
func (p *Permissions) candidates(in pathInfo, includeLexical bool) []string {
	form := func(root, path string) string {
		if rel, ok := relInside(root, path); ok {
			return rel
		}
		return path
	}
	out := []string{form(p.root, in.resolved)}
	if includeLexical {
		out = append(out, form(p.root, in.given), form(p.rawRoot, in.given))
	}
	return out
}

func relInside(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// resolvePath résout les liens du plus long préfixe existant puis rattache le
// reste. ok vaut false si la résolution échoue (lien pendant, boucle, droits) :
// l'appelant doit alors échouer fermé.
func resolvePath(abs string) (string, bool) {
	prefix, rest := abs, ""
	for {
		_, err := os.Lstat(prefix)
		if err == nil {
			real, err := filepath.EvalSymlinks(prefix)
			if err != nil {
				return "", false
			}
			return filepath.Join(real, rest), true
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", false
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			return "", false
		}
		rest = filepath.Join(filepath.Base(prefix), rest)
		prefix = parent
	}
}

// shellMeta sont les opérateurs qui enchaînent, substituent ou redirigent.
var shellMeta = []string{";", "&", "|", "`", "$(", ">", "<", "\n"}

// chainsBeyond rapporte si cmd contient un opérateur shell absent littéralement
// du motif : `go test *` ne doit pas autoriser `go test ./... ; touch x`.
func chainsBeyond(pattern, cmd string) bool {
	for _, m := range shellMeta {
		if strings.Contains(cmd, m) && !strings.Contains(pattern, m) {
			return true
		}
	}
	return false
}

func isSensitive(path string) bool {
	b := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(b, ".env") || strings.HasSuffix(b, ".pem") || strings.HasPrefix(b, "id_rsa")
}
