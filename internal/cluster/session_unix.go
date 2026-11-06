//go:build !windows

package cluster

import (
	"os/exec"
	"syscall"
)

// detachSession keeps an interactive shell from taking over the terminal when
// the app is started from one.
func detachSession(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
