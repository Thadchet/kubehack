//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"kubehack/internal/stream"
)

// webManifest serves the pod's own name on port 8080, behind a service whose
// target port is a name, on port 80.
const webManifest = `
apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  labels: {app: %[1]s}
spec:
  terminationGracePeriodSeconds: 0
  containers:
  - name: web
    image: busybox:1.36
    command: [sh, -c, "mkdir /www; hostname > /www/index.html; exec httpd -f -p 8080 -h /www"]
    ports: [{name: web, containerPort: 8080}]
    readinessProbe:
      tcpSocket: {port: 8080}
      periodSeconds: 1
---
apiVersion: v1
kind: Service
metadata:
  name: %[1]s
spec:
  selector: {app: %[1]s}
  ports: [{port: 80, targetPort: web}]
`

func runWebPod(t *testing.T, name string) {
	t.Helper()
	kubectl(t, fmt.Sprintf(webManifest, name), "apply", "-f", "-")
	t.Cleanup(func() {
		exec.Command("kubectl", "--context", kindContext, "delete", "pod,service", name, "--now", "--ignore-not-found").Run()
	})
	kubectl(t, "", "wait", "--for=condition=Ready", "pod/"+name, "--timeout=240s")
}

func startKindForward(t *testing.T, server *Server, kind, name string, port, localPort int) (stream.Forward, int, string) {
	t.Helper()
	body := fmt.Sprintf(`{"namespace":"default","kind":%q,"name":%q,"port":%d,"localPort":%d}`, kind, name, port, localPort)
	request, err := http.NewRequest(http.MethodPost, server.BaseURL()+"/api/v1/contexts/"+kindContext+"/portforwards", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+server.Token())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var forward stream.Forward
	if response.StatusCode == http.StatusCreated {
		if err := json.Unmarshal(content, &forward); err != nil {
			t.Fatal(err)
		}
	}
	return forward, response.StatusCode, string(content)
}

func fetchForward(port int) (string, error) {
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	return strings.TrimSpace(string(content)), err
}

func waitForLost(t *testing.T, server *Server, id string, limit time.Duration) stream.Forward {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		forward, err := server.forwards.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if forward.State == stream.ForwardLost {
			return forward
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("forward was not reported lost within %s", limit)
	return stream.Forward{}
}

func portIsFree(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// listeningAddresses asks the OS, rather than trusting what the code asked for.
func listeningAddresses(t *testing.T, port int) string {
	t.Helper()
	output, err := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN").CombinedOutput()
	if err != nil {
		t.Fatalf("lsof: %v\n%s", err, output)
	}
	return string(output)
}

func TestKindForwardToPodCarriesTrafficOnLoopbackOnly(t *testing.T) {
	runWebPod(t, "kh-fwd-pod")
	server := startKindServer(t)

	forward, status, body := startKindForward(t, server, "pod", "kh-fwd-pod", 8080, 0)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}
	for i := 0; i < 20; i++ {
		content, err := fetchForward(forward.LocalPort)
		if err != nil || content != "kh-fwd-pod" {
			t.Fatalf("request %d: %q, %v", i, content, err)
		}
	}

	listening := listeningAddresses(t, forward.LocalPort)
	if !strings.Contains(listening, fmt.Sprintf("127.0.0.1:%d", forward.LocalPort)) ||
		strings.Contains(listening, fmt.Sprintf("*:%d", forward.LocalPort)) ||
		strings.Contains(listening, "[::") {
		t.Fatalf("listening on:\n%s", listening)
	}

	if err := server.forwards.Stop(forward.ID); err != nil {
		t.Fatal(err)
	}
	if !portIsFree(forward.LocalPort) {
		t.Fatal("local port still held after stop")
	}
}

func TestKindForwardToServiceFollowsNamedTargetPort(t *testing.T) {
	runWebPod(t, "kh-fwd-svc")
	server := startKindServer(t)

	forward, status, body := startKindForward(t, server, "service", "kh-fwd-svc", 80, 0)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}
	if forward.Pod != "kh-fwd-svc" {
		t.Fatalf("pod = %q", forward.Pod)
	}
	if content, err := fetchForward(forward.LocalPort); err != nil || content != "kh-fwd-svc" {
		t.Fatalf("%q, %v", content, err)
	}
}

