package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCurseForgeOfficialDownloadURLResolution(t *testing.T) {
	const artifactURL = "https://edge.forgecdn.net/files/123/456/example.jar"
	redirectPolicy := curseForgeClient.CheckRedirect
	for _, tc := range []struct {
		name, inline, body, invalid string
		status                      int
		wantOK, wantFallback        bool
	}{
		{name: "inline", inline: artifactURL, wantOK: true},
		{name: "official fallback", body: `{"data":"` + artifactURL + `"}`, wantOK: true, wantFallback: true},
		{name: "wrong host", body: `{"data":"https://attacker.invalid/files/123/456/example.jar"}`, wantFallback: true},
		{name: "host suffix", body: `{"data":"https://edge.forgecdn.net.evil/files/123/456/example.jar"}`, wantFallback: true},
		{name: "query", body: `{"data":"` + artifactURL + `?key=private-fixture-key"}`, wantFallback: true},
		{name: "http", body: `{"data":"http://edge.forgecdn.net/files/123/456/example.jar"}`, wantFallback: true},
		{name: "empty fallback", body: `{"data":""}`, wantFallback: true},
		{name: "not found", status: http.StatusNotFound, body: "private-fixture-key private-response", wantFallback: true},
		{name: "forbidden", status: http.StatusForbidden, body: "private-fixture-key private-response", wantFallback: true},
		{name: "redirect", status: http.StatusFound, wantFallback: true},
		{name: "transport failure", invalid: "transport", wantFallback: true},
		{name: "wrong JSON type", body: `{"data":{"url":"` + artifactURL + `"}}`, wantFallback: true},
		{name: "trailing JSON", body: `{"data":"` + artifactURL + `"} {}`, wantFallback: true},
		{name: "oversized response", body: strings.Repeat(" ", (2<<20)+1), wantFallback: true},
		{name: "missing SHA1", invalid: "SHA1", body: `{"data":"` + artifactURL + `"}`},
		{name: "zero size", invalid: "size", body: `{"data":"` + artifactURL + `"}`},
		{name: "oversized file", invalid: "oversized", body: `{"data":"` + artifactURL + `"}`},
		{name: "invalid filename", invalid: "filename", body: `{"data":"` + artifactURL + `"}`},
		{name: "wrong project", invalid: "project", body: `{"data":"` + artifactURL + `"}`},
		{name: "wrong file", invalid: "file", body: `{"data":"` + artifactURL + `"}`},
		{name: "unsafe inline is rejected", inline: "https://attacker.invalid/example.jar", body: `{"data":"` + artifactURL + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server := curseForgeCatalogFixture(t, "fabric")
			curseForgeClient.CheckRedirect = redirectPolicy
			base := curseForgeClient.Transport
			calls := 0
			curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.curseforge.com" || r.Header.Get("x-api-key") != "private-fixture-key" || strings.Contains(r.URL.String(), "private-fixture-key") || r.URL.User != nil {
					t.Fatal("catalog credential boundary violated")
				}
				if strings.HasSuffix(r.URL.Path, "/download-url") {
					calls++
					if r.URL.Path != "/v1/mods/10/files/20/download-url" || r.URL.RawQuery != "" {
						t.Fatal("fallback did not use exact validated identity")
					}
					if tc.invalid == "transport" {
						return nil, errors.New("private-fixture-key private-response")
					}
					status := tc.status
					if status == 0 {
						status = http.StatusOK
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://attacker.invalid/stolen"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				}
				response, err := base.RoundTrip(r)
				if err != nil || r.URL.Path != "/v1/mods/10/files/20" {
					return response, err
				}
				defer response.Body.Close()
				var result struct {
					Data curseForgeFile `json:"data"`
				}
				if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
					t.Fatal(err)
				}
				result.Data.URL, result.Data.Filename = tc.inline, "example.jar"
				switch tc.invalid {
				case "SHA1":
					result.Data.Hashes = nil
				case "size":
					result.Data.Size = 0
				case "oversized":
					result.Data.Size = pluginMaxJar + 1
				case "filename":
					result.Data.Filename = "../example.jar"
				case "project":
					result.Data.ModID = 11
				case "file":
					result.Data.ID = 21
				}
				raw, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				response.Body = io.NopCloser(bytes.NewReader(raw))
				return response, nil
			})
			page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 8, Compatible: true, Release: true})
			if err != nil || len(page.Projects) != 1 || page.Projects[0].InstallAvailable || page.Projects[0].ReviewAvailable != (tc.invalid != "project" && tc.invalid != "file") {
				t.Fatal("unexpected Discover eligibility")
			}
			if page.Projects[0].ReviewAvailable && page.Projects[0].Compatibility != "Compatible Minecraft/loader release. Dedicated-server eligibility is checked during Review." {
				t.Fatal("dedicated-server compatibility wording changed")
			}
			wantCalls := 0
			if tc.wantFallback {
				wantCalls = 1
			}
			if calls != 0 {
				t.Fatal("Discover resolved an install artifact URL", calls)
			}
			// Planning must resolve independently of Discover, including direct requests.
			calls = 0
			plan, planErr := resolveCurseForgeInstallPlan(context.Background(), server, f.data, "10", "20")
			if (planErr == nil) != tc.wantOK || calls != wantCalls {
				t.Fatal("unexpected independent plan resolution", calls)
			}
			if planErr != nil && (strings.Contains(planErr.Error(), "private-fixture-key") || strings.Contains(planErr.Error(), "private-response")) {
				t.Fatal("private API failure exposed")
			}
			public, err := json.Marshal(struct {
				Page pluginCatalogPage
				Plan pluginInstallPlan
			}{page, plan})
			if err != nil || bytes.Contains(public, []byte("private-fixture-key")) || bytes.Contains(public, []byte("private-response")) {
				t.Fatal("private metadata exposed")
			}
			if !tc.wantOK {
				if len(plan.Artifacts) != 0 {
					t.Fatal("failed URL resolution synthesized an artifact")
				}
				return
			}
			if len(plan.Artifacts) != 1 || plan.Artifacts[0].URL != artifactURL || !validPluginDownloadMetadata(plan.Artifacts[0]) || plan.Fingerprint != fingerprintPluginPlan(plan) {
				t.Fatal("resolved artifact evidence not bound to plan")
			}
			for _, field := range []string{"URL", "SHA1", "size", "filename", "project", "file"} {
				changed := plan
				changed.Artifacts = append([]pluginInstallArtifact(nil), plan.Artifacts...)
				switch field {
				case "URL":
					changed.Artifacts[0].URL = "https://mediafilez.forgecdn.net/files/123/456/example.jar"
				case "SHA1":
					changed.Artifacts[0].SHA1 = strings.Repeat("0", 40)
				case "size":
					changed.Artifacts[0].Size++
				case "filename":
					changed.Artifacts[0].Filename = "other.jar"
				case "project":
					changed.Artifacts[0].ProjectID = "11"
				case "file":
					changed.Artifacts[0].VersionID = "21"
				}
				if fingerprintPluginPlan(changed) == plan.Fingerprint {
					t.Fatal("resolved evidence omitted from fingerprint", field)
				}
			}
		})
	}
}

