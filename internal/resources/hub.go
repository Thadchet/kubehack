package resources

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"kubehack/internal/cluster"
)

const (
	defaultIdleTimeout    = 60 * time.Second
	defaultHealthInterval = 5 * time.Second
	healthTimeout         = 4 * time.Second
	eventBuffer           = 256
)

// ErrHubClosed is returned when subscribing after the hub has shut down.
var ErrHubClosed = errors.New("hub closed")

// Key identifies one informer. An empty Namespace watches all namespaces. The
// namespace is part of the key because users without cluster-wide list rights
// can often still watch a single namespace.
type Key struct {
	Context   string
	GVR       schema.GroupVersionResource
	Namespace string
}

// EventType says what an Event reports.
type EventType string

// Event types.
const (
	Added    EventType = "added"
	Modified EventType = "modified"
	Deleted  EventType = "deleted"
	// Synced follows the last object of the initial state.
	Synced EventType = "synced"
	// Failed reports that the informer cannot reach or read the resource. It
	// keeps retrying, so Recovered or Synced can follow.
	Failed    EventType = "failed"
	Recovered EventType = "recovered"
)

// Event is one change delivered to a subscriber. Object is shared with the
// informer cache and must not be modified.
type Event struct {
	Type   EventType
	Object *unstructured.Unstructured
	Err    error
}

// ClientFactory builds a dynamic client for a kubeconfig context.
type ClientFactory func(contextName string) (dynamic.Interface, error)

// ClientForContext is the ClientFactory for real clusters.
func ClientForContext(contextName string) (dynamic.Interface, error) {
	config, err := cluster.RESTConfig(contextName)
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(rest.AddUserAgent(config, "kubehack"))
}

// Hub shares one informer between all subscribers of a Key. Informers start on
// first use and stop after idleTimeout without subscribers, so switching
// screens and back does not list the resource again.
type Hub struct {
	newClient      ClientFactory
	idleTimeout    time.Duration
	healthInterval time.Duration

	mu      sync.Mutex
	closed  bool
	watches map[Key]*watch
}

// NewHub creates a Hub.
func NewHub(newClient ClientFactory) *Hub {
	return &Hub{
		newClient:      newClient,
		idleTimeout:    defaultIdleTimeout,
		healthInterval: defaultHealthInterval,
		watches:        make(map[Key]*watch),
	}
}

type watch struct {
	informer cache.SharedIndexInformer
	cancel   context.CancelFunc
	// refs and idle are guarded by Hub.mu.
	refs int
	idle *time.Timer

	mu   sync.Mutex
	subs map[*Subscription]struct{}
	err  error
}

// Subscription receives the current state of a resource, then its changes.
type Subscription struct {
	Events <-chan Event

	events chan Event
	done   chan struct{}
	once   sync.Once
	close  func()
}

// Close ends the subscription. It is safe to call more than once.
func (s *Subscription) Close() {
	s.once.Do(func() {
		close(s.done)
		s.close()
	})
}

func (s *Subscription) send(event Event) {
	select {
	case s.events <- event:
	case <-s.done:
	}
}

// Subscribe starts delivering events for key until the subscription is closed.
func (h *Hub) Subscribe(key Key) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil, ErrHubClosed
	}
	w, ok := h.watches[key]
	if !ok {
		var err error
		if w, err = h.start(key); err != nil {
			return nil, err
		}
		h.watches[key] = w
	}
	if w.idle != nil {
		w.idle.Stop()
		w.idle = nil
	}

	events := make(chan Event, eventBuffer)
	sub := &Subscription{Events: events, events: events, done: make(chan struct{})}
	// This comes before the handler, which could otherwise fill the buffer and
	// block the send in add while Hub.mu is held.
	w.add(sub)
	registration, err := w.informer.AddEventHandler(sub.handler())
	if err != nil {
		w.remove(sub)
		h.releaseLocked(key, w)
		return nil, fmt.Errorf("add event handler: %w", err)
	}
	w.refs++
	sub.close = func() {
		w.remove(sub)
		// Only fails if the informer is already stopped, which is the goal.
		_ = w.informer.RemoveEventHandler(registration)
		h.mu.Lock()
		defer h.mu.Unlock()
		w.refs--
		h.releaseLocked(key, w)
	}

	go func() {
		// This runs after the handler has seen every initial object, so Synced
		// is queued behind them.
		if cache.WaitForCacheSync(sub.done, registration.HasSynced) {
			sub.send(Event{Type: Synced})
		}
	}()
	return sub, nil
}

