package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kubehack/internal/helm"
	"kubehack/internal/settings"
)

type stubHelm struct {
	calls atomic.Int32
	err   error
}

func (h *stubHelm) List(string, string) ([]helm.Release, error) {
	return []helm.Release{{Name: "demo"}}, h.err
}

func (h *stubHelm) Detail(string, string, string) (helm.Detail, error) {
	return helm.Detail{}, h.err
}

func (h *stubHelm) Values(string, string, string) (string, error) {
	return "a: 1\n", h.err
}

func (h *stubHelm) Install(context.Context, string, helm.Change) (helm.Result, error) {
	h.calls.Add(1)
	return helm.Result{}, h.err
}

func (h *stubHelm) Upgrade(context.Context, string, helm.Change) (helm.Result, error) {
	h.calls.Add(1)
	return helm.Result{}, h.err
}

func (h *stubHelm) Rollback(string, helm.Target) error {
	h.calls.Add(1)
	return h.err
}

func (h *stubHelm) Uninstall(string, helm.Target) error {
	h.calls.Add(1)
	return h.err
}

func startHelmServer(t *testing.T, client helm.Client) *Server {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{Token: "secret", Helm: client, Settings: store})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return server
}

const (
	helmBase    = "/api/v1/contexts/test/helm/"
	installBody = `{"namespace":"d","name":"demo","chart":"repo/demo"}`
	upgradeBody = `{"namespace":"d","name":"demo","expectedRevision":1}`
	targetBody  = `{"namespace":"d","name":"demo","revision":1,"expectedRevision":2}`
)

var helmWrites = map[string]string{
	"install":   installBody,
	"upgrade":   upgradeBody,
	"rollback":  targetBody,
	"uninstall": targetBody,
}

func TestHelmWritesSucceed(t *testing.T) {
	client := &stubHelm{}
	server := startHelmServer(t, client)
	for action, body := range helmWrites {
		if response, content := postForward(t, server, helmBase+action, body); response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", action, response.StatusCode, content)
		}
	}
	if client.calls.Load() != 4 {
		t.Fatalf("calls = %d", client.calls.Load())
	}
}

func TestReadOnlyBlocksHelmWrites(t *testing.T) {
	client := &stubHelm{}
	server := startHelmServer(t, client)
	if err := server.settings.SetReadOnly("test", true); err != nil {
		t.Fatal(err)
	}
	for action, body := range helmWrites {
		if response, _ := postForward(t, server, helmBase+action, body); response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status = %d", action, response.StatusCode)
		}
	}
	if client.calls.Load() != 0 {
		t.Fatal("read-only context reached Helm")
	}

	request, err := http.NewRequest(http.MethodGet, server.BaseURL()+helmBase+"releases", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", response.StatusCode)
	}
}

func TestHelmWritesValidate(t *testing.T) {
	client := &stubHelm{}
	server := startHelmServer(t, client)
	for name, test := range map[string][2]string{
		"install without chart":       {"install", `{"namespace":"d","name":"demo"}`},
		"install with blank chart":    {"install", `{"namespace":"d","name":"demo","chart":"  "}`},
		"upgrade without revision":    {"upgrade", `{"namespace":"d","name":"demo"}`},
		"rollback without target":     {"rollback", `{"namespace":"d","name":"demo","expectedRevision":2}`},
		"uninstall without revision":  {"uninstall", `{"namespace":"d","name":"demo"}`},
		"uninstall without namespace": {"uninstall", `{"name":"demo","expectedRevision":1}`},
		"not json":                    {"install", `nope`},
	} {
		t.Run(name, func(t *testing.T) {
			if response, _ := postForward(t, server, helmBase+test[0], test[1]); response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
	if client.calls.Load() != 0 {
		t.Fatal("invalid request reached Helm")
	}
}

func TestHelmErrorsTellTheUIWhatHappened(t *testing.T) {
	for name, test := range map[string]struct {
		err    error
		status int
		kind   string
	}{
		"stale":   {helm.ErrStale, http.StatusConflict, "stale"},
		"gone":    {helm.ErrNotFound, http.StatusNotFound, "gone"},
		"invalid": {helm.InvalidError("bad"), http.StatusUnprocessableEntity, "invalid"},
		"other":   {errors.New("boom"), http.StatusBadGateway, "failed"},
	} {
		t.Run(name, func(t *testing.T) {
			server := startHelmServer(t, &stubHelm{err: test.err})
			response, body := postForward(t, server, helmBase+"upgrade", upgradeBody)
			if response.StatusCode != test.status || !strings.Contains(body, test.kind) {
				t.Fatalf("status = %d: %s", response.StatusCode, body)
			}
		})
	}
}

// blockingHelm holds an install open until its context ends.
type blockingHelm struct {
	stubHelm
	started  chan struct{}
	finished chan struct{}
}

func (h *blockingHelm) Install(ctx context.Context, _ string, _ helm.Change) (helm.Result, error) {
	close(h.started)
	<-ctx.Done()
	close(h.finished)
	return helm.Result{}, ctx.Err()
}

func TestOperationOutlivesClientButNotShutdown(t *testing.T) {
	client := &blockingHelm{started: make(chan struct{}), finished: make(chan struct{})}
	server := startHelmServer(t, client)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost,
		server.BaseURL()+helmBase+"install", strings.NewReader(installBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	go func() {
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
		}
	}()
	<-client.started
	cancelRequest()
	time.Sleep(100 * time.Millisecond)

	select {
	case <-client.finished:
		t.Fatal("closing the UI stopped a running operation")
	default:
	}

	server.Shutdown(context.Background())
	select {
	case <-client.finished:
	default:
		t.Fatal("shutdown returned while the operation was still running")
	}
}
