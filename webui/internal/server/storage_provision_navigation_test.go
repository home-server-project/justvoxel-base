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
	markup := string(header)
	if !strings.Contains(markup, `data-storage-open`) || !strings.Contains(markup, "<strong>Storage</strong>") || strings.Contains(markup, `href="/settings/storage-provision"`) {
		t.Fatal("Control Center must launch the Storage workspace")
	}

}
