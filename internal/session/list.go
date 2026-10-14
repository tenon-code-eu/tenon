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

	"codeberg.org/tenon/tenon/internal/event"
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
		return nil, fmt.Errorf("lecture du dossier des sessions : %w", err)
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
					if json.Unmarshal(e.Payload, &p) == nil {
						r := []rune(p.Text)
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
		if !started || s.ProjectRoot != projectRoot {
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
