package server

import (
	"strings"
	"testing"
)

func TestFirstRunWorkspaceChoiceIsNonBlockingAndConfigurationDriven(t *testing.T) {
	templateSource, err := assets.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	templateText := string(templateSource)
	for _, want := range []string{
		"data-first-run-choice",
		"data-first-run-explore",
		"data-first-run-setup",
		"data-minecraft-setup-invitation",
		"href=\"/setup\"",
	} {
		if !strings.Contains(templateText, want) {
			t.Fatalf("dashboard first-run markup missing %q", want)
		}
	}

	scriptSource, err := assets.ReadFile("static/first-run.js")
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(scriptSource)
	if strings.Contains(scriptText, "window.location.replace(\"/setup\")") {
		t.Fatal("first-run flow must not force unconfigured administrators into setup")
	}
	for _, want := range []string{
		"role !== \"administrator\"",
		"configured === true",
		"window.localStorage.setItem(exploreStorageKey, \"true\")",
		"window.localStorage.removeItem(exploreStorageKey)",
		"showChoice()",
		"showInvitation()",
		"15000",
	} {
		if !strings.Contains(scriptText, want) {
			t.Fatalf("first-run behavior missing %q", want)
		}
	}

	styleSource, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styleText := string(styleSource)
	for _, want := range []string{
		".first-run-choice",
		".minecraft-setup-invitation",
		"@keyframes setup-invite-shimmer",
		"@media(prefers-reduced-motion:reduce)",
	} {
		if !strings.Contains(styleText, want) {
			t.Fatalf("first-run styling missing %q", want)
		}
	}
}
