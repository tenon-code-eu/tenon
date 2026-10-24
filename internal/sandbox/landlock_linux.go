//go:build linux

package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Droits Landlock applicables à un fichier (les autres ne valent que pour
// un dossier : Landlock rejette une règle fichier qui les contient).
const fileRights = unix.LANDLOCK_ACCESS_FS_EXECUTE |
	unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_TRUNCATE |
	unix.LANDLOCK_ACCESS_FS_IOCTL_DEV |
	unix.LANDLOCK_ACCESS_FS_RESOLVE_UNIX

// Droits de lecture accordés partout hors interdits. RESOLVE_UNIX en est
// exclu : une socket Unix n'est joignable que dans une zone inscriptible.
const readRights = unix.LANDLOCK_ACCESS_FS_EXECUTE |
	unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_DIR

// Droits réseau gérés en repli TCP (ABI ≥ 4) : sans règle de port, tout
// connect et bind TCP est refusé.
const tcpRights = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP

// rulesetAttr reprend les trois premiers champs de struct
// landlock_ruleset_attr ; la taille passée au noyau s'adapte à l'ABI.
type rulesetAttr struct {
	fs     uint64
	net    uint64
	scoped uint64
}

// landlockABI renvoie la version d'ABI Landlock du noyau, ou une erreur
// quand Landlock est absent (noyau trop ancien, LSM non chargé, seccomp).
func landlockABI() (int, error) {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	return int(v), nil
}

// fsRights renvoie tous les droits fichiers gérés par l'ABI donnée.
func fsRights(abi int) uint64 {
	// ABI 1 : de EXECUTE (bit 0) à MAKE_SYM (bit 12).
	r := uint64(unix.LANDLOCK_ACCESS_FS_MAKE_SYM<<1 - 1)
	if abi >= 2 {
		r |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		r |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	if abi >= 9 {
		r |= unix.LANDLOCK_ACCESS_FS_RESOLVE_UNIX
	}
	return r
}

// helperMain applique la Spec puis remplace le processus par argv. Tout
// échec écrit `sandbox: <raison>` et rend 126 sans lancer la commande.
func helperMain(argv []string) int {
	if err := confineAndExec(argv); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox: "+err.Error())
		return 126
	}
	return 0 // inatteignable : Exec ne revient qu'en cas d'erreur
}

// helperProbe sert au test des espaces de noms : le lancement suffit.
func helperProbe() int { return 0 }

func confineAndExec(argv []string) error {
	raw, ok := os.LookupEnv(SpecEnv)
	if err := os.Unsetenv(SpecEnv); err != nil {
		return fmt.Errorf("unset %s: %w", SpecEnv, err)
	}
	if !ok {
		return fmt.Errorf("missing %s", SpecEnv)
	}
	var s Spec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return fmt.Errorf("invalid spec: %w", err)
	}
	// Landlock et no_new_privs s'appliquent au thread appelant : le même
	// thread doit restreindre puis appeler execve.
	runtime.LockOSThread()
	abi, err := landlockABI()
	if err != nil || abi < 1 {
		return fmt.Errorf("landlock not supported by the kernel: %v", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl no_new_privs: %w", err)
	}
	attr := rulesetAttr{fs: fsRights(abi)}
	size := unsafe.Offsetof(attr.net)
	if s.TCP {
		if abi < 4 {
			return fmt.Errorf("landlock ABI %d cannot restrict TCP", abi)
		}
		attr.net = tcpRights
		size = unsafe.Offsetof(attr.scoped)
		// Repli TCP : les sockets Unix abstraites (hors système de fichiers)
		// créées hors du domaine sont aussi refusées (ABI ≥ 6).
		if abi >= 6 {
			attr.scoped = unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET
			size = unsafe.Sizeof(attr)
		}
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), size, 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	ruleset := int(fd)
	// Inutile si execve réussit (le noyau ferme le descripteur, O_CLOEXEC) ;
	// ferme le ruleset sur les chemins d'échec qui rendent 126.
	defer unix.Close(ruleset)

	reads, err := splitAround("/", s.DenyRead)
	if err != nil {
		return err
	}
	for _, p := range reads {
		if err := addPathRule(ruleset, p, readRights&attr.fs); err != nil {
			return err
		}
	}
	// Les ancêtres des interdits sont seulement listables : les noms
	// deviennent visibles, jamais le contenu des fichiers interdits.
	for _, p := range denyAncestors(s.DenyRead) {
		if err := addPathRule(ruleset, p, unix.LANDLOCK_ACCESS_FS_READ_DIR); err != nil {
			return err
		}
	}
	for _, w := range Canonical(s.Write) {
		entries, err := splitAround(w, s.DenyRead)
		if err != nil {
			return err
		}
		for _, p := range entries {
			if err := addPathRule(ruleset, p, attr.fs); err != nil {
				return err
			}
		}
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", errno)
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		return fmt.Errorf("exec %s: %w", argv[0], err)
	}
	return nil
}

// addPathRule accorde rights sous p. Un chemin absent est ignoré (ne rien
// accorder ne fait que restreindre). Pour un fichier, seuls les droits
// applicables aux fichiers sont gardés.
func addPathRule(ruleset int, p string, rights uint64) error {
	fd, err := unix.Open(p, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) {
			return nil
		}
		return fmt.Errorf("open %s: %w", p, err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
	case unix.S_IFLNK:
		// Course : le chemin est devenu un lien ; on n'accorde rien.
		return nil
	default:
		rights &= fileRights
	}
	if rights == 0 {
		return nil
	}
	attr := unix.LandlockPathBeneathAttr{Allowed_access: rights, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&attr)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_add_rule %s: %w", p, errno)
	}
	return nil
}
