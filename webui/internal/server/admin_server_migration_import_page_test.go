package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeServerMigrationImportAPI struct {
	fakeServerMigrationAPI
	storage      api.AdminStorageDiscovery
	plan         api.AdminMigrationImportPlanResponse
	planErr      error
	planCalls    int
	planReq      api.AdminMigrationImportRequest
	apply        api.AdminMigrationApplyResponse
	applyErr     error
	applyCalls   int
	applyReq     api.AdminMigrationImportApplyRequest
	storageCalls int
}

func (f *fakeServerMigrationImportAPI) AdminStorage(_ context.Context, session string) (api.AdminStorageDiscovery, error) {
	if session != "session-token" {
		return api.AdminStorageDiscovery{}, api.ErrUnauthorized
	}
	f.storageCalls++
	return f.storage, nil
}

func (f *fakeServerMigrationImportAPI) AdminMigrationImportPlan(_ context.Context, session string, request api.AdminMigrationImportRequest) (api.AdminMigrationImportPlanResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationImportPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planReq = request
	return f.plan, f.planErr
}

func (f *fakeServerMigrationImportAPI) AdminMigrationImportApply(_ context.Context, session string, request api.AdminMigrationImportApplyRequest) (api.AdminMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyReq = request
	return f.apply, f.applyErr
}

func importDiscoveryFixture(configured bool) api.AdminMigrationImportDiscoveryResponse {
	return api.AdminMigrationImportDiscoveryResponse{
		OK: true, SchemaVersion: "v1", Configured: configured,
		SourceKinds: []string{"local", "backup", "device", "nfs", "smb"},
		Defaults: api.AdminMigrationImportDefaults{
			DataPath: "/var/lib/justvoxel/minecraft", BackupPath: "/var/lib/justvoxel/backups",
			JavaMemory: "4G", ContainerMemory: "6G", Timezone: "UTC",
			JavaPort: 25565, BedrockPort: 19132, BackupKeep: 7, BackupDailyTime: "04:30", BackupAutomatic: true,
		},
	}
}

func importMediaFixture() api.AdminMigrationExportDiscoveryResponse {
	return api.AdminMigrationExportDiscoveryResponse{
		OK: true, SchemaVersion: "v1", SuggestedFilename: "justvoxel-migration-test.tar.gz",
		Devices: []api.AdminMigrationExportDevice{
			{Path: "/dev/sdb1", Filesystem: "xfs", Model: "Archive Disk", SizeBytes: 107374182400},
			{Path: "/dev/sdc1", Filesystem: "exfat", Model: "USB Import", Transport: "usb", SizeBytes: 34359738368, Removable: true},
		},
		TargetKinds: []string{"local", "backup", "device", "nfs", "smb"},
	}
}

func importStorageFixture() api.AdminStorageDiscovery {
	return api.AdminStorageDiscovery{Devices: []api.AdminStorageDevice{
		{Name: "sdb1", Path: "/dev/sdb1", Parent: "sdb", Type: "part", SizeBytes: 107374182400, Filesystem: "xfs", UUID: "data-uuid", Model: "Data Disk"},
		{Name: "sdd1", Path: "/dev/sdd1", Parent: "sdd", Type: "part", SizeBytes: 214748364800, Filesystem: "ext4", UUID: "backup-uuid", Model: "Backup Disk"},
	}}
}

func configuredImportRequestValues(kind string) url.Values {
	values := url.Values{"csrf": {"csrf-token"}, "source_kind": {kind}}
	switch kind {
	case "local":
		values.Set("local_path", "/srv/import/server")
	case "backup":
		values.Set("source_path", "minecraft-2026-09-21.tar.gz")
	case "device":
		values.Set("source_device", "/dev/sdc1")
		values.Set("source_path", "server-export.tar.gz")
	case "nfs":
		values.Set("source_remote", "nas:/exports/migrations")
		values.Set("source_path", "server-export.tar.gz")
	case "smb":
		values.Set("source_remote", "//nas/migrations")
		values.Set("source_username", "voxel")
		values.Set("source_domain", "HOME")
		values.Set("source_smb_password", "source-secret")
		values.Set("source_path", "server-export.tar.gz")
	}
	return values
}

func freshImportRequestValues(kind string) url.Values {
	values := configuredImportRequestValues(kind)
	values.Set("java_port", "25565")
	values.Set("bedrock_port", "19132")
	values.Set("java_memory", "4G")
	values.Set("container_memory", "6G")
	values.Set("timezone", "UTC")
	values.Set("backup_keep", "7")
	values.Set("backup_daily_time", "04:30")
	values.Set("backup_automatic", "on")
	values.Set("storage_type", "system")
	values.Set("backup_type", "system")
	return values
}

