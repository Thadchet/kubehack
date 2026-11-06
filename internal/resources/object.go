package resources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// FieldManager is the owner name recorded for fields this app applies.
const FieldManager = "kubehack"

// ErrInvalidEdit marks YAML that cannot be applied to the object being edited.
var ErrInvalidEdit = errors.New("invalid edit")

// ErrStale means the object changed after the edit started.
var ErrStale = errors.New("the object changed after it was opened")

// ObjectRef identifies one object.
type ObjectRef struct {
	GVR       schema.GroupVersionResource
	Namespace string
	Name      string
}

// EditableObject is an object prepared for editing. ResourceVersion and UID
// travel beside the YAML rather than in it, so deleting a line in the editor
// cannot turn off the overwrite protection they provide.
type EditableObject struct {
	YAML            string `json:"yaml"`
	ResourceVersion string `json:"resourceVersion"`
	UID             string `json:"uid"`
}

// ApplyRequest is an edit to apply. ResourceVersion and UID come from the
// EditableObject the edit started from.
type ApplyRequest struct {
	Ref             ObjectRef
	YAML            string
	ResourceVersion string
	UID             string
	DryRun          bool
	Force           bool
}

// ApplyResult holds the object before and after, in the same form as the
// editor, so that comparing them shows only what the edit changes.
type ApplyResult struct {
	Before  string `json:"before"`
	After   string `json:"after"`
	Applied bool   `json:"applied"`
}

func resourceFor(client dynamic.Interface, ref ObjectRef) dynamic.ResourceInterface {
	return client.Resource(ref.GVR).Namespace(ref.Namespace)
}

// Load fetches the real object, including Secret values, which the watch stream
// never carries.
func Load(ctx context.Context, client dynamic.Interface, ref ObjectRef) (EditableObject, error) {
	object, err := resourceFor(client, ref).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return EditableObject{}, err
	}
	text, err := editableYAML(object)
	if err != nil {
		return EditableObject{}, err
	}
	return EditableObject{
		YAML:            text,
		ResourceVersion: object.GetResourceVersion(),
		UID:             string(object.GetUID()),
	}, nil
}

// Apply runs a server-side apply. The current object is read first because
// apply creates what does not exist, which would silently bring back an object
// deleted while the editor was open.
func Apply(ctx context.Context, client dynamic.Interface, request ApplyRequest) (ApplyResult, error) {
	if request.ResourceVersion == "" || request.UID == "" {
		return ApplyResult{}, fmt.Errorf("%w: resourceVersion and uid are required", ErrInvalidEdit)
	}
	edited, err := parseSingleObject(request.YAML)
	if err != nil {
		return ApplyResult{}, err
	}

	target := resourceFor(client, request.Ref)
	current, err := target.Get(ctx, request.Ref.Name, metav1.GetOptions{})
	if err != nil {
		return ApplyResult{}, err
	}
	if err := checkSameObject(edited, current); err != nil {
		return ApplyResult{}, err
	}
	// The API server checks field ownership first, so without this a stale edit
	// reports a field conflict and invites a force that cannot succeed.
	if current.GetResourceVersion() != request.ResourceVersion || string(current.GetUID()) != request.UID {
		return ApplyResult{}, ErrStale
	}

	// These still matter: they close the gap between the read above and the
	// write, because the API server rejects the apply if either no longer matches.
	edited.SetResourceVersion(request.ResourceVersion)
	edited.SetUID(types.UID(request.UID))
	edited.SetManagedFields(nil)

	options := metav1.ApplyOptions{FieldManager: FieldManager, Force: request.Force}
	if request.DryRun {
		options.DryRun = []string{metav1.DryRunAll}
	}
	result, err := target.Apply(ctx, request.Ref.Name, edited, options)
	if err != nil {
		return ApplyResult{}, err
	}

	before, err := editableYAML(current)
	if err != nil {
		return ApplyResult{}, err
	}
	after, err := editableYAML(result)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Before: before, After: after, Applied: !request.DryRun}, nil
}

// editableYAML leaves out what the server owns. Sending these fields back would
// fail or claim ownership of them. Omitting last-applied also keeps a second
// copy of Secret values out of the editor.
func editableYAML(object *unstructured.Unstructured) (string, error) {
	clean := object.DeepCopy()
	delete(clean.Object, "status")
	for _, field := range []string{
		"managedFields", "resourceVersion", "uid", "generation", "creationTimestamp",
		"selfLink", "deletionTimestamp", "deletionGracePeriodSeconds",
	} {
		unstructured.RemoveNestedField(clean.Object, "metadata", field)
	}
	annotations := clean.GetAnnotations()
	delete(annotations, lastAppliedAnnotation)
	if len(annotations) == 0 {
		unstructured.RemoveNestedField(clean.Object, "metadata", "annotations")
	} else {
		clean.SetAnnotations(annotations)
	}

	text, err := yaml.Marshal(clean.Object)
	if err != nil {
		return "", fmt.Errorf("encode yaml: %w", err)
	}
	return string(text), nil
}

// parseSingleObject rejects extra documents, which a plain parse would drop
// without saying so.
func parseSingleObject(text string) (*unstructured.Unstructured, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(text), 4096)

	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEdit, err)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: the document is empty", ErrInvalidEdit)
	}

	var extra map[string]any
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrInvalidEdit, err)
	default:
		return nil, fmt.Errorf("%w: only one document can be applied here", ErrInvalidEdit)
	}
	return &unstructured.Unstructured{Object: fields}, nil
}

// checkSameObject stops an edit from turning into a change to another object,
// which is what renaming it in the YAML would do.
func checkSameObject(edited, current *unstructured.Unstructured) error {
	for _, check := range []struct{ label, got, want string }{
		{"apiVersion", edited.GetAPIVersion(), current.GetAPIVersion()},
		{"kind", edited.GetKind(), current.GetKind()},
		{"metadata.name", edited.GetName(), current.GetName()},
		{"metadata.namespace", edited.GetNamespace(), current.GetNamespace()},
	} {
		if check.got != check.want {
			return fmt.Errorf("%w: %s must stay %q", ErrInvalidEdit, check.label, check.want)
		}
	}
	return nil
}
