package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CI can check upstream containers without booting an appliance. Production
// systemd, firewalld and SELinux remain a separate image/runtime boundary.
func TestPlusUpstreamStack(t *testing.T) {
	if os.Getenv("JUSTVOXEL_PLUS_DOCKER_TEST") != "1" {
		t.Skip("explicit Docker integration environment required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("Docker integration fixture needs root for upstream bind-mount owners")
	}
	p := plusDeploymentFixture(t)
	p.Socket = "/var/run/docker.sock"
	p.CABundle = "/etc/ssl/certs/ca-certificates.crt"
	request := validPlusSetupRequest()
	request.Username = "plusadmin"
	request.Host = "plus-ci.local"
	if err := savePlusSetup(p.Preparation, request); err != nil {
		t.Fatal(err)
	}
	original := runPlusDeploymentCommand
	t.Cleanup(func() { runPlusDeploymentCommand = original })
	last := ""
	runPlusDeploymentCommand = func(ctx context.Context, command string, args []string, input []byte) ([]byte, error) {
		if command == "systemctl" || command == "firewall-cmd" {
			return nil, nil
		}
		// Arguments contain no credentials. Stdin remains private, even on failure.
		last = command
		for _, arg := range args {
			last += " " + arg
		}
		return original(ctx, command, args, input)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = plusCompose(ctx, p, nil, "down", "--remove-orphans")
		_, _ = original(ctx, "docker", []string{"network", "rm", "justvoxel-plus-games"}, nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	fixture := "justvoxel-plus-excluded-fixture"
	if _, err := original(ctx, "docker", []string{"run", "--detach", "--name", fixture, "redis:7.4-alpine"}, nil); err != nil {
		t.Fatal("unlabelled container fixture could not start")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = original(cleanup, "docker", []string{"rm", "--force", fixture}, nil)
	})
	if err := runPlusDeployment(ctx, p); err != nil {
		status, _ := readPlusDeploymentStatus(p)
		t.Fatalf("upstream stack failed at %s: %v; last command: %s", status.Stage, err, last)
	}
	applications, err := plusPublicApplications(p)
	if err != nil || applications.PanelURL == "" || applications.DrydockURL == "" {
		t.Fatal("application launchers were not finalized")
	}
	if _, err := os.Stat(filepath.Join(p.Preparation, "setup.json")); !os.IsNotExist(err) {
		t.Fatal("temporary account credentials remain after upstream setup")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if err := waitPlusCondition(ctx, 2*time.Minute, func(ctx context.Context) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:3000/api/v1/containers", nil)
		if err != nil {
			return false
		}
		req.SetBasicAuth(request.Username, request.Password)
		response, err := client.Do(req)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
		if err != nil {
			return false
		}
		if strings.Contains(string(body), fixture) {
			t.Fatal("Drydock default view included an unlabelled game-layer fixture")
		}
		for _, service := range []string{"panel", "database", "cache", "wings", "drydock"} {
			if !strings.Contains(string(body), "justvoxel-plus-"+service+"-1") {
				return false
			}
		}
		return true
	}); err != nil {
		t.Fatal("Drydock authentication or infrastructure-only view did not become ready")
	}
	t.Log("Upstream MariaDB, Redis, Panel, Wings and Drydock started; Panel authenticated to Wings; Drydock authentication and infrastructure-only view verified; initial account credentials removed.")
}