func importWebRequest(method, target string, values url.Values) *http.Request {
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

func successfulImportPlan(mode string) api.AdminMigrationImportPlanResponse {
	plan := api.AdminMigrationImportPlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: serverMigrationFingerprint,
		Normalized: &api.AdminMigrationImportNormalized{
			Source: api.AdminMigrationImportSourceNormalized{
				Path: "/srv/import/server", SelectedRoot: "", SourceClass: "paper", CandidateType: "paper",
				MinecraftVersion: "1.21.8", OnlineMode: "online", GameMode: "survival", Difficulty: "normal",
				WhitelistEnabled: true, EnforceWhitelist: true, MaxPlayers: 10, MOTD: "Imported server",
				PluginCount: 2, BedrockEnabled: true, BedrockManagedPlugins: true,
				JavaPortHint: 25565, BedrockPortHint: 19132, ExpandedBytes: 2147483648,
			},
			Destination: api.AdminMigrationImportDestinationNormalized{
				Mode: mode, DataPath: "/var/lib/justvoxel/minecraft", BackupPath: "/var/lib/justvoxel/backups",
				JavaPort: 25565, BedrockPort: 19132, JavaMemory: "4G", ContainerMemory: "6G", Timezone: "UTC",
				ImageTag: "stable", MinecraftUID: 1001, MinecraftGID: 1001, BackupKeep: 7,
				BackupSchedule: "*-*-* 04:30:00", BackupAutomatic: true,
			},
		},
		Warnings: []api.AdminMigrationWarning{
			{Code: "external_plugins", Message: "This source contains 2 plugin JAR(s), which are executable server code and will be preserved."},
			{Code: "online_mode_unknown", Message: "Source online-mode could not be proven automatically."},
		},
		Requirements: &api.AdminMigrationImportRequirements{
			ImportConfirmationRequired: true, PlayersConfirmationRequired: true, MinecraftState: "running",
			Online: 1, Players: []string{"PlayerOne"}, SourceRevalidationOnApply: true,
			RuntimeValidationRequired: true, RollbackRequired: true, PluginsConfirmationRequired: true,
			OnlineModeConfirmationRequired: true,
		},
		Candidates:    []api.AdminMigrationImportCandidate{},
		SourceEntries: []api.AdminMigrationImportSourceEntry{},
	}
	if mode == "fresh" {
		plan.Requirements.EULAAcceptanceRequired = true
		plan.Requirements.VanillaConfirmationRequired = true
		plan.Normalized.Source.CandidateType = "vanilla"
		plan.Normalized.Destination.Storage = &api.AdminSetupPlanStorage{Type: "system", Path: "/var/lib/justvoxel/minecraft"}
		plan.Normalized.Destination.Backups = &api.AdminSetupPlanBackups{Type: "smb", Path: "/var/mnt/backups/minecraft", Source: "//nas/backups", Username: "voxel", CredentialsRequired: true, Automatic: true, Keep: 7}
		plan.Requirements.BackupSMBPasswordRequired = true
	}
	return plan
}

func TestServerImportPageShowsSourcesAndFreshDestinationWithoutUpload(t *testing.T) {
	client := &fakeServerMigrationImportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			exportDiscovery: importMediaFixture(),
			importDiscovery: importDiscoveryFixture(false),
		},
		storage: importStorageFixture(),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, importWebRequest(http.MethodGet, "http://example/settings/server-migration/import", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("Server Import page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Local / server path", "Configured JustVoxel backup storage", "Attached disk / partition / USB media",
		"Temporary NFS", "Temporary SMB / CIFS", "Fresh destination", "Minecraft data storage",
		"Backup storage", "/dev/sdb1", "USB Import",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Server Import page missing %q: %s", want, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), `type="file"`) {
		t.Fatal("browser file upload unexpectedly appeared in Server Import")
	}
	if strings.Contains(page.Body.String(), `name="backup_smb_password"`) {
		t.Fatal("fresh backup SMB password appeared before Apply")
	}
}

func TestServerImportUsesAgentSourceEntrySelectionAndManualFallback(t *testing.T) {
	client := &fakeServerMigrationImportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			exportDiscovery: importMediaFixture(),
			importDiscovery: importDiscoveryFixture(true),
		},
		plan: api.AdminMigrationImportPlanResponse{
			OK: false, SchemaVersion: "v1", Code: "source_selection_required",
			Error:    "Choose an Import archive or server directory from this source.",
			Warnings: []api.AdminMigrationWarning{}, Candidates: []api.AdminMigrationImportCandidate{},
			SourceEntries: []api.AdminMigrationImportSourceEntry{{Path: "exports/server.tar.gz", Kind: "archive"}, {Path: "paper-server", Kind: "directory"}},
		},
		planErr: &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "Choose an Import archive or server directory from this source."},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := configuredImportRequestValues("backup")
	values.Del("source_path")
	page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/review", values))
	if page.Code != http.StatusOK {
		t.Fatalf("source selection page returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"exports/server.tar.gz", "paper-server", "Manual relative path fallback", "Continue review"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("source selection page missing %q: %s", want, page.Body.String())
		}
	}
	if client.planReq.Source.Path != "" || client.planReq.Source.Kind != "backup" {
		t.Fatalf("unexpected source discovery plan request: %#v", client.planReq.Source)
	}
}

