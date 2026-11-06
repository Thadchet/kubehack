package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	forwardStartTimeout = 30 * time.Second
	forwardCheckTimeout = 5 * time.Second
	// A forward that sees no traffic can outlive its pod without any error, so
	// the pod is checked as well as the connection.
	defaultForwardCheckInterval = 3 * time.Second
	forwardCheckFailureLimit    = 3
)

// ForwardKind is what a forward targets.
type ForwardKind string

// A service forward picks one pod when it starts, as kubectl does. It does not
// balance across pods.
const (
	ForwardPod     ForwardKind = "pod"
	ForwardService ForwardKind = "service"
)

// ForwardState is the lifecycle of a forward.
type ForwardState string

// A lost forward stays listed so that its reason can be read and it can be
// reconnected, but nothing is listening on its local port.
const (
	ForwardStarting ForwardState = "starting"
	ForwardActive   ForwardState = "active"
	ForwardLost     ForwardState = "lost"
)

var (
	// ErrForwardNotFound means no forward has that ID.
	ErrForwardNotFound = errors.New("port forward not found")
	// ErrForwardBusy means the forward is already running or starting.
	ErrForwardBusy = errors.New("port forward is already running")
	// ErrLocalPortInUse means another program holds the requested local port.
	ErrLocalPortInUse = errors.New("local port is in use")
	// ErrForwardTarget means the target cannot be forwarded to as requested.
	ErrForwardTarget = errors.New("invalid port forward target")
	// ErrManagerClosed means the app is quitting.
	ErrManagerClosed = errors.New("port forwards are closed")
)

// ForwardTargetError explains, in words fit to show the user, why a target
// cannot be forwarded to. It matches ErrForwardTarget.
type ForwardTargetError string

func (e ForwardTargetError) Error() string {
	return string(e)
}

// Is implements errors.Is.
func (e ForwardTargetError) Is(target error) bool {
	return target == ErrForwardTarget
}

// PodGoneError reports that a forward's pod no longer serves it.
type PodGoneError struct {
	Reason string
}

func (e PodGoneError) Error() string {
	return e.Reason
}

// ForwardRequest describes a forward. LocalPort 0 picks a free port.
type ForwardRequest struct {
	Context   string      `json:"context"`
	Namespace string      `json:"namespace"`
	Kind      ForwardKind `json:"kind"`
	Name      string      `json:"name"`
	Port      int         `json:"port"`
	LocalPort int         `json:"localPort"`
}

// Forward is a snapshot of one forward.
type Forward struct {
	ForwardRequest
	ID      string       `json:"id"`
	Pod     string       `json:"pod,omitempty"`
	State   ForwardState `json:"state"`
	Message string       `json:"message,omitempty"`
}

// ForwardTarget is the pod and port a request resolved to.
type ForwardTarget struct {
	Pod  string
	UID  string
	Port int
}

// ForwardDriver does the cluster side of forwarding.
type ForwardDriver interface {
	// Resolve finds the pod to connect to.
	Resolve(ctx context.Context, request ForwardRequest) (ForwardTarget, error)
	// Run forwards until the connection is lost or ctx is cancelled. It calls
	// ready once with the local port when it is listening.
	Run(ctx context.Context, request ForwardRequest, target ForwardTarget, ready func(localPort int)) error
	// Check returns PodGoneError if the pod can no longer serve the forward.
	Check(ctx context.Context, request ForwardRequest, target ForwardTarget) error
}

type forwardEntry struct {
	Forward
	created time.Time
	// cancel and done belong to the current run, and are nil between runs.
	cancel context.CancelFunc
	done   chan struct{}
}

// ForwardManager owns all forwards, across contexts. They keep running when the
// user switches context.
type ForwardManager struct {
	driver        ForwardDriver
	checkInterval time.Duration
	base          context.Context
	stop          context.CancelFunc
	runs          sync.WaitGroup

	mu          sync.Mutex
	entries     map[string]*forwardEntry
	subscribers map[chan struct{}]struct{}
	closed      bool
}

