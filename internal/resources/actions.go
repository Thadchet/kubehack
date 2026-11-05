package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

const restartAnnotation = "kubectl.kubernetes.io/restartedAt"

// ErrUnsupportedAction marks an action the resource type cannot perform.
var ErrUnsupportedAction = errors.New("unsupported action")

// Restartable reports whether a resource type rolls its pods when its template
// changes. These are the same types `kubectl rollout restart` accepts.
func Restartable(gvr schema.GroupVersionResource) bool {
	if gvr.Group != "apps" {
		return false
	}
	switch gvr.Resource {
	case "deployments", "statefulsets", "daemonsets":
		return true
	}
	return false
}

// Delete removes an object. The UID precondition means an object that was
// replaced under the same name since the user confirmed is left alone.
func Delete(ctx context.Context, client dynamic.Interface, ref ObjectRef, uid string) error {
	if uid == "" {
		return fmt.Errorf("%w: uid is required", ErrInvalidEdit)
	}
	id := types.UID(uid)
	return resourceFor(client, ref).Delete(ctx, ref.Name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &id},
	})
}

// Scale sets the replica count through the scale subresource, which also works
// for custom resources that support it.
func Scale(ctx context.Context, client dynamic.Interface, ref ObjectRef, replicas int32) error {
	if replicas < 0 {
		return fmt.Errorf("%w: replicas cannot be negative", ErrInvalidEdit)
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"replicas": replicas}})
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	_, err = resourceFor(client, ref).Patch(
		ctx, ref.Name, types.MergePatchType, patch,
		metav1.PatchOptions{FieldManager: FieldManager}, "scale",
	)
	return err
}

// Restart triggers a rolling restart the way `kubectl rollout restart` does, by
// changing an annotation on the pod template.
func Restart(ctx context.Context, client dynamic.Interface, ref ObjectRef) error {
	if !Restartable(ref.GVR) {
		return fmt.Errorf("%w: %s cannot be restarted", ErrUnsupportedAction, ref.GVR.Resource)
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{
			restartAnnotation: time.Now().UTC().Format(time.RFC3339),
		}},
	}}})
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	_, err = resourceFor(client, ref).Patch(
		ctx, ref.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: FieldManager},
	)
	return err
}
