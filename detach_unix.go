//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts cmd in its own session, so it outlives the terminal.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