func TestCurseForgeDownloadURLKeyStaysOutOfOtherProviders(t *testing.T) {
	for _, provider := range []string{"Modrinth", "Hangar"} {
		t.Run(provider, func(t *testing.T) {
			curseForgeCatalogFixture(t, "paper")
			var resolve func() pluginInstallPlan
			if provider == "Modrinth" {
				f := newPluginInstallFixture(t)
				resolve = func() pluginInstallPlan { return f.plan(t) }
			} else {
				f := newHangarInstallFixture(t, "paper")
				resolve = func() pluginInstallPlan { return f.hangarPlan(t) }
			}
			catalogCalls, artifactCalls := 0, 0
			check := func(r *http.Request) {
				if r.Header.Get("x-api-key") != "" || strings.Contains(r.URL.String(), "private-fixture-key") {
					t.Fatal("CurseForge key reached another provider")
				}
			}
			catalog, download := pluginCatalogClient.Transport, pluginDownloadClient.Transport
			pluginCatalogClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				catalogCalls++
				check(r)
				return catalog.RoundTrip(r)
			})
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				artifactCalls++
				check(r)
				return download.RoundTrip(r)
			})
			if err := reviewRootPluginArtifact(context.Background(), resolve()); err != nil {
				t.Fatal(err)
			}
			if catalogCalls == 0 || artifactCalls == 0 {
				t.Fatal("provider API and artifact boundaries were not both exercised")
			}
		})
	}
}

