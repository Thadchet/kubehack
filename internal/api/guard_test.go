package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const testOrigin = "wails://wails"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Options{Token: "secret", AllowedOrigins: []string{testOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.listener.Close() })
	return s
}

func TestGuard(t *testing.T) {
	s := newTestServer(t)
	validHost := s.listener.Addr().String()

	tests := []struct {
		name    string
		method  string
		host    string
		headers map[string]string
		want    int
	}{
		{name: "valid token", host: validHost, headers: map[string]string{"Authorization": "Bearer secret"}, want: http.StatusOK},
		{name: "valid token and origin", host: validHost, headers: map[string]string{"Authorization": "Bearer secret", "Origin": testOrigin}, want: http.StatusOK},
		{name: "websocket subprotocol token", host: validHost, headers: map[string]string{"Sec-WebSocket-Protocol": "kubehack, kubehack.token.secret"}, want: http.StatusOK},
		{name: "missing token", host: validHost, want: http.StatusUnauthorized},
		{name: "wrong token", host: validHost, headers: map[string]string{"Authorization": "Bearer nope"}, want: http.StatusUnauthorized},
		{name: "wrong subprotocol token", host: validHost, headers: map[string]string{"Sec-WebSocket-Protocol": "kubehack.token.nope"}, want: http.StatusUnauthorized},
		{name: "foreign origin with token", host: validHost, headers: map[string]string{"Authorization": "Bearer secret", "Origin": "https://evil.example"}, want: http.StatusForbidden},
		{name: "rebinding host with token", host: "evil.example", headers: map[string]string{"Authorization": "Bearer secret"}, want: http.StatusForbidden},
		{name: "preflight from app", method: http.MethodOptions, host: validHost, headers: map[string]string{"Origin": testOrigin}, want: http.StatusNoContent},
		{name: "preflight from foreign origin", method: http.MethodOptions, host: validHost, headers: map[string]string{"Origin": "https://evil.example"}, want: http.StatusForbidden},
		{name: "options without origin", method: http.MethodOptions, host: validHost, want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, "/api/v1/health", nil)
			req.Host = tt.host
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()
			s.http.Handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestListensOnLoopbackOnly(t *testing.T) {
	s := newTestServer(t)
	if got := s.listener.Addr().String(); got[:10] != "127.0.0.1:" {
		t.Fatalf("listener address = %s", got)
	}
}

func TestGeneratedTokensAreUnique(t *testing.T) {
	first, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.listener.Close()
	second, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.listener.Close()
	if len(first.Token()) != 64 || first.Token() == second.Token() {
		t.Fatalf("weak tokens: %q %q", first.Token(), second.Token())
	}
}

func TestContextRoutes(t *testing.T) {
	config := `apiVersion: v1
kind: Config
current-context: arn:aws:eks:ap-southeast-1:1:cluster/prod
clusters:
- name: c1
  cluster: {server: "http://127.0.0.1:1"}
users:
- name: u1
  user: {token: secret-value}
contexts:
- name: arn:aws:eks:ap-southeast-1:1:cluster/prod
  context: {cluster: c1, user: u1}
`
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)

	server, err := New(Options{Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })

	get := func(path string) (int, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.BaseURL()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer test-token")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(body)
	}

	status, body := get("/api/v1/contexts")
	if status != http.StatusOK || !strings.Contains(body, "cluster/prod") {
		t.Errorf("list: %d %s", status, body)
	}
	if strings.Contains(body, "secret-value") {
		t.Error("context list leaked a credential")
	}

	// Context names such as EKS ARNs contain slashes.
	escaped := url.PathEscape("arn:aws:eks:ap-southeast-1:1:cluster/prod")
	status, body = get("/api/v1/contexts/" + escaped + "/status")
	if status != http.StatusOK || !strings.Contains(body, `"reachable":false`) {
		t.Errorf("status: %d %s", status, body)
	}

	status, _ = get("/api/v1/contexts/nope/status")
	if status != http.StatusNotFound {
		t.Errorf("unknown context: %d", status)
	}
}

func TestGuardServesUIWithoutToken(t *testing.T) {
	s, err := New(Options{
		Token: "secret",
		UI:    fstest.MapFS{"index.html": {Data: []byte("page")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.listener.Close() })
	validHost := s.listener.Addr().String()

	tests := []struct {
		name string
		host string
		path string
		want int
	}{
		{name: "page", host: validHost, path: "/", want: http.StatusOK},
		{name: "api still needs the token", host: validHost, path: "/api/v1/health", want: http.StatusUnauthorized},
		{name: "page still checks host", host: "evil.test", path: "/", want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Host = test.host
			recorder := httptest.NewRecorder()
			s.http.Handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Errorf("status = %d, want %d", recorder.Code, test.want)
			}
		})
	}
}
