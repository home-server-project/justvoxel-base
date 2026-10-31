package main

import (
	"context"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var gatewayUnitPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:@.-]{0,199}\.(service|socket|timer|target|mount|automount|path|slice|scope)$`)

func gatewaySystemdArguments(action, unit string) ([]string, bool) {
	if action == "daemon-reload" {
		return []string{"daemon-reload"}, unit == ""
	}
	if !gatewayUnitPattern.MatchString(unit) || strings.Contains(unit, "..") {
		return nil, false
	}
	switch action {
	case "status":
		return []string{"show", "--property=Id,LoadState,ActiveState,SubState,UnitFileState", "--", unit}, true
	case "start", "stop", "restart", "enable", "disable", "mask", "unmask":
		return []string{action, "--", unit}, true
	default:
		return nil, false
	}
}

var gatewaySystemctl = func(ctx context.Context, args ...string) ([]byte, error) {
	// Output is deliberately bounded independently of command success. Only show
	// properties below may be returned; command stderr is never exposed externally.
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	out := &gatewayResponse{header: make(http.Header), status: 200}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	return out.body.Bytes(), err
}

func (s *server) gatewaySystemd(w http.ResponseWriter, r *http.Request) {
	actor, ok := r.Context().Value(gatewayPrincipalKey{}).(session)
	if !ok || actor.ConnectionID == "" {
		writeError(w, 403, "external Developer Mode required")
		return
	}
	var request struct {
		Action string `json:"action"`
		Unit   string `json:"unit"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	args, ok := gatewaySystemdArguments(request.Action, request.Unit)
	if !ok {
		writeError(w, 400, "invalid systemd action or unit")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := gatewaySystemctl(ctx, args...)
	if err != nil {
		writeError(w, 409, "systemd operation failed")
		return
	}
	properties := map[string]string{}
	if request.Action == "status" {
		for _, line := range strings.Split(string(out), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch k {
			case "Id":
				properties["unit"] = v
			case "LoadState":
				properties["load_state"] = v
			case "ActiveState":
				properties["active_state"] = v
			case "SubState":
				properties["sub_state"] = v
			case "UnitFileState":
				properties["unit_file_state"] = v
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "unit": request.Unit, "status": properties})
}
