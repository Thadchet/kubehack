package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"kubehack/internal/stream"
)

const (
	defaultTailLines = 1000
	logMaxBatch      = 500
	logLineBuffer    = 1024
)

// logMessage is one WebSocket frame. Type is "lines", "end" or "error".
type logMessage struct {
	Type    string   `json:"type"`
	Lines   []string `json:"lines,omitempty"`
	Message string   `json:"message,omitempty"`
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	request := stream.LogRequest{
		Context:   r.PathValue("name"),
		Namespace: query.Get("namespace"),
		Pod:       query.Get("pod"),
		Container: query.Get("container"),
		Previous:  query.Get("previous") == "true",
		Follow:    query.Get("follow") == "true",
		TailLines: defaultTailLines,
	}
	if request.Namespace == "" || request.Pod == "" {
		http.Error(w, "namespace and pod are required", http.StatusBadRequest)
		return
	}
	if tail := query.Get("tail"); tail != "" {
		lines, err := strconv.ParseInt(tail, 10, 64)
		if err != nil || lines < 0 {
			http.Error(w, "tail must be a non-negative number", http.StatusBadRequest)
			return
		}
		request.TailLines = lines
	}

	conn, err := acceptWebSocket(w, r)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// Everything hangs off this context, so a closed tab stops the read from
	// the API server rather than leaving it running.
	ctx := conn.CloseRead(r.Context())
	if err := pumpLogs(ctx, conn, s.openLogs, request); err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
	}
}

// pumpLogs returns an error only when the client can no longer be written to.
func pumpLogs(ctx context.Context, conn *websocket.Conn, open stream.LogOpener, request stream.LogRequest) error {
	write := func(message logMessage) error {
		ctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer cancel()
		return wsjson.Write(ctx, conn, message)
	}

	reader, err := open(ctx, request)
	if err != nil {
		return write(logMessage{Type: "error", Message: err.Error()})
	}
	defer reader.Close()

	lines := make(chan string, logLineBuffer)
	scanResult := make(chan error, 1)
	go func() {
		defer close(lines)
		scanResult <- stream.ScanLines(reader, func(line string) bool {
			select {
			case lines <- line:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()

	var batch []string
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := write(logMessage{Type: "lines", Lines: batch})
		batch = nil
		return err
	}

	ticker := time.NewTicker(watchFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := flush(); err != nil {
				return err
			}
		case line, open := <-lines:
			if !open {
				if err := flush(); err != nil {
					return err
				}
				if err := <-scanResult; err != nil && ctx.Err() == nil {
					return write(logMessage{Type: "error", Message: err.Error()})
				}
				return write(logMessage{Type: "end"})
			}
			batch = append(batch, line)
			if len(batch) >= logMaxBatch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
}
