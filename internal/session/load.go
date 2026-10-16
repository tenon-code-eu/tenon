package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tenon-code-eu/tenon/internal/event"
)

// Load reads a session. It stops at the first unreadable line, returning the
// valid events so far and corrupt=true. A missing file gives ErrNoSession.
func Load(dir, sessionID string) (events []event.Event, corrupt bool, err error) {
	if err := validID(sessionID); err != nil {
		return nil, false, err
	}
	f, err := os.Open(filepath.Join(dir, sessionID+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, ErrNoSession
	}
	if err != nil {
		return nil, false, fmt.Errorf("opening session %s: %w", sessionID, err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			var e event.Event
			if json.Unmarshal(t, &e) != nil {
				return events, true, nil
			}
			events = append(events, e)
		}
		if rerr == io.EOF {
			return events, false, nil
		}
		if rerr != nil {
			return events, false, fmt.Errorf("reading session %s: %w", sessionID, rerr)
		}
	}
}
