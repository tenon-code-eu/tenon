package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/event"
)

// Summary describes a stored session.
type Summary struct {
	ID, ProjectRoot, FirstPrompt string
	Updated                      time.Time
	Turns                        int
}

const promptMax = 80

// List returns the sessions of projectRoot, most recent first. Unreadable
// sessions are skipped. A missing dir gives an empty list.
func List(dir, projectRoot string) ([]Summary, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}
	var out []Summary
	for _, de := range ents {
		id, ok := strings.CutSuffix(de.Name(), ".jsonl")
		if !ok || de.IsDir() || validID(id) != nil {
			continue
		}
		evs, _, err := Load(dir, id)
		if err != nil {
			continue
		}
		s := Summary{ID: id}
		started := false
		for _, e := range evs {
			switch e.Type {
			case event.SessionStarted:
				if !started {
					var p event.SessionStartedPayload
					if json.Unmarshal(e.Payload, &p) == nil {
						s.ProjectRoot = p.ProjectRoot
						started = true
					}
				}
			case event.UserMessage:
				if s.FirstPrompt == "" {
					var p event.UserMessagePayload
					// Un message bloqué ou produit par un hook n'est pas ce que
					// l'utilisateur a demandé : il ne résume pas la session.
					if json.Unmarshal(e.Payload, &p) == nil && !p.Blocked && p.Hook == "" {
						// Caractères de contrôle et de format (U+202E,
						// U+200B…) : ils pourraient tromper le terminal.
						// Ce que l'utilisateur a tapé prime sur le texte
						// développé (skill) envoyé au modèle.
						first := p.Text
						if p.Typed != "" {
							first = p.Typed
						}
						r := []rune(strings.Map(func(c rune) rune {
							if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
								return ' '
							}
							return c
						}, first))
						if len(r) > promptMax {
							r = r[:promptMax]
						}
						s.FirstPrompt = string(r)
					}
				}
			case event.TurnCompleted:
				s.Turns++
			}
		}
		if !started || !SameRoot(s.ProjectRoot, projectRoot) {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, de.Name()))
		if err != nil {
			continue
		}
		s.Updated = info.ModTime()
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// SameRoot reports whether two project roots designate the same directory:
// paths are cleaned and symlinks resolved when possible. An empty root never
// matches.
func SameRoot(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return canonical(a) == canonical(b)
}

func canonical(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Latest returns the ID of the most recent session of projectRoot.
func Latest(dir, projectRoot string) (string, error) {
	l, err := List(dir, projectRoot)
	if err != nil {
		return "", err
	}
	if len(l) == 0 {
		return "", ErrNoSession
	}
	return l[0].ID, nil
}