// Nothing crosses an idle forward, so client-go alone would not notice this.
func TestKindDeletedPodIsReportedWithoutTraffic(t *testing.T) {
	runWebPod(t, "kh-fwd-gone")
	server := startKindServer(t)
	forward, status, body := startKindForward(t, server, "pod", "kh-fwd-gone", 8080, 0)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}

	kubectl(t, "", "delete", "pod", "kh-fwd-gone", "--now")
	lost := waitForLost(t, server, forward.ID, 15*time.Second)
	if !strings.Contains(lost.Message, "kh-fwd-gone") {
		t.Fatalf("message = %q", lost.Message)
	}
	if !portIsFree(forward.LocalPort) {
		t.Fatal("lost forward still holds its local port")
	}
}

func TestKindReconnectAfterReplacementKeepsLocalPort(t *testing.T) {
	runWebPod(t, "kh-fwd-again")
	server := startKindServer(t)
	forward, status, body := startKindForward(t, server, "service", "kh-fwd-again", 80, 0)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}

	kubectl(t, "", "delete", "pod", "kh-fwd-again", "--now")
	waitForLost(t, server, forward.ID, 15*time.Second)
	kubectl(t, fmt.Sprintf(webManifest, "kh-fwd-again"), "apply", "-f", "-")
	kubectl(t, "", "wait", "--for=condition=Ready", "pod/kh-fwd-again", "--timeout=120s")

	restarted, err := server.forwards.Restart(forward.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.LocalPort != forward.LocalPort {
		t.Fatalf("port changed from %d to %d", forward.LocalPort, restarted.LocalPort)
	}
	if content, err := fetchForward(restarted.LocalPort); err != nil || content != "kh-fwd-again" {
		t.Fatalf("%q, %v", content, err)
	}
}

// client-go drops the whole forward when one connection is refused in the pod.
func TestKindClosedPodPortIsExplained(t *testing.T) {
	runWebPod(t, "kh-fwd-closed")
	server := startKindServer(t)
	forward, status, body := startKindForward(t, server, "pod", "kh-fwd-closed", 9999, 0)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}
	if _, err := fetchForward(forward.LocalPort); err == nil {
		t.Fatal("expected the request to fail")
	}
	lost := waitForLost(t, server, forward.ID, 15*time.Second)
	if !strings.Contains(lost.Message, "9999") {
		t.Fatalf("message = %q", lost.Message)
	}
}

func TestKindLocalPortInUseIsReported(t *testing.T) {
	runWebPod(t, "kh-fwd-busy")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	server := startKindServer(t)
	_, status, body := startKindForward(t, server, "pod", "kh-fwd-busy", 8080, port)
	if status != http.StatusConflict || !strings.Contains(body, "portInUse") {
		t.Fatalf("status = %d: %s", status, body)
	}
	if forwards := server.forwards.List(); len(forwards) != 0 {
		t.Fatalf("forwards = %+v", forwards)
	}
}

func TestKindMissingPodIsReported(t *testing.T) {
	server := startKindServer(t)
	_, status, body := startKindForward(t, server, "pod", "kh-fwd-nope", 80, 0)
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "kh-fwd-nope no longer exists") {
		t.Fatalf("status = %d: %s", status, body)
	}
}

func TestKindShutdownFreesLocalPorts(t *testing.T) {
	runWebPod(t, "kh-fwd-quit")
	server := startKindServer(t)
	forward, status, body := startKindForward(t, server, "pod", "kh-fwd-quit", 8080, 0)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !portIsFree(forward.LocalPort) {
		t.Fatal("local port still held after shutdown")
	}
}
