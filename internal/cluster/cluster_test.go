package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
)

const pluginToken = "plugin-token"

// writeKubeconfig points KUBECONFIG at a config whose only user authenticates
// through the given exec plugin command.
func writeKubeconfig(t *testing.T, server, command string) {
	t.Helper()
	config := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: zeta/one
clusters:
- name: c1
  cluster: {server: %q, insecure-skip-tls-verify: true}
users:
- name: u1
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: %s
      interactiveMode: Never
contexts:
- name: zeta/one
  context: {cluster: c1, user: u1, namespace: apps}
- name: alpha
  context: {cluster: missing, user: missing}
`, server, command)
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
}

func fakeAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	// client-go only applies user credentials to TLS servers.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+pluginToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"gitVersion":"v1.99.0"}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// installPlugin puts an exec credential plugin in a directory on PATH only.
func installPlugin(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
echo '{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":%q}}'
`, pluginToken)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(filepath.ListSeparator)+os.Getenv("PATH"))
}

func TestListContexts(t *testing.T) {
	writeKubeconfig(t, "https://example.test", "some-plugin")

	got, err := ListContexts()
	if err != nil {
		t.Fatal(err)
	}
	if got.Current != "zeta/one" {
		t.Errorf("current = %q", got.Current)
	}
	if len(got.Contexts) != 2 || got.Contexts[0].Name != "alpha" {
		t.Fatalf("contexts = %+v, want 2 sorted by name", got.Contexts)
	}
	item := got.Contexts[1]
	if item.Server != "https://example.test" || item.Namespace != "apps" || item.AuthCommand != "some-plugin" {
		t.Errorf("context = %+v", item)
	}
}

func TestListContextsWithoutKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))

	got, err := ListContexts()
	if err != nil {
		t.Fatal(err)
	}
	if got.Contexts == nil || len(got.Contexts) != 0 {
		t.Errorf("contexts = %#v, want empty non-nil list", got.Contexts)
	}
}

func TestProbeUsesExecPluginFromPath(t *testing.T) {
	server := fakeAPIServer(t)
	writeKubeconfig(t, server.URL, "kubehack-test-plugin")
	installPlugin(t, "kubehack-test-plugin")

	status, err := Probe(context.Background(), "zeta/one")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Reachable || status.Version != "v1.99.0" {
		t.Errorf("status = %+v", status)
	}
}

func TestProbeReportsMissingPlugin(t *testing.T) {
	server := fakeAPIServer(t)
	writeKubeconfig(t, server.URL, "kubehack-absent-plugin")

	status, err := Probe(context.Background(), "zeta/one")
	if err != nil {
		t.Fatal(err)
	}
	if status.Reachable || !strings.Contains(status.Error, "kubehack-absent-plugin") {
		t.Errorf("status = %+v, want error naming the plugin", status)
	}
}

func TestProbeReportsDanglingContext(t *testing.T) {
	writeKubeconfig(t, "https://example.test", "some-plugin")

	status, err := Probe(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if status.Reachable || status.Error == "" {
		t.Errorf("status = %+v, want an error", status)
	}
}

func TestProbeUnknownContext(t *testing.T) {
	writeKubeconfig(t, "https://example.test", "some-plugin")

	_, err := Probe(context.Background(), "nope")
	if !errors.Is(err, ErrContextNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestMergePath(t *testing.T) {
	got := mergePath("/shell/bin:/usr/bin", "/usr/bin:/bin")
	want := "/shell/bin:/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseShellEnvIgnoresProfileOutput(t *testing.T) {
	output := "welcome banner" + shellEnvMarker + "PATH=/a:/b\x00MULTI=one\ntwo=x\x00" + shellEnvMarker + "bye"

	env, err := parseShellEnv([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if env["PATH"] != "/a:/b" || env["MULTI"] != "one\ntwo=x" {
		t.Errorf("env = %q", env)
	}
	if len(env) != 2 {
		t.Errorf("env has %d entries, want 2", len(env))
	}
}

func TestParseShellEnvWithoutMarkers(t *testing.T) {
	if _, err := parseShellEnv([]byte("PATH=/a")); err == nil {
		t.Error("want error")
	}
}

func TestImportShellEnvFromRealShell(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "fakeshell")
	script := fmt.Sprintf(`#!/bin/sh
echo noisy profile
printf '%[1]sPATH=/from/shell\0KUBEHACK_NEW=shell\0KUBEHACK_SET=shell\0PWD=/nope\0%[1]s'
`, shellEnvMarker)
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("KUBEHACK_SET", "existing")
	t.Setenv("KUBEHACK_NEW", "")
	os.Unsetenv("KUBEHACK_NEW")
	pwd := os.Getenv("PWD")

	if err := ImportShellEnv(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("KUBEHACK_NEW"); got != "shell" {
		t.Errorf("KUBEHACK_NEW = %q", got)
	}
	if got := os.Getenv("KUBEHACK_SET"); got != "existing" {
		t.Errorf("KUBEHACK_SET = %q", got)
	}
	if got := os.Getenv("PWD"); got != pwd {
		t.Errorf("PWD = %q", got)
	}
	if got := os.Getenv("PATH"); !strings.HasPrefix(got, "/from/shell:/usr/bin") {
		t.Errorf("PATH = %q", got)
	}
}

func TestWriteContainerKubeconfig(t *testing.T) {
	source := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(source, []byte(`apiVersion: v1
kind: Config
clusters:
- name: local
  cluster: {server: "https://127.0.0.1:6443"}
- name: named
  cluster: {server: "https://localhost:6443", tls-server-name: custom.test}
- name: remote
  cluster: {server: "https://34.1.2.3"}
users:
- name: plugin
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: /opt/homebrew/bin/some-plugin
      interactiveMode: Never
- name: gke
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: /opt/homebrew/bin/gke-gcloud-auth-plugin
      installHint: Install it from elsewhere
      interactiveMode: Never
- name: bare
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: other-plugin
      interactiveMode: Never
contexts:
- name: one
  context: {cluster: local, user: plugin}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", source)

	target := filepath.Join(t.TempDir(), "container")
	if err := WriteContainerKubeconfig(target, "host.test"); err != nil {
		t.Fatal(err)
	}
	got, err := clientcmd.LoadFromFile(target)
	if err != nil {
		t.Fatal(err)
	}

	clusters := []struct{ name, server, tlsName string }{
		{"local", "https://host.test:6443", "127.0.0.1"},
		{"named", "https://host.test:6443", "custom.test"},
		{"remote", "https://34.1.2.3", ""},
	}
	for _, want := range clusters {
		entry := got.Clusters[want.name]
		if entry.Server != want.server || entry.TLSServerName != want.tlsName {
			t.Errorf("%s = %s, %q", want.name, entry.Server, entry.TLSServerName)
		}
	}
	if command := got.AuthInfos["plugin"].Exec.Command; command != "some-plugin" {
		t.Errorf("plugin command = %q", command)
	}
	if hint := got.AuthInfos["gke"].Exec.InstallHint; hint != gkeImageHint {
		t.Errorf("gke hint = %q", hint)
	}
	if command := got.AuthInfos["bare"].Exec.Command; command != "other-plugin" {
		t.Errorf("bare command = %q", command)
	}
}