// NewForwardManager creates a manager with no forwards.
func NewForwardManager(driver ForwardDriver) *ForwardManager {
	base, stop := context.WithCancel(context.Background())
	return &ForwardManager{
		driver:        driver,
		checkInterval: defaultForwardCheckInterval,
		base:          base,
		stop:          stop,
		entries:       map[string]*forwardEntry{},
		subscribers:   map[chan struct{}]struct{}{},
	}
}

// List returns all forwards, oldest first.
func (m *ForwardManager) List() []Forward {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make([]*forwardEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].created.Before(entries[j].created) })
	forwards := make([]Forward, len(entries))
	for i, entry := range entries {
		forwards[i] = entry.Forward
	}
	return forwards
}

// Get returns one forward.
func (m *ForwardManager) Get(id string) (Forward, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[id]
	if !ok {
		return Forward{}, ErrForwardNotFound
	}
	return entry.Forward, nil
}

// Subscribe returns a channel that receives after any change. Changes that
// arrive while one is unread merge into it, because readers fetch the whole list.
func (m *ForwardManager) Subscribe() (<-chan struct{}, func()) {
	changes := make(chan struct{}, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribers[changes] = struct{}{}
	return changes, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.subscribers, changes)
	}
}

// Start opens a forward and returns once it is listening. A forward that fails
// to open is not kept, because the caller is told directly.
func (m *ForwardManager) Start(request ForwardRequest) (Forward, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return Forward{}, fmt.Errorf("generate id: %w", err)
	}
	entry := &forwardEntry{
		Forward: Forward{ForwardRequest: request, ID: hex.EncodeToString(id), State: ForwardStarting},
		created: time.Now(),
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Forward{}, ErrManagerClosed
	}
	m.entries[entry.ID] = entry
	m.publishLocked()
	m.mu.Unlock()

	forward, err := m.launch(entry)
	if err != nil {
		m.mu.Lock()
		delete(m.entries, entry.ID)
		m.publishLocked()
		m.mu.Unlock()
		return Forward{}, err
	}
	return forward, nil
}

// Restart reconnects a lost forward on the same local port, so that whatever
// was pointed at it keeps working. A service picks a pod again.
func (m *ForwardManager) Restart(id string) (Forward, error) {
	m.mu.Lock()
	entry, ok := m.entries[id]
	switch {
	case m.closed:
		m.mu.Unlock()
		return Forward{}, ErrManagerClosed
	case !ok:
		m.mu.Unlock()
		return Forward{}, ErrForwardNotFound
	case entry.State != ForwardLost:
		m.mu.Unlock()
		return Forward{}, ErrForwardBusy
	}
	entry.State = ForwardStarting
	entry.Message = ""
	m.publishLocked()
	m.mu.Unlock()

	forward, err := m.launch(entry)
	if err != nil {
		m.markLost(entry, err.Error())
		return Forward{}, err
	}
	return forward, nil
}

// Stop closes a forward and removes it from the list.
func (m *ForwardManager) Stop(id string) error {
	m.mu.Lock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return ErrForwardNotFound
	}
	delete(m.entries, id)
	cancel, done := entry.cancel, entry.done
	m.publishLocked()
	m.mu.Unlock()

	if cancel != nil {
		cancel()
		// The local port is free once this returns.
		<-done
	}
	return nil
}

// Close stops every forward. Forwards end with the app, like its shells.
func (m *ForwardManager) Close(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stop()

	finished := make(chan struct{})
	go func() {
		m.runs.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
	}
}

