package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPluginManageExplicitClientContracts(t *testing.T) {
	for _, action := range []string{"enable", "disable", "remove"} {
		t.Run(action, func(t *testing.T) {
			calls := []string{}
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer session" {
					t.Fatal("administrator session not forwarded")
				}
				payload := `{"operation":{"operation_id":"` + resetTestOperationID + `","operation_type":"plugin_` + action + `","state":"running"}}`
				if r.URL.Path == "/v1/admin/plugins/manage/plan" {
					var request PluginManageRequest
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.Action != action || request.ProjectID != "root" {
						t.Fatal("plan identity changed")
					}
					payload = `{"action":"` + action + `","project_id":"root","state":"enabled","plan_fingerprint":"` + resetTestFingerprint + `"}`
				}
				if r.URL.Path == "/v1/admin/plugins/manage" {
					var request PluginManageApplyRequest
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.Action != action || request.PlanFingerprint != resetTestFingerprint || !request.ConfirmPlayers {
						t.Fatal("apply confirmation changed")
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
			})}}
			if _, err := client.AdminPluginManagePlan(context.Background(), "session", PluginManageRequest{action, "root"}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.AdminPluginManage(context.Background(), "session", PluginManageApplyRequest{action, "root", resetTestFingerprint, true}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.AdminPluginManageAcknowledge(context.Background(), "session", PluginInstallAcknowledgeRequest{resetTestOperationID}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.AdminCurrentPluginOperation(context.Background(), "session"); err != nil {
				t.Fatal(err)
			}
			if strings.Join(calls, ",") != "/v1/admin/plugins/manage/plan,/v1/admin/plugins/manage,/v1/admin/plugins/manage/acknowledge,/v1/admin/plugins/operation" {
				t.Fatal(calls)
			}
		})
	}
}
func TestPluginManageRejectsArbitraryActionsAndInvalidIdentity(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid request reached Agent"); return nil, nil })}}
	for _, req := range []PluginManageRequest{{"update", "root"}, {"delete", "root"}, {"enable", "../root"}, {"disable", ""}} {
		if _, err := client.AdminPluginManagePlan(context.Background(), "session", req); err == nil {
			t.Fatal("invalid request accepted", req)
		}
	}
	if _, err := client.AdminPluginManage(context.Background(), "session", PluginManageApplyRequest{Action: "remove", ProjectID: "root", PlanFingerprint: "invalid"}); err == nil {
		t.Fatal("invalid fingerprint accepted")
	}
	if _, err := client.AdminPluginManageAcknowledge(context.Background(), "session", PluginInstallAcknowledgeRequest{OperationID: "../operation"}); err == nil {
		t.Fatal("invalid acknowledgement accepted")
	}
}
