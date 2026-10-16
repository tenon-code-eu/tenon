package gitmark

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HookScript est le hook commit-msg installé par InstallHook. Il s'exécute
// après l'éditeur : un message laissé vide reste vide (Mark n'y touche pas)
// et le commit est abandonné. Sans tenon ou en cas d'erreur, il sort avec 0
// sans toucher au message.
const HookScript = `#!/bin/sh
# tenon : marquage du code produit par l'agent (ne bloque jamais le commit)
command -v tenon >/dev/null 2>&1 || exit 0
tenon git mark "$1" >/dev/null 2>&1 || true
exit 0
`

// legacyHookScript est l'ancien hook prepare-commit-msg de tenon, retiré par
// InstallHook : avant l'éditeur, il rendait non vide un message abandonné.
const legacyHookScript = `#!/bin/sh
# tenon : marquage du code produit par l'agent (ne bloque jamais le commit)
case "$2" in merge) exit 0 ;; esac
command -v tenon >/dev/null 2>&1 || exit 0
tenon git mark "$1" >/dev/null 2>&1 || true
exit 0
`

// hookName est le hook installé ; legacyHookName celui des versions
// précédentes.
const (
	hookName       = "commit-msg"
	legacyHookName = "prepare-commit-msg"
)

// ErrHookExists signale un hook commit-msg différent déjà installé.
var ErrHookExists = errors.New("a commit-msg hook already exists")

// InstallHook installe HookScript (mode 0755) comme hook commit-msg
// dans le dossier de hooks effectif du dépôt contenant repoRoot (core.hooksPath
// respecté) et renvoie son chemin. Un hook identique est laissé tel quel ; un
// hook différent donne ErrHookExists, sauf avec force : il est alors renommé
// en commit-msg.bak (un .bak précédent est écrasé). Un ancien hook
// prepare-commit-msg de tenon (identique à legacyHookScript) est supprimé.
func InstallHook(repoRoot string, force bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	// --git-path et un core.hooksPath relatif s'entendent depuis la racine
	// de l'arbre de travail.
	top, err := toplevel(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	out, err := git(ctx, top, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSuffix(string(out), "\n")
	if dir == "" {
		return "", errors.New("git hooks directory not found")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(top, dir)
	}
	path := filepath.Join(dir, hookName)

	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, []byte(HookScript)) {
		return path, removeLegacy(dir)
	}
	// Tout autre fichier (ou lien, même cassé) occupe la place.
	if _, err := os.Lstat(path); err == nil {
		if !force {
			return path, fmt.Errorf("%w (%s)", ErrHookExists, path)
		}
		if err := os.Rename(path, path+".bak"); err != nil {
			return path, fmt.Errorf("backing up existing hook: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return path, fmt.Errorf("reading existing hook: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path, fmt.Errorf("creating hooks directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return path, fmt.Errorf("writing hook: %w", err)
	}
	_, werr := f.WriteString(HookScript)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(path)
		return path, fmt.Errorf("writing hook: %w", err)
	}
	// Le umask a pu retirer des droits.
	if err := os.Chmod(path, 0o755); err != nil {
		return path, fmt.Errorf("hook permissions: %w", err)
	}
	return path, removeLegacy(dir)
}

// removeLegacy supprime l'ancien hook prepare-commit-msg de tenon ; un autre
// hook prepare-commit-msg est conservé.
func removeLegacy(dir string) error {
	p := filepath.Join(dir, legacyHookName)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	if cur, err := os.ReadFile(p); err != nil || !bytes.Equal(cur, []byte(legacyHookScript)) {
		return nil
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("removing legacy hook %s: %w", legacyHookName, err)
	}
	return nil
}
