package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"kubehack/internal/stream"
)

const logsPath = "/api/v1/contexts/test/logs?namespace=default&pod=p1&container=app&follow=true&tail=5"

func startLogServer(t *testing.T, open stream.LogOpener) *Server {
	t.Helper()
	server, err := New(Options{Token: "secret", AllowedOrigins: []string{testOrigin}, OpenLogs: open})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return server
}

func dialLogs(t *testing.T, ctx context.Context, server *Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsURL(server, logsPath), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func readLogMessage(t *testing.T, ctx context.Context, conn *websocket.Conn) logMessage {
	t.Helper()
	_, body, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message logMessage
	if err := json.Unmarshal(body, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestLogsStreamLinesThenEnd(t *testing.T) {
	var got stream.LogRequest
	server := startLogServer(t, func(_ context.Context, request stream.LogRequest) (io.ReadCloser, error) {
		got = request
		return io.NopCloser(strings.NewReader("one\ntwo\n")), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialLogs(t, ctx, server)

	first := readLogMessage(t, ctx, conn)
	if first.Type != "lines" || strings.Join(first.Lines, ",") != "one,two" {
		t.Fatalf("first = %+v", first)
	}
	if second := readLogMessage(t, ctx, conn); second.Type != "end" {
		t.Errorf("second = %+v", second)
	}
	want := stream.LogRequest{
		Context: "test", Namespace: "default", Pod: "p1", Container: "app", Follow: true, TailLines: 5,
	}
	if got != want {
		t.Errorf("request = %+v", got)
	}
}

func TestLogsReportOpenFailure(t *testing.T) {
	server := startLogServer(t, func(context.Context, stream.LogRequest) (io.ReadCloser, error) {
		return nil, errors.New("previous terminated container not found")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	message := readLogMessage(t, ctx, dialLogs(t, ctx, server))
	if message.Type != "error" || !strings.Contains(message.Message, "previous terminated") {
		t.Errorf("message = %+v", message)
	}
}

// blockingLog stays open like a followed log until it is closed.
type blockingLog struct {
	closed chan struct{}
}

func (b *blockingLog) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.EOF
}

func (b *blockingLog) Close() error {
	close(b.closed)
	return nil
}

func TestClosingTheSocketStopsTheStream(t *testing.T) {
	log := &blockingLog{closed: make(chan struct{})}
	cancelled := make(chan struct{})
	server := startLogServer(t, func(ctx context.Context, _ stream.LogRequest) (io.ReadCloser, error) {
		go func() {
			<-ctx.Done()
			close(cancelled)
		}()
		return log, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialLogs(t, ctx, server)

	// The opener runs after the upgrade, so give it a moment before closing.
	time.Sleep(50 * time.Millisecond)
	conn.Close(websocket.StatusNormalClosure, "")

	for name, done := range map[string]chan struct{}{"context": cancelled, "reader": log.closed} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s was not released after the socket closed", name)
		}
	}
}

func TestLogsValidateParameters(t *testing.T) {
	server := startLogServer(t, nil)
	for _, path := range []string{
		"/api/v1/contexts/test/logs?namespace=default",
		"/api/v1/contexts/test/logs?pod=p1",
		"/api/v1/contexts/test/logs?namespace=default&pod=p1&tail=-1",
		"/api/v1/contexts/test/logs?namespace=default&pod=p1&tail=many",
	} {
		request, err := http.NewRequest(http.MethodGet, server.BaseURL()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d", path, response.StatusCode)
		}
	}
}

func TestLogsRejectMissingToken(t *testing.T) {
	server := startLogServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, wsURL(server, logsPath), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol},
	})
	if err == nil {
		conn.CloseNow()
		t.Fatal("upgrade succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Errorf("response = %+v", response)
	}
}
