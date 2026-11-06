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
	"sync"
	"time"

	"kubehack/internal/cluster"
	"kubehack/internal/helm"
	"kubehack/internal/resources"
	"kubehack/internal/settings"
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
	// RunShell overrides how shells run, for tests.
	RunShell stream.ExecRunner
	// HangupShell overrides how dropped shells are ended, for tests.
	HangupShell stream.ShellCloser
	// Forwards overrides how port forwards reach the cluster, for tests.
	Forwards stream.ForwardDriver
	// Helm overrides how releases are managed, for tests.
	Helm helm.Client
	// Settings holds user preferences. Nil keeps them in memory only.
	Settings *settings.Store
}

// Server is an API server that only listens on the loopback interface.
type Server struct {
	token       string
	origins     map[string]struct{}
	listener    net.Listener
	http        *http.Server
	hub         *resources.Hub
	openLogs    stream.LogOpener
	runShell    stream.ExecRunner
	hangupShell stream.ShellCloser
	// sessions counts open shells, which http.Server.Shutdown does not track.
	sessions sync.WaitGroup
	forwards *stream.ForwardManager
	helm     helm.Client
	// newClient builds dynamic clients for requests outside the hub.
	newClient resources.ClientFactory
	settings  *settings.Store
	// baseCtx ends when the app quits. Work that must outlive a request uses it.
	baseCtx context.Context
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

	runShell := opts.RunShell
	if runShell == nil {
		runShell = stream.RunShell
	}

	hangupShell := opts.HangupShell
	if hangupShell == nil {
		hangupShell = stream.Hangup
	}

	forwardDriver := opts.Forwards
	if forwardDriver == nil {
		forwardDriver = stream.ClusterForwards{}
	}

	var helmClient helm.Client = helm.New()
	if opts.Helm != nil {
		helmClient = opts.Helm
	}

	store := opts.Settings
	if store == nil {
		if store, err = settings.Open(""); err != nil {
			return nil, err
		}
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	s := &Server{
		token:       token,
		origins:     origins,
		listener:    listener,
		hub:         resources.NewHub(newClient),
		openLogs:    openLogs,
		runShell:    runShell,
		hangupShell: hangupShell,
		forwards:    stream.NewForwardManager(forwardDriver),
		helm:        helmClient,
		baseCtx:     baseCtx,
		newClient:   newClient,
		settings:    store,
		cancel:      cancel,
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
	// This comes first so that a running Helm operation gets to record that it
	// stopped, while Shutdown is still waiting for its request.
	s.cancel()
	err := s.http.Shutdown(ctx)
	s.hub.Close()
	s.forwards.Close(ctx)

	finished := make(chan struct{})
	go func() {
		s.sessions.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
	}
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
	mux.HandleFunc("GET /api/v1/contexts/{name}/exec", s.execShell)
	mux.HandleFunc("GET /api/v1/contexts/{name}/metrics", s.readMetrics)
	mux.HandleFunc("GET /api/v1/contexts/{name}/object", s.loadObject)
	mux.HandleFunc("POST /api/v1/contexts/{name}/apply", s.applyObject)
	mux.HandleFunc("POST /api/v1/contexts/{name}/delete", s.handleAction(deleteObject))
	mux.HandleFunc("POST /api/v1/contexts/{name}/scale", s.handleAction(scaleObject))
	mux.HandleFunc("POST /api/v1/contexts/{name}/restart", s.handleAction(restartObject))
	mux.HandleFunc("POST /api/v1/contexts/{name}/portforwards", s.startForward)
	mux.HandleFunc("GET /api/v1/portforwards/watch", s.watchForwards)
	mux.HandleFunc("POST /api/v1/portforwards/{id}/restart", s.restartForward)
	mux.HandleFunc("POST /api/v1/portforwards/{id}/stop", s.stopForward)
	mux.HandleFunc("GET /api/v1/contexts/{name}/helm/releases", s.listReleases)
	mux.HandleFunc("GET /api/v1/contexts/{name}/helm/releases/{namespace}/{release}", s.releaseDetail)
	mux.HandleFunc("GET /api/v1/contexts/{name}/helm/releases/{namespace}/{release}/values", s.releaseValues)
	mux.HandleFunc("POST /api/v1/contexts/{name}/helm/install", s.installRelease)
	mux.HandleFunc("POST /api/v1/contexts/{name}/helm/upgrade", s.upgradeRelease)
	mux.HandleFunc("POST /api/v1/contexts/{name}/helm/rollback", s.rollbackRelease)
	mux.HandleFunc("POST /api/v1/contexts/{name}/helm/uninstall", s.uninstallRelease)
	mux.HandleFunc("GET /api/v1/settings", s.getSettings)
	mux.HandleFunc("PUT /api/v1/contexts/{name}/readonly", s.setReadOnly)
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
