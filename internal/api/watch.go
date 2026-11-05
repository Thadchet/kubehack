package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"kubehack/internal/cluster"
	"kubehack/internal/resources"
)

const (
	// wsProtocol is the subprotocol the server selects. Browsers require one to
	// be echoed, and echoing the token protocol would send the token back.
	wsProtocol = "kubehack.v1"

	watchFlushInterval = 100 * time.Millisecond
	watchMaxBatch      = 200
	wsWriteTimeout     = 10 * time.Second
)

type watchObject struct {
	Op     resources.EventType        `json:"op"`
	Object *unstructured.Unstructured `json:"object"`
}

// watchMessage is one WebSocket frame. Type is "events", "synced", "failed" or
// "recovered". A client that reconnects receives the full state again, followed
// by "synced", so it never has to resume from a position.
type watchMessage struct {
	Type    string        `json:"type"`
	Events  []watchObject `json:"events,omitempty"`
	Message string        `json:"message,omitempty"`
	Reason  string        `json:"reason,omitempty"`
}

func discoverResources(w http.ResponseWriter, r *http.Request) {
	config, err := cluster.RESTConfig(r.PathValue("name"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	result, err := resources.Discover(r.Context(), config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeClusterError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, cluster.ErrContextNotFound) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}

func (s *Server) watchResource(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	key := resources.Key{
		Context: r.PathValue("name"),
		GVR: schema.GroupVersionResource{
			Group:    query.Get("group"),
			Version:  query.Get("version"),
			Resource: query.Get("resource"),
		},
		Namespace: query.Get("namespace"),
	}
	if key.GVR.Version == "" || key.GVR.Resource == "" {
		http.Error(w, "version and resource are required", http.StatusBadRequest)
		return
	}

	// Subscribing first lets failures use a normal HTTP status.
	subscription, err := s.hub.Subscribe(key)
	if err != nil {
		writeClusterError(w, err)
		return
	}
	defer subscription.Close()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{wsProtocol},
		// guard has already checked Origin against the allowlist, which is
		// stricter than this library's same-host check and allows wails://.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// The client sends nothing, so reading only detects that it has gone.
	ctx := conn.CloseRead(r.Context())
	if err := streamEvents(ctx, conn, subscription); err == nil {
		conn.Close(websocket.StatusGoingAway, "")
	}
}

// streamEvents batches changes, because a large cluster's initial state would
// otherwise be thousands of frames. It returns nil when ctx ends.
func streamEvents(ctx context.Context, conn *websocket.Conn, subscription *resources.Subscription) error {
	write := func(message watchMessage) error {
		ctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer cancel()
		return wsjson.Write(ctx, conn, message)
	}

	var batch []watchObject
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := write(watchMessage{Type: "events", Events: batch})
		batch = nil
		return err
	}

	ticker := time.NewTicker(watchFlushInterval)
	defer ticker.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			err = flush()
		case event := <-subscription.Events:
			switch event.Type {
			case resources.Added, resources.Modified, resources.Deleted:
				batch = append(batch, watchObject{Op: event.Type, Object: event.Object})
				if len(batch) >= watchMaxBatch {
					err = flush()
				}
			default:
				// Status has to arrive after the objects queued before it.
				if err = flush(); err == nil {
					err = write(statusMessage(event))
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func statusMessage(event resources.Event) watchMessage {
	message := watchMessage{Type: string(event.Type)}
	if event.Err != nil {
		message.Message = event.Err.Error()
		message.Reason = string(apierrors.ReasonForError(event.Err))
	}
	return message
}
