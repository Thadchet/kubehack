package resources

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func storedPod() *unstructured.Unstructured {
	pod := object("v1", "Pod", "p1")
	pod.SetUID("uid-1")
	pod.SetGeneration(3)
	pod.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl"}})
	pod.SetAnnotations(map[string]string{lastAppliedAnnotation: "{}", "keep": "yes"})
	pod.Object["status"] = map[string]any{"phase": "Running"}
	return pod
}

var podRef = ObjectRef{GVR: podsGVR, Namespace: "default", Name: "p1"}

func TestLoadHidesServerFieldsAndKeepsLockValues(t *testing.T) {
	got, err := Load(context.Background(), fakeClient(storedPod()), podRef)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResourceVersion != "1" || got.UID != "uid-1" {
		t.Errorf("lock values = %q, %q", got.ResourceVersion, got.UID)
	}
	for _, hidden := range []string{"managedFields", "resourceVersion", "uid", "generation", "status", "last-applied"} {
		if strings.Contains(got.YAML, hidden) {
			t.Errorf("yaml contains %q:\n%s", hidden, got.YAML)
		}
	}
	if !strings.Contains(got.YAML, "keep: \"yes\"") {
		t.Errorf("yaml lost an annotation:\n%s", got.YAML)
	}
}

func TestParseSingleObject(t *testing.T) {
	if _, err := parseSingleObject("kind: Pod\nmetadata: {name: a}\n"); err != nil {
		t.Errorf("valid document: %v", err)
	}
	for name, text := range map[string]string{
		"empty":     "",
		"comment":   "# nothing here\n",
		"two docs":  "kind: Pod\n---\nkind: Secret\n",
		"bad yaml":  "kind: [unclosed\n",
		"not a map": "- one\n- two\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSingleObject(text); !errors.Is(err, ErrInvalidEdit) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestApplyRejectsEditsThatChangeTheTarget(t *testing.T) {
	valid := "apiVersion: v1\nkind: Pod\nmetadata: {name: p1, namespace: default}\n"
	for name, text := range map[string]string{
		"name":       strings.Replace(valid, "name: p1", "name: other", 1),
		"namespace":  strings.Replace(valid, "namespace: default", "namespace: prod", 1),
		"kind":       strings.Replace(valid, "kind: Pod", "kind: Secret", 1),
		"apiVersion": strings.Replace(valid, "apiVersion: v1", "apiVersion: v2", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Apply(context.Background(), fakeClient(storedPod()), ApplyRequest{
				Ref: podRef, YAML: text, ResourceVersion: "1", UID: "uid-1",
			})
			if !errors.Is(err, ErrInvalidEdit) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestApplyRequiresLockValues(t *testing.T) {
	text := "apiVersion: v1\nkind: Pod\nmetadata: {name: p1, namespace: default}\n"
	for _, request := range []ApplyRequest{
		{Ref: podRef, YAML: text, UID: "uid-1"},
		{Ref: podRef, YAML: text, ResourceVersion: "1"},
	} {
		if _, err := Apply(context.Background(), fakeClient(storedPod()), request); !errors.Is(err, ErrInvalidEdit) {
			t.Errorf("err = %v", err)
		}
	}
}

func TestEditableYAMLDropsEmptiedAnnotations(t *testing.T) {
	pod := storedPod()
	pod.SetAnnotations(map[string]string{lastAppliedAnnotation: "{}"})

	text, err := editableYAML(pod)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "annotations") {
		t.Errorf("yaml:\n%s", text)
	}
}

func TestApplyRejectsChangedObjects(t *testing.T) {
	text := "apiVersion: v1\nkind: Pod\nmetadata: {name: p1, namespace: default}\n"
	for name, request := range map[string]ApplyRequest{
		"version": {Ref: podRef, YAML: text, ResourceVersion: "0", UID: "uid-1", Force: true},
		"uid":     {Ref: podRef, YAML: text, ResourceVersion: "1", UID: "uid-2", Force: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Apply(context.Background(), fakeClient(storedPod()), request); !errors.Is(err, ErrStale) {
				t.Errorf("err = %v", err)
			}
		})
	}
}
