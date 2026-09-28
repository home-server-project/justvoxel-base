package server

import (
	"regexp"
	"strings"
	"testing"
)

func TestInteractiveHoverAndFocusStayUnderPointer(t *testing.T) {
	cases := []struct {
		file     string
		selector string
		feedback []string
	}{
		{"static/new-backups.css", ".backup-file-selectable:hover", []string{"border-color:", "background:"}},
		{"static/app.css", ".nav-trigger:hover", []string{"background:", "border-color:", "color:", "box-shadow:"}},
		{"static/app.css", ".nav-menu a:hover", []string{"background:", "border-color:", "color:"}},
		{"static/app.css", ".nav-logout button:hover", []string{"background:", "border-color:"}},
		{"static/app.css", ".system-power-button:hover,.system-power[open]>.system-power-button", []string{"background:", "border-color:", "color:", "box-shadow:"}},
		{"static/app.css", ".control-center-trigger:hover,.control-center[open]>.control-center-trigger", []string{"background:", "border-color:", "color:"}},
		{"static/app.css", ".control-tile:hover,.control-tile:focus-visible", []string{"background:", "border-color:", "color:"}},
		{"static/app.css", ".first-run-choice-card:hover,.first-run-choice-card:focus-visible", []string{"border-color:", "background:", "box-shadow:", "color:"}},
		{"static/app.css", ".minecraft-setup-invitation:hover,.minecraft-setup-invitation:focus-visible", []string{"border-color:", "box-shadow:", "color:"}},
	}
	movement := regexp.MustCompile(`transform\s*:\s*(?:translateY|scale)\s*\(`)
	for _, tc := range cases {
		t.Run(tc.selector, func(t *testing.T) {
			content, err := assets.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			rule := regexp.MustCompile(regexp.QuoteMeta(tc.selector) + `\s*\{([^{}]*)\}`)
			matches := rule.FindAllStringSubmatch(string(content), -1)
			if len(matches) == 0 {
				t.Fatalf("missing CSS rule %q", tc.selector)
			}
			for _, match := range matches {
				if movement.MatchString(match[1]) {
					t.Fatalf("%q moves the control: %s", tc.selector, match[1])
				}
			}
			for _, property := range tc.feedback {
				if !strings.Contains(matches[0][1], property) {
					t.Errorf("%q lost %s feedback", tc.selector, property)
				}
			}
		})
	}
}
