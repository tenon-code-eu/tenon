package orgpolicy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// SystemDir renvoie le répertoire système des politiques.
func SystemDir() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/tenon"
	}
	return "/etc/tenon"
}

// Resolve détermine le répertoire de politique : le répertoire système s'il
// contient policy.toml (system = true), sinon TENON_POLICY_DIR.
func Resolve(getenv func(string) string) (dir string, system bool) {
	return resolve(SystemDir(), getenv)
}

func resolve(systemDir string, getenv func(string) string) (string, bool) {
	// Toute erreur autre que « absent » compte comme présent : on ne laisse
	// pas un état illisible ouvrir la voie à TENON_POLICY_DIR.
	if _, err := os.Stat(filepath.Join(systemDir, "policy.toml")); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return systemDir, true
	}
	if d := getenv("TENON_POLICY_DIR"); d != "" {
		return d, false
	}
	return "", false
}

// fail enveloppe une erreur avec le chemin fautif et ErrPolicy.
func fail(path string, err error) error {
	if errors.Is(err, ErrPolicy) {
		return fmt.Errorf("%s: %w", path, err)
	}
	return fmt.Errorf("%s: %w: %w", path, ErrPolicy, err)
}

// Load lit, vérifie et décode la politique de dir. Renvoie nil, nil si dir
// est vide ou si policy.toml est absent.
func Load(dir string, checkPermsOn bool) (*Policy, error) {
	if dir == "" {
		return nil, nil
	}
	tomlPath := filepath.Join(dir, "policy.toml")
	if _, err := os.Stat(tomlPath); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	pubPath := filepath.Join(dir, "policy.pub")
	sigPath := filepath.Join(dir, "policy.toml.minisig")
	if checkPermsOn {
		for _, p := range []string{dir, tomlPath, pubPath, sigPath} {
			if err := checkPerms(p); err != nil {
				return nil, err
			}
		}
	}
	msg, err := os.ReadFile(tomlPath)
	if err != nil {
		return nil, fail(tomlPath, err)
	}
	pubRaw, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, fail(pubPath, err)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return nil, fail(sigPath, err)
	}
	pk, err := ParsePublicKey(pubRaw)
	if err != nil {
		return nil, fail(pubPath, err)
	}
	if _, err := Verify(pk, msg, sig); err != nil {
		return nil, fail(tomlPath, err)
	}
	p, err := Parse(msg)
	if err != nil {
		return nil, fail(tomlPath, err)
	}
	p.KeyID = pk.ID()
	return &p, nil
}

// checkPerms refuse un chemin inscriptible par groupe/autres ou n'appartenant
// pas à root (uid 0). Échoue fermé si le propriétaire est indéterminable.
func checkPerms(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fail(path, err)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fail(path, errors.New("writable by group or others"))
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fail(path, errors.New("cannot determine owner"))
	}
	if st.Uid != 0 {
		return fail(path, errors.New("not owned by root"))
	}
	return nil
}
