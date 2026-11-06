package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	utilexec "k8s.io/client-go/util/exec"

	"kubehack/internal/stream"
)

const (
	// Pasting into a terminal arrives as one frame.
	execReadLimit = 1 << 20

	defaultCols = 80
	defaultRows = 24

	hangupTimeout = 5 * time.Second
)

// execControl is a text frame from the UI. Binary frames are keystrokes.
type execControl struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// execExit is the text frame sent when the shell ends. Binary frames from the
// server are terminal output.
type execExit struct {
	Type    string `json:"type"`
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

// socketWriter sends terminal output as binary frames, because output can split
// a multi-byte character across writes.
type socketWriter struct {
	ctx  context.Context
	conn *websocket.Conn
}

func (w socketWriter) Write(content []byte) (int, error) {
	ctx, cancel := context.WithTimeout(w.ctx, wsWriteTimeout)
	defer cancel()
	if err := w.conn.Write(ctx, websocket.MessageBinary, content); err != nil {
		return 0, err
	}
	return len(content), nil
}

func parseDimension(value string, fallback uint16) uint16 {
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil || parsed == 0 {
		return fallback
	}
	return uint16(parsed)
}

func (s *Server) execShell(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	request := stream.ExecRequest{
		Context:   r.PathValue("name"),
		Namespace: query.Get("namespace"),
		Pod:       query.Get("pod"),
		Container: query.Get("container"),
	}
	if request.Namespace == "" || request.Pod == "" {
		http.Error(w, "namespace and pod are required", http.StatusBadRequest)
		return
	}
	// A shell can change anything the container can reach.
	if s.rejectReadOnly(w, r) {
		return
	}

	session := make([]byte, 16)
	if _, err := rand.Read(session); err != nil {
		http.Error(w, "could not start a session", http.StatusInternalServerError)
		return
	}
	request.Session = hex.EncodeToString(session)

	conn, err := acceptWebSocket(w, r)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(execReadLimit)

	// Shutdown waits for this, so quitting the app still ends open shells.
	s.sessions.Add(1)
	defer s.sessions.Done()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sizes := stream.NewSizeQueue(ctx.Done())
	sizes.Set(parseDimension(query.Get("cols"), defaultCols), parseDimension(query.Get("rows"), defaultRows))

	stdin, stdinWriter := io.Pipe()
	defer stdin.Close()
	go func() {
		// Ending the session here is what stops the remote shell when the tab
		// closes.
		defer cancel()
		defer stdinWriter.Close()
		pumpTerminalInput(ctx, conn, stdinWriter, sizes)
	}()

	err = s.runShell(ctx, request, stream.ExecStreams{
		Stdin:  stdin,
		Stdout: socketWriter{ctx: ctx, conn: conn},
		Sizes:  sizes,
	})
	if ctx.Err() != nil {
		s.endShell(request)
		return
	}

	writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer writeCancel()
	if wsjson.Write(writeCtx, conn, exitMessage(err)) == nil {
		conn.Close(websocket.StatusNormalClosure, "")
	}
}

// endShell uses its own context because the session's has already ended. Failing
// is expected if the pod has gone, and there is nothing further to try.
func (s *Server) endShell(request stream.ExecRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), hangupTimeout)
	defer cancel()
	if err := s.hangupShell(ctx, request); err != nil {
		log.Printf("end shell in pod %s: %v", request.Pod, err)
	}
}

func pumpTerminalInput(ctx context.Context, conn *websocket.Conn, stdin io.Writer, sizes *stream.SizeQueue) {
	for {
		kind, content, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageBinary {
			if _, err := stdin.Write(content); err != nil {
				return
			}
			continue
		}
		var control execControl
		if json.Unmarshal(content, &control) == nil && control.Type == "resize" &&
			control.Cols > 0 && control.Rows > 0 {
			sizes.Set(control.Cols, control.Rows)
		}
	}
}

func exitMessage(err error) execExit {
	message := execExit{Type: "exit"}
	if err == nil {
		return message
	}
	var exit utilexec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		message.Code = exit.ExitStatus()
		return message
	}
	message.Code = -1
	message.Message = err.Error()
	return message
}
