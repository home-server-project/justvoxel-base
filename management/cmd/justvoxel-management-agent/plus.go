package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func isPlusImage() bool {
	data, err := os.ReadFile("/usr/lib/justvoxel/variant")
	return err == nil && strings.TrimSpace(string(data)) == "justvoxel-plus-base"
}

func (s *server) plusRoutes(next http.Handler) http.Handler {
	if !s.plus {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/admin/reset/factory/") {
			next.ServeHTTP(w, r)
			return
		}
		for _, prefix := range []string{
			"/v1/minecraft", "/v1/players", "/v1/whitelist", "/v1/backups", "/v1/logs/minecraft",
			"/v1/admin/backup-storage", "/v1/admin/backups", "/v1/admin/configuration/plan", "/v1/admin/configuration/apply",
			"/v1/admin/data-migration", "/v1/admin/migration", "/v1/admin/restore", "/v1/admin/setup",
			"/v1/admin/setup-defaults", "/v1/admin/reset", "/v1/admin/version", "/v1/admin/storage-provision", "/v1/admin/logs",
		} {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.NotFound(w, r)
				return
			}
		}
		if strings.Contains(r.URL.Path, "-allowance/reset") || strings.HasSuffix(r.URL.Path, "/usage") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var runPlusHostJournal = func(ctx context.Context, unit string) ([]byte, error) {
	// Fixed service names, bounded current-boot output, no shell or arbitrary units.
	return exec.CommandContext(ctx, "journalctl", "--boot", "--no-pager", "--lines=200", "--output=short-iso", "--unit="+unit).Output()
}

func (s *server) plusHostLogs(w http.ResponseWriter, r *http.Request) {
	if !s.plus {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	service := r.URL.Query().Get("service")
	if service == "" {
		service = "management-agent"
	}
	units := map[string]string{"management-agent": "justvoxel-management.service", "webui": "justvoxel-webui.service", "docker": "docker.service", "containerd": "containerd.service", "plus-setup": plusSetupUnit, "plus-stack": "justvoxel-plus-stack.service"}
	unit, ok := units[service]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid host log service")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	data, err := runPlusHostJournal(ctx, unit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "host logs are unavailable")
		return
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if operationDiagnosticSensitivePattern.MatchString(line) {
			lines[i] = "<sensitive log line redacted>"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": service, "output": strings.Join(lines, "\n")})
}
