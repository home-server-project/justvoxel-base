package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeUPSAPI struct {
	fakeAPI
	role            string
	forgetCalls     int
	shutdownCalls   int
	sharingCalls    int
	shutdownRequest api.UPSShutdownRequest
	sharingRequest  api.UPSSharingRequest
	sourceRequest   api.UPSSource
}

func (f *fakeUPSAPI) Session(_ context.Context, _ string) (api.SessionInfo, error) {
	return api.SessionInfo{Role: f.role}, nil
}

func (f *fakeUPSAPI) UPSStatus(context.Context, string) (api.UPSStatus, error) {
	return api.UPSStatus{}, nil
}

func (f *fakeUPSAPI) AdminSetUPSSource(_ context.Context, _ string, source api.UPSSource) (api.UPSStatus, error) {
	f.sourceRequest = source
	return api.UPSStatus{}, nil
}

func (f *fakeUPSAPI) AdminForgetUPSSource(context.Context, string) (api.UPSStatus, error) {
	f.forgetCalls++
	return api.UPSStatus{Available: true, Source: api.UPSSource{Mode: "local"}}, nil
}

func (f *fakeUPSAPI) AdminSetUPSShutdown(_ context.Context, _ string, request api.UPSShutdownRequest) (api.UPSStatus, error) {
	f.shutdownCalls++
	f.shutdownRequest = request
	return api.UPSStatus{Available: true, ProtectionEnabled: request.Enabled}, nil
}

func (f *fakeUPSAPI) AdminSetUPSSharing(_ context.Context, _ string, request api.UPSSharingRequest) (api.UPSStatus, error) {
	f.sharingCalls++
	f.sharingRequest = request
	return api.UPSStatus{Available: true, SharingEnabled: request.Enabled}, nil
}

func TestUPSOptionsWebRoutesRequireAdministratorAndCSRF(t *testing.T) {
	client := &fakeUPSAPI{role: "administrator"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/ups/shutdown", "/api/ups/sharing"} {
		request := func(form string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "http://example"+path, strings.NewReader(form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", "null")
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
			req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			return rr
		}
		if rr := request("enabled=false"); rr.Code != http.StatusForbidden {
			t.Fatalf("%s missing CSRF = %d", path, rr.Code)
		}
		client.role = "operator"
		if rr := request("csrf=token&enabled=false"); rr.Code != http.StatusForbidden {
			t.Fatalf("%s operator = %d", path, rr.Code)
		}
		client.role = "administrator"
		if rr := request("csrf=token&enabled=false"); rr.Code != http.StatusOK {
			t.Fatalf("%s administrator = %d: %s", path, rr.Code, rr.Body.String())
		}
	}
	if client.shutdownCalls != 1 || client.sharingCalls != 1 {
		t.Fatalf("unexpected Management calls: shutdown=%d sharing=%d", client.shutdownCalls, client.sharingCalls)
	}
}

func TestUPSOptionsWebRoutesForwardSettings(t *testing.T) {
	client := &fakeUPSAPI{role: "administrator"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ path, body string }{
		{"/api/ups/shutdown", "csrf=token&enabled=true&delay_seconds=120&monitor_username=monitor&monitor_password=secret"},
		{"/api/ups/sharing", "csrf=token&enabled=true&listen_address=192.0.2.4&listen_port=3493&client_username=client&client_password=secret"},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://example"+item.path, strings.NewReader(item.body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "secret") {
			t.Fatalf("%s response = %d: %s", item.path, rr.Code, rr.Body.String())
		}
	}
	if !client.shutdownRequest.Enabled || client.shutdownRequest.DelaySeconds != 120 || client.shutdownRequest.MonitorPassword != "secret" {
		t.Fatalf("shutdown request was not forwarded: %#v", client.shutdownRequest)
	}
	if !client.sharingRequest.Enabled || client.sharingRequest.ListenAddress != "192.0.2.4" || client.sharingRequest.ClientPassword != "secret" {
		t.Fatalf("sharing request was not forwarded: %#v", client.sharingRequest)
	}
}

func TestUPSSourceWebRouteForwardsLocalFields(t *testing.T) {
	client := &fakeUPSAPI{role: "administrator"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/api/ups/source", strings.NewReader("csrf=token&mode=local&ups_name=main&driver=usbhid-ups&device_port=auto"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || client.sourceRequest.UPSName != "main" || client.sourceRequest.Driver != "usbhid-ups" || client.sourceRequest.DevicePort != "auto" {
		t.Fatalf("local source not forwarded: status=%d source=%#v", rr.Code, client.sourceRequest)
	}
}

func TestUPSSourceForgetWebRouteRequiresAdministratorAndCSRF(t *testing.T) {
	client := &fakeUPSAPI{role: "administrator"}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(form string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://example/api/ups/source/forget", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		return rr
	}
	if rr := request(""); rr.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d, want 403", rr.Code)
	}
	client.role = "operator"
	if rr := request("csrf=token"); rr.Code != http.StatusForbidden {
		t.Fatalf("operator forget = %d, want 403", rr.Code)
	}
	if client.forgetCalls != 0 {
		t.Fatal("forbidden request reached Management API")
	}
	client.role = "administrator"
	rr := request("csrf=token")
	if rr.Code != http.StatusOK {
		t.Fatalf("administrator forget = %d: %s", rr.Code, rr.Body.String())
	}
	var status api.UPSStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Source.Mode != "local" || client.forgetCalls != 1 {
		t.Fatalf("unexpected forget result: status=%#v calls=%d", status, client.forgetCalls)
	}
}