func TestCurseForgeEscapedArtifactFilename(t *testing.T) {
	const prefix = "https://edge.forgecdn.net/files/123/456/"
	for _, filename := range []string{"ageingspawners-1.0.0%2B26.3-fabric.jar", "ageingspawners-1.0.0+26.3-fabric.jar"} {
		if !safeCurseForgeArtifactURL(prefix + filename) {
			t.Error("legitimate filename rejected", filename)
		}
	}
	for _, raw := range []string{
		prefix + "dir%2Fmod.jar", prefix + "dir%2fmod.jar", prefix + "dir%5Cmod.jar",
		prefix + "%2e%2e%2fmod.jar", prefix + "%2E%2E%5cmod.jar",
		prefix + "../mod.jar", prefix + "%00mod.jar", prefix + "%ZZ.jar", prefix + "mod%.jar",
		prefix + "mod.jar?", prefix + "mod.jar?token=x", prefix + "mod.jar#", prefix + "mod.jar#x",
		"https://edge.forgecdn.net/%66iles/123/456/mod.jar",
		"https://edge.forgecdn.net/files/%31%32%33/456/mod.jar",
		"https://edge.forgecdn.net/files/123/%34%35%36/mod.jar",
		"https://edge.forgecdn.net/files/0/456/mod.jar",
		"https://edge.forgecdn.net/files/123/0/mod.jar",
		"https://user@edge.forgecdn.net/files/123/456/mod.jar",
		"https://edge.forgecdn.net.evil/files/123/456/mod.jar",
		"https://evilforgecdn.net/files/123/456/mod.jar",
		"https://127.0.0.1/files/123/456/mod.jar",
		"https://edge.forgecdn.net:444/files/123/456/mod.jar",
		"http://edge.forgecdn.net/files/123/456/mod.jar",
	} {
		if safeCurseForgeArtifactURL(raw) {
			t.Error("hostile artifact URL accepted", raw)
		}
	}
}

