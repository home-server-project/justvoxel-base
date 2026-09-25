package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSystemWorkspaceDoesNotDependOnLegacyPageInterfaces(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate System Workspace dependency test source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "system_workspace.go"))
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
		"type systemWorkspaceResetAPI interface",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("System Workspace missing neutral API contract %q", want)
		}
	}
}