func (m *ForwardManager) launch(entry *forwardEntry) (Forward, error) {
	ctx, cancel := context.WithCancel(m.base)
	done := make(chan struct{})
	result := make(chan error, 1)
	ready := make(chan int, 1)

	m.mu.Lock()
	request := entry.ForwardRequest
	if _, ok := m.entries[entry.ID]; !ok || m.closed {
		m.mu.Unlock()
		cancel()
		return Forward{}, ErrForwardNotFound
	}
	entry.cancel, entry.done = cancel, done
	m.runs.Add(1)
	m.mu.Unlock()

	finish := func() {
		m.mu.Lock()
		if entry.done == done {
			entry.cancel, entry.done = nil, nil
		}
		m.mu.Unlock()
		cancel()
		close(done)
		m.runs.Done()
	}

	resolveCtx, resolveCancel := context.WithTimeout(ctx, forwardStartTimeout)
	target, err := m.driver.Resolve(resolveCtx, request)
	resolveCancel()
	if err != nil {
		finish()
		return Forward{}, err
	}

	go func() {
		result <- m.driver.Run(ctx, request, target, func(port int) { ready <- port })
	}()

	timeout := time.NewTimer(forwardStartTimeout)
	defer timeout.Stop()
	select {
	case port := <-ready:
		m.mu.Lock()
		if _, ok := m.entries[entry.ID]; !ok {
			m.mu.Unlock()
			cancel()
			<-result
			finish()
			return Forward{}, ErrForwardNotFound
		}
		entry.LocalPort = port
		entry.Pod = target.Pod
		entry.State = ForwardActive
		entry.Message = ""
		forward := entry.Forward
		m.publishLocked()
		m.mu.Unlock()

		go func() {
			defer finish()
			m.supervise(ctx, cancel, entry, target, result)
		}()
		return forward, nil
	case err := <-result:
		finish()
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = errors.New("the connection closed while opening")
		}
		return Forward{}, err
	case <-timeout.C:
		cancel()
		// Dialing ignores ctx, so this must not wait for it.
		go func() {
			<-result
			finish()
		}()
		return Forward{}, errors.New("timed out connecting to the pod")
	}
}

// supervise runs until the forward ends, and reports it if the user didn't ask.
func (m *ForwardManager) supervise(
	ctx context.Context,
	cancel context.CancelFunc,
	entry *forwardEntry,
	target ForwardTarget,
	result <-chan error,
) {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			<-result
			return
		case <-result:
			if ctx.Err() == nil {
				m.markLost(entry, m.lostReason(ctx, entry, target))
			}
			return
		case <-ticker.C:
			err := m.check(ctx, entry, target)
			var gone PodGoneError
			switch {
			case err == nil:
				failures = 0
				continue
			case errors.As(err, &gone):
			case ctx.Err() != nil:
				continue
			default:
				if failures++; failures < forwardCheckFailureLimit {
					continue
				}
				err = errors.New("Lost the connection to the cluster.")
			}
			// Listeners close before this is reported, so a lost forward never
			// appears to still be listening.
			cancel()
			<-result
			m.markLost(entry, err.Error())
			return
		}
	}
}

func (m *ForwardManager) check(ctx context.Context, entry *forwardEntry, target ForwardTarget) error {
	ctx, cancel := context.WithTimeout(ctx, forwardCheckTimeout)
	defer cancel()
	m.mu.Lock()
	request := entry.ForwardRequest
	m.mu.Unlock()
	return m.driver.Check(ctx, request, target)
}

// lostReason works out why a connection closed. client-go drops the whole
// forward when one connection fails, and logs the cause instead of returning it.
func (m *ForwardManager) lostReason(ctx context.Context, entry *forwardEntry, target ForwardTarget) string {
	err := m.check(ctx, entry, target)
	var gone PodGoneError
	switch {
	case errors.As(err, &gone):
		return gone.Reason
	case err != nil:
		return "Lost the connection to the cluster."
	}
	return fmt.Sprintf(
		"The connection closed, but pod %s is still running. Check that it listens on port %d.",
		target.Pod, target.Port,
	)
}

func (m *ForwardManager) markLost(entry *forwardEntry, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.State = ForwardLost
	entry.Message = reason
	m.publishLocked()
}

func (m *ForwardManager) publishLocked() {
	for subscriber := range m.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}
