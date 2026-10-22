//go:build !linux && !darwin

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Probe : aucun mécanisme d'isolation sur ce système.
func Probe() Status {
	return Status{Available: false, Mechanism: "none", Network: "none", Reason: "unsupported OS"}
}

// Wrap échoue : pas d'isolation sur ce système.
func Wrap(*exec.Cmd, Spec) error { return errors.New("sandbox: unsupported OS") }

func helperMain([]string) int { return unsupportedHelper() }
func helperProbe() int        { return unsupportedHelper() }

func unsupportedHelper() int {
	fmt.Fprintln(os.Stderr, "sandbox: unsupported OS")
	return 126
}
