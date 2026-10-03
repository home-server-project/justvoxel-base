package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMinecraftStackUpdateClientUsesFixedAuthenticatedEndpoint(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/version/update" || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("unexpected update request: %s %s", r.Method, r.URL.Path)
		}
		var request MinecraftStackUpdateRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.PlanFingerprint != persistentOperationTestFingerprint || !request.ConfirmPlayers {
			t.Fatal("review or player consent lost")
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(strings.ReplaceAll(persistentOperationTestBody, `"setup"`, `"minecraft_update"`))), Header: make(http.Header)}, nil
	})}}
	result, err := client.AdminMinecraftStackUpdate(context.Background(), "session-token", MinecraftStackUpdateRequest{PlanFingerprint: persistentOperationTestFingerprint, ConfirmPlayers: true})
	if err != nil || result.Operation == nil || result.Operation.OperationType != "minecraft_update" {
		t.Fatalf("result: %#v %v", result, err)
	}
}
