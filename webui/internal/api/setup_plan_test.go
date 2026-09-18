package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const setupPlanTestFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupPlanTestRequest() AdminSetupPlanRequest {
	return AdminSetupPlanRequest{
		Server: AdminSetupPlanServerRequest{
			MOTD:           "Family Minecraft",
			MaxPlayers:     10,
			BedrockEnabled: true,
			Timezone:       "America/Toronto",
		},
		Minecraft: AdminSetupPlanMinecraftRequest{
			JavaMemory:      "6G",
			ContainerMemory: "8G",
			JavaPort:        25565,
			BedrockPort:     19132,
			ImageTag:        "stable",
			VersionPolicy:   "recommended",
			Version:         "",
		},
		Storage: AdminSetupPlanStorageRequest{
			Type:       "partition",
			Path:       "/var/mnt/justvoxel-data/minecraft",
			Device:     "/dev/vdb1",
			MountPoint: "/var/mnt/justvoxel-data",
		},
		Backups: AdminSetupPlanBackupsRequest{
			Automatic:  true,
			DailyTime:  "04:30",
			Keep:       7,
			Type:       "smb",
			Path:       "/var/mnt/justvoxel-backup/backups",
			MountPoint: "/var/mnt/justvoxel-backup",
			Source:     "//nas/minecraft-backups",
			Username:   "backup-user",
			Domain:     "HOME",
		},
	}
}

func TestAdminSetupPlanClientContract(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/setup/plan" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, want := range []string{
			`"motd":"Family Minecraft"`,
			`"max_players":10`,
			`"bedrock_enabled":true`,
			`"java_memory":"6G"`,
			`"container_memory":"8G"`,
			`"version_policy":"recommended"`,
			`"device":"/dev/vdb1"`,
			`"type":"smb"`,
			`"source":"//nas/minecraft-backups"`,
			`"username":"backup-user"`,
			`"domain":"HOME"`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("request body missing %s: %s", want, text)
			}
		}
		if strings.Contains(strings.ToLower(text), "password") {
			t.Fatalf("setup planning request must not contain password data: %s", text)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"ok":true,
				"schema_version":"v1",
				"plan_fingerprint":"` + setupPlanTestFingerprint + `",
				"normalized":{
					"server":{"motd":"Family Minecraft","max_players":10,"bedrock_enabled":true,"timezone":"America/Toronto"},
					"minecraft":{"java_memory":"6G","container_memory":"8G","java_port":25565,"bedrock_port":19132,"image_tag":"stable","requested_version_policy":"recommended","version_policy":"pinned","version":"1.21.8","system_memory_mib":16384,"system_reserve_mib":8192,"minecraft_uid":1000,"minecraft_gid":1000},
					"storage":{"type":"partition","path":"/var/mnt/justvoxel-data/minecraft","device":"/dev/vdb1","parent_disk":"/dev/vdb","model":"Virtual Disk","transport":"virtio","size_bytes":10737418240,"filesystem":"xfs","uuid":"data-uuid","mount_point":"/var/mnt/justvoxel-data","expected_uuid":"data-uuid","expected_source":"","mounted_at":"","source":"","system_disk":false,"purpose":"data"},
					"backups":{"type":"smb","path":"/var/mnt/justvoxel-backup/backups","device":"","parent_disk":"","model":"","transport":"","size_bytes":0,"filesystem":"","uuid":"","mount_point":"/var/mnt/justvoxel-backup","expected_uuid":"","expected_source":"//nas/minecraft-backups","mounted_at":"","source":"//nas/minecraft-backups","system_disk":false,"purpose":"","username":"backup-user","domain":"HOME","credentials_required":true,"automatic":true,"daily_time":"04:30","schedule":"*-*-* 04:30:00","keep":7}
				},
				"warnings":[{"code":"network_backup_dependency","message":"Network backup storage will be verified when setup is executed."}],
				"requirements":{"smb_password_required":true,"network_backup_validation_on_apply":true}
			}`)),
			Header: make(http.Header),
		}, nil
	})}}

	plan, err := client.AdminSetupPlan(context.Background(), "session-token", setupPlanTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.OK || plan.SchemaVersion != "v1" {
		t.Fatalf("unexpected setup plan envelope: %#v", plan)
	}
	if plan.PlanFingerprint != setupPlanTestFingerprint {
		t.Fatalf("fingerprint = %q, want %q", plan.PlanFingerprint, setupPlanTestFingerprint)
	}
	if plan.Normalized.Minecraft.VersionPolicy != "pinned" || plan.Normalized.Minecraft.Version != "1.21.8" {
		t.Fatalf("normalized Minecraft policy not preserved: %#v", plan.Normalized.Minecraft)
	}
	if plan.Normalized.Minecraft.MinecraftUID != 1000 || plan.Normalized.Minecraft.MinecraftGID != 1000 {
		t.Fatalf("normalized Minecraft runtime identity not preserved: %#v", plan.Normalized.Minecraft)
	}
	if plan.Normalized.Storage.ExpectedUUID != "data-uuid" {
		t.Fatalf("normalized storage identity missing: %#v", plan.Normalized.Storage)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0].Code != "network_backup_dependency" {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
	if !plan.Requirements.SMBPasswordRequired || !plan.Requirements.NetworkBackupValidationOnApply {
		t.Fatalf("requirements = %#v", plan.Requirements)
	}
}

func TestAdminSetupPlanPreservesValidationResponse(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body: io.NopCloser(strings.NewReader(`{
				"ok":false,
				"schema_version":"v1",
				"code":"invalid_storage_layout",
				"error":"Minecraft data and backups cannot overlap.",
				"warnings":[{"code":"same_physical_disk","message":"Same disk warning"}]
			}`)),
			Header: make(http.Header),
		}, nil
	})}}

	plan, err := client.AdminSetupPlan(context.Background(), "session-token", setupPlanTestRequest())
	if err == nil {
		t.Fatal("expected validation error")
	}
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("error = %#v, want HTTP 400 ResponseError", err)
	}
	if responseErr.Message != "Minecraft data and backups cannot overlap." {
		t.Fatalf("validation message = %q", responseErr.Message)
	}
	if plan.Code != "invalid_storage_layout" || len(plan.Warnings) != 1 {
		t.Fatalf("structured validation response was not preserved: %#v", plan)
	}
	if plan.PlanFingerprint != "" {
		t.Fatalf("invalid plan unexpectedly received fingerprint %q", plan.PlanFingerprint)
	}
}

func TestAdminSetupPlanServiceFailureIsSeparateFromValidation(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader(`{"error":"first-run setup planning is unavailable"}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	plan, err := client.AdminSetupPlan(context.Background(), "session-token", setupPlanTestRequest())
	if err == nil {
		t.Fatal("expected service failure")
	}
	if plan.Code != "" || plan.Error != "" {
		t.Fatalf("service failure must not masquerade as a structured validation result: %#v", plan)
	}
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %#v, want HTTP 503 ResponseError", err)
	}
}

