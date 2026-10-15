package main

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a new process group, so it outlives the console.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
