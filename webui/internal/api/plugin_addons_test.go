package api

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestAddonKindsAndStreamingClient(t *testing.T) {
	for _, kind := range []string{"placeholderapi_expansion", "geyser_extension", "bluemap_addon", "skript_script", "worldedit_schematic"} {
		if !ValidAddonKind(kind) {
			t.Fatal(kind)
		}
	}
	for _, kind := range []string{"", "unsupported", "plugin", "/path"} {
		if ValidAddonKind(kind) {
			t.Fatal("unsupported kind", kind)
		}
	}
	for _, apply := range []bool{false, true} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		writer.WriteField("type", "skript_script")
		file, _ := writer.CreateFormFile("file", "script.sk")
		io.WriteString(file, "on join: stop")
		if apply {
			writer.WriteField("plan_fingerprint", resetTestFingerprint)
			writer.WriteField("confirm_players", "false")
		}
		writer.Close()
		c := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			want := "/v1/admin/plugins/addons/plan"
			if apply {
				want = "/v1/admin/plugins/addons/install"
			}
			if r.URL.Path != want || r.Header.Get("Authorization") != "Bearer session" {
				t.Fatal("upload boundary", r.URL)
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			parts := map[string]string{}
			for {
				part, e := reader.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				raw, _ := io.ReadAll(part)
				parts[part.FormName()] = string(raw)
			}
			if parts["file"] != "on join: stop" || parts["type"] != "skript_script" || apply && parts["plan_fingerprint"] != resetTestFingerprint {
				t.Fatal("file resend lost", parts)
			}
			response := `{"kind":"skript_script","filename":"script.sk","restart_required":true}`
			status := 200
			if apply {
				response = `{"operation":{"operation_type":"plugin_addon_install"}}`
				status = 202
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
		})}}
		plan, op, err := c.AdminAddonUpload(context.Background(), "session", writer.FormDataContentType(), &body, apply)
		if err != nil || !apply && plan.Kind != "skript_script" || apply && (op.Operation == nil || op.Operation.OperationType != "plugin_addon_install") {
			t.Fatal(plan, op, err)
		}
	}
}