func TestAdminSetupPlanRejectsUnexpectedResponseFields(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"schema_version":"v1","warnings":[],"unexpected":"value"}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	_, err := client.AdminSetupPlan(context.Background(), "session-token", setupPlanTestRequest())
	if err == nil || !strings.Contains(err.Error(), "invalid setup planning response") {
		t.Fatalf("error = %v, want strict response decode failure", err)
	}
}

func TestAdminSetupPlanRejectsTrailingResponseJSON(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"schema_version":"v1","warnings":[]} {"second":true}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	_, err := client.AdminSetupPlan(context.Background(), "session-token", setupPlanTestRequest())
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("error = %v, want trailing JSON rejection", err)
	}
}

func TestAdminSetupPlanUsesBoundedClientTimeout(t *testing.T) {
	oldTimeout := adminSetupPlanClientTimeout
	adminSetupPlanClientTimeout = 10 * time.Millisecond
	defer func() { adminSetupPlanClientTimeout = oldTimeout }()

	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(250 * time.Millisecond):
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"schema_version":"v1","warnings":[]}`)), Header: make(http.Header)}, nil
		}
	})}}

	started := time.Now()
	_, err := client.AdminSetupPlan(context.Background(), "session-token", setupPlanTestRequest())
	if err == nil || !strings.Contains(err.Error(), "management API unavailable") {
		t.Fatalf("error = %v, want bounded transport timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("setup planning timeout took too long: %s", elapsed)
	}
}

func TestAdminSetupPlanClientHasNoApplySurface(t *testing.T) {
	source, err := os.ReadFile("setup_plan.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{"/v1/admin/setup/apply", "AdminSetupApply"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("A4.4.5 must not expose setup Apply surface: found %q", forbidden)
		}
	}
}
