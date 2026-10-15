//go:build unix && !linux

package mcp

import "syscall"

// procAttr : groupe de processus propre (Pdeathsig n'existe que sous Linux).
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
