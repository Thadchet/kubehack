//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// These tests need a disposable cluster, and they create and delete objects:
//
//	kind create cluster --name kubehack
//	go test -tags integration ./internal/api
const kindContext = "kind-kubehack"

func kubectl(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	// Every command names the context, so the current context never matters.
	cmd := exec.Command("kubectl", append([]string{"--context", kindContext}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %v: %v\n%s", args, err, output)
	}
	return string(output)
}

type kindWatch struct {
	t    *testing.T
	ctx  context.Context
	conn *websocket.Conn
}

func openKindWatch(t *testing.T, server *Server, contextName, query string) *kindWatch {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	path := "/api/v1/contexts/" + url.PathEscape(contextName) + "/watch?" + query
	conn, _, err := websocket.Dial(ctx, wsURL(server, path), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + server.Token()},
	})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(-1)
	t.Cleanup(func() { conn.CloseNow() })
	return &kindWatch{t: t, ctx: ctx, conn: conn}
}

// until reads frames until match returns true, and returns all raw frames.
func (w *kindWatch) until(match func(message watchMessage, raw string) bool) string {
	w.t.Helper()
	var frames strings.Builder
	for {
		_, body, err := w.conn.Read(w.ctx)
		if err != nil {
			w.t.Fatalf("read: %v; frames so far: %.500s", err, frames.String())
		}
		frames.Write(body)
		var message watchMessage
		if err := json.Unmarshal(body, &message); err != nil {
			w.t.Fatal(err)
		}
		if match(message, string(body)) {
			return frames.String()
		}
	}
}

func isSynced(message watchMessage, _ string) bool { return message.Type == "synced" }

func hasEvent(op, name string) func(watchMessage, string) bool {
	return func(message watchMessage, _ string) bool {
		for _, event := range message.Events {
			if string(event.Op) == op && event.Object.GetName() == name {
				return true
			}
		}
		return false
	}
}

func startKindServer(t *testing.T) *Server {
	t.Helper()
	server, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return server
}

func TestKindDiscoveryIncludesCustomResources(t *testing.T) {
	applyWidget(t)
	server := startKindServer(t)

	request, err := http.NewRequest(http.MethodGet,
		server.BaseURL()+"/api/v1/contexts/"+kindContext+"/resources", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+server.Token())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%d: %s", response.StatusCode, body)
	}
	for _, want := range []string{`"resource":"pods"`, `"resource":"widgets"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("discovery lacks %s", want)
		}
	}
	if strings.Contains(string(body), "pods/log") {
		t.Error("discovery includes a subresource")
	}
}

func applyWidget(t *testing.T) {
	t.Helper()
	kubectl(t, `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.demo.kubehack.io}
spec:
  group: demo.kubehack.io
  names: {kind: Widget, plural: widgets, singular: widget}
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
`, "apply", "-f", "-")
	kubectl(t, "", "wait", "--for=condition=established", "crd/widgets.demo.kubehack.io", "--timeout=30s")
	kubectl(t, `apiVersion: demo.kubehack.io/v1
kind: Widget
metadata: {name: w1, namespace: default}
`, "apply", "-f", "-")
}

func TestKindWatchFollowsPodLifecycle(t *testing.T) {
	server := startKindServer(t)
	watch := openKindWatch(t, server, kindContext, "version=v1&resource=pods&namespace=default")
	watch.until(isSynced)

	name := fmt.Sprintf("kh-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		exec.Command("kubectl", "--context", kindContext, "delete", "pod", name, "--now", "--ignore-not-found").Run()
	})
	kubectl(t, "", "run", name, "--image=registry.k8s.io/pause:3.10", "--restart=Never")
	watch.until(hasEvent("added", name))
	kubectl(t, "", "label", "pod", name, "changed=yes")
	watch.until(hasEvent("modified", name))
	kubectl(t, "", "delete", "pod", name, "--now")
	watch.until(hasEvent("deleted", name))
}

func TestKindWatchShowsCustomResources(t *testing.T) {
	applyWidget(t)
	server := startKindServer(t)
	watch := openKindWatch(t, server, kindContext, "group=demo.kubehack.io&version=v1&resource=widgets")

	frames := watch.until(isSynced)
	if !strings.Contains(frames, `"name":"w1"`) {
		t.Errorf("widget missing: %.500s", frames)
	}
}

func TestKindWatchNeverStreamsSecretValues(t *testing.T) {
	const value = "kh-plain-value"
	kubectl(t, `apiVersion: v1
