package main

import (
	"context"
	"log"
	"os"
	"time"

	"kubehack/internal/api"
	"kubehack/internal/cluster"
	"kubehack/internal/settings"
)

// appOrigins holds the Origin sent by the macOS webview in a built app.
var appOrigins = []string{"wails://wails"}

// Connection tells the UI how to reach the local API.
type Connection struct {
	BaseURL string `json:"baseUrl"`
	Token   string `json:"token"`
}

// App owns the lifecycle of the local API server.
type App struct {
	server *api.Server
	// ready closes once server is set. Wails loads the UI while startup is still
	// running, so anything reading server has to wait for it.
	ready chan struct{}
}

// NewApp creates the application.
func NewApp() *App {
	return &App{ready: make(chan struct{})}
}

func (a *App) startup(ctx context.Context) {
	// This must finish before the API starts, or the first request could run a
	// credential plugin with Finder's minimal PATH.
	if err := cluster.ImportShellEnv(ctx); err != nil {
		log.Printf("import shell environment: %v", err)
	}

	path, err := settings.DefaultPath()
	if err != nil {
		log.Fatalf("find settings: %v", err)
	}
	store, err := settings.Open(path)
	if err != nil {
		log.Fatalf("open settings: %v", err)
	}

	server, err := api.New(api.Options{
		AllowedOrigins: appOrigins,
		Debug:          os.Getenv("KUBEHACK_DEBUG") != "",
		Settings:       store,
	})
	if err != nil {
		log.Fatalf("start api: %v", err)
	}
	a.server = server
	close(a.ready)
	go func() {
		if err := server.Serve(); err != nil {
			log.Printf("api stopped: %v", err)
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	<-a.ready
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.server.Shutdown(ctx); err != nil {
		log.Printf("stop api: %v", err)
	}
}

// Connection returns the API address and token for this launch.
func (a *App) Connection() Connection {
	<-a.ready
	return Connection{BaseURL: a.server.BaseURL(), Token: a.server.Token()}
}
