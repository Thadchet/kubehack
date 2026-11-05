package resources

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

var (
	podsGVR    = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	secretsGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	widgetsGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	podsKey    = Key{Context: "test", GVR: podsGVR}
)

func object(apiVersion, kind, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": "default", "resourceVersion": "1"},
	}}
}

func fakeClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			podsGVR:    "PodList",
			secretsGVR: "SecretList",
			widgetsGVR: "WidgetList",
		}, objects...)
}

func newHub(t *testing.T, client dynamic.Interface) *Hub {
	t.Helper()
	hub := NewHub(func(string) (dynamic.Interface, error) { return client, nil })
	t.Cleanup(hub.Close)
	return hub
}

func subscribe(t *testing.T, hub *Hub, key Key) *Subscription {
	t.Helper()
	sub, err := hub.Subscribe(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	return sub
}

func next(t *testing.T, sub *Subscription) Event {
	t.Helper()
	select {
	case event := <-sub.Events:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func expect(t *testing.T, sub *Subscription, eventType EventType, name string) Event {
	t.Helper()
	event := next(t, sub)
	if event.Type != eventType {
		t.Fatalf("event = %+v, want %s", event, eventType)
	}
	if name != "" && event.Object.GetName() != name {
		t.Fatalf("object = %s, want %s", event.Object.GetName(), name)
	}
	return event
}

func (h *Hub) watchCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.watches)
}

func TestSubscribeDeliversStateThenChanges(t *testing.T) {
	client := fakeClient(object("v1", "Pod", "existing"))
	sub := subscribe(t, newHub(t, client), podsKey)

	expect(t, sub, Added, "existing")
	expect(t, sub, Synced, "")

	pods := client.Resource(podsGVR).Namespace("default")
	ctx := context.Background()
	created, err := pods.Create(ctx, object("v1", "Pod", "new"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, sub, Added, "new")

	created.SetResourceVersion("2")
	if _, err := pods.Update(ctx, created, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, Modified, "new")

	if err := pods.Delete(ctx, "new", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, Deleted, "new")
}

func TestCustomResourcesNeedNoSpecificCode(t *testing.T) {
	client := fakeClient(object("example.com/v1", "Widget", "w1"))
	sub := subscribe(t, newHub(t, client), Key{Context: "test", GVR: widgetsGVR})

	expect(t, sub, Added, "w1")
}

func TestSubscribersShareOneInformerAndBothGetState(t *testing.T) {
	hub := newHub(t, fakeClient(object("v1", "Pod", "p1")))

	first := subscribe(t, hub, podsKey)
	expect(t, first, Added, "p1")
	expect(t, first, Synced, "")

	second := subscribe(t, hub, podsKey)
	expect(t, second, Added, "p1")
	expect(t, second, Synced, "")

	if got := hub.watchCount(); got != 1 {
		t.Errorf("watches = %d, want 1", got)
	}
}

func TestInformerStopsAfterIdleTimeout(t *testing.T) {
	hub := newHub(t, fakeClient())
	hub.idleTimeout = 20 * time.Millisecond

	sub := subscribe(t, hub, podsKey)
	sub.Close()
	sub.Close()

	deadline := time.Now().Add(5 * time.Second)
	for hub.watchCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("informer did not stop")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestResubscribingCancelsIdleStop(t *testing.T) {
	hub := newHub(t, fakeClient())
	hub.idleTimeout = 50 * time.Millisecond

	subscribe(t, hub, podsKey).Close()
	sub := subscribe(t, hub, podsKey)
	expect(t, sub, Synced, "")

	time.Sleep(120 * time.Millisecond)
	if got := hub.watchCount(); got != 1 {
		t.Errorf("watches = %d, want 1", got)
	}
}

func TestForbiddenIsReportedToCurrentAndLaterSubscribers(t *testing.T) {
	client := fakeClient()
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", fmt.Errorf("denied"))
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden
	})
	client.PrependWatchReactor("pods", func(clienttesting.Action) (bool, k8swatch.Interface, error) {
		return true, nil, forbidden
	})
	hub := newHub(t, client)

	first := subscribe(t, hub, podsKey)
	event := next(t, first)
	if event.Type != Failed || !apierrors.IsForbidden(event.Err) {
		t.Fatalf("event = %+v", event)
	}

	second := subscribe(t, hub, podsKey)
	if event := next(t, second); event.Type != Failed {
		t.Fatalf("late subscriber event = %+v", event)
	}
}

func TestManyObjectsWithPendingFailureDoNotDeadlock(t *testing.T) {
	var objects []runtime.Object
	for i := range eventBuffer * 2 {
		objects = append(objects, object("v1", "Pod", fmt.Sprintf("p%d", i)))
	}
	hub := newHub(t, fakeClient(objects...))

	first := subscribe(t, hub, podsKey)
	for range eventBuffer * 2 {
		expect(t, first, Added, "")
	}
	expect(t, first, Synced, "")

	// Force the path that sends on a new subscriber's buffer.
	hub.mu.Lock()
	w := hub.watches[podsKey]
	hub.mu.Unlock()
	w.mu.Lock()
	w.err = fmt.Errorf("lost connection")
	w.mu.Unlock()

	done := make(chan *Subscription, 1)
	go func() {
		sub, err := hub.Subscribe(podsKey)
		if err != nil {
			t.Error(err)
		}
		done <- sub
	}()
	select {
	case sub := <-done:
		sub.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe deadlocked")
	}
}

// TestOutageAndRecoveryAreReported covers the case the informer stays silent
// about: refused connections never reach a watch error handler.
func TestOutageAndRecoveryAreReported(t *testing.T) {
	client := fakeClient(object("v1", "Pod", "p1"))
	var down atomic.Bool
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		if down.Load() {
			return true, nil, fmt.Errorf("connection refused")
		}
		return false, nil, nil
	})
	hub := newHub(t, client)
	hub.healthInterval = 10 * time.Millisecond

	sub := subscribe(t, hub, podsKey)
	expect(t, sub, Added, "p1")
	expect(t, sub, Synced, "")

	down.Store(true)
	expect(t, sub, Failed, "")
	down.Store(false)
	expect(t, sub, Recovered, "")
}

