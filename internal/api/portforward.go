package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"kubehack/internal/cluster"
	"kubehack/internal/stream"
)

const maxForwardBody = 4 << 10

func validForward(request stream.ForwardRequest) bool {
	return request.Namespace != "" && request.Name != "" &&
		(request.Kind == stream.ForwardPod || request.Kind == stream.ForwardService) &&
		request.Port >= 1 && request.Port <= 65535 &&
		request.LocalPort >= 0 && request.LocalPort <= 65535
}

func writeForwardError(w http.ResponseWriter, err error) {
	failure := applyFailure{Kind: "failed", Message: err.Error()}
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, stream.ErrForwardNotFound), errors.Is(err, cluster.ErrContextNotFound), apierrors.IsNotFound(err):
		failure.Kind, status = "gone", http.StatusNotFound
	case errors.Is(err, stream.ErrLocalPortInUse):
		failure.Kind, status = "portInUse", http.StatusConflict
	case errors.Is(err, stream.ErrForwardBusy):
		status = http.StatusConflict
	case errors.Is(err, stream.ErrForwardTarget):
		failure.Kind, status = "invalid", http.StatusUnprocessableEntity
	case apierrors.IsForbidden(err):
		failure.Kind, status = "forbidden", http.StatusForbidden
	}
	writeJSON(w, status, failure)
}

// startForward is blocked in read-only mode for the same reason as a shell: the
// tunnel reaches whatever the pod serves, and that can change data.
func (s *Server) startForward(w http.ResponseWriter, r *http.Request) {
	if s.noForwards {
		writeJSON(w, http.StatusConflict, applyFailure{
			Kind:    "invalid",
			Message: "Port forwards aren't available when Kubehack runs in Docker. Use the desktop app, or kubectl port-forward.",
		})
		return
	}
	if s.rejectReadOnly(w, r) {
		return
	}
	var request stream.ForwardRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxForwardBody)).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	request.Context = r.PathValue("name")
	if !validForward(request) {
		http.Error(w, "namespace, kind, name and valid ports are required", http.StatusBadRequest)
		return
	}
	forward, err := s.forwards.Start(request)
	if err != nil {
		writeForwardError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, forward)
}

func (s *Server) restartForward(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := s.forwards.Get(id)
	if err != nil {
		writeForwardError(w, err)
		return
	}
	if s.writeIfReadOnly(w, current.Context) {
		return
	}
	forward, err := s.forwards.Restart(id)
	if err != nil {
		writeForwardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, forward)
}

// stopForward is allowed in read-only mode, because it only closes a tunnel.
func (s *Server) stopForward(w http.ResponseWriter, r *http.Request) {
	if err := s.forwards.Stop(r.PathValue("id")); err != nil {
		writeForwardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// watchForwards sends the whole list on connect and after every change. The list
// is short, and a client that reconnects needs no position to resume from.
func (s *Server) watchForwards(w http.ResponseWriter, r *http.Request) {
	conn, err := acceptWebSocket(w, r)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	changes, unsubscribe := s.forwards.Subscribe()
	defer unsubscribe()

	// Reading is what notices that the client has gone.
	ctx := conn.CloseRead(r.Context())
	for {
		if err := writeForwards(ctx, conn, s.forwards.List()); err != nil {
			return
		}
		select {
		case <-changes:
		case <-ctx.Done():
			conn.Close(websocket.StatusGoingAway, "")
			return
		}
	}
}

func writeForwards(ctx context.Context, conn *websocket.Conn, forwards []stream.Forward) error {
	ctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return wsjson.Write(ctx, conn, forwards)
}
