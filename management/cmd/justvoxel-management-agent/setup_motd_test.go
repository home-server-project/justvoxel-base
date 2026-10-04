package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSetupAutomaticMOTDUsesResolvedPlanBeforeFingerprint(t *testing.T) {
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	for _, tc := range []struct{ software, version, want string }{
		{"paper", "26.3", "JustVoxel Paper Minecraft 26.3 Server"},
		{"purpur", "26.2", "JustVoxel Purpur Minecraft 26.2 Server"},
		{"vanilla", "26.3", "JustVoxel Vanilla Minecraft 26.3 Server"},
	} {
		t.Run(tc.software, func(t *testing.T) {
			var request adminSetupPlanRequest
			if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
				t.Fatal(err)
			}
			request.Server.MOTDAutomatic = true
			request.Server.MOTD = ""
			request.Minecraft.ServerType = tc.software
			request.Minecraft.VersionPolicy = "latest"
			request.Minecraft.Version = "LATEST"
			request.Server.BedrockEnabled = tc.software != "vanilla"
			runAdminSetupPlanHelper = func(_ context.Context, payload []byte) ([]byte, error) {
				if strings.Contains(string(payload), "motd_automatic") {
					t.Fatal("automatic state leaked into strict shell helper contract")
				}
				var response adminSetupPlanResponse
				if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &response); err != nil {
					t.Fatal(err)
				}
				response.Normalized.Minecraft.ServerType = tc.software
				response.Normalized.Minecraft.Version = tc.version
				response.Normalized.Server.BedrockEnabled = request.Server.BedrockEnabled
				return json.Marshal(response)
			}
			plan, err := authoritativeAdminSetupPlan(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Normalized.Server.MOTD != tc.want {
				t.Fatalf("MOTD = %q, want %q", plan.Normalized.Server.MOTD, tc.want)
			}
			fingerprint, fingerprintErr := adminSetupPlanFingerprint(plan.SchemaVersion, plan.Normalized, plan.Requirements)
			if fingerprintErr != nil || fingerprint != plan.PlanFingerprint {
				t.Fatal("generated MOTD is not bound to plan identity")
			}
			generatedFingerprint := plan.PlanFingerprint
			request.Server.MOTDAutomatic = false
			runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) { return []byte(validAdminSetupPlanResponse), nil }
			custom, err := authoritativeAdminSetupPlan(context.Background(), request)
			if err != nil || custom.Normalized.Server.MOTD != "Family server" {
				t.Fatalf("existing nonautomatic setup changed: %#v, %v", custom, err)
			}
			if custom.PlanFingerprint == generatedFingerprint {
				t.Fatal("MOTD change did not change plan identity")
			}
		})
	}
}

func TestSetupAutomaticMOTDRejectsUnresolvedVersion(t *testing.T) {
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	for _, version := range []string{"", "LATEST", "latest", "recommended", "pinned"} {
		t.Run(version, func(t *testing.T) {
			var request adminSetupPlanRequest
			_ = json.Unmarshal([]byte(validAdminSetupPlanRequest), &request)
			request.Server.MOTDAutomatic = true
			runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
				var response adminSetupPlanResponse
				_ = json.Unmarshal([]byte(validAdminSetupPlanResponse), &response)
				response.Normalized.Minecraft.Version = version
				return json.Marshal(response)
			}
			plan, err := authoritativeAdminSetupPlan(context.Background(), request)
			if err == nil || plan.PlanFingerprint != "" {
				t.Fatal("unresolved version produced an applicable automatic MOTD plan")
			}
		})
	}
}

func TestSetupCustomMOTDIsNeverRegenerated(t *testing.T) {
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	for _, software := range []string{"paper", "purpur", "vanilla"} {
		for _, version := range []string{"26.2", "26.3"} {
			var request adminSetupPlanRequest
			_ = json.Unmarshal([]byte(validAdminSetupPlanRequest), &request)
			request.Server.MOTD = "  Our §a Minecraft server  "
			request.Minecraft.ServerType = software
			request.Server.BedrockEnabled = software != "vanilla"
			runAdminSetupPlanHelper = func(_ context.Context, payload []byte) ([]byte, error) {
				var helper adminSetupPlanRequest
				if err := json.Unmarshal(payload, &helper); err != nil {
					t.Fatal(err)
				}
				if helper.Server.MOTD != request.Server.MOTD {
					t.Fatal("custom MOTD changed before validation")
				}
				var response adminSetupPlanResponse
				_ = json.Unmarshal([]byte(validAdminSetupPlanResponse), &response)
				response.Normalized.Minecraft.ServerType = software
				response.Normalized.Minecraft.Version = version
				response.Normalized.Server.MOTD = helper.Server.MOTD
				response.Normalized.Server.BedrockEnabled = helper.Server.BedrockEnabled
				return json.Marshal(response)
			}
			plan, err := authoritativeAdminSetupPlan(context.Background(), request)
			if err != nil || plan.Normalized.Server.MOTD != request.Server.MOTD {
				t.Fatalf("%s %s changed custom MOTD: %#v, %v", software, version, plan, err)
			}
		}
	}
}
