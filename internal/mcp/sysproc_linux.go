//go:build linux

package mcp

import "syscall"

// procAttr : groupe de processus propre, et SIGKILL au serveur si tenon meurt.
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
