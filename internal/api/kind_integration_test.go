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

	"kubehack/internal/resources"
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

type kindApply struct {
	t      *testing.T
	server *Server
}

func (k kindApply) call(method, path string, body any, out any) int {
	k.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			k.t.Fatal(err)
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequest(method, k.server.BaseURL()+"/api/v1/contexts/"+kindContext+path, reader)
	if err != nil {
		k.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+k.server.Token())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		k.t.Fatal(err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		k.t.Fatal(err)
	}
	return response.StatusCode
}

func (k kindApply) load(resource, name string) resources.EditableObject {
	k.t.Helper()
	var object resources.EditableObject
	path := "/object?version=v1&resource=" + resource + "&namespace=default&name=" + name
	if status := k.call(http.MethodGet, path, nil, &object); status != http.StatusOK {
		k.t.Fatalf("load: status %d", status)
	}
	return object
}

func editBody(resource, name string, object resources.EditableObject, text string, dryRun, force bool) applyBody {
	return applyBody{
		Version: "v1", Resource: resource, Namespace: "default", Name: name,
		YAML: text, ResourceVersion: object.ResourceVersion, UID: object.UID,
		DryRun: dryRun, Force: force,
	}
}

func applyConfigMap(t *testing.T, name, value string) {
	t.Helper()
	kubectl(t, fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata: {name: %s, namespace: default}
data: {key: %s}
`, name, value), "apply", "-f", "-")
	t.Cleanup(func() {
		exec.Command("kubectl", "--context", kindContext, "delete", "configmap", name, "--ignore-not-found").Run()
	})
}

func configMapValue(t *testing.T, name string) string {
	t.Helper()
	return kubectl(t, "", "get", "configmap", name, "-o", "jsonpath={.data.key}")
}

func TestKindDryRunShowsChangeWithoutWriting(t *testing.T) {
	applyConfigMap(t, "kh-dry", "original")
	client := kindApply{t, startKindServer(t)}
	object := client.load("configmaps", "kh-dry")
	edited := strings.Replace(object.YAML, "original", "changed", 1)

	var result resources.ApplyResult
	status := client.call(http.MethodPost, "/apply", editBody("configmaps", "kh-dry", object, edited, true, true), &result)
	if status != http.StatusOK || result.Applied {
		t.Fatalf("status %d, result %+v", status, result)
	}
	if !strings.Contains(result.Before, "original") || !strings.Contains(result.After, "changed") {
		t.Errorf("before:\n%s\nafter:\n%s", result.Before, result.After)
	}
	if got := configMapValue(t, "kh-dry"); got != "original" {
		t.Errorf("dry run wrote to the cluster: %q", got)
	}
}

func TestKindFieldConflictNeedsForce(t *testing.T) {
	applyConfigMap(t, "kh-conflict", "original")
	client := kindApply{t, startKindServer(t)}
	object := client.load("configmaps", "kh-conflict")
	edited := strings.Replace(object.YAML, "original", "changed", 1)

	var failure applyFailure
	status := client.call(http.MethodPost, "/apply", editBody("configmaps", "kh-conflict", object, edited, false, false), &failure)
	if status != http.StatusConflict || failure.Kind != "fieldConflict" || len(failure.Conflicts) == 0 {
		t.Fatalf("status %d, failure %+v", status, failure)
	}
	if !strings.Contains(failure.Conflicts[0].Message, "kubectl") {
		t.Errorf("conflict does not name the owner: %+v", failure.Conflicts[0])
	}
	if got := configMapValue(t, "kh-conflict"); got != "original" {
		t.Fatalf("conflicting apply wrote to the cluster: %q", got)
	}

	var result resources.ApplyResult
	status = client.call(http.MethodPost, "/apply", editBody("configmaps", "kh-conflict", object, edited, false, true), &result)
	if status != http.StatusOK || !result.Applied {
		t.Fatalf("force: status %d, result %+v", status, result)
	}
	if got := configMapValue(t, "kh-conflict"); got != "changed" {
		t.Errorf("value = %q", got)
	}
}

func TestKindStaleEditIsRejectedEvenWithForce(t *testing.T) {
	applyConfigMap(t, "kh-stale", "original")
	client := kindApply{t, startKindServer(t)}
	object := client.load("configmaps", "kh-stale")

	kubectl(t, "", "patch", "configmap", "kh-stale", "-p", `{"data":{"key":"someone-else"}}`)

	edited := strings.Replace(object.YAML, "original", "mine", 1)
	for _, force := range []bool{false, true} {
		var failure applyFailure
		status := client.call(http.MethodPost, "/apply", editBody("configmaps", "kh-stale", object, edited, false, force), &failure)
		if status != http.StatusConflict || failure.Kind != "stale" {
			t.Fatalf("force %v: status %d, failure %+v", force, status, failure)
		}
	}
	if got := configMapValue(t, "kh-stale"); got != "someone-else" {
		t.Errorf("stale edit overwrote the cluster: %q", got)
	}
}

func TestKindRecreatedObjectIsNotOverwritten(t *testing.T) {
	applyConfigMap(t, "kh-recreated", "original")
	client := kindApply{t, startKindServer(t)}
	object := client.load("configmaps", "kh-recreated")
	edited := strings.Replace(object.YAML, "original", "mine", 1)

	kubectl(t, "", "delete", "configmap", "kh-recreated")
	var gone applyFailure
	status := client.call(http.MethodPost, "/apply", editBody("configmaps", "kh-recreated", object, edited, false, true), &gone)
	if status != http.StatusNotFound || gone.Kind != "gone" {
		t.Fatalf("deleted: status %d, failure %+v", status, gone)
	}
	if out := kubectl(t, "", "get", "configmap", "kh-recreated", "--ignore-not-found"); out != "" {
		t.Fatalf("apply brought a deleted object back: %s", out)
	}

	applyConfigMap(t, "kh-recreated", "new-owner")
	var failure applyFailure
	status = client.call(http.MethodPost, "/apply", editBody("configmaps", "kh-recreated", object, edited, false, true), &failure)
	if status != http.StatusConflict {
		t.Fatalf("recreated: status %d, failure %+v", status, failure)
	}
	if got := configMapValue(t, "kh-recreated"); got != "new-owner" {
		t.Errorf("value = %q", got)
	}
}

func TestKindSecretEditKeepsValues(t *testing.T) {
	kubectl(t, `apiVersion: v1
kind: Secret
metadata: {name: kh-edit-secret, namespace: default}
stringData: {password: keep-me}
`, "apply", "-f", "-")
	t.Cleanup(func() {
		exec.Command("kubectl", "--context", kindContext, "delete", "secret", "kh-edit-secret", "--ignore-not-found").Run()
	})
	client := kindApply{t, startKindServer(t)}
	object := client.load("secrets", "kh-edit-secret")
	before := kubectl(t, "", "get", "secret", "kh-edit-secret", "-o", "jsonpath={.data.password}")
	if !strings.Contains(object.YAML, before) {
		t.Fatalf("load did not return the real value:\n%s", object.YAML)
	}

	edited := strings.Replace(object.YAML, "metadata:\n", "metadata:\n  labels:\n    edited: \"yes\"\n", 1)
	var result resources.ApplyResult
	status := client.call(http.MethodPost, "/apply", editBody("secrets", "kh-edit-secret", object, edited, false, false), &result)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got := kubectl(t, "", "get", "secret", "kh-edit-secret", "-o", "jsonpath={.data.password}"); got != before {
		t.Errorf("value changed from %q to %q", before, got)
	}
	if got := kubectl(t, "", "get", "secret", "kh-edit-secret", "-o", "jsonpath={.metadata.labels.edited}"); got != "yes" {
		t.Errorf("label = %q", got)
	}
}
