package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

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

func TestServerImportIncompatibleWorkspaceReview(t *testing.T) {
	for _, surface := range []string{"/workspace/migration/import/review"} {
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
					"Start Server Import", "/workspace/migration/import/apply",
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
