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
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/session"
)

// loadSession lit une session ; remplacée dans les tests.
var loadSession = session.Load

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
// sous-dossiers. Les sessions illisibles et les entrées qui ne sont pas des
// fichiers réguliers (FIFO, lien, dossier…) sont ignorées ; un dossier absent
// donne une liste vide.
func AgentWrites(sessionsDir, projectRoot string) ([]Write, error) {
	return agentWrites(context.Background(), sessionsDir, projectRoot)
}

// agentWrites est AgentWrites, interrompu entre deux sessions quand ctx
// expire.
func agentWrites(ctx context.Context, sessionsDir, projectRoot string) ([]Write, error) {
	ents, err := os.ReadDir(sessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture du dossier des sessions : %w", err)
	}
	root := canonical(projectRoot)
	load := loadSession
	var out []Write
	for _, de := range ents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, ok := strings.CutSuffix(de.Name(), ".jsonl")
		// Une FIFO bloquerait l'ouverture : fichiers réguliers seulement
		// (type lu par ReadDir, sans suivre les liens).
		if !ok || !de.Type().IsRegular() {
			continue
		}
		// Load valide l'identifiant ; une session endommagée garde ses
		// événements lisibles.
		evs, _, err := load(sessionsDir, id)
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
// correspondance, ou si le message est vide (seulement des commentaires, des
// lignes vides et des trailers : le commit doit rester abandonné), le message
// n'est pas modifié. L'exécution est bornée par Timeout.
func Mark(msgFile, dir, sessionsDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	return MarkContext(ctx, msgFile, dir, sessionsDir)
}

// MarkContext est Mark borné par ctx : une fois ctx expiré, il rend la main
// sans attendre une lecture de session bloquée, et le message n'est plus
// modifié.
func MarkContext(ctx context.Context, msgFile, dir, sessionsDir string) error {
	msg, err := filepath.Abs(msgFile)
	if err != nil {
		return err
	}
	root, err := toplevel(ctx, dir)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(msg)
	if err != nil {
		return err
	}
	if isEmptyMessage(string(content), commentPrefixes(ctx, root)) {
		return nil
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

	// La lecture des sessions (système de fichiers) ne prend pas de
	// contexte : elle est abandonnée, et non attendue, quand ctx expire.
	type result struct {
		writes []Write
		err    error
	}
	done := make(chan result, 1)
	go func() {
		w, err := agentWrites(ctx, sessionsDir, root)
		done <- result{w, err}
	}()
	var writes []Write
	select {
	case r := <-done:
		if r.err != nil {
			return r.err
		}
		writes = r.writes
	case <-ctx.Done():
		return ctx.Err()
	}

	trailers := Trailers(staged, root, writes)
	if len(trailers) == 0 {
		return nil
	}
	// La configuration trailer.* du dépôt est neutralisée : séparateur « : »,
	// trailers en fin de message.
	args := []string{
		"-c", "trailer.separators=:", "-c", "trailer.where=end", "-c", "core.quotepath=off",
		"interpret-trailers", "--in-place", "--where", "end", "--if-exists", "addIfDifferent", "--if-missing", "add",
	}
	for _, t := range trailers {
		args = append(args, "--trailer", t)
	}
	args = append(args, "--", msg)
	// Dernier point de décision : après expiration, aucune écriture. git
	// interpret-trailers --in-place remplace le fichier d'un bloc (fichier
	// temporaire renommé) : un git tué ne laisse pas de message à moitié écrit.
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = git(ctx, root, args...)
	return err
}

// scissors est la ligne de coupe de « git commit -v » (après le préfixe de
// commentaire) : tout ce qui suit est ignoré.
const scissors = " ------------------------ >8 ------------------------"

// reTrailerLine reconnaît une ligne « Clé: valeur » de trailer.
var reTrailerLine = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*:`)

// isEmptyMessage indique si git abandonnerait le commit de message msg (des
// trailers ajoutés l'en empêcheraient). Les commentaires (lignes commençant
// par un des prefixes) et tout ce qui suit la ligne de coupe sont ignorés.
// Comme pour git, le sujet (première ligne) n'est jamais un trailer : le
// message est vide s'il n'a aucune ligne non blanche, ou si son sujet est
// vide et que ses seules lignes sont des trailers (« git commit -s » quitté
// sans rien écrire).
func isEmptyMessage(msg string, prefixes []string) bool {
	var lines []string
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimRight(line, "\r")
		comment, cut := false, false
		for _, p := range prefixes {
			if strings.HasPrefix(line, p) {
				comment, cut = true, line == p+scissors
				break
			}
		}
		if cut {
			break
		}
		if !comment {
			lines = append(lines, line)
		}
	}
	content := false
	onlyTrailers := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		content = true
		if !reTrailerLine.MatchString(l) {
			onlyTrailers = false
		}
	}
	if !content {
		return true
	}
	subjectEmpty := strings.TrimSpace(lines[0]) == ""
	return subjectEmpty && onlyTrailers
}

// autoCommentChars sont les caractères que git peut choisir avec
// core.commentChar=auto.
const autoCommentChars = "#;@!$%^&|:"

// commentPrefixes renvoie les préfixes de commentaire du dépôt :
// core.commentString, sinon core.commentChar, sinon « # ». Avec « auto »,
// tous les caractères possibles sont retenus.
func commentPrefixes(ctx context.Context, root string) []string {
	v := "#"
	for _, key := range []string{"core.commentChar", "core.commentString"} {
		if out, err := git(ctx, root, "config", "--get", key); err == nil {
			if s := strings.TrimSuffix(string(out), "\n"); s != "" {
				v = s
			}
		}
	}
	if v == "auto" {
		out := make([]string, 0, len(autoCommentChars))
		for _, c := range autoCommentChars {
			out = append(out, string(c))
		}
		return out
	}
	return []string{v}
}

// git exécute git dans dir (argv explicite, aucun shell) et renvoie sa
// sortie standard.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Un descendant qui garde les tubes ouverts ne retient pas Wait.
	cmd.WaitDelay = time.Second
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
