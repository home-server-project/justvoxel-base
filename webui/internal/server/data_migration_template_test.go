package server

import (
	"strings"
	"testing"
)

func TestDataMigrationTemplatesKeepSafetyAndProgressInAgentFlow(t *testing.T) {
	review, err := assets.ReadFile("templates/data_migration_review.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(review)
	for _, want := range []string{
		"Agent-owned safety sequence",
		`name="plan_fingerprint"`,
		`name="destructive_confirmation"`,
		"Type MIGRATE",
		`name="players_confirmed"`,
		`action="/settings/data-migration/apply"`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("migration review missing %q", want)
		}
	}

	progress, err := assets.ReadFile("templates/data_migration_progress.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(progress), "Refreshing this page does not start another migration.") {
		t.Fatal("migration progress page does not explain reconnect semantics")
	}

	script, err := assets.ReadFile("static/data-migration-operation.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, want := range []string{
		`operation.operation_type !== "data_migration"`,
		`fetch(statusURL`,
		`["succeeded", "rolled_back", "needs_attention"]`,
		`window.setTimeout(poll, 2500)`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("migration progress script missing %q", want)
		}
	}
}