kind: Secret
metadata: {name: kh-secret-test, namespace: default}
stringData: {password: `+value+`}
`, "apply", "-f", "-")
	t.Cleanup(func() {
		exec.Command("kubectl", "--context", kindContext, "delete", "secret", "kh-secret-test", "--ignore-not-found").Run()
	})

	server := startKindServer(t)
	watch := openKindWatch(t, server, kindContext, "version=v1&resource=secrets&namespace=default")
	frames := watch.until(isSynced)

	if !strings.Contains(frames, "kh-secret-test") {
		t.Fatalf("secret missing: %.500s", frames)
	}
	encoded := strings.TrimSpace(kubectl(t, "", "get", "secret", "kh-secret-test", "-o", "jsonpath={.data.password}"))
	for _, leaked := range []string{value, encoded, "last-applied-configuration", "managedFields"} {
		if strings.Contains(frames, leaked) {
			t.Errorf("stream contains %q", leaked)
		}
	}
}

// TestKindWatchReportsForbidden uses a service account with no permissions, so
// this is the API server's real RBAC answer.
func TestKindWatchReportsForbidden(t *testing.T) {
	kubectl(t, `apiVersion: v1
kind: ServiceAccount
metadata: {name: kh-norights, namespace: default}
`, "apply", "-f", "-")
	token := strings.TrimSpace(kubectl(t, "", "create", "token", "kh-norights"))
	server := strings.TrimSpace(kubectl(t, "", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}"))

	config := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: limited
clusters:
- name: c
  cluster: {server: %q, insecure-skip-tls-verify: true}
users:
- name: u
  user: {token: %q}
contexts:
- name: limited
  context: {cluster: c, user: u}
`, server, token)
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)

	api := startKindServer(t)
	watch := openKindWatch(t, api, "limited", "version=v1&resource=pods")
	var reason string
	watch.until(func(message watchMessage, _ string) bool {
		reason = message.Reason
		return message.Type == "failed"
	})
	if reason != "Forbidden" {
		t.Errorf("reason = %q", reason)
	}
}

const logPodManifest = `apiVersion: v1
kind: Pod
metadata: {name: kh-logs, namespace: default}
spec:
  restartPolicy: Always
  terminationGracePeriodSeconds: 0
  containers:
  - name: ticker
    image: busybox:1.36
    command: [sh, -c, 'i=0; while true; do echo tick-$i; i=$((i+1)); sleep 1; done']
  - name: crasher
    image: busybox:1.36
    command: [sh, -c, 'echo crash-output; sleep 2; exit 1']
`

func openKindLogs(t *testing.T, server *Server, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, wsURL(server, "/api/v1/contexts/"+kindContext+"/logs?"+query), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + server.Token()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func readUntilLine(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		_, body, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v", want, err)
		}
		var message logMessage
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "error" {
			t.Fatalf("waiting for %q: %s", want, message.Message)
		}
		if strings.Contains(strings.Join(message.Lines, "\n"), want) {
			return
		}
	}
}

func TestKindLogsFollowContainersAndPrevious(t *testing.T) {
	kubectl(t, logPodManifest, "apply", "-f", "-")
	t.Cleanup(func() {
		exec.Command("kubectl", "--context", kindContext, "delete", "pod", "kh-logs", "--now", "--ignore-not-found").Run()
	})
	deadline := time.Now().Add(5 * time.Minute)
	for {
		restarts := kubectl(t, "", "get", "pod", "kh-logs", "-o",
			`jsonpath={.status.containerStatuses[?(@.name=="crasher")].restartCount}`)
		if restarts != "" && restarts != "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("crasher never restarted; the image may not have been pulled")
		}
		time.Sleep(time.Second)
	}

	server := startKindServer(t)

	follow := openKindLogs(t, server, "namespace=default&pod=kh-logs&container=ticker&follow=true&tail=1")
	readUntilLine(t, follow, "tick-")
	// A second line can only come from following, because tail=1 sent just one.
	first := kubectl(t, "", "logs", "kh-logs", "-c", "ticker", "--tail=1")
	number := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(first), "tick-"))
	next, err := strconv.Atoi(number)
	if err != nil {
		t.Fatal(err)
	}
	readUntilLine(t, follow, fmt.Sprintf("tick-%d", next+2))

	previous := openKindLogs(t, server, "namespace=default&pod=kh-logs&container=crasher&previous=true")
	readUntilLine(t, previous, "crash-output")
}
