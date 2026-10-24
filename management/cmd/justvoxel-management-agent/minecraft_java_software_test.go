package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestJavaServerCapabilities(t *testing.T) {
	for _, tc := range []struct {
		software, upstream     string
		plugins, mods, managed bool
	}{
		{"paper", "PAPER", true, false, true},
		{"purpur", "PURPUR", true, false, true},
		{"vanilla", "VANILLA", false, false, false},
		{"spigot", "SPIGOT", true, false, false},
		{"forge", "FORGE", false, true, false},
		{"neoforge", "NEOFORGE", false, true, false},
		{"fabric", "FABRIC", false, true, false},
		{"quilt", "QUILT", false, true, false},
	} {
		c, ok := javaServerSoftware(tc.software)
		if !ok || !validSetupServerType(tc.software) || c.Type != tc.upstream || c.BukkitPlugins != tc.plugins || c.ModLoader != tc.mods || c.PluginsWorkspace != (tc.plugins) || c.ManagedCrossplay != (tc.software == "paper" || tc.software == "purpur" || tc.software == "spigot" || tc.software == "fabric" || tc.software == "neoforge") {
			t.Fatalf("capabilities for %s: %#v", tc.software, c)
		}
		if instanceDefaultName(tc.software) != "JV "+c.Name {
			t.Fatalf("default name for %s", tc.software)
		}
		for _, target := range []string{"paper", "purpur", "vanilla", "spigot", "forge", "neoforge", "fabric", "quilt"} {
			want := tc.software == target || tc.managed && (target == "paper" || target == "purpur")
			if javaServerAllowsSwitch(tc.software, target) != want {
				t.Fatalf("unsafe switch %s -> %s", tc.software, target)
			}
		}
	}
	if !validSetupServerType("bedrock") {
		t.Fatal("Bedrock must be a valid JustVoxel server type")
	}
	if _, ok := javaServerSoftware("bedrock"); ok {
		t.Fatal("Bedrock must not have Java server capabilities")
	}
	if !javaServerAllowsSwitch("bedrock", "bedrock") {
		t.Fatal("Bedrock must allow retaining its server type")
	}
	if javaServerAllowsSwitch("bedrock", "paper") || javaServerAllowsSwitch("paper", "bedrock") {
		t.Fatal("Bedrock and Java server types must not allow switching between each other")
	}
	for _, unknown := range []string{"", "SPIGOT", "bukkit", "future"} {
		if validSetupServerType(unknown) || javaServerAllowsSwitch(unknown, unknown) {
			t.Fatalf("accepted unknown software %q", unknown)
		}
	}
}

