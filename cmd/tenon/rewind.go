package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/checkpoint"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/session"
)

// hashPrefix précède l'empreinte dans hash_before ; le magasin n'utilise que
// l'hexadécimal.
const hashPrefix = "sha256:"

// keepHashes rassemble les hash_before des sessions modifiées dans les
// retentionDays derniers jours (un fichier illisible est ignoré). Une copie
// dédupliquée garde son ancien mtime : elle doit rester protégée tant qu'une
// session récente la référence.
func keepHashes(sdir string, retentionDays int, now time.Time) map[string]bool {
	keep := map[string]bool{}
	ents, err := os.ReadDir(sdir)
	if err != nil {
		return keep
	}
	limit := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	for _, de := range ents {
		name := de.Name()
		id, ok := strings.CutSuffix(name, ".jsonl")
		if !ok || de.IsDir() {
			continue
		}
		info, err := de.Info()
		if err != nil || info.ModTime().Before(limit) {
			continue
		}
		events, _, err := session.Load(sdir, id)
		if err != nil {
			continue
		}
		for _, ev := range events {
			if ev.Type != event.ToolResult {
				continue
			}
			var p event.ToolResultPayload
			if json.Unmarshal(ev.Payload, &p) != nil {
				continue
			}
			if h := strings.TrimPrefix(p.HashBefore, hashPrefix); h != "" {
				keep[h] = true
			}
		}
	}
	return keep
}

// openCheckpoints ouvre le magasin et le nettoie. Renvoie le magasin (nil si
// désactivé ou en échec) et les avis à afficher.
func openCheckpoints(lang i18n.Lang, getenv func(string) string, retentionDays int) (*checkpoint.Store, []string) {
	if retentionDays <= 0 {
		return nil, []string{i18n.T(lang, "checkpoints.disabled")}
	}
	store, err := checkpoint.Open(checkpoint.Dir(getenv))
	if err != nil {
		return nil, []string{i18n.T(lang, "checkpoints.prune_failed", err)}
	}
	now := time.Now()
	keep := keepHashes(sessionsDir(getenv), retentionDays, now)
	if _, err := store.Prune(time.Duration(retentionDays)*24*time.Hour, keep, now); err != nil {
		return store, []string{i18n.T(lang, "checkpoints.prune_failed", err)}
	}
	return store, nil
}

// listRewind affiche « tenon rewind --list » : les points de la session,
// le plus récent d'abord, numérotés depuis le plus ancien. Lecture seule :
// ni verrou, ni writer.
func listRewind(lang i18n.Lang, resume string, stdout, stderr io.Writer, getenv func(string) string, wd string) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	sdir := sessionsDir(getenv)
	sid := resume
	if resume == "last" {
		var err error
		if sid, err = session.Latest(sdir, wd); err != nil {
			if errors.Is(err, session.ErrNoSession) {
				return fail(errors.New(i18n.T(lang, "err.no_session_project")))
			}
			return fail(err)
		}
	}
	if err := session.Exists(sdir, sid); err != nil {
		switch {
		case errors.Is(err, session.ErrInvalidID):
			return fail(errors.New(i18n.T(lang, "err.invalid_session_id", sid)))
		case errors.Is(err, session.ErrNoSession):
			return fail(errors.New(i18n.T(lang, "err.no_session", sid)))
		}
		return fail(fmt.Errorf("session %s : %w", sid, err))
	}
	events, _, err := session.Load(sdir, sid)
	if err != nil {
		return fail(fmt.Errorf("session %s : %w", sid, err))
	}
	if !sameProject(events, wd) {
		return fail(errors.New(i18n.T(lang, "err.no_session", sid)))
	}
	// Moteur jetable : Restore n'a besoin d'aucun fournisseur.
	eng := engine.New(engine.Deps{Lang: lang, ProjectRoot: wd})
	if err := eng.Restore(sid, events); err != nil {
		return fail(fmt.Errorf("session %s : %w", sid, err))
	}
	pts := eng.RewindPoints(sid)
	for i, p := range pts {
		line := fmt.Sprintf("#%d  %d  %s", len(pts)-i, p.Seq, clean(p.Excerpt))
		if !p.Conversation {
			line += " " + i18n.T(lang, "rewind.code_only")
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}
