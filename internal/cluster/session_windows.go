package cluster

import "os/exec"

// detachSession has nothing to do, because Windows never reads a login shell.
func detachSession(*exec.Cmd) {}
