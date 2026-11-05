// Command kubehack-dev runs the API without the desktop shell, so the UI can be
// developed in a normal browser against the Vite dev server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"kubehack/internal/api"
	"kubehack/internal/cluster"
)

func main() {
	port := flag.Int("port", 8765, "loopback port")
	origin := flag.String("origin", "http://localhost:5173", "allowed UI origin")
	token := flag.String("token", "", "API token; random when empty. A fixed token lets the UI reconnect across restarts")
	flag.Parse()

	if err := cluster.ImportShellEnv(context.Background()); err != nil {
		log.Printf("import shell environment: %v", err)
	}

	server, err := api.New(api.Options{Port: *port, Token: *token, AllowedOrigins: []string{*origin}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("VITE_KUBEHACK_API=%s\nVITE_KUBEHACK_TOKEN=%s\n", server.BaseURL(), server.Token())
	log.Fatal(server.Serve())
}
