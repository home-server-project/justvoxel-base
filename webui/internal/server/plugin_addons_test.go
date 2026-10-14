package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeAddonsAPI struct {
	fakePluginsWorkspaceAPI
	uploads int
	fields  map[string]string
}

func (f *fakeAddonsAPI) AdminAddonInventory(context.Context, string) (api.AddonInventory, error) {
	return api.AddonInventory{Sections: []api.AddonSection{{Kind: "skript_script", Host: api.AddonHost{Available: false, Message: "Host unavailable"}, Files: []api.AddonItem{{AddonEntry: api.AddonEntry{Filename: "test.sk"}, State: "installed"}}}}}, nil
}
func (f *fakeAddonsAPI) AdminAddonUpload(_ context.Context, _ string, contentType string, body io.Reader, apply bool) (api.AddonPlan, api.PersistentOperationResponse, error) {
	req := httptest.NewRequest("POST", "/", body)
	req.Header.Set("Content-Type", contentType)
	reader, err := req.MultipartReader()
	if err != nil {
		return api.AddonPlan{}, api.PersistentOperationResponse{}, err
	}
	fields := map[string]string{}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return api.AddonPlan{}, api.PersistentOperationResponse{}, e
		}
		raw, e := io.ReadAll(part)
		if e != nil {
			return api.AddonPlan{}, api.PersistentOperationResponse{}, e
		}
		fields[part.FormName()] = string(raw)
	}
	f.uploads++
	f.fields = fields
	return api.AddonPlan{AddonEntry: api.AddonEntry{Kind: fields["type"], Filename: "script.sk"}, Fingerprint: strings.Repeat("a", 64)}, api.PersistentOperationResponse{}, nil
}
func (f *fakeAddonsAPI) AdminAddonRecover(context.Context, string, api.PluginInstallAcknowledgeRequest) (api.PersistentOperationResponse, error) {
	return api.PersistentOperationResponse{}, nil
}
func addonWebRequest(t *testing.T, fields [][2]string, csrf string, apply bool) *http.Request {
	t.Helper()
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	for _, field := range fields {
		writer.WriteField(field[0], field[1])
	}
	part, _ := writer.CreateFormFile("file", "script.sk")
	part.Write([]byte("on join: stop"))
	writer.Close()
	path := "plan"
	if apply {
		path = "install"
	}
	req := authenticatedAdminRequest("POST", "http://example/api/plugins/workspace/addons/"+path, "")
	req.Body = io.NopCloser(&b)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", csrf)
	return req
}
func TestAddonWorkspaceStreamingAuthorizationAndContract(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "administrator"} {
		client := &fakeAddonsAPI{}
		client.role = role
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, addonWebRequest(t, [][2]string{{"type", "skript_script"}}, "csrf-token", false))
		want := 403
		if role == "administrator" {
			want = 200
		}
		if rr.Code != want {
			t.Fatal(role, rr.Code, rr.Body.String())
		}
		if role != "administrator" && client.uploads != 0 {
			t.Fatal("unauthorized upload")
		}
	}
	client := &fakeAddonsAPI{}
	client.role = "administrator"
	app, _ := New(client, Config{})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, addonWebRequest(t, [][2]string{{"type", "skript_script"}}, "wrong", false))
	if rr.Code != 403 || client.uploads != 0 {
		t.Fatal("CSRF bypass")
	}
	for _, fields := range [][][2]string{{{"type", "unsupported"}}, {{"type", "skript_script"}, {"path", "plugins/Skript/scripts"}}, {{"type", "skript_script"}, {"url", "https://example.test"}}, {{"type", "skript_script"}, {"type", "skript_script"}}} {
		rr = httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, addonWebRequest(t, fields, "csrf-token", false))
		if rr.Code < 400 || client.uploads != 0 {
			t.Fatal("unsafe contract forwarded", fields, rr.Code)
		}
	}
	fingerprint := "sha256:" + strings.Repeat("a", 64)
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, addonWebRequest(t, [][2]string{{"type", "skript_script"}, {"plan_fingerprint", fingerprint}, {"confirm_players", "false"}}, "csrf-token", true))
	if rr.Code != 202 || client.fields["file"] != "on join: stop" || client.fields["plan_fingerprint"] != fingerprint {
		t.Fatal("apply did not resend", rr.Code, client.fields)
	}
}
func TestAddonWorkspaceRenderingContract(t *testing.T) {
	script := pluginWorkspaceScript(t)
	raw, err := os.ReadFile("templates/header.html")
	if err != nil {
		t.Fatal(err)
	}
	header := string(raw)
	if strings.Count(header, `data-plugins-tab=`) != 3 || !strings.Contains(header, `data-plugins-tab="addons"`) {
		t.Fatal("third tab missing")
	}
	for _, want := range []string{`picker.type = "file"`, `picker.accept = section.accept`, `picker.disabled = !section.host.available`, `"Review / Install"`, `"Managed by Add-ons"`, `"Updates are not available yet"`, `rows("Destination", plan.destination)`, `rows("Type", section.name)`, `rows("File", plan.filename)`, `rows("Size", readableSize(plan.size))`, `rows("Restart required", plan.restart_required`, `body.append("file", file, file.name)`, `body.append("plan_fingerprint", plan.plan_fingerprint)`, `addonRequest("install", section.kind, file, plan`, `Plugins -> Add-ons -> BlueMap Add-ons`} {
		if !strings.Contains(script, want) {
			t.Fatal("missing UI contract", want)
		}
	}
	section := pluginScriptSection(t, script, `  const addonRequest =`, `  const load =`)
	for _, bad := range []string{`localStorage`, `sessionStorage`, `type = "url"`, `type = "text"`, `body.append("path"`, `body.append("destination"`} {
		if strings.Contains(section, bad) {
			t.Fatal("unexpected Add-ons capability", bad)
		}
	}
}