func TestRepeatedFailureIsReportedOnce(t *testing.T) {
	w := &watch{subs: make(map[*Subscription]struct{})}
	events := make(chan Event, 8)
	sub := &Subscription{Events: events, events: events, done: make(chan struct{})}
	w.add(sub)

	w.setHealth(fmt.Errorf("refused"))
	w.setHealth(fmt.Errorf("refused"))
	w.setHealth(nil)
	w.setHealth(nil)

	if len(events) != 2 {
		t.Errorf("got %d events, want Failed and Recovered only", len(events))
	}
}

func TestSanitizeRemovesSecretValues(t *testing.T) {
	secret := object("v1", "Secret", "s1")
	secret.Object["data"] = map[string]any{"password": "aHVudGVyMg=="}
	secret.Object["stringData"] = map[string]any{"token": "plain"}
	secret.SetAnnotations(map[string]string{
		lastAppliedAnnotation: `{"data":{"password":"aHVudGVyMg=="}}`,
		"keep":                "yes",
	})
	secret.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl"}})

	sub := subscribe(t, newHub(t, fakeClient(secret)), Key{Context: "test", GVR: secretsGVR})
	got := expect(t, sub, Added, "s1").Object

	body, err := got.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"aHVudGVyMg==", "plain", "managedFields", "last-applied"} {
		if strings.Contains(string(body), leaked) {
			t.Errorf("object still contains %q: %s", leaked, body)
		}
	}
	data := got.Object["data"].(map[string]any)
	if value, ok := data["password"]; !ok || value != "" {
		t.Errorf("data = %v, want key kept with blank value", data)
	}
	if got.GetAnnotations()["keep"] != "yes" {
		t.Errorf("annotations = %v", got.GetAnnotations())
	}
}

func TestSanitizeKeepsDataOfOtherResources(t *testing.T) {
	item := object("v1", "ConfigMap", "c1")
	item.Object["data"] = map[string]any{"key": "value"}

	result, err := sanitize(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"})(item)
	if err != nil {
		t.Fatal(err)
	}
	data := result.(*unstructured.Unstructured).Object["data"].(map[string]any)
	if data["key"] != "value" {
		t.Errorf("data = %v", data)
	}
}