func TestJavaServerSelectionRequiresExplicitCompatiblePins(t *testing.T) {
	for _, software := range []string{"spigot", "forge", "neoforge", "fabric", "quilt"} {
		loader, image := "0.16.10", "java21"
		if software == "spigot" {
			loader, image = "", "java21-jdk"
		}
		for _, game := range []string{"1.20.5", "1.20.6", "1.21", "1.21.11", "26.1", "26.1.1", "26.2"} {
			selectedImage := image
			if strings.HasPrefix(game, "26.") {
				selectedImage = strings.Replace(image, "21", "25", 1)
			}
			if err := validateJavaServerSelection(software, "pinned", game, loader, selectedImage, false); err != nil {
				t.Fatalf("%s %s: %v", software, game, err)
			}
		}
		for _, bad := range []struct {
			policy, game, loader, image string
			bedrock                     bool
		}{
			{"latest", "1.21.1", loader, image, false},
			{"recommended", "1.21.1", loader, image, false},
			{"", "1.21.1", loader, image, false},
			{"pinned", "LATEST", loader, image, false},
			{"pinned", "1.21.1-pre1", loader, image, false},
			{"pinned", "1.20.1", loader, image, false},
			{"pinned", "26.1", loader, image, false},
			{"pinned", "1.20.4", loader, image, false},
			{"pinned", "1.20.7", loader, image, false},
			{"pinned", "1.21.12", loader, image, false},
			{"pinned", "1.21.1", loader, "stable", false},
			{"pinned", "1.21.1", loader, "java25", false},
		} {
			if validateJavaServerSelection(software, bad.policy, bad.game, bad.loader, bad.image, bad.bedrock) == nil {
				t.Fatalf("%s accepted %#v", software, bad)
			}
		}
		java25Image := "java25"
		wrongImage := "java25-jdk"
		if software == "spigot" {
			java25Image, wrongImage = "java25-jdk", "java25"
		}
		if validateJavaServerSelection(software, "pinned", "26.1", loader, wrongImage, false) == nil {
			t.Fatal("accepted incorrect JDK image selection")
		}
		for _, policy := range []string{"", "latest", "recommended"} {
			if validateJavaServerSelection(software, policy, "26.1", loader, java25Image, false) == nil {
				t.Fatal("accepted unpinned 26.x selection")
			}
		}
		for _, game := range []string{"26.1-snapshot-1", "26.1-pre-1", "26.1-rc-1", "26w01a", "26", "26.0", "26.01", "26.1.01", "26.1.1.1", "27.1", "1.21.999999999999999999999999999999"} {
			if validateJavaServerSelection(software, "pinned", game, loader, java25Image, false) == nil {
				t.Fatalf("accepted game version %q", game)
			}
		}
		if (software == "forge" || software == "quilt") && validateJavaServerSelection(software, "pinned", "26.1", loader, java25Image, true) == nil {
			t.Fatal("accepted managed Bedrock for 26.x")
		}
		if software != "spigot" {
			for _, game := range []string{"1.21.1", "26.1"} {
				selectedImage := image
				if game == "26.1" {
					selectedImage = java25Image
				}
				for _, bad := range []string{"", "latest", "recommended", "beta", "0.16.10\nTYPE=PAPER", "https://example.org/loader.jar"} {
					if validateJavaServerSelection(software, "pinned", game, bad, selectedImage, false) == nil {
						t.Fatalf("accepted loader %q", bad)
					}
				}
			}
		}
	}
	// Deployed types retain their original version and image policies.
	for _, software := range []string{"paper", "purpur", "vanilla"} {
		for _, policy := range []string{"", "latest", "recommended", "pinned"} {
			if err := validateJavaServerSelection(software, policy, "LATEST", "", "stable", false); err != nil {
				t.Fatal(err)
			}
		}
		if validateJavaServerSelection(software, "pinned", "1.21.1", "0.16.10", "stable", false) == nil {
			t.Fatal("loader accepted on non-loader software")
		}
	}
}

