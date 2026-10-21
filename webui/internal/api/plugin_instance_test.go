package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSelectedPluginPathsAndMismatchedOperations(t *testing.T) {
	ctx := WithMinecraftMaintenanceInstance(context.Background(), "jv-family1", "plugins")
	for _, suffix := range []string{"installed", "discover?q=test", "install/plan", "install", "manage/plan", "manage", "addons/plan", "addons/install", "addons/recover", "install/acknowledge", "manage/acknowledge", "operation"} {
		path, err := selectedMaintenancePath(ctx, "/v1/admin/plugins/"+suffix)
		if err != nil || path != "/v1/minecraft/instances/jv-family1/plugins/"+suffix {
			t.Fatal(path, err)
		}
	}
	for _, id := range []string{"", "minecraft", "main", "jv-short", "../jv-family1"} {
		if _, err := selectedMaintenancePath(WithMinecraftMaintenanceInstance(context.Background(), id, "plugins"), "/v1/admin/plugins/installed"); err == nil {
			t.Fatal("invalid ID accepted", id)
		}
	}
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/minecraft/instances/jv-family1/plugins/operation" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"operation":{"instance_id":"jv-sibling1","operation_type":"plugin_install"}}`))}, nil
	})}}
	if _, err := client.AdminCurrentPluginOperation(ctx, "session"); err == nil {
		t.Fatal("mismatched operation accepted")
	}
	if _, err := client.AdminCurrentPluginOperation(context.Background(), "session"); err == nil {
		t.Fatal("missing selection accepted")
	}
}

func TestSystemResetRejectsSelectedPlan(t *testing.T) {
	ctx := WithMinecraftMaintenanceInstance(context.Background(), "jv-family1", "reset/minecraft")
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/admin/reset/minecraft/plan" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"instance_id":"jv-sibling1","mode":"minecraft","plan_fingerprint":"` + resetTestFingerprint + `"}`))}, nil
	})}}
	if _, err := client.AdminMinecraftResetPlan(ctx, "session"); err == nil {
		t.Fatal("mismatched reset plan accepted")
	}
}
