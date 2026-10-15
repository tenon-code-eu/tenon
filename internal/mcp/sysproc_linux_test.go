//go:build linux

package mcp

import (
	"syscall"
	"testing"
)

func TestStdioPdeathsig(t *testing.T) {
	a := procAttr()
	if !a.Setpgid || a.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("SysProcAttr %+v", a)
	}
}
