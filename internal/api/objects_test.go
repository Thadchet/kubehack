package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"kubehack/internal/resources"
)

func TestClassifyApplyError(t *testing.T) {
	resource := schema.GroupResource{Resource: "pods"}
	fieldConflictError := &apierrors.StatusError{ErrStatus: metav1.Status{
		Status: metav1.StatusFailure,
		Code:   http.StatusConflict,
		Reason: metav1.StatusReasonConflict,
		Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{
			Type:    metav1.CauseTypeFieldManagerConflict,
			Field:   ".spec.replicas",
			Message: `conflict with "kubectl"`,
		}}},
	}}

	tests := []struct {
		name   string
		err    error
		status int
		kind   string
	}{
		{"field conflict", fieldConflictError, http.StatusConflict, "fieldConflict"},
		{"stale", apierrors.NewConflict(resource, "p1", errors.New("modified")), http.StatusConflict, "stale"},
		{"changed", resources.ErrStale, http.StatusConflict, "stale"},
		{"gone", apierrors.NewNotFound(resource, "p1"), http.StatusNotFound, "gone"},
		{"forbidden", apierrors.NewForbidden(resource, "p1", errors.New("no")), http.StatusForbidden, "forbidden"},
		{"rejected", apierrors.NewBadRequest("bad"), http.StatusUnprocessableEntity, "invalid"},
		{"edit", fmt.Errorf("%w: renamed", resources.ErrInvalidEdit), http.StatusUnprocessableEntity, "invalid"},
		{"other", errors.New("connection refused"), http.StatusBadGateway, "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, failure := classifyApplyError(test.err)
			if status != test.status || failure.Kind != test.kind {
				t.Errorf("got %d %q", status, failure.Kind)
			}
		})
	}

	_, failure := classifyApplyError(fieldConflictError)
	if len(failure.Conflicts) != 1 || failure.Conflicts[0].Field != ".spec.replicas" {
		t.Errorf("conflicts = %+v", failure.Conflicts)
	}
}

func TestApplyRoutesRequireToken(t *testing.T) {
	server := startWatchServer(t)
	response, err := http.Post(server.BaseURL()+"/api/v1/contexts/test/apply", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestApplyFromForeignOriginIsRejected(t *testing.T) {
	server := startWatchServer(t)
	request, err := http.NewRequest(http.MethodPost, server.BaseURL()+"/api/v1/contexts/test/apply", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Origin", "https://evil.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", response.StatusCode)
	}
}
