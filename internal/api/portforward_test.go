package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"kubehack/internal/settings"
	"kubehack/internal/stream"
)

const forwardBody = `{"namespace":"default","kind":"pod","name":"p1","port":80}`

type stubForwards struct {
	runs    atomic.Int32
	drop    chan struct{}
	failure error
}

func (f *stubForwards) Resolve(context.Context, stream.ForwardRequest) (stream.ForwardTarget, error) {
	return stream.ForwardTarget{Pod: "p1", Port: 80}, nil
}

func (f *stubForwards) Run(ctx context.Context, _ stream.ForwardRequest, _ stream.ForwardTarget, ready func(int)) error {
	f.runs.Add(1)
	if f.failure != nil {
		return f.failure
	}
	ready(4321)
	select {
	case <-f.drop:
		return errors.New("lost")
	case <-ctx.Done():
		return nil
	}
}

func (f *stubForwards) Check(context.Context, stream.ForwardRequest, stream.ForwardTarget) error {
	return nil
}

func startForwardServer(t *testing.T, driver stream.ForwardDriver) *Server {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{Token: "secret", Forwards: driver, Settings: store})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return server
}

func postForward(t *testing.T, server *Server, path, body string) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var content strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := response.Body.Read(buf)
		content.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return response, content.String()
}

const startPath = "/api/v1/contexts/test/portforwards"

func TestStartForwardReturnsLocalPort(t *testing.T) {
	server := startForwardServer(t, &stubForwards{drop: make(chan struct{})})
	response, body := postForward(t, server, startPath, forwardBody)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
	var forward stream.Forward
	if err := json.Unmarshal([]byte(body), &forward); err != nil {
		t.Fatal(err)
	}
	if forward.LocalPort != 4321 || forward.Context != "test" || forward.State != stream.ForwardActive {
		t.Fatalf("forward = %+v", forward)
	}
}

func TestStartForwardValidates(t *testing.T) {
	driver := &stubForwards{drop: make(chan struct{})}
	server := startForwardServer(t, driver)
	for name, body := range map[string]string{
		"not json":  `nope`,
		"no name":   `{"namespace":"d","kind":"pod","port":80}`,
		"bad kind":  `{"namespace":"d","kind":"node","name":"n","port":80}`,
		"no port":   `{"namespace":"d","kind":"pod","name":"p"}`,
		"big port":  `{"namespace":"d","kind":"pod","name":"p","port":70000}`,
		"bad local": `{"namespace":"d","kind":"pod","name":"p","port":80,"localPort":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if response, _ := postForward(t, server, startPath, body); response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
	if driver.runs.Load() != 0 {
		t.Fatal("invalid request reached the cluster")
	}
}

func TestStartForwardReportsPortInUse(t *testing.T) {
	server := startForwardServer(t, &stubForwards{failure: stream.ErrLocalPortInUse})
	response, body := postForward(t, server, startPath, forwardBody)
	if response.StatusCode != http.StatusConflict || !strings.Contains(body, "portInUse") {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
}

func TestNoForwardsRefusesNewForwards(t *testing.T) {
	driver := &stubForwards{}
	server := startForwardServer(t, driver)
	server.noForwards = true
	response, body := postForward(t, server, startPath, forwardBody)
	if response.StatusCode != http.StatusConflict || !strings.Contains(body, "Docker") {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
}

func TestReadOnlyBlocksForwards(t *testing.T) {
	driver := &stubForwards{drop: make(chan struct{}, 1)}
	server := startForwardServer(t, driver)

	response, body := postForward(t, server, startPath, forwardBody)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
	var forward stream.Forward
	if err := json.Unmarshal([]byte(body), &forward); err != nil {
		t.Fatal(err)
	}
	driver.drop <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := server.forwards.Get(forward.ID)
		if err == nil && current.State == stream.ForwardLost {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("forward was never lost")
		}
		time.Sleep(time.Millisecond)
	}

	if err := server.settings.SetReadOnly("test", true); err != nil {
		t.Fatal(err)
	}
	runs := driver.runs.Load()
	if response, _ := postForward(t, server, startPath, forwardBody); response.StatusCode != http.StatusForbidden {
		t.Fatalf("start status = %d", response.StatusCode)
	}
	restart := "/api/v1/portforwards/" + forward.ID + "/restart"
	if response, _ := postForward(t, server, restart, ""); response.StatusCode != http.StatusForbidden {
		t.Fatalf("restart status = %d", response.StatusCode)
	}
	if driver.runs.Load() != runs {
		t.Fatal("read-only context opened a forward")
	}

	// Closing a tunnel must stay possible, or it could not be got rid of.
	stop := "/api/v1/portforwards/" + forward.ID + "/stop"
	if response, _ := postForward(t, server, stop, ""); response.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d", response.StatusCode)
	}
}

func TestForwardRoutesNeedToken(t *testing.T) {
	server := startForwardServer(t, &stubForwards{drop: make(chan struct{})})
	response, err := http.Post(server.BaseURL()+startPath, "application/json", strings.NewReader(forwardBody))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestUnknownForward(t *testing.T) {
	server := startForwardServer(t, &stubForwards{drop: make(chan struct{})})
	for _, action := range []string{"stop", "restart"} {
		if response, _ := postForward(t, server, "/api/v1/portforwards/nope/"+action, ""); response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d", action, response.StatusCode)
		}
	}
}

func TestWatchForwardsPushesChanges(t *testing.T) {
	driver := &stubForwards{drop: make(chan struct{}, 1)}
	server := startForwardServer(t, driver)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(server, "/api/v1/portforwards/watch"), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	waitFor := func(check func([]stream.Forward) bool) {
		t.Helper()
		for {
			var forwards []stream.Forward
			if err := wsjson.Read(ctx, conn, &forwards); err != nil {
				t.Fatal(err)
			}
			if check(forwards) {
				return
			}
		}
	}

	waitFor(func(forwards []stream.Forward) bool { return len(forwards) == 0 })
	postForward(t, server, startPath, forwardBody)
	waitFor(func(forwards []stream.Forward) bool {
		return len(forwards) == 1 && forwards[0].State == stream.ForwardActive
	})
	driver.drop <- struct{}{}
	waitFor(func(forwards []stream.Forward) bool {
		return len(forwards) == 1 && forwards[0].State == stream.ForwardLost && forwards[0].Message != ""
	})
}
