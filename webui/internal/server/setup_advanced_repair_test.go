package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

func TestAdvancedCrossplayApprovedOrder(t *testing.T) {
	markup := correctionSource(t, "templates/setup_wizard.html")
	_, step, _ := strings.Cut(markup, "{{else if eq .CurrentStep 2}}")
	step, _, _ = strings.Cut(step, "{{else if eq .CurrentStep 3}}")
	previous := -1
	for _, want := range []string{"Minecraft Java TCP port", "Bedrock UDP port", "Server update channel", `id="version-policy"`, `id="setup-version-preview"`, "Enable Bedrock cross-play", `class="setup-step-actions"`} {
		index := strings.Index(step, want)
		if index <= previous {
			t.Fatalf("approved Cross-play order broken at %q", want)
		}
		previous = index
	}
	if strings.Contains(step, "Container updates") {
		t.Fatal("internal update wording exposed")
	}
	if !strings.Contains(correctionSource(t, "static/setup.css"), ".setup-advanced .setup-wizard-panel .setup-version-preview{grid-template-columns:1fr}") {
		t.Fatal("status cards must stack across the full content width")
	}
}

// Render both Setup contexts before exercising the production selection script
// and pre-submit constraint validation in the dependency-free DOM fixture.
func TestSetupStorageBrowserFormSubmission(t *testing.T) {
	app, err := New(setupWizardStorageClient(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)
	type page struct {
		Name string `json:"name"`
		HTML string `json:"html"`
	}
	var pages []page
	capture := func(name string) {
		t.Helper()
		rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d", name, rr.Code)
		}
		pages = append(pages, page{name, rr.Body.String()})
	}
	capture("storage-system")
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	draft.Storage.Type = "partition"
	firstRunSetupDrafts.save(app, "session-token", draft)
	capture("storage-custom")
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", "csrf=csrf-token&storage_type=system&direction=next"))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("system storage Continue: %d", rr.Code)
	}
	capture("backups-system")
	// The shared component still renders the full migration workflow for Control
	// Center. Setup isolation must not remove or disable those controls globally.
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	controlCenter := setupStorageBrowserData(draft, "csrf-token", false)
	controlCenter.SetupPurpose = ""
	var rendered bytes.Buffer
	if err := app.templates.ExecuteTemplate(&rendered, "storage_browser_content", controlCenter); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"data-storage-minecraft-dialog", "data-storage-minecraft-mount required", "data-storage-minecraft-path required"} {
		if !strings.Contains(rendered.String(), want) {
			t.Fatalf("Control Center migration lost %q", want)
		}
	}

	payload, err := json.Marshal(pages)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "../../../tests/test-setup-form-submission.js")
	cmd.Stdin = strings.NewReader(string(payload))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered Setup DOM submission: %v\n%s", err, output)
	}
}
