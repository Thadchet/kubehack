package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	shellEnvTimeout = 5 * time.Second
	shellEnvMarker  = "__KUBEHACK_ENV__"
	defaultShell    = "/bin/zsh"
)

// fallbackPathDirs stay on PATH even when the login shell cannot be read.
var fallbackPathDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// shellOnlyVars describe the probe shell itself, not the user's environment.
var shellOnlyVars = map[string]struct{}{
	"_": {}, "OLDPWD": {}, "PWD": {}, "SHLVL": {},
}

// ImportShellEnv copies the login shell's environment into this process. Apps
// opened from Finder start with a minimal launchd environment, so without this
// credential plugins such as aws or gke-gcloud-auth-plugin cannot be found and
// variables such as KUBECONFIG or AWS_PROFILE are missing. Values that are
// already set win, except PATH, which is merged. Values are never logged
// because they can hold secrets.
func ImportShellEnv(ctx context.Context) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = defaultShell
	}

	output, err := readShellEnv(ctx, shell)
	if err != nil {
		// A broken shell profile must not stop the fallback directories.
		setPath(mergePath("", os.Getenv("PATH")))
		return err
	}
	env, err := parseShellEnv(output)
	if err != nil {
		setPath(mergePath("", os.Getenv("PATH")))
		return err
	}

	for name, value := range env {
		if _, skip := shellOnlyVars[name]; skip || name == "PATH" {
			continue
		}
		if _, exists := os.LookupEnv(name); !exists {
			if err := os.Setenv(name, value); err != nil {
				return fmt.Errorf("set %s: %w", name, err)
			}
		}
	}
	setPath(mergePath(env["PATH"], os.Getenv("PATH")))
	return nil
}

func setPath(path string) {
	// Setenv only fails for invalid names, and PATH is valid.
	_ = os.Setenv("PATH", path)
}

func readShellEnv(ctx context.Context, shell string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, shellEnvTimeout)
	defer cancel()

	// -i is needed because many tools add to PATH from .zshrc, not .zprofile.
	// The markers separate the result from anything the profile prints.
	script := fmt.Sprintf("printf %[1]s; /usr/bin/env -0; printf %[1]s", shellEnvMarker)
	cmd := exec.CommandContext(ctx, shell, "-ilc", script)
	// A new session keeps an interactive shell from taking over the terminal
	// when the app is started from one.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Background jobs from the profile can hold stdout open after a timeout.
	cmd.WaitDelay = time.Second

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read environment from %s: %w", filepath.Base(shell), err)
	}
	return output, nil
}

func parseShellEnv(output []byte) (map[string]string, error) {
	parts := bytes.Split(output, []byte(shellEnvMarker))
	if len(parts) < 3 {
		return nil, fmt.Errorf("shell environment markers not found")
	}

	env := make(map[string]string)
	for _, entry := range bytes.Split(parts[1], []byte{0}) {
		name, value, ok := strings.Cut(string(entry), "=")
		if ok && name != "" {
			env[name] = value
		}
	}
	return env, nil
}

// mergePath keeps the shell's order first so it resolves tools the same way a
// terminal does, then adds whatever else was already available.
func mergePath(shellPath, currentPath string) string {
	seen := make(map[string]struct{})
	var merged []string
	add := func(dirs ...string) {
		for _, dir := range dirs {
			if _, ok := seen[dir]; dir != "" && !ok {
				seen[dir] = struct{}{}
				merged = append(merged, dir)
			}
		}
	}
	add(filepath.SplitList(shellPath)...)
	add(filepath.SplitList(currentPath)...)
	add(fallbackPathDirs...)
	return strings.Join(merged, string(filepath.ListSeparator))
}