func TestCurseForgeExactModrinthAlternateIdentity(t *testing.T) {
	for _, state := range []string{"exact", "lookup failure", "invalid project", "invalid version", "game", "loader", "client_only", "singleplayer_only", "unknown environment", "hash", "missing hash", "unsafe URL", "filename", "size", "SHA512", "beta", "other file", "invalid input"} {
		t.Run(state, func(t *testing.T) {
			f, server := curseForgeCatalogFixture(t, "fabric")
			plan, err := resolveCurseForgeInstallPlan(context.Background(), server, f.data, "10", "20")
			if err != nil || len(plan.Artifacts) != 1 {
				t.Fatal(plan, err)
			}
			a := plan.Artifacts[0]
			file := pluginArtifactFile{Filename: "official.jar", URL: "https://cdn.modrinth.com/data/exact/version/official.jar", Size: a.Size}
			file.Hashes.SHA1, file.Hashes.SHA512 = a.SHA1, strings.Repeat("b", 128)
			v := pluginCatalogVersion{ProjectID: "DifferentOfficialProject", ID: "ExactVersion", Number: "1", Type: "release", Environment: "server_only", GameVersions: []string{server.MinecraftVersion}, Loaders: []string{"fabric"}, Files: []pluginArtifactFile{file}}
			switch state {
			case "invalid project":
				v.ProjectID = "../root"
			case "invalid version":
				v.ID = "../version"
			case "game":
				v.GameVersions = []string{"1.20.1"}
			case "loader":
				v.Loaders = []string{"forge"}
			case "client_only", "singleplayer_only":
				v.Environment = state
			case "unknown environment":
				v.Environment = ""
			case "hash":
				v.Files[0].Hashes.SHA1 = strings.Repeat("0", 40)
			case "missing hash":
				v.Files[0].Hashes.SHA1 = ""
			case "unsafe URL":
				v.Files[0].URL = "https://cdn.modrinth.com.evil/data/official.jar"
			case "filename":
				v.Files[0].Filename = "../official.jar"
			case "size":
				v.Files[0].Size++
			case "SHA512":
				v.Files[0].Hashes.SHA512 = ""
			case "beta":
				v.Type = "beta"
			case "other file":
				// A valid hash on a different unsafe file is not evidence for
				// the safe file whose hash does not match.
				v.Files[0].Hashes.SHA1 = strings.Repeat("0", 40)
				file.URL = "https://attacker.invalid/official.jar"
				v.Files = append(v.Files, file)
			}
			old := pluginCatalogClient
			t.Cleanup(func() { pluginCatalogClient = old })
			calls := 0
			pluginCatalogClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" || r.URL.Host != "api.modrinth.com" || r.URL.Path != "/v2/version_file/"+a.SHA1 || r.URL.Query().Get("algorithm") != "sha1" || len(r.URL.Query()) != 1 || r.Header.Get("x-api-key") != "" {
					t.Fatal("alternate lookup guessed identity or crossed credential boundary", r.URL)
				}
				if state == "lookup failure" {
					return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				raw, err := json.Marshal(v)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, err
			})}
			input := a
			if state == "invalid input" {
				input.SHA1 = "../not-a-hash"
			}
			alternate := exactCurseForgeModrinthVersion(context.Background(), input, server)
			if (alternate != nil) != (state == "exact") {
				t.Fatal("unproven alternate identity", alternate)
			}
			if state == "invalid input" {
				if calls != 0 {
					t.Fatal("invalid hash reached provider")
				}
			} else if calls != 1 {
				t.Fatal("lookup was not bounded", calls)
			}
			if alternate != nil && (alternate.ProjectID != v.ProjectID || alternate.ID != v.ID || len(alternate.Files) != 1 || alternate.Files[0].Hashes.SHA1 != a.SHA1) {
				t.Fatal("alternate did not preserve exact official identity", alternate)
			}
			// Alternate evidence, including lookup failure, never rewrites the
			// CurseForge plan, dependency resolver, fingerprint or ownership.
			if plan.Source != "CurseForge" || plan.Artifacts[0].Source != "CurseForge" || plan.Artifacts[0].ProjectID != "10" || plan.Artifacts[0].VersionID != "20" || plan.Fingerprint != fingerprintPluginPlan(plan) {
				t.Fatal("alternate corrupted authoritative install plan", plan)
			}
			if err := reviewRootPluginArtifact(context.Background(), plan); err != nil {
				t.Fatal("alternate blocked CurseForge review", err)
			}
			if journal := f.execute(t, plan, false); journal.State != operationSucceeded {
				t.Fatal(journal)
			}
			manifest, _, err := readPluginManifest(f.data)
			if err != nil || len(manifest.Artifacts) != 1 || manifest.Artifacts[0].Source != "CurseForge" || manifest.Artifacts[0].ProjectID != "10" || manifest.Artifacts[0].VersionID != "20" {
				t.Fatal("alternate corrupted ownership", manifest, err)
			}
		})
	}
}

func TestCurseForgeEscapedFilenameRedirect(t *testing.T) {
	for _, tc := range []struct {
		filename string
		want     bool
	}{
		{"ageingspawners-1.0.0%2B26.3-fabric.jar", true},
		{"dir%2fmod.jar", false},
		{"dir%5cmod.jar", false},
		{"%2e%2e%2fmod.jar", false},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			_, server := curseForgeCatalogFixture(t, "fabric")
			file, err := exactCurseForgeFile(context.Background(), "10", "20")
			if err != nil {
				t.Fatal(err)
			}
			a, err := curseForgeArtifact(context.Background(), file, "10", server, curseForgeContext{Game: 1, Class: 6})
			if err != nil {
				t.Fatal(err)
			}
			base := pluginDownloadClient.Transport
			calls := 0
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://edge.forgecdn.net/files/123/456/" + tc.filename}}, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				if !tc.want || r.URL.EscapedPath() != "/files/123/456/"+tc.filename {
					t.Fatal("unsafe redirect contacted or filename changed", r.URL)
				}
				return base.RoundTrip(r)
			})
			_, err = downloadVerifiedPluginArtifact(context.Background(), a, t.TempDir())
			if (err == nil) != tc.want || calls != map[bool]int{true: 2, false: 1}[tc.want] {
				t.Fatal("redirect did not use corrected artifact validation", calls, err)
			}
		})
	}
}