func TestNewJavaRuntimeIsolationAndIntegrations(t *testing.T) {
	for _, software := range []string{"spigot", "forge", "neoforge", "fabric", "quilt"} {
		for _, version := range []string{"1.21.1", "26.1"} {
			t.Run(software+"/"+version, func(t *testing.T) {
				store, ctx, configs := instanceRuntimeFixture(t)
				baseline, err := store.renderInstanceRuntimes(ctx, configs)
				if err != nil {
					t.Fatal(err)
				}
				c := &configs[0]
				c.Software, c.VersionPolicy, c.Version, c.ImageTag = software, "pinned", version, "java21"
				c.LoaderVersion = "0.16.10"
				if software == "spigot" {
					c.LoaderVersion, c.ImageTag = "", "java21-jdk"
				}
				if version == "26.1" {
					c.ImageTag = strings.Replace(c.ImageTag, "21", "25", 1)
				}
				if software == "neoforge" {
					c.LoaderVersion = "21.1.172"
					if version == "26.1" {
						c.LoaderVersion = "26.1.0.5-beta"
					}
				}
				files, err := store.renderInstanceRuntimes(ctx, configs)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(files[1], baseline[1]) {
					t.Fatal("another instance changed")
				}
				env, quadlet := string(files[0].Environment), string(files[0].Quadlet)
				if !strings.Contains(env, "TYPE="+strings.ToUpper(software)+"\n") || !strings.Contains(env, "VERSION="+version+"\n") || !strings.Contains(quadlet, "itzg/minecraft-server:"+c.ImageTag) {
					t.Fatal("missing exact software/game selection")
				}
				for _, forbidden := range []string{"MODRINTH_PROJECTS=", "PAPER_CHANNEL=", "GEYSER", "FLOODGATE", "PLUGINS="} {
					if strings.Contains(env, forbidden) && !(software == "spigot" && forbidden == "MODRINTH_PROJECTS=") {
						t.Fatalf("new type gained integration %s", forbidden)
					}
				}
				if (strings.Contains(quadlet, "instance-crossplay-start") != javaServerCrossplay(software)) || (!javaServerCrossplay(software) && !strings.Contains(quadlet, "validate-instance-start-memory "+c.ID)) || !strings.Contains(quadlet, "Volume="+files[0].DataPath+":/data:Z") {
					t.Fatal("incorrect startup isolation")
				}
				capability, _ := javaServerSoftware(software)
				if software == "spigot" {
					if !strings.Contains(env, "BUILD_FROM_SOURCE=TRUE\n") || strings.Contains(env, "DOWNLOAD_URL=") {
						t.Fatal("Spigot must build with BuildTools")
					}
				} else if !strings.Contains(env, capability.LoaderEnvironment+"="+c.LoaderVersion+"\n") {
					t.Fatal("missing loader pin")
				}
				// Deployed runtime records may still decode an independent timezone.
				if err := json.Unmarshal([]byte(`{"timezone":"UTC"}`), c); err != nil {
					t.Fatal(err)
				}
				if c.Timezone != "UTC" {
					t.Fatal("legacy runtime timezone did not decode")
				}
				settings := instancePublicSettings(*c)
				roundTrip := settings.runtime(c.ID, c.UID, c.GID, c.RCONPassword)
				if settings.LoaderVersion != c.LoaderVersion || roundTrip.LoaderVersion != c.LoaderVersion {
					t.Fatalf("loader pin changed: public %q, round trip %q, want %q", settings.LoaderVersion, roundTrip.LoaderVersion, c.LoaderVersion)
				}
				if settings.Timezone != "" || roundTrip.Timezone != "" {
					t.Fatal("legacy timezone carried forward as an independent per-server setting")
				}
				publicJSON, err := json.Marshal(settings)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(publicJSON), `"timezone"`) {
					t.Fatal("public settings expose an independent timezone")
				}
				for _, field := range []struct {
					name                   string
					public, restored, want any
				}{
					{"Software", settings.Software, roundTrip.Software, c.Software},
					{"Version", settings.Version, roundTrip.Version, c.Version},
					{"ImageTag", settings.ImageTag, roundTrip.ImageTag, c.ImageTag},
					{"JavaPort", settings.JavaPort, roundTrip.JavaPort, c.JavaPort},
					{"JavaMemory", settings.JavaMemory, roundTrip.JavaMemory, c.JavaMemory},
					{"ContainerMemory", settings.ContainerMemory, roundTrip.ContainerMemory, c.ContainerMemory},
					{"VersionPolicy", settings.VersionPolicy, roundTrip.VersionPolicy, c.VersionPolicy},
					{"PaperChannel", settings.PaperChannel, roundTrip.PaperChannel, c.PaperChannel},
					{"StartOnBoot", settings.StartOnBoot, roundTrip.StartOnBoot, c.StartOnBoot},
					{"EULAAccepted", settings.EULAAccepted, roundTrip.EULAAccepted, c.EULAAccepted},
					{"BedrockEnabled", settings.BedrockEnabled, roundTrip.BedrockEnabled, c.BedrockEnabled},
					{"BedrockPort", settings.BedrockPort, roundTrip.BedrockPort, c.BedrockPort},
					{"BedrockTCPPort", settings.BedrockTCPPort, roundTrip.BedrockTCPPort, c.BedrockTCPPort},
					{"BedrockUDPStart", settings.BedrockUDPStart, roundTrip.BedrockUDPStart, c.BedrockUDPStart},
					{"BedrockUDPEnd", settings.BedrockUDPEnd, roundTrip.BedrockUDPEnd, c.BedrockUDPEnd},
					{"GameMode", settings.GameMode, roundTrip.GameMode, c.GameMode},
					{"Difficulty", settings.Difficulty, roundTrip.Difficulty, c.Difficulty},
					{"MaxPlayers", settings.MaxPlayers, roundTrip.MaxPlayers, c.MaxPlayers},
					{"Whitelist", settings.Whitelist, roundTrip.Whitelist, c.Whitelist},
					{"EnforceWhitelist", settings.EnforceWhitelist, roundTrip.EnforceWhitelist, c.EnforceWhitelist},
					{"MOTD", settings.MOTD, roundTrip.MOTD, c.MOTD},
				} {
					if field.public != field.want || field.restored != field.want {
						t.Errorf("%s changed: public %v, round trip %v, want %v", field.name, field.public, field.restored, field.want)
					}
				}
				c.BedrockEnabled, c.BedrockPort = true, 19132
				if _, err := store.renderInstanceRuntimes(ctx, configs); (err == nil) != javaServerCrossplay(software) {
					t.Fatal("incorrect managed Bedrock capability")
				}
			})
		}
	}
}