func TestServerImportReviewShowsAuthoritativeConfirmations(t *testing.T) {
	client := &fakeServerMigrationImportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			exportDiscovery: importMediaFixture(),
			importDiscovery: importDiscoveryFixture(false),
		},
		storage: importStorageFixture(),
		plan:    successfulImportPlan("fresh"),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := freshImportRequestValues("local")
	page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/review", values))
	if page.Code != http.StatusOK {
		t.Fatalf("Server Import review returned %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{
		"Authoritative source plan", "Minecraft 1.21.8", "Fresh JustVoxel destination",
		"PlayerOne", "Minecraft End User License Agreement", "Vanilla server will be opened by Paper",
		"external plugin JARs", "online-mode=true", "Slide to confirm Import",
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Server Import review missing %q: %s", want, page.Body.String())
		}
	}
	if !strings.Contains(page.Body.String(), `name="backup_smb_password"`) {
		t.Fatal("fresh SMB backup Apply-only credential input missing")
	}
}

func TestServerImportSMBSourcePasswordIsNeverEchoedIntoReview(t *testing.T) {
	client := &fakeServerMigrationImportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			exportDiscovery: importMediaFixture(),
			importDiscovery: importDiscoveryFixture(true),
		},
		plan: successfulImportPlan("replace"),
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := configuredImportRequestValues("smb")
	page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/review", values))
	if page.Code != http.StatusOK {
		t.Fatalf("SMB Server Import review returned %d: %s", page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), "source-secret") {
		t.Fatal("SMB source password was echoed into reviewed HTML")
	}
	if strings.Contains(page.Body.String(), `name="source_smb_password"`) {
		t.Fatal("SMB source password must not be embedded in final Apply review")
	}
	if !strings.Contains(page.Body.String(), "JustVoxel will request the source password only when you start this reviewed Import") {
		t.Fatal("SMB transient source credential guidance is missing from final Apply review")
	}
	if client.planReq.Source.SMBPassword != "source-secret" {
		t.Fatal("SMB source password was not supplied ephemerally to Agent planning")
	}
}

func TestServerImportApplyReplansAndStartsPersistentOperation(t *testing.T) {
	client := &fakeServerMigrationImportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{
			exportDiscovery: importMediaFixture(),
			importDiscovery: importDiscoveryFixture(false),
		},
		storage: importStorageFixture(),
		plan:    successfulImportPlan("fresh"),
	}
	client.apply = api.AdminMigrationApplyResponse{OK: true, Created: true, Operation: &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: serverMigrationOperationID, OperationType: "migration_import",
		PlanFingerprint: serverMigrationFingerprint, State: "queued", Stage: "queued", Status: "Server migration import operation queued.",
		StartedAt: "2026-09-21T15:00:00Z", UpdatedAt: "2026-09-21T15:00:00Z",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := freshImportRequestValues("local")
	values.Set("plan_fingerprint", serverMigrationFingerprint)
	values.Set("import_confirmation", "IMPORT")
	values.Set("players_confirmed", "yes")
	values.Set("eula_accepted", "yes")
	values.Set("vanilla_confirmed", "yes")
	values.Set("plugins_confirmed", "yes")
	values.Set("online_mode_confirmed", "yes")
	values.Set("backup_smb_password", "backup-apply-secret")
	page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/apply", values))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/settings/server-migration/progress/"+serverMigrationOperationID {
		t.Fatalf("Server Import apply returned %d %q: %s", page.Code, page.Header().Get("Location"), page.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 1 {
		t.Fatalf("Import calls plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
	if !client.applyReq.ImportConfirmed || !client.applyReq.PlayersConfirmed || !client.applyReq.EULAAccepted ||
		!client.applyReq.VanillaConfirmed || !client.applyReq.PluginsConfirmed || !client.applyReq.OnlineModeConfirmed ||
		client.applyReq.BackupSMBPassword != "backup-apply-secret" {
		t.Fatalf("unexpected Import Apply request: %#v", client.applyReq)
	}
}

func TestServerImportSourceVersionAndMultipleRootPrompts(t *testing.T) {
	for _, tc := range []struct {
		code string
		want string
	}{
		{code: "multiple_roots", want: "Choose exactly one Minecraft server root"},
		{code: "source_version_required", want: "exact Minecraft version"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			client := &fakeServerMigrationImportAPI{
				fakeServerMigrationAPI: fakeServerMigrationAPI{
					exportDiscovery: importMediaFixture(),
					importDiscovery: importDiscoveryFixture(true),
				},
				plan: api.AdminMigrationImportPlanResponse{
					OK: false, SchemaVersion: "v1", Code: tc.code, Error: "more input required",
					Warnings:      []api.AdminMigrationWarning{},
					Candidates:    []api.AdminMigrationImportCandidate{{RootRelative: "server-a", SourceType: "paper", Supported: true}, {RootRelative: "server-b", SourceType: "vanilla", Supported: true}},
					SourceEntries: []api.AdminMigrationImportSourceEntry{},
				},
				planErr: &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "more input required"},
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/review", configuredImportRequestValues("local")))
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), tc.want) {
				t.Fatalf("%s prompt returned %d: %s", tc.code, page.Code, page.Body.String())
			}
		})
	}
}
