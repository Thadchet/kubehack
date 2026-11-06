package resources

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRestartable(t *testing.T) {
	for gvr, want := range map[schema.GroupVersionResource]bool{
		{Group: "apps", Version: "v1", Resource: "deployments"}:        true,
		{Group: "apps", Version: "v1", Resource: "statefulsets"}:       true,
		{Group: "apps", Version: "v1", Resource: "daemonsets"}:         true,
		{Group: "apps", Version: "v1", Resource: "replicasets"}:        false,
		{Version: "v1", Resource: "pods"}:                              false,
		{Group: "example.com", Version: "v1", Resource: "deployments"}: false,
	} {
		if got := Restartable(gvr); got != want {
			t.Errorf("%v = %v", gvr, got)
		}
	}
}

func TestRestartRejectsOtherResources(t *testing.T) {
	err := Restart(context.Background(), fakeClient(storedPod()), podRef)
	if !errors.Is(err, ErrUnsupportedAction) {
		t.Errorf("err = %v", err)
	}
}

func TestScaleRejectsNegativeReplicas(t *testing.T) {
	err := Scale(context.Background(), fakeClient(), podRef, -1)
	if !errors.Is(err, ErrInvalidEdit) {
		t.Errorf("err = %v", err)
	}
}

func TestDeleteRequiresUID(t *testing.T) {
	err := Delete(context.Background(), fakeClient(storedPod()), podRef, "")
	if !errors.Is(err, ErrInvalidEdit) {
		t.Errorf("err = %v", err)
	}
}
