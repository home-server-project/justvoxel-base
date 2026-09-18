package server

import (
	"strings"
	"testing"
)

func TestAdvancedStorageNavigation(t *testing.T) {
	header, err := assets.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(header), `href="/settings/storage-provision">Advanced storage</a>`) {
		t.Fatal("Storage & Backups menu does not link to Advanced storage")
	}

	page, err := assets.ReadFile("templates/storage_provision.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(page)
	for _, want := range []string{`{{template "app-header" .}}`, `/static/app.js`, `/static/storage-provision.js`} {
		if !strings.Contains(markup, want) {
			t.Fatalf("advanced storage page missing shared navigation element %q", want)
		}
	}
}
