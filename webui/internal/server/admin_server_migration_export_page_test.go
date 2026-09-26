package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeServerMigrationExportAPI struct {
	fakeServerMigrationAPI
	plan       api.AdminMigrationExportPlanResponse
	planErr    error
	planCalls  int
	planReq    api.AdminMigrationExportTargetRequest
	apply      api.AdminMigrationApplyResponse
	applyErr   error
	applyCalls int
	applyReq   api.AdminMigrationExportApplyRequest
}

func (f *fakeServerMigrationExportAPI) AdminMigrationExportPlan(_ context.Context, session string, request api.AdminMigrationExportTargetRequest) (api.AdminMigrationExportPlanResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationExportPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planReq = request
	return f.plan, f.planErr
}

func (f *fakeServerMigrationExportAPI) AdminMigrationExportApply(_ context.Context, session string, request api.AdminMigrationExportApplyRequest) (api.AdminMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyReq = request
	return f.apply, f.applyErr
}

func exportDiscoveryFixture() api.AdminMigrationExportDiscoveryResponse {
	return api.AdminMigrationExportDiscoveryResponse{
		OK: true, SchemaVersion: "v1", SuggestedFilename: "justvoxel-migration-test.tar.gz",
		ConfiguredBackupAvailable: true,
		TargetKinds:               []string{"local", "backup", "device", "nfs", "smb"},
		Devices: []api.AdminMigrationExportDevice{
			{Path: "/dev/sdb1", Filesystem: "xfs", Model: "Backup Disk", Transport: "sata", SizeBytes: 107374182400},
			{Path: "/dev/sdc1", Filesystem: "exfat", Model: "USB Stick", Transport: "usb", SizeBytes: 34359738368, Removable: true},
		},
	}
}

func exportPlanFixture(kind string, players bool) api.AdminMigrationExportPlanResponse {
	requirements := &api.AdminMigrationExportRequirements{
		ExportConfirmationRequired: true, PlayersConfirmationRequired: players,
		MinecraftState: "running", Online: 0, Players: []string{},
		SMBPasswordRequired: kind == "smb", TargetValidationOnApply: true,
		IntegrityValidationRequired: true, RuntimeValidationRequired: true,
	}
	if players {
		requirements.Online = 1
		requirements.Players = []string{"PlayerOne"}
	}
	normalized := &api.AdminMigrationExportNormalized{
		Kind: kind, Filename: "justvoxel-migration-test.tar.gz", DataPath: "/var/lib/justvoxel/minecraft",
		DataBytes: 2147483648, TargetAvailableBytes: 10737418240, TargetFilesystem: "xfs",
		TargetDisplay: "/srv/migrations/justvoxel-migration-test.tar.gz",
	}
	switch kind {
	case "local":
		normalized.Path = "/srv/migrations"
	case "backup":
		normalized.Path = "/var/mnt/backups"
	case "device":
		normalized.Device = "/dev/sdb1"
	case "nfs":
		normalized.Source = "nas:/exports/migrations"
	case "smb":
		normalized.Source = "//nas/migrations"
		normalized.Username = "voxel"
		normalized.Domain = "HOME"
		normalized.TargetDisplay = "//nas/migrations/justvoxel-migration-test.tar.gz"
	}
	return api.AdminMigrationExportPlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: serverMigrationFingerprint,
		Normalized:   normalized,
		Warnings:     []api.AdminMigrationWarning{{Code: "players_online", Message: "Online players will be interrupted while the export snapshot is prepared."}},
		Requirements: requirements,
	}
}

func exportWebRequest(method, target string, values url.Values) *http.Request {
	body := strings.NewReader("")
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
	return req
}

func TestServerExportPageShowsAgentDestinationsAndSeparatesRemovableMedia(t *testing.T) {
	client := &fakeServerMigrationExportAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, exportWebRequest(http.MethodGet, "http://example/settings/server-migration/export", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("Server Export page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Server Export", "Local / server path", "Configured backup storage", "Attached disk / partition / USB media",
		"Temporary NFS", "Temporary SMB / CIFS", "/dev/sdb1", "Backup Disk", "/dev/sdc1", "USB Stick",
		"justvoxel-migration-test.tar.gz",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Server Export page missing %q: %s", want, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), `name="smb_password"`) {
		t.Fatal("SMB password field appeared before the reviewed Apply step")
	}
}

func TestServerExportReviewUsesAuthoritativePlanAndPlayerWarning(t *testing.T) {
	client := &fakeServerMigrationExportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()},
		plan:                   exportPlanFixture("local", true),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "kind": {"local"}, "path": {"/srv/migrations"},
		"filename": {"justvoxel-migration-test.tar.gz"},
	}
	page := httptestResponse(app, exportWebRequest(http.MethodPost, "http://example/settings/server-migration/export/review", values))
	if page.Code != http.StatusOK {
		t.Fatalf("Server Export review returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Review Server Export", "/srv/migrations/justvoxel-migration-test.tar.gz", "2.0 GiB", "10.0 GiB",
		"Online players will be interrupted", "PlayerOne", "Type EXPORT", "JustVoxel will revalidate",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Server Export review missing %q: %s", want, page.Body.String())
		}
	}
	if client.planCalls != 1 || client.planReq.Path != "/srv/migrations" || client.planReq.Kind != "local" {
		t.Fatalf("unexpected Export plan request: %#v calls=%d", client.planReq, client.planCalls)
	}
	if strings.Contains(page.Body.String(), `name="smb_password"`) {
		t.Fatal("non-SMB Export review exposed an SMB password field")
	}
}

