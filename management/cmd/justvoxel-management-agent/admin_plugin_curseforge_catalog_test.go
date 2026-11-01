package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func curseForgeCatalogFixture(t *testing.T, loader string) (*pluginInstallFixture, pluginServerContext) {
	t.Helper()
	f := newPluginInstallFixtureFor(t, loader, "1.21.8")
	oldDir, oldClient := curseForgeDirectory, curseForgeClient
	curseForgeDirectory = curseForgeTestDirectory(t)
	t.Cleanup(func() { curseForgeDirectory = oldDir; curseForgeClient = oldClient })
	dir, err := openCurseForgeDirectory(curseForgeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	err = replaceCurseForgeKey(dir, "private-fixture-key")
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	c := pluginContentContext(loader)
	c.InstanceID = "jv-a1b2c3"
	c.Configured = true
	c.MinecraftVersion = "1.21.8"
	raw := f.jars["root.jar"]
	class := int64(5)
	versions := []string{"1.21.8"}
	if c.ContentKind == "mods" {
		if err := os.Mkdir(filepath.Join(f.data, "mods"), 0700); err != nil {
			t.Fatal(err)
		}
		raw = resetModJar(t, loader)
		class = 6
		_, name := curseForgeLoader(c)
		versions = append(versions, name)
	}
	sum := sha1.Sum(raw)
	file := curseForgeFile{ID: 20, ModID: 10, Game: 1, Available: true, Filename: "root.jar", DisplayName: "1", Size: int64(len(raw)), URL: "https://edge.forgecdn.net/files/1234/567/root.jar", GameVersions: versions, Release: 1, Status: 4, Published: "2026-01-01T00:00:00Z", Dependencies: []curseForgeDependency{}}
	file.Hashes = append(file.Hashes, struct {
		Value string `json:"value"`
		Algo  int    `json:"algo"`
	}{hex.EncodeToString(sum[:]), 1})
	project := curseForgeProject{ID: 10, Game: 1, Class: class, Name: "Example", Slug: "example"}
	curseForgeClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.curseforge.com" || r.Header.Get("x-api-key") != "private-fixture-key" {
			t.Fatal("credential origin", r.URL.Host)
		}
		var payload any
		switch r.URL.Path {
		case "/v1/games":
			payload = map[string]any{"data": []any{map[string]any{"id": 1, "slug": "minecraft"}}, "pagination": curseForgePagination{0, 50, 1, 1}}
		case "/v1/categories":
			payload = map[string]any{"data": []any{map[string]any{"id": 5, "gameId": 1, "slug": "bukkit-plugins", "isClass": true}, map[string]any{"id": 6, "gameId": 1, "slug": "mc-mods", "isClass": true}}}
		case "/v1/mods/10":
			payload = map[string]any{"data": project}
		case "/v1/mods/10/files/20":
			payload = map[string]any{"data": file}
		case "/v1/mods/10/files":
			payload = map[string]any{"data": []curseForgeFile{file}, "pagination": curseForgePagination{0, 25, 1, 1}}
		case "/v1/mods/search":
			if r.URL.Query().Get("gameId") != "1" {
				t.Fatal("missing resolved game")
			}
			payload = map[string]any{"data": []curseForgeProject{project}, "pagination": curseForgePagination{0, 8, 1, 1}}
		default:
			return nil, errors.New("unexpected catalog path")
		}
		body, err := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, err
	})}
	pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-api-key") != "" || r.URL.Host != "edge.forgecdn.net" {
			t.Fatal("credential reached artifact origin")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(raw)), Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})}
	return f, c
}
func TestCurseForgeCatalogAndInstallPluginsAndMods(t *testing.T) {
	for _, loader := range []string{"paper", "spigot", "pufferfish", "leaf", "fabric", "forge", "neoforge", "quilt"} {
		t.Run(loader, func(t *testing.T) {
			f, c := curseForgeCatalogFixture(t, loader)
			page, err := discoverCurseForgePlugins(context.Background(), c, pluginDiscoverQuery{Limit: 8, Compatible: true, Release: true})
			if err != nil || len(page.Projects) != 1 || !page.Projects[0].InstallAvailable {
				t.Fatal(page, err)
			}
			plan, err := resolveCurseForgeInstallPlan(context.Background(), c, f.data, "10", "20")
			if err != nil {
				t.Fatal(err)
			}
			public, _ := json.Marshal(plan)
			if bytes.Contains(public, []byte("private-fixture-key")) || bytes.Contains(public, []byte("forgecdn.net")) {
				t.Fatal("private metadata exposed")
			}
			journal := f.execute(t, plan, false)
			if journal.State != operationSucceeded {
				t.Fatal(journal.State, journal.Status)
			}
			m, _, err := readPluginManifest(f.data)
			if err != nil || len(m.Artifacts) != 1 || m.Artifacts[0].Source != "CurseForge" || pluginTarget(m.Artifacts[0].Target) != c.ContentKind || !pluginHashPattern.MatchString(m.Artifacts[0].SHA512) {
				t.Fatal(m, err)
			}
			root, err := openContentDirectory(f.data, c.ContentKind)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err = verifyManagedArtifactAt(root, m.Artifacts[0]); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCurseForgeArtifactSafety(t *testing.T) {
	for _, raw := range []string{"http://edge.forgecdn.net/files/123/456/x.jar", "https://edge.forgecdn.net.evil/files/123/456/x.jar", "https://edge.forgecdn.net/files/123/456/x.jar?key=secret", "https://user@edge.forgecdn.net/files/123/456/x.jar", "https://edge.forgecdn.net/files/123/456/../x.jar"} {
		if safeCurseForgeArtifactURL(raw) {
			t.Fatal("unsafe artifact origin", raw)
		}
	}
	c := pluginContentContext("fabric")
	c.MinecraftVersion = "1.21.8"
	for _, versions := range [][]string{{"1.21.8"}, {"1.21.7", "Fabric"}, {"1.21.8", "Forge"}} {
		if curseForgeCompatible(curseForgeFile{GameVersions: versions}, c) {
			t.Fatal("loader/game mismatch", versions)
		}
	}
	a := pluginInstallArtifact{Source: "CurseForge", Filename: "x.jar", Size: 3, URL: "https://edge.forgecdn.net/files/123/456/x.jar", SHA1: strings.Repeat("0", 40)}
	old := pluginDownloadClient
	t.Cleanup(func() { pluginDownloadClient = old })
	pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: 3, Body: io.NopCloser(strings.NewReader("bad"))}, nil
	})}
	if _, err := downloadVerifiedPluginArtifact(context.Background(), a, t.TempDir()); err == nil {
		t.Fatal("SHA-1 mismatch accepted")
	}
	sum := sha1.Sum([]byte("bad"))
	a.SHA1 = hex.EncodeToString(sum[:])
	got, err := downloadVerifiedPluginArtifact(context.Background(), a, t.TempDir())
	want := sha512.Sum512([]byte("bad"))
	if err != nil || got != hex.EncodeToString(want[:]) {
		t.Fatal("durable SHA-512", got, err)
	}
}
