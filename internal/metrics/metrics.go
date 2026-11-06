// Package metrics reads CPU and memory usage from metrics-server.
package metrics

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Kind is what usage can be read for.
type Kind string

// Metrics exist for pods and nodes only.
const (
	Pods  Kind = "pods"
	Nodes Kind = "nodes"
)

// Reason says why usage is unavailable. Clusters without metrics-server are
// normal, so this is an answer rather than an error.
type Reason string

// NotInstalled, Unavailable and Forbidden need different advice in the UI.
const (
	NotInstalled Reason = "notInstalled"
	Unavailable  Reason = "unavailable"
	Forbidden    Reason = "forbidden"
	Failed       Reason = "failed"
)

// Usage is what a container, pod or node is using now.
type Usage struct {
	// CPU is in millicores.
	CPU int64 `json:"cpu"`
	// Memory is in bytes.
	Memory int64 `json:"memory"`
}

// ContainerUsage is the usage of one container in a pod.
type ContainerUsage struct {
	Usage
	Name string `json:"name"`
}

// Item is the usage of one pod or node. A pod's usage is the sum of its
// containers.
type Item struct {
	Usage
	Namespace  string           `json:"namespace,omitempty"`
	Name       string           `json:"name"`
	Containers []ContainerUsage `json:"containers,omitempty"`
}

// Snapshot is the result of one read.
type Snapshot struct {
	Available bool   `json:"available"`
	Reason    Reason `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Items     []Item `json:"items"`
}

// ErrKind means usage cannot be read for that kind.
var ErrKind = errors.New("metrics exist for pods and nodes only")

// Read returns current usage. It only returns an error for an invalid kind:
// anything the cluster reports is described in the Snapshot, so that a cluster
// without metrics-server cannot break the screen showing its pods.
func Read(ctx context.Context, client dynamic.Interface, kind Kind, namespace string) (Snapshot, error) {
	if kind != Pods && kind != Nodes {
		return Snapshot{}, ErrKind
	}
	gvr := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: string(kind)}

	var reader dynamic.ResourceInterface = client.Resource(gvr)
	if kind == Pods && namespace != "" {
		reader = client.Resource(gvr).Namespace(namespace)
	}
	list, err := reader.List(ctx, metav1.ListOptions{})
	if err != nil {
		return unavailable(err), nil
	}

	items := make([]Item, 0, len(list.Items))
	for i := range list.Items {
		if kind == Pods {
			items = append(items, podItem(&list.Items[i]))
		} else {
			items = append(items, nodeItem(&list.Items[i]))
		}
	}
	return Snapshot{Available: true, Items: items}, nil
}

func unavailable(err error) Snapshot {
	snapshot := Snapshot{Reason: Failed, Message: err.Error(), Items: []Item{}}
	switch {
	case apierrors.IsNotFound(err):
		snapshot.Reason = NotInstalled
	case apierrors.IsServiceUnavailable(err):
		snapshot.Reason = Unavailable
	case apierrors.IsForbidden(err):
		snapshot.Reason = Forbidden
	}
	return snapshot
}

func podItem(object *unstructured.Unstructured) Item {
	item := Item{Namespace: object.GetNamespace(), Name: object.GetName()}
	containers, _, _ := unstructured.NestedSlice(object.Object, "containers")
	for _, value := range containers {
		fields, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(fields, "name")
		usage := readUsage(fields)
		item.Containers = append(item.Containers, ContainerUsage{Usage: usage, Name: name})
		item.CPU += usage.CPU
		item.Memory += usage.Memory
	}
	return item
}

func nodeItem(object *unstructured.Unstructured) Item {
	return Item{Usage: readUsage(object.Object), Name: object.GetName()}
}

// readUsage treats an unreadable value as zero, because one odd number is no
// reason to lose the rest of the table.
func readUsage(fields map[string]any) Usage {
	cpu, _, _ := unstructured.NestedString(fields, "usage", "cpu")
	memory, _, _ := unstructured.NestedString(fields, "usage", "memory")
	return Usage{CPU: quantity(cpu).MilliValue(), Memory: quantity(memory).Value()}
}

func quantity(value string) *resource.Quantity {
	parsed, err := resource.ParseQuantity(value)
	if err != nil {
		return &resource.Quantity{}
	}
	return &parsed
}
