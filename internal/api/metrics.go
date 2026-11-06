package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"kubehack/internal/metrics"
)

const metricsTimeout = 10 * time.Second

func (s *Server) readMetrics(w http.ResponseWriter, r *http.Request) {
	client, err := s.newClient(r.PathValue("name"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), metricsTimeout)
	defer cancel()

	query := r.URL.Query()
	snapshot, err := metrics.Read(ctx, client, metrics.Kind(query.Get("kind")), query.Get("namespace"))
	if errors.Is(err, metrics.ErrKind) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		writeClusterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
