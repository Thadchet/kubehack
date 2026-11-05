// Package api exposes the local HTTP and WebSocket API used by the UI.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"kubehack/internal/cluster"
	"kubehack/internal/resources"
	"kubehack/internal/stream"
)

// Options configures a Server. Zero values select a random port and token.
type Options struct {
	Port           int
	Token          string
	AllowedOrigins []string
	Debug          bool
	// NewClient overrides how clients are built, for tests.
	NewClient resources.ClientFactory
	// OpenLogs overrides how logs are read, for tests.
	OpenLogs stream.LogOpener
}

// Server is an API server that only listens on the loopback interface.
type Server struct {
	token    string
	origins  map[string]struct{}
	listener net.Listener
	http     *http.Server
	hub      *resources.Hub
	openLogs stream.LogOpener
	// newClient builds dynamic clients for requests outside the hub.
	newClient resources.ClientFactory
	// cancel ends WebSocket handlers, which http.Server.Shutdown does not track.
	cancel context.CancelFunc
}

// New binds the listener on 127.0.0.1 and prepares the server.
func New(opts Options) (*Server, error) {
	token := opts.Token
	if token == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate token: %w", err)
		}
		token = hex.EncodeToString(buf)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	origins := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, origin := range opts.AllowedOrigins {
		origins[origin] = struct{}{}
	}

	newClient := opts.NewClient
	if newClient == nil {
		newClient = resources.ClientForContext
	}

	openLogs := opts.OpenLogs
	if openLogs == nil {
		openLogs = stream.OpenLogs
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	s := &Server{
		token:     token,
		origins:   origins,
		listener:  listener,
		hub:       resources.NewHub(newClient),
		openLogs:  openLogs,
		newClient: newClient,
		cancel:    cancel,
	}
	handler := s.guard(s.routes())
	if opts.Debug {
		handler = logRequests(handler)
	}
	s.http = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	return s, nil
}

// Port returns the bound TCP port.
func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// BaseURL returns the URL the UI should call.
func (s *Server) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Port())
}

// Token returns the secret required on every request.
func (s *Server) Token() string {
	return s.token
}

// Serve blocks until the server stops.
func (s *Server) Serve() error {
	if err := s.http.Serve(s.listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	s.cancel()
	s.hub.Close()
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/contexts", listContexts)
	mux.HandleFunc("GET /api/v1/contexts/{name}/status", contextStatus)
	mux.HandleFunc("GET /api/v1/contexts/{name}/resources", discoverResources)
	mux.HandleFunc("GET /api/v1/contexts/{name}/watch", s.watchResource)
	mux.HandleFunc("GET /api/v1/contexts/{name}/logs", s.streamLogs)
	mux.HandleFunc("GET /api/v1/contexts/{name}/object", s.loadObject)
	mux.HandleFunc("POST /api/v1/contexts/{name}/apply", s.applyObject)
	return mux
}

func listContexts(w http.ResponseWriter, _ *http.Request) {
	contexts, err := cluster.ListContexts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, contexts)
}

func contextStatus(w http.ResponseWriter, r *http.Request) {
	status, err := cluster.Probe(r.Context(), r.PathValue("name"))
	if err != nil {
		writeClusterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
