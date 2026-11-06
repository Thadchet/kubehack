package api

import (
	"encoding/json"
	"errors"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"kubehack/internal/cluster"
	"kubehack/internal/resources"
)

const maxApplyBody = 8 << 20

// applyBody is the request for POST .../apply. Force is only ever set after the
// user has seen the field conflicts it would override.
type applyBody struct {
	Group           string `json:"group"`
	Version         string `json:"version"`
	Resource        string `json:"resource"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	YAML            string `json:"yaml"`
	ResourceVersion string `json:"resourceVersion"`
	UID             string `json:"uid"`
	DryRun          bool   `json:"dryRun"`
	Force           bool   `json:"force"`
}

type fieldConflict struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// applyFailure tells the UI which recovery to offer. Kind is "fieldConflict",
// "stale", "gone", "invalid", "forbidden", "readOnly" or "failed".
type applyFailure struct {
	Kind      string          `json:"kind"`
	Message   string          `json:"message"`
	Conflicts []fieldConflict `json:"conflicts,omitempty"`
}

func (s *Server) loadObject(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	ref := resources.ObjectRef{
		GVR: schema.GroupVersionResource{
			Group:    query.Get("group"),
			Version:  query.Get("version"),
			Resource: query.Get("resource"),
		},
		Namespace: query.Get("namespace"),
		Name:      query.Get("name"),
	}
	if ref.GVR.Version == "" || ref.GVR.Resource == "" || ref.Name == "" {
		http.Error(w, "version, resource and name are required", http.StatusBadRequest)
		return
	}

	client, err := s.newClient(r.PathValue("name"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	object, err := resources.Load(r.Context(), client, ref)
	if err != nil {
		writeApplyFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) applyObject(w http.ResponseWriter, r *http.Request) {
	var body applyBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxApplyBody)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Version == "" || body.Resource == "" || body.Name == "" {
		http.Error(w, "version, resource and name are required", http.StatusBadRequest)
		return
	}
	// A dry run changes nothing, so previews stay available in read-only mode.
	if !body.DryRun && s.rejectReadOnly(w, r) {
		return
	}

	client, err := s.newClient(r.PathValue("name"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	result, err := resources.Apply(r.Context(), client, resources.ApplyRequest{
		Ref: resources.ObjectRef{
			GVR:       schema.GroupVersionResource{Group: body.Group, Version: body.Version, Resource: body.Resource},
			Namespace: body.Namespace,
			Name:      body.Name,
		},
		YAML:            body.YAML,
		ResourceVersion: body.ResourceVersion,
		UID:             body.UID,
		DryRun:          body.DryRun,
		Force:           body.Force,
	})
	if err != nil {
		writeApplyFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeApplyFailure(w http.ResponseWriter, err error) {
	status, failure := classifyApplyError(err)
	writeJSON(w, status, failure)
}

func classifyApplyError(err error) (int, applyFailure) {
	failure := applyFailure{Kind: "failed", Message: err.Error()}
	switch {
	case errors.Is(err, cluster.ErrContextNotFound):
		return http.StatusNotFound, failure
	case errors.Is(err, resources.ErrInvalidEdit), errors.Is(err, resources.ErrUnsupportedAction):
		failure.Kind = "invalid"
		return http.StatusUnprocessableEntity, failure
	case errors.Is(err, resources.ErrStale):
		failure.Kind = "stale"
		return http.StatusConflict, failure
	case apierrors.IsNotFound(err):
		failure.Kind = "gone"
		return http.StatusNotFound, failure
	case apierrors.IsForbidden(err):
		failure.Kind = "forbidden"
		return http.StatusForbidden, failure
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		failure.Kind = "invalid"
		return http.StatusUnprocessableEntity, failure
	case apierrors.IsConflict(err):
		failure.Conflicts = fieldConflicts(err)
		failure.Kind = "stale"
		if len(failure.Conflicts) > 0 {
			failure.Kind = "fieldConflict"
		}
		return http.StatusConflict, failure
	}
	return http.StatusBadGateway, failure
}

// fieldConflicts separates "another manager owns this field" from "the object
// changed", because both arrive as 409 and they need different responses.
func fieldConflicts(err error) []fieldConflict {
	var status apierrors.APIStatus
	if !errors.As(err, &status) || status.Status().Details == nil {
		return nil
	}
	var conflicts []fieldConflict
	for _, cause := range status.Status().Details.Causes {
		if cause.Type == metav1.CauseTypeFieldManagerConflict {
			conflicts = append(conflicts, fieldConflict{Field: cause.Field, Message: cause.Message})
		}
	}
	return conflicts
}
