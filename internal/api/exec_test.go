package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	utilexec "k8s.io/client-go/util/exec"

	"kubehack/internal/settings"
	"kubehack/internal/stream"
)

const execPath = "/api/v1/contexts/test/exec?namespace=default&pod=p1&container=app&cols=100&rows=30"

func startExecServer(t *testing.T, run stream.ExecRunner) *Server {
	t.Helper()
	server, err := New(Options{Token: "secret", RunShell: run})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return server
}

func dialExec(t *testing.T, ctx context.Context, server *Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, wsURL(server, execPath), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

// echoShell repeats input lines, and reports each terminal size it is given.
func echoShell(ctx context.Context, _ stream.ExecRequest, streams stream.ExecStreams) error {
	go func() {
		for {
			size := streams.Sizes.Next()
			if size == nil {
				return
			}
			fmt.Fprintf(streams.Stdout, "size %dx%d\n", size.Width, size.Height)
		}
	}()
	scanner := bufio.NewScanner(streams.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "exit" {
			return utilexec.CodeExitError{Err: errors.New("exit"), Code: 7}
		}
		fmt.Fprintf(streams.Stdout, "echo %s\n", scanner.Text())
	}
	return ctx.Err()
}

func readOutput(t *testing.T, ctx context.Context, conn *websocket.Conn, want string) {
	t.Helper()
	var seen strings.Builder
	for !strings.Contains(seen.String(), want) {
		kind, content, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q, saw %q: %v", want, seen.String(), err)
		}
		if kind != websocket.MessageBinary {
			t.Fatalf("unexpected text frame %s", content)
		}
		seen.Write(content)
	}
}

func TestExecRelaysInputOutputAndResize(t *testing.T) {
	server := startExecServer(t, echoShell)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialExec(t, ctx, server)

	readOutput(t, ctx, conn, "size 100x30")

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	readOutput(t, ctx, conn, "echo hello")

	resize, err := json.Marshal(execControl{Type: "resize", Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, resize); err != nil {
		t.Fatal(err)
	}
	readOutput(t, ctx, conn, "size 120x40")
}

func TestExecReportsExitCode(t *testing.T) {
	server := startExecServer(t, echoShell)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialExec(t, ctx, server)

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	for {
		kind, content, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind != websocket.MessageText {
			continue
		}
		var exit execExit
		if err := json.Unmarshal(content, &exit); err != nil {
			t.Fatal(err)
		}
		if exit.Type != "exit" || exit.Code != 7 {
			t.Errorf("exit = %+v", exit)
		}
		return
	}
}

func TestExecReportsStartFailure(t *testing.T) {
	server := startExecServer(t, func(context.Context, stream.ExecRequest, stream.ExecStreams) error {
		return errors.New("no such file or directory")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, content, err := dialExec(t, ctx, server).Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var exit execExit
	if err := json.Unmarshal(content, &exit); err != nil {
		t.Fatal(err)
	}
	if exit.Code != -1 || !strings.Contains(exit.Message, "no such file") {
		t.Errorf("exit = %+v", exit)
	}
}

func TestClosingTheSocketEndsTheShell(t *testing.T) {
	ended := make(chan struct{})
	hungUp := make(chan string, 1)
	var started string
	server, err := New(Options{
		Token: "secret",
		RunShell: func(ctx context.Context, request stream.ExecRequest, _ stream.ExecStreams) error {
			started = request.Session
			<-ctx.Done()
			close(ended)
			return ctx.Err()
		},
		HangupShell: func(_ context.Context, request stream.ExecRequest) error {
			hungUp <- request.Session
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialExec(t, ctx, server)

	time.Sleep(50 * time.Millisecond)
	conn.Close(websocket.StatusNormalClosure, "")

	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("shell kept running after the socket closed")
	}
	select {
	case session := <-hungUp:
		if session == "" || session != started {
			t.Errorf("hung up %q, started %q", session, started)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the dropped shell was never hung up")
	}
}

func TestShellThatExitsIsNotHungUp(t *testing.T) {
	hungUp := false
	server, err := New(Options{
		Token:    "secret",
		RunShell: func(context.Context, stream.ExecRequest, stream.ExecStreams) error { return nil },
		HangupShell: func(context.Context, stream.ExecRequest) error {
			hungUp = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialExec(t, ctx, server)
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if hungUp {
		t.Error("a shell that exited by itself was hung up")
	}
}

func TestExecIsBlockedInReadOnlyContexts(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetReadOnly("test", true); err != nil {
		t.Fatal(err)
	}
	started := false
	server, err := New(Options{
		Token:    "secret",
		Settings: store,
		RunShell: func(context.Context, stream.ExecRequest, stream.ExecStreams) error {
			started = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, wsURL(server, execPath), &websocket.DialOptions{
		Subprotocols: []string{wsProtocol, wsTokenPrefix + "secret"},
	})
	if err == nil {
		conn.CloseNow()
		t.Fatal("upgrade succeeded")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		t.Errorf("response = %+v", response)
	}
	if started {
		t.Error("a shell started in a read-only context")
	}
}
