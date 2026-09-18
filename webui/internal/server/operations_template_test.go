package server

import (
	"strings"
	"testing"
)

func TestOperationsTemplateExplainsBedrockWhitelistIdentity(t *testing.T) {
	content, err := assets.ReadFile("templates/operations.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{
		"Xbox gamertag",
		"Floodgate UUID",
		"without the leading <code>.</code>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("operations template missing %q", want)
		}
	}
}
