package server

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoginTemplateUsesLocalLogo(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	err = app.templates.ExecuteTemplate(&rendered, "login.html", map[string]string{
		"Title":         "Sign in",
		"Version":       "webui-test-version",
		"ManagementAPI": "api-test-version",
		"Message":       "Login message",
		"Error":         "Login error",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, want := range []string{
		`<h1 class="login-brand"><img src="/static/justvoxel-login-logo.png" alt="JustVoxel" width="2172" height="724"></h1>`,
		`Minecraft Server Appliance`,
		`<form method="post" action="/login">`,
		`name="username" value="voxel" autocomplete="username" required`,
		`type="password" name="password" autocomplete="current-password" required autofocus`,
		`<button type="submit">Sign in</button>`,
		`<script src="/static/app.js" defer></script>`,
		`<div class="notice">Login message</div>`,
		`<div class="alert">Login error</div>`,
		`WebUI webui-test-version · Management API api-test-version`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login template missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"Sign in as the JustVoxel administrator.",
		"<h1>JustVoxel</h1>",
		"justvoxel-iso",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("login template contains unwanted branding %q", unwanted)
		}
	}
	if _, err := assets.ReadFile("static/justvoxel-login-logo.png"); err != nil {
		t.Fatalf("local login logo is unavailable: %v", err)
	}
}
