package server

import (
	"strings"
	"testing"
)

func TestSystemWorkspaceDoesNotDependOnLegacyPageInterfaces(t *testing.T) {
	source, err := assets.ReadFile("system_workspace.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)

	for _, forbidden := range []string{
		"adminValidationAPI",
		"rolePagesAPI",
		"adminUsersAPI",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("System Workspace still depends on legacy page interface %q", forbidden)
		}
	}

	for _, want := range []string{
		"type systemWorkspaceValidationAPI interface",
		"type systemWorkspaceHistoryAPI interface",
		"type systemWorkspaceUsersAPI interface",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("System Workspace missing neutral API contract %q", want)
		}
	}
}