func TestJavaLoaderPlanFingerprintAndRuntimePropagation(t *testing.T) {
	var plan adminSetupPlanResponse
	if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &plan); err != nil {
		t.Fatal(err)
	}
	plan.Normalized.Minecraft.ServerType = "fabric"
	plan.Normalized.Minecraft.VersionPolicy = "pinned"
	plan.Normalized.Minecraft.Version = "1.21.1"
	plan.Normalized.Minecraft.ImageTag = "java21"
	plan.Normalized.Minecraft.LoaderVersion = "0.16.10"
	plan.Normalized.Server.BedrockEnabled = false
	first, err := adminSetupPlanFingerprint(plan.SchemaVersion, plan.Normalized, plan.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	plan.Normalized.Minecraft.LoaderVersion = "0.16.11"
	second, err := adminSetupPlanFingerprint(plan.SchemaVersion, plan.Normalized, plan.Requirements)
	if err != nil || first == second {
		t.Fatal("loader selection is not bound to the reviewed plan")
	}
	op := operationJournal{OperationType: operationTypeSetup, OperationID: "12345678-1234-4234-8234-123456789abc", PlanFingerprint: second}
	request, err := setupRuntimeRequestForOperation(op, plan.Normalized)
	if err != nil || request.Minecraft.LoaderVersion != "0.16.11" || !request.InstanceOnly {
		t.Fatalf("loader lost at runtime boundary: %v", err)
	}
}

func TestNewJavaSelectionRejectedBeforePlanningHelper(t *testing.T) {
	previous := runAdminSetupPlanHelper
	t.Cleanup(func() { runAdminSetupPlanHelper = previous })
	runAdminSetupPlanHelper = func(context.Context, []byte) ([]byte, error) {
		t.Fatal("unsafe request reached planner")
		return nil, nil
	}
	for _, software := range []string{"spigot", "forge", "neoforge", "fabric", "quilt"} {
		var request adminSetupPlanRequest
		if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
			t.Fatal(err)
		}
		request.Minecraft.ServerType = software
		if _, failure := authoritativeAdminSetupPlan(context.Background(), request); failure == nil {
			t.Fatalf("unsafe %s request accepted", software)
		}
	}
}
