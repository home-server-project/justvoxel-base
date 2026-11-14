package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExactCatalogPlatformCapabilitiesAcrossReleases(t *testing.T) {
	// The same platform contract must apply to old, current and future releases.
	accepted := map[string][]string{
		"spigot": {"spigot", "bukkit"}, "paper": {"paper", "spigot", "bukkit"},
		"purpur": {"purpur", "paper", "spigot", "bukkit"}, "pufferfish": {"paper", "spigot", "bukkit"}, "leaf": {"paper", "spigot", "bukkit"},
		"fabric": {"fabric"}, "forge": {"forge"}, "neoforge": {"neoforge"}, "quilt": {"quilt"},
	}
	for _, game := range []string{"1.21.8", "26.3", "99.1-test"} {
		for _, software := range []string{"spigot", "paper", "purpur", "pufferfish", "leaf", "fabric", "forge", "neoforge", "quilt"} {
			server := pluginContentContext(software)
			server.MinecraftVersion = game
			for _, platform := range []string{"bukkit", "spigot", "paper", "purpur", "fabric", "forge", "neoforge", "quilt"} {
				want := slices.Contains(accepted[software], platform)
				for _, exactGame := range []string{game, "wrong-game"} {
					expected := want && exactGame == game
					// Project identities and titles are deliberately irrelevant to this contract.
					for index, project := range []string{"exampleA", "unrelatedB"} {
						v := pluginCatalogVersion{ProjectID: project, GameVersions: []string{exactGame}, Loaders: []string{platform}}
						if pluginVersionCompatible(v, server) != expected {
							t.Fatal("Modrinth", game, software, platform, exactGame)
						}
						f := curseForgeFile{ID: int64(100 + index), ModID: int64(200 + index), GameVersions: []string{exactGame, strings.ToUpper(platform)}}
						if curseForgeArtifactEligible(f, server) != expected {
							t.Fatal("CurseForge artifact eligibility", game, software, platform, exactGame)
						}
						if curseForgeCompatible(f, server) != expected {
							t.Fatal("CurseForge", game, software, platform, exactGame)
						}
					}
				}
			}
			if software == "spigot" && !pluginVersionCompatible(pluginCatalogVersion{GameVersions: []string{game}, Loaders: []string{"bukkit", "paper"}}, server) {
				t.Fatal("explicit Bukkit evidence ignored")
			}
			for _, generic := range []string{"", "Bukkit Plugins", "Server", "Paper-compatible", "some words"} {
				if curseForgeCompatible(curseForgeFile{GameVersions: []string{game, generic}}, server) {
					t.Fatal("generic metadata became positive evidence", software, generic)
				}
			}
		}
	}
}

func TestCurseForgeUnknownPlatformDiscoveryAndReview(t *testing.T) {
	for _, game := range []string{"1.21.8", "99.1-test"} {
		t.Run(game, func(t *testing.T) {
			f, server := curseForgeCatalogFixture(t, "spigot")
			server.MinecraftVersion = game
			original := curseForgeClient.Transport
			curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				response, err := original.RoundTrip(r)
				if err != nil {
					return response, err
				}
				if strings.Contains(r.URL.Path, "/files") {
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					response.Body.Close()
					if strings.HasSuffix(r.URL.Path, "/files") {
						var files []curseForgeFile
						if err := json.Unmarshal(payload["data"], &files); err != nil {
							t.Fatal(err)
						}
						for i := range files {
							files[i].GameVersions = []string{game}
						}
						payload["data"], _ = json.Marshal(files)
					} else {
						var file curseForgeFile
						if err := json.Unmarshal(payload["data"], &file); err != nil {
							t.Fatal(err)
						}
						file.GameVersions = []string{game}
						payload["data"], _ = json.Marshal(file)
					}
					raw, _ := json.Marshal(payload)
					response.Body = io.NopCloser(strings.NewReader(string(raw)))
				}
				return response, nil
			})
			for _, compatible := range []bool{false, true} {
				page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 8, Compatible: compatible, Release: true})
				if err != nil {
					t.Fatal(err)
				}
				if compatible {
					if len(page.Projects) != 0 {
						t.Fatal("unproven file in compatible-only discovery", page)
					}
				} else {
					if len(page.Projects) != 1 || page.Projects[0].Compatible || !strings.Contains(page.Projects[0].Compatibility, "unknown") || !page.Projects[0].InstallAvailable {
						t.Fatal(page)
					}
				}
			}
			plan, err := resolveCurseForgeInstallPlan(context.Background(), server, f.data, "10", "20")
			if err != nil || !strings.Contains(plan.Compatibility, "unknown") {
				t.Fatal(plan, err)
			}
			if err := reviewRootPluginArtifact(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSpigotExactArtifactStructure(t *testing.T) {
	for _, descriptor := range []string{"plugin.yml", "paper-plugin.yml"} {
		path := filepath.Join(t.TempDir(), "example.jar")
		pluginTestJar(t, path, map[string]string{descriptor: "name: ExamplePlugin\nmain: example.Plugin\nversion: 1\n"})
		jar, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = inspectServerPlugin(jar, "example.jar", "spigot")
		jar.Close()
		if (err == nil) != (descriptor == "plugin.yml") {
			t.Fatal(descriptor, err)
		}
	}
}

func TestHangarExactPlatformAcrossReleases(t *testing.T) {
	for _, game := range []string{"1.21.8", "26.3", "99.1-test"} {
		for _, software := range []string{"paper", "purpur", "pufferfish", "leaf", "spigot"} {
			for _, exact := range []string{game, "wrong-game"} {
				t.Run(game+"/"+software+"/"+exact, func(t *testing.T) {
					f := newHangarInstallFixture(t, software)
					f.minecraftVersion = game
					v := f.releases["1"]
					v.PlatformDependencies["PAPER"] = []string{exact}
					f.releases["1"] = v
					server := pluginContentContext(software)
					server.InstanceID, server.MinecraftVersion = "jv-a1b2c3", game
					want := software != "spigot" && exact == game
					selected, matches, err := selectHangarVersion(context.Background(), 1, server, true, true)
					if (err == nil && selected != nil && matches) != want {
						t.Fatal("discovery", selected, matches, err)
					}
					_, err = resolveHangarPluginInstallPlan(context.Background(), server, f.data, "1", "10")
					if (err == nil) != want {
						t.Fatal("install", err)
					}
				})
			}
		}
	}
}

func TestModrinthExactReleaseRevalidatedAcrossReleases(t *testing.T) {
	for _, game := range []string{"1.21.8", "26.3", "99.1-test"} {
		for _, platform := range []string{"bukkit", "spigot", "paper", "fabric", "forge", "neoforge", "quilt"} {
			for _, exact := range []string{game, "wrong-game"} {
				t.Run(game+"/"+platform+"/"+exact, func(t *testing.T) {
					f := newPluginInstallFixture(t)
					server := pluginContentContext("spigot")
					server.InstanceID, server.MinecraftVersion = "jv-a1b2c3", game
					v := f.versions["release"]
					v.Loaders, v.GameVersions = []string{platform}, []string{exact}
					f.versions["release"] = v
					_, err := resolvePluginInstallPlan(context.Background(), server, f.data, "root", "release")
					want := (platform == "bukkit" || platform == "spigot") && exact == game
					if (err == nil) != want {
						t.Fatal(game, platform, exact, err)
					}
				})
			}
		}
	}
	for _, environment := range []string{"client_only", "singleplayer_only", "", "unknown"} {
		if modrinthServerEnvironment(environment) {
			t.Fatal("client/unknown eligibility accepted", environment)
		}
	}
}
