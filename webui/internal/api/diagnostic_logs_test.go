package api

import "testing"

func TestValidDiagnosticLogIdentifier(t *testing.T) {
	const validID = "12345678-1234-4234-8234-123456789abc"
	categories := []string{"setup", "migration", "restore", "reset", "operation"}
	for _, category := range categories {
		t.Run(category, func(t *testing.T) {
			if !validDiagnosticLogIdentifier(category, validID) {
				t.Fatal("valid diagnostic log identifier rejected")
			}
		})
	}

	for _, category := range []string{"unknown", "", "../setup", "setup/operation"} {
		t.Run("invalid category/"+category, func(t *testing.T) {
			if validDiagnosticLogIdentifier(category, validID) {
				t.Fatal("invalid diagnostic log category accepted")
			}
		})
	}

	invalidIDs := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"operation name", "operation-123"},
		{"traversal", "../" + validID},
		{"absolute path", "/" + validID},
		{"relative path", "logs/" + validID},
		{"backslash path", "logs\\" + validID},
		{"filename", validID + ".log"},
		{"missing hyphens", "12345678123442348234123456789abc"},
		{"short UUID", "12345678-1234-4234-8234-123456789ab"},
		{"nonhex UUID", "12345678-1234-4234-8234-123456789abg"},
		{"uppercase UUID", "12345678-1234-4234-8234-123456789ABC"},
		{"wrong UUID version", "12345678-1234-1234-8234-123456789abc"},
		{"wrong UUID variant", "12345678-1234-4234-7234-123456789abc"},
	}
	for _, category := range categories {
		for _, invalid := range invalidIDs {
			t.Run(category+"/"+invalid.name, func(t *testing.T) {
				if validDiagnosticLogIdentifier(category, invalid.id) {
					t.Fatal("invalid diagnostic log ID accepted")
				}
			})
		}
	}
}
