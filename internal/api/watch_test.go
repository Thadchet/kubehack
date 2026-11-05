package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

const watchPath = "/api/v1/contexts/test/watch?version=v1&resource=pods"

func startWatchServer(t *testing.T) *Server {
	t.Helper()
	pod := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "p1", "namespace": "default"},
	}}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{{Version: "v1", Resource: "pods"}: "PodList"}, pod)

	server, err := New(Options{
		Token:          "secret",
		AllowedOrigins: []string{testOrigin},
		NewClient:      func(string) (dynamic.Interface, error) { return client, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return server
}

func wsURL(server *Server, path string) string {
	return strings.Replace(server.BaseURL(), "http://", "ws://", 1) + path
}

func TestWatchStreamsStateOverWebSocket(t *testing.T) {
	server := startWatchServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(server, watchPath), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + "secret"},
		HTTPHeader:   http.Header{"Origin": {testOrigin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	if conn.Subprotocol() != wsProtocol {
		t.Errorf("subprotocol = %q, must not echo the token", conn.Subprotocol())
	}

	read := func() map[string]any {
		t.Helper()
		_, body, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}

	first := read()
	if first["type"] != "events" {
		t.Fatalf("first message = %v", first)
	}
	event := first["events"].([]any)[0].(map[string]any)
	if event["op"] != "added" {
		t.Errorf("event = %v", event)
	}
	if second := read(); second["type"] != "synced" {
		t.Errorf("second message = %v", second)
	}
}

func TestWatchRejectsUnauthorizedUpgrades(t *testing.T) {
	server := startWatchServer(t)
	tests := []struct {
		name      string
		protocols []string
		origin    string
		want      int
	}{
		{"no token", []string{wsProtocol}, testOrigin, http.StatusUnauthorized},
		{"wrong token", []string{wsProtocol, wsTokenPrefix + "wrong"}, testOrigin, http.StatusUnauthorized},
		{"foreign origin", []string{wsProtocol, wsTokenPrefix + "secret"}, "https://evil.example", http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, wsURL(server, watchPath), &websocket.DialOptions{
				Subprotocols: test.protocols,
				HTTPHeader:   http.Header{"Origin": {test.origin}},
			})
			if err == nil {
				conn.CloseNow()
				t.Fatal("upgrade succeeded")
			}
			if response == nil || response.StatusCode != test.want {
				t.Errorf("response = %+v, want status %d", response, test.want)
			}
		})
	}
}

func TestWatchRequiresResource(t *testing.T) {
	server := startWatchServer(t)
	request, err := http.NewRequest(http.MethodGet, server.BaseURL()+"/api/v1/contexts/test/watch", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestShutdownEndsOpenWatches(t *testing.T) {
	server := startWatchServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(server, watchPath), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("connection stayed open after shutdown")
			}
			return
		}
	}
}
