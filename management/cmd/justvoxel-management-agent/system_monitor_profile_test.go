package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func systemMonitorProfileTestServer(t *testing.T, role principalRole) (*server, *webUIStore) {
	t.Helper()
	store, _ := openTestWebUIStore(t)
	now := time.Now()
	return &server{
		store: store,
		sessions: map[string]session{
			"token": {
				Username:   "test-user",
				Role:       role,
				AuthSource: authSourceWebUI,
				Created:    now,
				LastSeen:   now,
			},
		},
	}, store
}

func systemMonitorProfileRequest(method, path string, body any) *http.Request {
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		data, _ := json.Marshal(body)
		payload = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, "http://unix"+path, payload)
	req.Header.Set("Authorization", "Bearer token")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestSystemMonitorProfileDefaultAndPersistence(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	profile, err := store.readSystemMonitorProfile()
	if err != nil {
		t.Fatal(err)
	}
	if profile != defaultSystemMonitorProfile() {
		t.Fatalf("default profile = %#v, want %#v", profile, defaultSystemMonitorProfile())
	}

	profile.Sensors = false
	profile.Alerts = false
	profile.ProcessCount = 15
	if err := store.writeSystemMonitorProfile(profile); err != nil {
		t.Fatal(err)
	}
	got, err := store.readSystemMonitorProfile()
	if err != nil {
		t.Fatal(err)
	}
	if got != profile {
		t.Fatalf("saved profile = %#v, want %#v", got, profile)
	}
}

func TestViewerAndOperatorCanReadSystemMonitorProfile(t *testing.T) {
	for _, role := range []principalRole{roleViewer, roleOperator} {
		t.Run(string(role), func(t *testing.T) {
			s, _ := systemMonitorProfileTestServer(t, role)
			rr := httptest.NewRecorder()
			s.systemMonitorProfileGet(rr, systemMonitorProfileRequest(http.MethodGet, "/v1/system-monitor/profile", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("read returned %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestOnlyAdministratorCanWriteSystemMonitorProfile(t *testing.T) {
	profile := defaultSystemMonitorProfile()
	profile.Network = false

	viewer, _ := systemMonitorProfileTestServer(t, roleViewer)
	rr := httptest.NewRecorder()
	viewer.systemMonitorProfileSet(rr, systemMonitorProfileRequest(http.MethodPost, "/v1/admin/system-monitor/profile", profile))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer write returned %d, want %d", rr.Code, http.StatusForbidden)
	}

	admin, store := systemMonitorProfileTestServer(t, roleAdministrator)
	rr = httptest.NewRecorder()
	admin.systemMonitorProfileSet(rr, systemMonitorProfileRequest(http.MethodPost, "/v1/admin/system-monitor/profile", profile))
	if rr.Code != http.StatusOK {
		t.Fatalf("administrator write returned %d: %s", rr.Code, rr.Body.String())
	}
	got, err := store.readSystemMonitorProfile()
	if err != nil {
		t.Fatal(err)
	}
	if got != profile {
		t.Fatalf("administrator write saved %#v, want %#v", got, profile)
	}
}

func TestSystemMonitorProfileProcessCountValidation(t *testing.T) {
	for _, count := range []int{5, 10, 15, 20, 12, 0, -1} {
		admin, store := systemMonitorProfileTestServer(t, roleAdministrator)
		profile := defaultSystemMonitorProfile()
		profile.ProcessCount = count
		rr := httptest.NewRecorder()
		admin.systemMonitorProfileSet(rr, systemMonitorProfileRequest(http.MethodPost, "/v1/admin/system-monitor/profile", profile))
		if count == 5 || count == 10 || count == 15 {
			if rr.Code != http.StatusOK {
				t.Errorf("process count %d returned %d: %s", count, rr.Code, rr.Body.String())
			}
			got, err := store.readSystemMonitorProfile()
			if err != nil || got.ProcessCount != count {
				t.Errorf("process count %d was not saved: profile=%#v, err=%v", count, got, err)
			}
		} else if rr.Code != http.StatusBadRequest {
			t.Errorf("invalid process count %d returned %d: %s", count, rr.Code, rr.Body.String())
		}
	}
}
