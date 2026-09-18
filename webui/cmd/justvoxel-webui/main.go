package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/home-server-project/justvoxel-webui/internal/api"
	"github.com/home-server-project/justvoxel-webui/internal/server"
	"github.com/home-server-project/justvoxel-webui/internal/version"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8099", "HTTP listen address")
	socket := flag.String("agent-socket", "/run/justvoxel/management.sock", "JustVoxel management Unix socket")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("JustVoxel WebUI %s\nsource=%s\nmanagement-api=%s\n", version.Version, version.Commit, version.ManagementAPI)
		return
	}

	client := api.NewClient(*socket)
	app, err := server.New(client, server.Config{
		Version:        version.Version,
		Commit:         version.Commit,
		ManagementAPI:  version.ManagementAPI,
		ExternalScheme: "http",
		SecureCookies:  false,
	})
	if err != nil {
		log.Fatalf("initialize web server: %v", err)
	}

	log.Printf("JustVoxel WebUI %s listening on http://%s", version.Version, *listen)
	if err := app.ListenAndServe(*listen); err != nil {
		log.Printf("web server stopped: %v", err)
		os.Exit(1)
	}
}
