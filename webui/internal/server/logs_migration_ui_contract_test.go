package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemLogsGroupedResizableWorkspace(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`["Setup", "Migration", "Restore", "Reset", "Support"]`,
		`entry.category === selectedGroup.toLowerCase()`,
		`No support bundles generated.`,
		`justvoxel.system.logs.columns`,
		`handle.dataset.logsSplitter`,
		`setPointerCapture`,
	} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("Logs workspace missing %q", want)
		}
	}
	for _, want := range []string{
		`.system-logs-list{`, `.system-logs-viewer{`, `overflow-y:auto`,
		`.system-logs-splitter{`, `minmax(220px,1fr)`,
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("Logs CSS missing %q", want)
		}
	}
}

func TestMigrationWorkspaceHasBoundedContentAndNormalConfirmation(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.migration-workspace-dialog{position:absolute`,
		`height:min(720px,calc(100dvh - 76px))`,
		`max-height:calc(100dvh - 76px)`,
		`.migration-workspace-shell{display:flex;flex-direction:column;height:100%;min-height:0`,
		`.migration-workspace-header{display:flex;flex:none`,
		`.migration-workspace-tabs{display:flex;flex:none`,
		`.migration-workspace-body{display:flex;flex:1;min-height:0;min-width:0;flex-direction:column;overflow-y:auto;overflow-x:hidden`,
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("bounded Migration workspace missing %q", want)
		}
	}
	if strings.Contains(string(css), `.migration-workspace-content form>.action-row:last-of-type{position:sticky`) {
		t.Fatal("Migration confirmation must remain in the content flow")
	}
	for _, name := range []string{"migration_workspace_recovery_review.html", "server_migration_recovery_review.html"} {
		markup, err := os.ReadFile(filepath.Join("templates", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Your server data is safe", "Clean Up Recovery Files", "Clean up recovery files?", `name="cleanup_confirmed"`, "Server Import needs attention"} {
			if !strings.Contains(string(markup), want) {
				t.Fatalf("%s missing %q", name, want)
			}
		}
		for _, forbidden := range []string{"Type FINALIZE", "Authoritative Recovery plan", "Finalize Migration Recovery"} {
			if strings.Contains(string(markup), forbidden) {
				t.Fatalf("%s contains %q", name, forbidden)
			}
		}
	}
}

func TestMigrationWorkspaceScrollsOnlyItsBody(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`.migration-workspace-body>[data-migration-workspace-content]{flex:1;min-height:0;overflow:auto}`,
		`.migration-workspace-content{overflow:auto`,
	} {
		if strings.Contains(string(css), forbidden) {
			t.Fatalf("Migration has a nested content scroller: %q", forbidden)
		}
	}
}
