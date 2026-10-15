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

// HookScript est le hook prepare-commit-msg installé par InstallHook : sans
// tenon, pour un merge ou en cas d'erreur, il sort avec 0 sans toucher au
// message.
const HookScript = `#!/bin/sh
# tenon : marquage du code produit par l'agent (ne bloque jamais le commit)
case "$2" in merge) exit 0 ;; esac
command -v tenon >/dev/null 2>&1 || exit 0
tenon git mark "$1" >/dev/null 2>&1 || true
exit 0
`

// ErrHookExists signale un hook prepare-commit-msg différent déjà installé.
var ErrHookExists = errors.New("un hook prepare-commit-msg existe déjà")

// InstallHook installe HookScript (mode 0755) comme hook prepare-commit-msg
// dans le dossier de hooks effectif du dépôt contenant repoRoot (core.hooksPath
// respecté) et renvoie son chemin. Un hook identique est laissé tel quel ; un
// hook différent donne ErrHookExists, sauf avec force : il est alors renommé
// en prepare-commit-msg.bak (un .bak précédent est écrasé).
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
		return "", errors.New("dossier des hooks git introuvable")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(top, dir)
	}
	path := filepath.Join(dir, "prepare-commit-msg")

	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, []byte(HookScript)) {
		return path, nil
	}
	// Tout autre fichier (ou lien, même cassé) occupe la place.
	if _, err := os.Lstat(path); err == nil {
		if !force {
			return path, fmt.Errorf("%w (%s)", ErrHookExists, path)
		}
		if err := os.Rename(path, path+".bak"); err != nil {
			return path, fmt.Errorf("sauvegarde du hook existant : %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return path, fmt.Errorf("lecture du hook existant : %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path, fmt.Errorf("création du dossier des hooks : %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return path, fmt.Errorf("écriture du hook : %w", err)
	}
	_, werr := f.WriteString(HookScript)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(path)
		return path, fmt.Errorf("écriture du hook : %w", err)
	}
	// Le umask a pu retirer des droits.
	if err := os.Chmod(path, 0o755); err != nil {
		return path, fmt.Errorf("droits du hook : %w", err)
	}
	return path, nil
}
