//go:build linux

package sandbox

import (
	"errors"
	"os/exec"
)

// Temporaire : la tâche 3 remplace ce fichier par le mécanisme Landlock.

// Probe : mécanisme Linux pas encore implémenté.
func Probe() Status {
	return Status{Available: false, Mechanism: "none", Network: "none", Reason: "not implemented"}
}

// Wrap échoue tant que le mécanisme Linux manque.
func Wrap(*exec.Cmd, Spec) error { return errors.New("sandbox: not implemented") }

func helperMain([]string) int { return 126 }
func helperProbe() int        { return 126 }
