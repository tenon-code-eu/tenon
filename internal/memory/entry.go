// Package memory range les entrées de mémoire gouvernée : un fichier
// Markdown à frontmatter par entrée, dans trois portées (project, user,
// global).
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/frontmatter"
)

// Scope est la portée d'une entrée.
type Scope string

const (
	Project Scope = "project"
	User    Scope = "user"
	Global  Scope = "global"
)

const (
	maxBody    = 4096
	maxTitle   = 80
	maxEntries = 200
	// maxFileBytes borne la lecture d'un fichier : corps + frontmatter.
	maxFileBytes = maxBody + 8192
	maxFMLines   = 20
)

var (
	// ErrNotFound : aucune entrée à cet id.
	ErrNotFound = errors.New("memory: entry not found")
	// ErrInvalid : entrée ou id invalide ; l'erreur enveloppée porte la raison.
	ErrInvalid = errors.New("memory: invalid entry")
	// ErrTooLarge : corps au-delà de 4096 octets.
	ErrTooLarge = errors.New("memory: entry too large")
	// ErrFull : portée à 200 entrées.
	ErrFull = errors.New("memory: scope full")
)

type tooLargeError struct{ n int }

func (e tooLargeError) Error() string {
	return fmt.Sprintf("memory: entry too large (%d bytes, max %d)", e.n, maxBody)
}
func (e tooLargeError) Is(t error) bool { return t == ErrTooLarge }

type fullError struct{ scope Scope }

func (e fullError) Error() string {
	return fmt.Sprintf("memory: %s holds %d entries; forget some first", e.scope, maxEntries)
}
func (e fullError) Is(t error) bool { return t == ErrFull }

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ParseScope convertit un texte en portée.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case Project, User, Global:
		return Scope(s), nil
	}
	return "", invalid("unknown scope %q (project, user or global)", s)
}

// Entry est une entrée de mémoire. SHA256 est l'empreinte hexadécimale des
// octets du fichier, remplie à la lecture et à l'écriture.
type Entry struct {
	Scope     Scope
	ID        string
	Title     string
	Body      string
	Author    string
	Sensitive bool
	Created   time.Time
	SHA256    string
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

func validID(id string) error {
	if !idRe.MatchString(id) {
		return invalid("id %q must match [a-z0-9][a-z0-9-]{0,47}", id)
	}
	return nil
}

// validate vérifie les champs de l'entrée.
func (e Entry) validate() error {
	if err := validID(e.ID); err != nil {
		return err
	}
	if n := utf8.RuneCountInString(e.Title); n < 1 || n > maxTitle {
		return invalid("title must be 1 to %d characters", maxTitle)
	}
	if strings.ContainsAny(e.Title, "\n\r") {
		return invalid("title must be one line")
	}
	if e.Author != "model" && e.Author != "user" {
		return invalid("author must be model or user")
	}
	if len(e.Body) > maxBody {
		return tooLargeError{len(e.Body)}
	}
	return nil
}

// plainTitle : un titre sans guillemets se relit à l'identique tel quel.
var plainTitle = regexp.MustCompile(`^[A-Za-z0-9À-ÿ][^#"'\\]*[^\s#"'\\]$|^[A-Za-z0-9À-ÿ]$`)

func quoteTitle(t string) string {
	if plainTitle.MatchString(t) && !strings.Contains(t, " #") && !strings.HasSuffix(t, ":") {
		return t
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\t", `\t`).Replace(t) + `"`
}

// encodeEntry valide l'entrée et produit les octets du fichier.
func encodeEntry(e Entry) ([]byte, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", e.ID)
	fmt.Fprintf(&b, "title: %s\n", quoteTitle(e.Title))
	fmt.Fprintf(&b, "sensitive: %t\n", e.Sensitive)
	fmt.Fprintf(&b, "author: %s\n", e.Author)
	fmt.Fprintf(&b, "created: %s\n", e.Created.UTC().Format(time.RFC3339))
	b.WriteString("---\n")
	b.WriteString(e.Body)
	return []byte(b.String()), nil
}

// parseEntry relit un fichier ; id est le nom du fichier sans .md.
func parseEntry(scope Scope, id string, data []byte) (Entry, error) {
	f, body, err := frontmatter.ParseAny(data, 4096, maxFMLines)
	if err != nil {
		return Entry{}, invalid("%v", err)
	}
	created, err := time.Parse(time.RFC3339, f.String("created"))
	if err != nil {
		return Entry{}, invalid("created is not RFC 3339")
	}
	sum := sha256.Sum256(data)
	e := Entry{
		Scope: scope, ID: f.String("id"), Title: f.String("title"), Body: body,
		Author: f.String("author"), Sensitive: f.Bool("sensitive"),
		Created: created.UTC(), SHA256: hex.EncodeToString(sum[:]),
	}
	if e.ID != id {
		return Entry{}, invalid("id %q differs from file name %q", e.ID, id)
	}
	if err := e.validate(); err != nil {
		return Entry{}, err
	}
	return e, nil
}
