package metrics

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

var (
	podsGVR  = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}
	nodesGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}
)

// fakeClient adds objects by resource, because the fake would otherwise derive
// "podmetrics" from the kind, and the real API calls that resource "pods".
func fakeClient(t *testing.T, objects ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{podsGVR: "PodMetricsList", nodesGVR: "NodeMetricsList"})
	for _, object := range objects {
		gvr := podsGVR
		if object.GetKind() == "NodeMetrics" {
			gvr = nodesGVR
		}
		if err := client.Tracker().Create(gvr, object, object.GetNamespace()); err != nil {
			t.Fatal(err)
		}
	}
	return client
}

func podMetrics(namespace, name string, containers ...any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "metrics.k8s.io/v1beta1",
		"kind":       "PodMetrics",
		"metadata":   map[string]any{"namespace": namespace, "name": name},
		"containers": containers,
	}}
}

func container(name, cpu, memory string) map[string]any {
	return map[string]any{"name": name, "usage": map[string]any{"cpu": cpu, "memory": memory}}
}

func TestPodUsageSumsContainers(t *testing.T) {
	client := fakeClient(t, podMetrics("n", "p1",
		container("app", "250m", "64Mi"),
		container("sidecar", "1500000n", "1024Ki"),
	))
	snapshot, err := Read(context.Background(), client, Pods, "")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Available || len(snapshot.Items) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	item := snapshot.Items[0]
	if item.CPU != 252 || item.Memory != 65<<20 {
		t.Fatalf("item = %+v", item)
	}
	if len(item.Containers) != 2 || item.Containers[0].Name != "app" || item.Containers[0].CPU != 250 {
		t.Fatalf("containers = %+v", item.Containers)
	}
}

func TestPodUsageFiltersNamespace(t *testing.T) {
	client := fakeClient(t,
		podMetrics("a", "p1", container("c", "1m", "1Mi")),
		podMetrics("b", "p2", container("c", "1m", "1Mi")),
	)
	snapshot, err := Read(context.Background(), client, Pods, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].Name != "p2" {
		t.Fatalf("items = %+v", snapshot.Items)
	}
}

func TestNodeUsage(t *testing.T) {
	client := fakeClient(t, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "metrics.k8s.io/v1beta1",
		"kind":       "NodeMetrics",
		"metadata":   map[string]any{"name": "n1"},
		"usage":      map[string]any{"cpu": "2", "memory": "1Gi"},
	}})
	snapshot, err := Read(context.Background(), client, Nodes, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].CPU != 2000 || snapshot.Items[0].Memory != 1<<30 {
		t.Fatalf("items = %+v", snapshot.Items)
	}
}

func TestUnreadableValueKeepsTheRest(t *testing.T) {
	client := fakeClient(t, podMetrics("n", "p1",
		container("bad", "lots", "many"),
		container("good", "5m", "1Mi"),
	))
	snapshot, err := Read(context.Background(), client, Pods, "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Items[0].CPU != 5 {
		t.Fatalf("item = %+v", snapshot.Items[0])
	}
}

func TestClusterFailuresAreDescribed(t *testing.T) {
	resource := schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"}
	for name, test := range map[string]struct {
		err  error
		want Reason
	}{
		"not installed": {apierrors.NewNotFound(resource, ""), NotInstalled},
		"not answering": {apierrors.NewServiceUnavailable("the server is currently unable to handle the request"), Unavailable},
		"no permission": {apierrors.NewForbidden(resource, "", errors.New("rbac")), Forbidden},
		"network":       {errors.New("connection refused"), Failed},
	} {
		t.Run(name, func(t *testing.T) {
			client := fakeClient(t)
			client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
				return true, nil, test.err
			})
			snapshot, err := Read(context.Background(), client, Pods, "")
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Available || snapshot.Reason != test.want || snapshot.Items == nil {
				t.Fatalf("snapshot = %+v", snapshot)
			}
		})
	}
}

func TestInvalidKind(t *testing.T) {
	if _, err := Read(context.Background(), fakeClient(t), Kind("services"), ""); !errors.Is(err, ErrKind) {
		t.Fatalf("err = %v", err)
	}
}
