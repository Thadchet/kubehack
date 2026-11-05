package api

import (
	"encoding/json"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"kubehack/internal/resources"
)

// actionBody is the request for POST .../delete, .../scale and .../restart.
type actionBody struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
	Replicas  int32  `json:"replicas"`
}

func (b actionBody) ref() resources.ObjectRef {
	return resources.ObjectRef{
		GVR:       schema.GroupVersionResource{Group: b.Group, Version: b.Version, Resource: b.Resource},
		Namespace: b.Namespace,
		Name:      b.Name,
	}
}

// rejectReadOnly enforces read-only mode here rather than only hiding buttons,
// so that no UI path or bug can write to a protected context.
func (s *Server) rejectReadOnly(w http.ResponseWriter, r *http.Request) bool {
	name := r.PathValue("name")
	if !s.settings.IsReadOnly(name) {
		return false
	}
	writeJSON(w, http.StatusForbidden, applyFailure{
		Kind:    "readOnly",
		Message: name + " is read-only. Turn off read-only to change it.",
	})
	return true
}

type action func(r *http.Request, client dynamic.Interface, body actionBody) error

func (s *Server) handleAction(run action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.rejectReadOnly(w, r) {
			return
		}
		var body actionBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxApplyBody)).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if body.Version == "" || body.Resource == "" || body.Name == "" {
			http.Error(w, "version, resource and name are required", http.StatusBadRequest)
			return
		}
		client, err := s.newClient(r.PathValue("name"))
		if err != nil {
			writeClusterError(w, err)
			return
		}
		if err := run(r, client, body); err != nil {
			writeApplyFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"done": true})
	}
}

func deleteObject(r *http.Request, client dynamic.Interface, body actionBody) error {
	return resources.Delete(r.Context(), client, body.ref(), body.UID)
}

func scaleObject(r *http.Request, client dynamic.Interface, body actionBody) error {
	return resources.Scale(r.Context(), client, body.ref(), body.Replicas)
}

func restartObject(r *http.Request, client dynamic.Interface, body actionBody) error {
	return resources.Restart(r.Context(), client, body.ref())
}

type settingsResponse struct {
	ReadOnlyContexts []string `json:"readOnlyContexts"`
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, settingsResponse{ReadOnlyContexts: s.settings.ReadOnlyContexts()})
}

func (s *Server) setReadOnly(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReadOnly *bool `json:"readOnly"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxApplyBody)).Decode(&body)
	if err != nil || body.ReadOnly == nil {
		http.Error(w, "readOnly is required", http.StatusBadRequest)
		return
	}
	if err := s.settings.SetReadOnly(r.PathValue("name"), *body.ReadOnly); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.getSettings(w, r)
}
