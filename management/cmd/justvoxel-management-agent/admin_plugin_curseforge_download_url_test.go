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
			if err != nil || len(page.Projects) != 1 || page.Projects[0].InstallAvailable != tc.wantOK {
				t.Fatal("unexpected Discover eligibility")
			}
			if tc.wantOK && page.Projects[0].Compatibility != "Compatible Minecraft/loader release. Dedicated-server eligibility is checked during Review." {
				t.Fatal("dedicated-server compatibility wording changed")
			}
			if !tc.wantOK && tc.invalid != "project" && tc.invalid != "file" && page.Projects[0].Compatibility != "CurseForge does not expose a verifiable downloadable JAR for this release." {
				t.Fatal("missing unavailable artifact explanation")
			}
			wantCalls := 0
			if tc.wantFallback {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("unexpected Discover fallback request count", calls)
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
