package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"kubehack/internal/cluster"
	"kubehack/internal/helm"
)

const maxHelmBody = 1 << 20

func writeHelmError(w http.ResponseWriter, err error) {
	failure := applyFailure{Kind: "failed", Message: err.Error()}
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, helm.ErrNotFound), errors.Is(err, cluster.ErrContextNotFound):
		failure.Kind, status = "gone", http.StatusNotFound
	case errors.Is(err, helm.ErrStale):
		failure.Kind, status = "stale", http.StatusConflict
	case errors.Is(err, helm.ErrInvalid):
		failure.Kind, status = "invalid", http.StatusUnprocessableEntity
	case apierrors.IsForbidden(err):
		failure.Kind, status = "forbidden", http.StatusForbidden
	}
	writeJSON(w, status, failure)
}

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	releases, err := s.helm.List(r.PathValue("name"), r.URL.Query().Get("namespace"))
	if err != nil {
		writeHelmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

func (s *Server) releaseDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := s.helm.Detail(r.PathValue("name"), r.PathValue("namespace"), r.PathValue("release"))
	if err != nil {
		writeHelmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) releaseValues(w http.ResponseWriter, r *http.Request) {
	values, err := s.helm.Values(r.PathValue("name"), r.PathValue("namespace"), r.PathValue("release"))
	if err != nil {
		writeHelmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"values": values})
}

// operate runs a Helm change that must not stop halfway because the UI went
// away: a release left in a pending state blocks every later operation. It ends
// only when the app quits, and Helm then records the release as failed.
func (s *Server) operate(run func(ctx context.Context)) {
	s.sessions.Add(1)
	defer s.sessions.Done()
	run(s.baseCtx)
}

func decodeHelm(w http.ResponseWriter, r *http.Request, body any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHelmBody)).Decode(body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) installRelease(w http.ResponseWriter, r *http.Request) {
	if s.rejectReadOnly(w, r) {
		return
	}
	var change helm.Change
	if !decodeHelm(w, r, &change) {
		return
	}
	if change.Namespace == "" || change.Name == "" || strings.TrimSpace(change.Chart) == "" {
		http.Error(w, "namespace, name and chart are required", http.StatusBadRequest)
		return
	}
	s.operate(func(ctx context.Context) {
		result, err := s.helm.Install(ctx, r.PathValue("name"), change)
		if err != nil {
			writeHelmError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
}

func (s *Server) upgradeRelease(w http.ResponseWriter, r *http.Request) {
	if s.rejectReadOnly(w, r) {
		return
	}
	var change helm.Change
	if !decodeHelm(w, r, &change) {
		return
	}
	if change.Namespace == "" || change.Name == "" || change.ExpectedRevision < 1 {
		http.Error(w, "namespace, name and expectedRevision are required", http.StatusBadRequest)
		return
	}
	s.operate(func(ctx context.Context) {
		result, err := s.helm.Upgrade(ctx, r.PathValue("name"), change)
		if err != nil {
			writeHelmError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
}

func (s *Server) rollbackRelease(w http.ResponseWriter, r *http.Request) {
	s.changeTarget(w, r, true, s.helm.Rollback)
}

func (s *Server) uninstallRelease(w http.ResponseWriter, r *http.Request) {
	s.changeTarget(w, r, false, s.helm.Uninstall)
}

func (s *Server) changeTarget(
	w http.ResponseWriter,
	r *http.Request,
	needsRevision bool,
	run func(contextName string, target helm.Target) error,
) {
	if s.rejectReadOnly(w, r) {
		return
	}
	var target helm.Target
	if !decodeHelm(w, r, &target) {
		return
	}
	if target.Namespace == "" || target.Name == "" || target.ExpectedRevision < 1 ||
		(needsRevision && target.Revision < 1) {
		http.Error(w, "namespace, name and revisions are required", http.StatusBadRequest)
		return
	}
	s.operate(func(context.Context) {
		if err := run(r.PathValue("name"), target); err != nil {
			writeHelmError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "done"})
	})
}
