package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAdminSystemUpdateStatusReadsDeploymentState(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != adminSystemUpdatesPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		body := `{"ok":true,"read_only":false,"reboot_required":true,"running":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing","version":"10","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"staged":{"image":"ghcr.io/home-server-project/justvoxel-vm:testing","version":"11","digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	status, err := client.AdminSystemUpdateStatus(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if !status.OK || !status.RebootRequired || status.Staged == nil || status.Staged.Version != "11" {
		t.Fatalf("unexpected system update status: %#v", status)
	}
}

func TestAdminSystemUpdatePostsOrdinaryUpdateRequest(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != adminSystemUpdatesPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body := `{"ok":true,"read_only":false,"reboot_required":false,"running":{"version":"10","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"message":"JustVoxel OS is current."}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	status, err := client.AdminSystemUpdate(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if status.Message != "JustVoxel OS is current." || status.RebootRequired {
		t.Fatalf("unexpected update response: %#v", status)
	}
}


func TestAdminSystemUpdateRebootSendsSelectedOptions(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != adminSystemUpdateRebootPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var request adminSystemUpdateRebootRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.ActionConfirmed || !request.ConfirmPlayers || !request.BackupMinecraft || request.WarningSeconds != 10 {
			t.Fatalf("unexpected reboot request: %#v", request)
		}
		body := `{"ok":true,"state":"queued","accepted":true,"warning_seconds":10,"backup_minecraft":true}`
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	result, err := client.AdminSystemUpdateReboot(context.Background(), "session-token", AdminSystemUpdateRebootOptions{
		ConfirmPlayers: true, BackupMinecraft: true, WarningSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.WarningSeconds != 10 || !result.BackupMinecraft {
		t.Fatalf("unexpected reboot response: %#v", result)
	}
}

func TestAdminSystemUpdateRebootStatusReadsCountdown(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != adminSystemUpdateRebootPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body := `{"ok":true,"state":"countdown","deadline_unix":1234,"online":2,"warning_seconds":60}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	result, err := client.AdminSystemUpdateRebootStatus(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "countdown" || result.DeadlineUnix != 1234 || result.WarningSeconds != 60 {
		t.Fatalf("unexpected reboot status: %#v", result)
	}
}