func (h *Hub) start(key Key) (*watch, error) {
	client, err := h.newClient(key.Context)
	if err != nil {
		return nil, err
	}

	informer := dynamicinformer.NewFilteredDynamicInformer(
		client, key.GVR, key.Namespace, 0, cache.Indexers{}, nil,
	).Informer()
	if err := informer.SetTransform(sanitize(key.GVR)); err != nil {
		return nil, fmt.Errorf("set transform: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &watch{informer: informer, cancel: cancel, subs: make(map[*Subscription]struct{})}
	go informer.RunWithContext(ctx)
	go w.monitorHealth(ctx, h.healthInterval, func(ctx context.Context) error {
		_, err := client.Resource(key.GVR).Namespace(key.Namespace).List(ctx, metav1.ListOptions{Limit: 1})
		return err
	})
	return w, nil
}

// releaseLocked schedules the informer to stop if nothing uses it.
func (h *Hub) releaseLocked(key Key, w *watch) {
	if w.refs > 0 || h.closed || h.watches[key] != w {
		return
	}
	w.idle = time.AfterFunc(h.idleTimeout, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if w.refs == 0 && h.watches[key] == w {
			delete(h.watches, key)
			w.cancel()
		}
	})
}

// Close stops every informer.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for key, w := range h.watches {
		if w.idle != nil {
			w.idle.Stop()
		}
		w.cancel()
		delete(h.watches, key)
	}
}

func (s *Subscription) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if item, ok := obj.(*unstructured.Unstructured); ok {
				s.send(Event{Type: Added, Object: item})
			}
		},
		UpdateFunc: func(before, after any) {
			previous, ok := before.(*unstructured.Unstructured)
			current, ok2 := after.(*unstructured.Unstructured)
			// A relist reports every object as updated, changed or not.
			if ok && ok2 && previous.GetResourceVersion() != current.GetResourceVersion() {
				s.send(Event{Type: Modified, Object: current})
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if item, ok := obj.(*unstructured.Unstructured); ok {
				s.send(Event{Type: Deleted, Object: item})
			}
		},
	}
}

func (w *watch) add(sub *Subscription) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.subs[sub] = struct{}{}
	if w.err != nil {
		sub.events <- Event{Type: Failed, Err: w.err}
	}
}

func (w *watch) remove(sub *Subscription) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.subs, sub)
}

// notifyLocked never blocks, because it runs on the informer's own goroutines.
// A subscriber whose buffer is full is too slow to keep and gets disconnected
// by its write timeout, then resynchronizes on reconnect.
func (w *watch) notifyLocked(event Event) {
	for sub := range w.subs {
		select {
		case sub.events <- event:
		default:
		}
	}
}

// monitorHealth exists because an informer cannot say whether it is working.
// Its reflector retries refused connections silently, so a table would keep
// showing old data as if it were live, and it never reports recovery.
func (w *watch) monitorHealth(ctx context.Context, interval time.Duration, check func(context.Context) error) {
	run := func() {
		checkCtx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		err := check(checkCtx)
		if ctx.Err() == nil {
			w.setHealth(err)
		}
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// setHealth reports changes only, so subscribers do not get a message per check.
func (w *watch) setHealth(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case err != nil && (w.err == nil || w.err.Error() != err.Error()):
		w.notifyLocked(Event{Type: Failed, Err: err})
	case err == nil && w.err != nil:
		w.notifyLocked(Event{Type: Recovered})
	}
	w.err = err
}
