//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	probeOnce   sync.Once
	probeStatus Status
)

// resetProbe vide le cache de Probe (hook pour les tests).
func resetProbe() { probeOnce = sync.Once{} }

// Probe vérifie une fois l'ABI Landlock, puis tente de lancer l'assistant
// dans de nouveaux espaces de noms utilisateur et réseau (délai 2 s). Sans
// espace de noms, le repli Landlock TCP (ABI ≥ 4) donne un réseau "tcp".
func Probe() Status {
	probeOnce.Do(func() { probeStatus = probe() })
	return probeStatus
}

func probe() Status {
	abi, err := landlockABI()
	if err != nil || abi < 1 {
		return Status{Mechanism: "none", Network: "none", Reason: "landlock not supported by the kernel"}
	}
	exe, err := os.Executable()
	if err != nil {
		return Status{Mechanism: "none", Network: "none", Reason: "executable: " + err.Error()}
	}
	st := Status{Available: true, Mechanism: "landlock", UnixSockets: abi >= 9}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "__sandbox", "--probe")
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	addNamespaces(cmd.SysProcAttr)
	nsErr := cmd.Run()
	switch {
	case nsErr == nil:
		st.Network = "full"
	case abi >= 4:
		st.Network = "tcp"
		st.Reason = "user namespaces unavailable: " + nsErr.Error()
	default:
		st.Network = "none"
		st.Reason = fmt.Sprintf("user namespaces unavailable (%v) and landlock ABI %d has no TCP control", nsErr, abi)
	}
	return st
}

// addNamespaces ajoute à attr un espace de noms utilisateur (l'utilisateur
// projeté sur lui-même) et un espace de noms réseau vide (loopback seul,
// sans route), sans toucher aux autres champs (Setpgid, Pdeathsig…).
func addNamespaces(attr *syscall.SysProcAttr) {
	attr.Cloneflags |= syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET
	attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: os.Getuid(), HostID: os.Getuid(), Size: 1}}
	attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: os.Getgid(), HostID: os.Getgid(), Size: 1}}
	attr.GidMappingsEnableSetgroups = false
}

// Wrap réécrit cmd en `<exécutable> __sandbox -- <argv>` avec la Spec en
// JSON dans SpecEnv ; Dir, Cancel, WaitDelay et les champs existants de
// SysProcAttr restent intacts. Réseau fermé : espaces de noms si la sonde
// les a validés, sinon repli Landlock TCP (Spec.TCP) si l'ABI le permet.
func Wrap(cmd *exec.Cmd, s Spec) error {
	if cmd.Path == "" {
		return fmt.Errorf("sandbox: empty command path")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("sandbox: executable: %w", err)
	}
	s.Write = Canonical(s.Write)
	s.DenyRead = Canonical(s.DenyRead)
	s.TCP = false
	if !s.Network {
		switch Probe().Network {
		case "full":
			if cmd.SysProcAttr == nil {
				cmd.SysProcAttr = &syscall.SysProcAttr{}
			}
			addNamespaces(cmd.SysProcAttr)
		case "tcp":
			s.TCP = true
		}
	}
	spec, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("sandbox: spec: %w", err)
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, SpecEnv+"=") {
			out = append(out, kv)
		}
	}
	cmd.Env = append(out, SpecEnv+"="+string(spec))
	args := []string{exe, "__sandbox", "--", cmd.Path}
	if len(cmd.Args) > 1 {
		args = append(args, cmd.Args[1:]...)
	}
	cmd.Path = exe
	cmd.Args = args
	return nil
}
