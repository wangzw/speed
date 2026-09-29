//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// configureChild has the kernel kill the child when the thread that started it
// goes away, so a test binary that is killed or panics past its deadline takes
// its children with it rather than leaving them bound to ports and holding
// database connections.
//
// The signal follows the starting thread rather than the process. The Go
// runtime ends a thread only when a goroutine locked to it exits, and nothing
// in this harness locks one, so for this binary the two are the same.
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
