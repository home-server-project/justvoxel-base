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
				Path: "/srv/import/server", SelectedRoot: "", SourceClass: "paper", CandidateType: "paper", ServerType: "paper",
				MinecraftVersion: "1.21.8", OnlineMode: "online", GameMode: "survival", Difficulty: "normal",
				WhitelistEnabled: true, EnforceWhitelist: true, MaxPlayers: 10, MOTD: "Imported server",
				PluginCount: 2, BedrockEnabled: true, BedrockManagedPlugins: true,
				JavaPortHint: 25565, BedrockPortHint: 19132, ExpandedBytes: 2147483648,
			},
			Destination: api.AdminMigrationImportDestinationNormalized{
				Mode: mode, ServerType: "paper", DataPath: "/var/lib/justvoxel/minecraft", BackupPath: "/var/lib/justvoxel/backups",
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
		plan.Normalized.Source.ServerType = "vanilla"
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
	page := legacyPageTestResponse(app, importWebRequest(http.MethodGet, "http://example/settings/server-migration/import", nil))
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
	for _, want := range []string{"exports/server.tar.gz", "paper-server", "Advanced: enter a path manually", "Continue review"} {
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
		"Reviewed source plan", "Minecraft 1.21.8", "Fresh JustVoxel destination",
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

func TestServerImportSMBNormalNetworkFolderIsSplitInternally(t *testing.T) {
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
	values.Set("source_remote", "//192.168.0.50/storage/TEMP")
	values.Del("source_path")
	page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/settings/server-migration/import/review", values))
	if page.Code != http.StatusOK {
		t.Fatalf("SMB folder Import review returned %d: %s", page.Code, page.Body.String())
	}
	if client.planReq.Source.Source != "//192.168.0.50/storage" || client.planReq.Source.Path != "TEMP" {
		t.Fatalf("normal SMB folder was not split internally: %#v", client.planReq.Source)
	}
}

func TestServerImportIncompatibleReviewOnBothSurfaces(t *testing.T) {
	for _, surface := range []string{"/settings/server-migration/import/review", "/workspace/migration/import/review"} {
		for _, tc := range []struct {
			source      string
			destination string
		}{
			{source: "paper", destination: "vanilla"},
			{source: "purpur", destination: "vanilla"},
			{source: "vanilla", destination: "vanilla"},
			{source: "vanilla", destination: "purpur"}, // Defensive fallback for a rejected plan.
		} {
			t.Run(surface+"/"+tc.source+"-to-"+tc.destination, func(t *testing.T) {
				plan := successfulImportPlan("replace")
				plan.OK = false
				plan.Code = "unsupported_server_type"
				plan.Error = "Migration v1 import requires a Paper or Purpur destination."
				plan.PlanFingerprint = ""
				plan.Normalized.Source.ServerType = tc.source
				plan.Normalized.Source.CandidateType = "itzg-paper"
				plan.Normalized.Source.SourceClass = "itzg-paper"
				if tc.source == "vanilla" {
					plan.Normalized.Source.CandidateType = "vanilla"
					plan.Normalized.Source.SourceClass = "vanilla"
				} else if tc.source == "purpur" {
					// Native bundles retain their software in the manifest while detection identifies Paper data.
					plan.Normalized.Source.SourceClass = "justvoxel"
				}
				plan.Normalized.Destination.ServerType = tc.destination
				// Even unexpected confirmation requirements must never expose Apply here.
				client := &fakeServerMigrationImportAPI{
					fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: importMediaFixture(), importDiscovery: importDiscoveryFixture(true)},
					plan:                   plan,
					planErr:                &api.ResponseError{StatusCode: http.StatusBadRequest, Message: plan.Error},
				}
				app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
				if err != nil {
					t.Fatal(err)
				}
				page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example"+surface, configuredImportRequestValues("local")))
				body := page.Body.String()
				if page.Code != http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", page.Code, body)
				}
				for _, want := range []string{
					"Reviewed source plan", "Reviewed destination plan", "Online identity", "Imported data", "Game settings", "Plugins",
					"Minecraft data", "Backups", "Ports", "Memory / timezone",
					`<section class="panel notice error">`, "Safety checks and confirmations", "This import can’t continue",
					`class="button-link" href="/?workspace=version&amp;tab=software">Server Version`,
					`class="button-link secondary" href="/?workspace=migration&amp;tab=import">Back to Server Import`,
				} {
					if !strings.Contains(body, want) {
						t.Fatalf("missing %q: %s", want, body)
					}
				}
				var wording []string
				switch {
				case tc.source != "vanilla" && tc.destination == "vanilla":
					wording = []string{
						"This backup uses " + importServerSoftwareLabel(tc.source) + " server software, but your JustVoxel server is currently using Vanilla.",
						importServerSoftwareLabel(tc.source) + " server backups cannot be imported into a Vanilla server.",
						"Change Server Version to Paper or Purpur, then try the import again.",
					}
				case tc.source == "vanilla" && tc.destination == "vanilla":
					wording = []string{
						"This backup uses Vanilla server software.",
						"Server Import currently supports this backup only when your JustVoxel server uses Paper or Purpur.",
						"Change Server Version to Paper or Purpur, then try the import again.",
					}
					if strings.Contains(body, "but your JustVoxel server is currently using Vanilla") {
						t.Fatalf("Vanilla source incorrectly described as a software mismatch: %s", body)
					}
				default:
					wording = []string{
						"This backup and your current server software cannot be imported together safely.",
						"Backup server software: " + importServerSoftwareLabel(tc.source),
						"Current server software: " + importServerSoftwareLabel(tc.destination),
					}
				}
				for _, want := range wording {
					if !strings.Contains(body, want) {
						t.Fatalf("missing %q: %s", want, body)
					}
				}
				if strings.Count(body, "<strong>Server software</strong>") != 2 {
					t.Fatalf("both software facts required: %s", body)
				}
				for _, unwanted := range []string{
					"Migration v1", "unsupported_server_type", "unsupported server type", "itzg-paper", "Server Import needs attention",
					"data-destructive-confirmation", "data-destructive-slider", "data-destructive-toggle", `name="import_confirmation"`,
					"Start Server Import", "/workspace/migration/import/apply", "/settings/server-migration/import/apply",
				} {
					if strings.Contains(body, unwanted) {
						t.Fatalf("unexpected %q: %s", unwanted, body)
					}
				}
				if client.applyCalls != 0 {
					t.Fatal("incompatible review reached Apply")
				}
			})
		}
	}
}
