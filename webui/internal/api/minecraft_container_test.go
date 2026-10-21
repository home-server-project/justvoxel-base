package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestContainerClientUsesFixedSelectedSurfaceAndBoundedDecisions(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/minecraft/instances/jv-family1/container/update" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("unexpected container endpoint %s", r.URL)
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid request")
		}
		if len(body) != 4 || body["plan_fingerprint"] != persistentOperationTestFingerprint || body["restart_now"] != false || body["backup_before_restart"] != false || body["confirm_players"] != false {
			t.Fatalf("unbounded or changed decisions: %#v", body)
		}
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(strings.ReplaceAll(persistentOperationTestBody, `"setup"`, `"minecraft_container_update"`))), Header: make(http.Header)}, nil
	})}}
	result, err := client.MinecraftInstanceContainerUpdate(context.Background(), "session-token", "jv-family1", MinecraftContainerUpdateRequest{PlanFingerprint: persistentOperationTestFingerprint})
	if err != nil || result.Operation == nil || result.Operation.OperationType != "minecraft_container_update" {
		t.Fatal(result, err)
	}
}
func TestContainerClientRejectsWrongSelectedStatusIdentity(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/minecraft/instances/jv-family1/container/status" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"instance_id":"jv-sibling","state":"up_to_date"}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.MinecraftInstanceContainerStatus(context.Background(), "session-token", "jv-family1"); err == nil {
		t.Fatal("wrong selected identity accepted")
	}
}
