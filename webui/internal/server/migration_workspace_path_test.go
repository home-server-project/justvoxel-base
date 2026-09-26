package server

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSplitMigrationSMBLocationAcceptsNormalNetworkFolders(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantRoot string
		wantPath string
	}{
		{name: "share root", input: "//192.168.0.50/storage", wantRoot: "//192.168.0.50/storage"},
		{name: "folder below share", input: "//192.168.0.50/storage/TEMP", wantRoot: "//192.168.0.50/storage", wantPath: "TEMP"},
		{name: "nested folder", input: "//server/share/backups/minecraft", wantRoot: "//server/share", wantPath: "backups/minecraft"},
		{name: "windows slashes", input: "\\\\server\\share\\backups", wantRoot: "//server/share", wantPath: "backups"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, path, err := splitMigrationSMBLocation(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if root != tc.wantRoot || path != tc.wantPath {
				t.Fatalf("split %q = root %q path %q, want %q %q", tc.input, root, path, tc.wantRoot, tc.wantPath)
			}
		})
	}
}

func TestSplitMigrationSMBLocationRejectsUnsafePaths(t *testing.T) {
	for _, input := range []string{
		"server/share",
		"//server",
		"//server/share/../secret",
		"//server//folder",
	} {
		t.Run(input, func(t *testing.T) {
			if _, _, err := splitMigrationSMBLocation(input); err == nil {
				t.Fatalf("unsafe SMB location %q unexpectedly accepted", input)
			}
		})
	}
}

func TestMigrationWorkspaceImportParsesHumanSMBFolder(t *testing.T) {
	values := url.Values{
		"source_kind":         {"smb"},
		"source_remote":       {"//192.168.0.50/storage/TEMP"},
		"source_username":     {"iegor"},
		"source_smb_password": {"secret"},
	}
	req := httptest.NewRequest("POST", "/workspace/migration/import/review", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	parsed, err := parseMigrationWorkspaceImportForm(req, importDiscoveryFixture(true), importMediaFixture(), importStorageFixture())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Source.Source != "//192.168.0.50/storage" || parsed.Source.Path != "TEMP" || parsed.Source.Username != "iegor" {
		t.Fatalf("unexpected parsed SMB Import source: %#v", parsed.Source)
	}
}

func TestMigrationWorkspaceExportPreservesSMBFolderAcrossReviewApply(t *testing.T) {
	initial := url.Values{
		"kind":     {"smb"},
		"source":   {"//192.168.0.50/storage/TEMP"},
		"username": {"iegor"},
		"filename": {"justvoxel-migration-test.tar.gz"},
	}
	req := httptest.NewRequest("POST", "/workspace/migration/export/review", strings.NewReader(initial.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	parsed, err := parseMigrationWorkspaceExportForm(req, exportDiscoveryFixture())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Source != "//192.168.0.50/storage" || parsed.Path != "TEMP" {
		t.Fatalf("unexpected initial SMB Export target: %#v", parsed)
	}

	reviewed := url.Values{
		"kind":     {"smb"},
		"source":   {parsed.Source},
		"path":     {parsed.Path},
		"username": {"iegor"},
		"filename": {"justvoxel-migration-test.tar.gz"},
	}
	req = httptest.NewRequest("POST", "/workspace/migration/export/apply", strings.NewReader(reviewed.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reparsed, err := parseMigrationWorkspaceExportForm(req, exportDiscoveryFixture())
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Source != parsed.Source || reparsed.Path != parsed.Path {
		t.Fatalf("SMB Export folder changed across review/apply: first=%#v second=%#v", parsed, reparsed)
	}
}


func TestMigrationWorkspaceStatusAndChoiceStyling(t *testing.T) {
	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(styles)
	for _, want := range []string{
		".migration-workspace-state.is-error",
		".migration-workspace-state.is-info",
		".migration-choice:has(input:checked)",
		".migration-choice input[type=\"radio\"]{position:absolute!important",
		"opacity:0",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("migration workspace styling missing %q", want)
		}
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	if !strings.Contains(js, `setMigrationBusy(error?.message || "Migration operation could not be completed.", "error")`) {
		t.Fatal("migration errors are not rendered through the framed error state")
	}
}
