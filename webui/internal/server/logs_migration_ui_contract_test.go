package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSystemLogsGroupedResizableWorkspace(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil { t.Fatal(err) }
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil { t.Fatal(err) }
	for _, want := range []string{
		`["Setup", "Migration", "Restore", "Reset", "Support"]`,
		`entry.category === selectedGroup.toLowerCase()`,
		`No support bundles generated.`,
		`justvoxel.system.logs.columns`,
		`handle.dataset.logsSplitter`,
		`setPointerCapture`,
	} {
		if !strings.Contains(string(js), want) { t.Fatalf("Logs workspace missing %q", want) }
	}
	for _, want := range []string{
		`.system-logs-list{`, `.system-logs-viewer{`, `overflow-y:auto`,
		`.system-logs-splitter{`, `minmax(220px,1fr)`,
	} {
		if !strings.Contains(string(css), want) { t.Fatalf("Logs CSS missing %q", want) }
	}
}

func TestMigrationWorkspaceHasNaturalContentAndNormalConfirmation(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil { t.Fatal(err) }
	for _, forbidden := range []string{
		`.migration-workspace-body>[data-migration-workspace-content]{flex:1;min-height:0;overflow:auto}`,
		`.migration-workspace-content form>.action-row:last-of-type{position:sticky`,
	} {
		if strings.Contains(string(css), forbidden) { t.Fatalf("nested Migration scrolling remains: %q", forbidden) }
	}
	for _, name := range []string{"migration_workspace_recovery_review.html", "server_migration_recovery_review.html"} {
		markup, err := os.ReadFile(filepath.Join("templates", name))
		if err != nil { t.Fatal(err) }
		for _, want := range []string{"Your server data is safe", "Clean Up Recovery Files", "Clean up recovery files?", `name="cleanup_confirmed"`, "Server Import needs attention"} {
			if !strings.Contains(string(markup), want) { t.Fatalf("%s missing %q", name, want) }
		}
		for _, forbidden := range []string{"Type FINALIZE", "Authoritative Recovery plan", "Finalize Migration Recovery"} {
			if strings.Contains(string(markup), forbidden) { t.Fatalf("%s contains %q", name, forbidden) }
		}
	}
}

func TestMigrationWorkspaceHasNoInternalScroller(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil { t.Fatal(err) }
	rules := regexp.MustCompile(`(?s)([^{}]*\.migration-workspace-(?:tabs|body|content)[^{]*\{([^}]*)\})`).FindAllStringSubmatch(string(css), -1)
	if len(rules) == 0 { t.Fatal("Migration workspace CSS rules missing") }
	autoOverflow := regexp.MustCompile(`overflow(?:-x|-y)?\s*:\s*auto\b`)
	for _, rule := range rules {
		if autoOverflow.MatchString(rule[2]) { t.Fatalf("Migration internal scrolling remains: %s", rule[0]) }
	}
}