func TestServerExportSMBPasswordAppearsOnlyOnApplyReviewAndIsNotEchoed(t *testing.T) {
	client := &fakeServerMigrationExportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()},
		plan:                   exportPlanFixture("smb", false),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "kind": {"smb"}, "source": {"//nas/migrations"},
		"username": {"voxel"}, "domain": {"HOME"}, "filename": {"justvoxel-migration-test.tar.gz"},
		"smb_password": {"must-not-enter-planning"},
	}
	page := httptestResponse(app, exportWebRequest(http.MethodPost, "http://example/settings/server-migration/export/review", values))
	if page.Code != http.StatusOK {
		t.Fatalf("SMB Server Export review returned %d: %s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), `type="password" name="smb_password"`) || !strings.Contains(page.Body.String(), `autocomplete="off"`) {
		t.Fatalf("SMB Apply-only password input missing: %s", page.Body.String())
	}
	if strings.Contains(page.Body.String(), "must-not-enter-planning") {
		t.Fatal("SMB password value was echoed into reviewed HTML")
	}
	if client.planReq.Source != "//nas/migrations" || client.planReq.Username != "voxel" || client.planReq.Domain != "HOME" {
		t.Fatalf("unexpected SMB plan request: %#v", client.planReq)
	}
}

func TestServerExportApplyReplansAndPassesSMBPasswordOnlyToApply(t *testing.T) {
	client := &fakeServerMigrationExportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()},
		plan:                   exportPlanFixture("smb", true),
	}
	client.apply = api.AdminMigrationApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_export",
		PlanFingerprint: serverMigrationFingerprint, State: "queued", Stage: "queued", Status: "Server migration export operation queued.",
		StartedAt: "2026-09-21T14:00:00Z", UpdatedAt: "2026-09-21T14:00:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "kind": {"smb"}, "source": {"//nas/migrations"},
		"username": {"voxel"}, "domain": {"HOME"}, "filename": {"justvoxel-migration-test.tar.gz"},
		"plan_fingerprint": {serverMigrationFingerprint}, "export_confirmation": {"EXPORT"},
		"players_confirmed": {"yes"}, "smb_password": {"apply-only-secret"},
	}
	page := httptestResponse(app, exportWebRequest(http.MethodPost, "http://example/settings/server-migration/export/apply", values))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/server-migration/progress/"+serverMigrationOperationID {
		t.Fatalf("Server Export apply returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 1 {
		t.Fatalf("Export calls plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
	if client.applyReq.SMBPassword != "apply-only-secret" || !client.applyReq.ExportConfirmed || !client.applyReq.PlayersConfirmed {
		t.Fatalf("unexpected Export apply request: %#v", client.applyReq)
	}
}

func TestServerExportApplyRejectsStaleReviewBeforeAgentApply(t *testing.T) {
	client := &fakeServerMigrationExportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture()},
		plan:                   exportPlanFixture("local", false),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "kind": {"local"}, "path": {"/srv/migrations"},
		"filename": {"justvoxel-migration-test.tar.gz"}, "plan_fingerprint": {"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"export_confirmation": {"EXPORT"},
	}
	page := httptestResponse(app, exportWebRequest(http.MethodPost, "http://example/settings/server-migration/export/apply", values))
	if page.Code != http.StatusConflict || !strings.Contains(page.Body.String(), "changed since it was reviewed") {
		t.Fatalf("stale Server Export returned %d: %s", page.Code, page.Body.String())
	}
	if client.applyCalls != 0 {
		t.Fatalf("Export Apply called for stale plan: %d", client.applyCalls)
	}
}

func TestServerMigrationProgressEndpointReconnectsToExportJournal(t *testing.T) {
	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_export",
		PlanFingerprint: serverMigrationFingerprint, State: "running", Stage: "archive_integrity",
		Status:    "Verifying the migration bundle.",
		StartedAt: "x", UpdatedAt: "x",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client := &fakeServerMigrationExportAPI{fakeServerMigrationAPI: fakeServerMigrationAPI{operation: operation}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, exportWebRequest(http.MethodGet, "http://example/settings/server-migration/progress/"+serverMigrationOperationID, nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "/api/server-migration/progress/"+serverMigrationOperationID) || !strings.Contains(page.Body.String(), "/static/server-migration-operation.js") {
		t.Fatalf("Server Migration progress page unexpected: %d %s", page.Code, page.Body.String())
	}
	status := httptestResponse(app, exportWebRequest(http.MethodGet, "http://example/api/server-migration/progress/"+serverMigrationOperationID, nil))
	if status.Code != http.StatusOK {
		t.Fatalf("Server Migration progress API returned %d: %s", status.Code, status.Body.String())
	}
	var response api.PersistentOperationResponse
	if err := json.Unmarshal(status.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.OperationID != serverMigrationOperationID || response.Operation.Stage != "archive_integrity" {
		t.Fatalf("unexpected Server Migration progress response: %#v", response)
	}
}
