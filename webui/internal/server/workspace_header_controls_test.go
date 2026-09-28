package server

import (
	"regexp"
	"strings"
	"testing"
)

func TestWorkspaceHeaderControls(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	refresh := regexp.MustCompile(`<button[^>]*data-([a-z-]+)-refresh\b[^>]*>(.*?)</button>`)
	close := regexp.MustCompile(`<button[^>]*data-([a-z-]+)-close\b[^>]*>(.*?)</button>`)
	refreshes := make(map[string]string)
	closes := make(map[string]string)
	for _, match := range refresh.FindAllStringSubmatch(string(header), -1) {
		refreshes[match[1]] = match[0]
		if match[2] != "Refresh" || !strings.Contains(match[0], "workspace-header-refresh") || strings.Contains(match[0], "↻") {
			t.Errorf("%s refresh is not the shared text control", match[1])
		}
	}
	for _, match := range close.FindAllStringSubmatch(string(header), -1) {
		closes[match[1]] = match[0]
		if !strings.Contains(match[2], "<svg ") || !strings.Contains(match[0], "workspace-header-close") || strings.Contains(match[0], "&times;") || strings.Contains(match[0], "×") || !strings.Contains(match[0], "aria-label=") {
			t.Errorf("%s close is not the shared accessible SVG control", match[1])
		}
	}
	for _, name := range []string{"network", "system", "minecraft", "storage", "backups", "migration", "version", "system-update"} {
		if refreshes[name] == "" || closes[name] == "" {
			t.Errorf("%s workspace lost its header controls", name)
		}
	}
	if closes["system-monitor"] == "" {
		t.Error("system monitor lost its close control")
	}
	if refreshes["system-monitor"] != "" {
		t.Error("system monitor gained an unexpected refresh control")
	}
	css, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{".workspace-header-refresh{", ".workspace-header-close{", ".workspace-header-close svg{"} {
		if !strings.Contains(string(css), class) {
			t.Errorf("shared workspace control CSS missing %s", class)
		}
	}
}
