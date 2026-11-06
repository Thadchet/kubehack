// Command kubehack-docker runs the API and the UI from one port, for the Docker
// image. It has no desktop shell and reads everything from what's mounted in.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"kubehack/internal/api"
	"kubehack/internal/cluster"
	"kubehack/internal/settings"
)

const (
	uiDir        = "/app/ui"
	settingsPath = "/settings/settings.json"
	// hostAddress is how Docker Desktop containers reach the host.
	hostAddress = "host.docker.internal"
)

func main() {
	port := flag.Int("port", 8765, "port, the same inside the container and on the host")
	flag.Parse()

	kubeconfig := filepath.Join(os.TempDir(), "kubehack-kubeconfig")
	if err := cluster.WriteContainerKubeconfig(kubeconfig, hostAddress); err != nil {
		log.Fatal(err)
	}
	if err := os.Setenv("KUBECONFIG", kubeconfig); err != nil {
		log.Fatal(err)
	}

	store, err := settings.Open(settingsPath)
	if err != nil {
		log.Fatal(err)
	}

	// Traffic from a published port arrives on the container's own network, not
	// on its loopback. run.sh publishes the port on the host's loopback only.
	server, err := api.New(api.Options{
		Address: "0.0.0.0",
		Port:    *port,
		Token:   os.Getenv("KUBEHACK_TOKEN"),
		AllowedOrigins: []string{
			fmt.Sprintf("http://localhost:%d", *port),
			fmt.Sprintf("http://127.0.0.1:%d", *port),
		},
		Debug:      os.Getenv("KUBEHACK_DEBUG") != "",
		Settings:   store,
		UI:         os.DirFS(uiDir),
		NoForwards: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		// Docker allows 10 seconds before it kills the container.
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(timeout); err != nil {
			log.Printf("stop api: %v", err)
		}
	}()

	fmt.Printf("Kubehack is at http://localhost:%d/#token=%s\n", *port, server.Token())
	if err := server.Serve(); err != nil {
		log.Fatal(err)
	}
}
