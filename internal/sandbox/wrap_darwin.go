//go:build darwin

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

const sandboxExecPath = "/usr/bin/sandbox-exec"

var (
	probeOnce   sync.Once
	probeStatus Status
)

// resetProbe vide le cache de Probe (hook pour les tests).
func resetProbe() { probeOnce = sync.Once{} }

// Probe vérifie une fois que sandbox-exec fonctionne.
func Probe() Status {
	probeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := exec.CommandContext(ctx, sandboxExecPath, "-p", "(version 1)(allow default)", "/usr/bin/true").Run()
		if err != nil {
			probeStatus = Status{Available: false, Mechanism: "none", Network: "none", Reason: "sandbox-exec: " + err.Error()}
			return
		}
		probeStatus = Status{Available: true, Mechanism: "sandbox-exec", Network: "full", UnixSockets: true}
	})
	return probeStatus
}

// Wrap réécrit cmd pour l'exécuter sous sandbox-exec ; Dir, Env, SysProcAttr,
// Cancel et WaitDelay restent intacts. Les chemins de la Spec sont
// canonisés ici : le profil SBPL ne suit pas les liens symboliques.
func Wrap(cmd *exec.Cmd, s Spec) error {
	// exec.Command n'a pas trouvé le programme : rien à isoler.
	if cmd.Err != nil {
		return cmd.Err
	}
	if cmd.Path == "" {
		return fmt.Errorf("sandbox: empty command path")
	}
	if err := checkPaths(s); err != nil {
		return err
	}
	s.Write = Canonical(s.Write)
	s.DenyRead = Canonical(s.DenyRead)
	s.DenyWrite = Canonical(s.DenyWrite)
	args := []string{"sandbox-exec", "-p", seatbeltProfile(s), "--", cmd.Path}
	if len(cmd.Args) > 1 {
		args = append(args, cmd.Args[1:]...)
	}
	cmd.Path = sandboxExecPath
	cmd.Args = args
	return nil
}

// L'assistant interne ne sert que sous Linux.
func helperMain([]string) int { return unsupportedHelper() }
func helperProbe() int        { return unsupportedHelper() }

func unsupportedHelper() int {
	fmt.Fprintln(os.Stderr, "sandbox: unsupported OS")
	return 126
}
