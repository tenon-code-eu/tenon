// Package gitmark marque les commits qui contiennent du code écrit par
// l'agent : des trailers git (AI-Assisted-By, AI-Session) sont ajoutés au
// message par un hook prepare-commit-msg qui ne bloque jamais le commit.
package gitmark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/session"
)

// Timeout borne une exécution complète de Mark.
const Timeout = 5 * time.Second

// Write est un fichier écrit avec succès par l'agent (write_file ou
// edit_file) au cours d'une session.
type Write struct {
	Path, HashAfter, SessionID, Model, Provider, Country string
	TS                                                   time.Time
}

// AgentWrites renvoie les écritures réussies (ToolResult write_file ou
// edit_file, sans erreur, avec un chemin) de toutes les sessions du projet
// projectRoot : sessions ouvertes à sa racine ou dans un de ses
// sous-dossiers. Les sessions illisibles sont ignorées ; un dossier absent
// donne une liste vide.
func AgentWrites(sessionsDir, projectRoot string) ([]Write, error) {
	ents, err := os.ReadDir(sessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture du dossier des sessions : %w", err)
	}
	root := canonical(projectRoot)
	var out []Write
	for _, de := range ents {
		id, ok := strings.CutSuffix(de.Name(), ".jsonl")
		if !ok || de.IsDir() {
			continue
		}
		// Load valide l'identifiant ; une session endommagée garde ses
		// événements lisibles.
		evs, _, err := session.Load(sessionsDir, id)
		if err != nil {
			continue
		}
		out = append(out, sessionWrites(id, evs, root)...)
	}
	return out, nil
}

// sessionWrites extrait les écritures d'une session dont le premier
// SessionStarted désigne root ou un de ses sous-dossiers.
func sessionWrites(id string, evs []event.Event, root string) []Write {
	var start event.SessionStartedPayload
	started := false
	tools := map[string]string{} // call_id → outil
	var out []Write
	for _, e := range evs {
		switch e.Type {
		case event.SessionStarted:
			if started {
				continue
			}
			if json.Unmarshal(e.Payload, &start) != nil || start.ProjectRoot == "" || !within(canonical(start.ProjectRoot), root) {
				return nil
			}
			started = true
		case event.ToolCallRequested:
			var p event.ToolCallRequestedPayload
			if json.Unmarshal(e.Payload, &p) == nil {
				tools[p.CallID] = p.Tool
			}
		case event.ToolResult:
			if !started {
				return nil
			}
			var p event.ToolResultPayload
			if json.Unmarshal(e.Payload, &p) != nil || p.IsError || p.Path == "" {
				continue
			}
			tool := p.Tool
			if tool == "" {
				tool = tools[p.CallID]
			}
			if tool != "write_file" && tool != "edit_file" {
				continue
			}
			out = append(out, Write{
				Path: p.Path, HashAfter: p.HashAfter, SessionID: id,
				Model: start.Provider.Model, Provider: start.Provider.Name, Country: start.Provider.Country,
				TS: e.TS,
			})
		}
	}
	return out
}

// within indique si p est root ou un chemin sous root (chemins canoniques).
func within(p, root string) bool {
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// canonical nettoie p et résout ses liens symboliques. Un fichier absent
// (suppression indexée) garde le dossier parent résolu.
func canonical(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if d, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(d, filepath.Base(p))
	}
	return p
}

// Trailers renvoie les trailers des fichiers indexés staged (chemins relatifs
// à repoRoot) écrits par l'agent : une ligne AI-Assisted-By par triplet
// (modèle, fournisseur, pays) distinct, puis une ligne AI-Session par
// session distincte, chaque groupe trié. Aucune correspondance : nil.
func Trailers(staged []string, repoRoot string, writes []Write) []string {
	if len(staged) == 0 || len(writes) == 0 {
		return nil
	}
	root := canonical(repoRoot)
	byPath := map[string][]Write{}
	for _, w := range writes {
		k := canonical(w.Path)
		byPath[k] = append(byPath[k], w)
	}
	assisted, sessions := map[string]bool{}, map[string]bool{}
	for _, s := range staged {
		for _, w := range byPath[canonical(filepath.Join(root, s))] {
			country := sanitize(w.Country)
			if country == "" {
				country = "—"
			}
			assisted[fmt.Sprintf("AI-Assisted-By: tenon (%s, %s, %s)", sanitize(w.Model), sanitize(w.Provider), country)] = true
			if id := sanitize(w.SessionID); id != "" {
				sessions["AI-Session: "+id] = true
			}
		}
	}
	if len(assisted) == 0 {
		return nil
	}
	a, s := sortedKeys(assisted), sortedKeys(sessions)
	return append(a, s...)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// sanitize retire d'une valeur de trailer les retours à la ligne, les
// caractères de contrôle et de format, les espaces de bord et les « : » de
// tête : elle ne peut ni injecter une ligne ni changer de clé.
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, ":")
	return strings.TrimSpace(s)
}

// Mark ajoute au fichier msgFile les trailers des fichiers indexés du dépôt
// contenant dir écrits par l'agent (sessions de sessionsDir). Sans
// correspondance, le message n'est pas modifié. L'exécution est bornée par
// Timeout.
func Mark(msgFile, dir, sessionsDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	msg, err := filepath.Abs(msgFile)
	if err != nil {
		return err
	}
	root, err := toplevel(ctx, dir)
	if err != nil {
		return err
	}
	out, err := git(ctx, root, "-c", "core.quotepath=off", "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return err
	}
	var staged []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			staged = append(staged, p)
		}
	}
	if len(staged) == 0 {
		return nil
	}
	writes, err := AgentWrites(sessionsDir, root)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	trailers := Trailers(staged, root, writes)
	if len(trailers) == 0 {
		return nil
	}
	args := []string{"interpret-trailers", "--in-place", "--if-exists", "addIfDifferent"}
	for _, t := range trailers {
		args = append(args, "--trailer", t)
	}
	args = append(args, "--", msg)
	_, err = git(ctx, root, args...)
	return err
}

// git exécute git dans dir (argv explicite, aucun shell) et renvoie sa
// sortie standard.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s : %w", args[0], ctx.Err())
		}
		return nil, fmt.Errorf("git %s : %w : %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// toplevel renvoie la racine de l'arbre de travail du dépôt contenant dir.
func toplevel(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSuffix(string(out), "\n")
	if root == "" {
		return "", errors.New("racine du dépôt git introuvable")
	}
	return root, nil
}
