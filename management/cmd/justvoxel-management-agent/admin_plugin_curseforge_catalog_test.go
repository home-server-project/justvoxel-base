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
	"reflect"
	"strconv"
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
	// Normal projects permit installation; restrictions have dedicated cases.
	projectRaw, _ := json.Marshal(project)
	var projectJSON map[string]any
	if err := json.Unmarshal(projectRaw, &projectJSON); err != nil {
		t.Fatal(err)
	}
	projectJSON["allowModDistribution"] = true
	projectJSON["isAvailable"] = true
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
			payload = map[string]any{"data": projectJSON}
		case "/v1/mods/10/files/20":
			payload = map[string]any{"data": file}
		case "/v1/mods/10/files":
			payload = map[string]any{"data": []curseForgeFile{file}, "pagination": curseForgePagination{0, 25, 1, 1}}
		case "/v1/mods/search":
			if r.URL.Query().Get("gameId") != "1" {
				t.Fatal("missing resolved game")
			}
			payload = map[string]any{"data": []any{projectJSON}, "pagination": curseForgePagination{0, 8, 1, 1}}
		default:
			return nil, errors.New("unexpected catalog path")
		}
		body, err := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, err
	})}
	pluginDownloadClient = &http.Client{Transport: pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-api-key") != "private-fixture-key" || r.URL.Host != "edge.forgecdn.net" {
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
			if err != nil || len(page.Projects) != 1 || !(page.Projects[0].InstallAvailable || c.ContentKind == "mods" && page.Projects[0].ReviewAvailable) {
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
	curseForgeCatalogFixture(t, "paper")
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

// Keep context/key setup from the catalog fixture, but model raw search positions
// independently from the release filtering performed by Discover.
func curseForgeDiscoverFixture(t *testing.T, projects []curseForgeProject, rejected map[int64]bool, pagination func(*curseForgePagination)) (pluginServerContext, *[]int, *[]int64) {
	t.Helper()
	_, server := curseForgeCatalogFixture(t, "paper")
	base := curseForgeClient.Transport
	searches, inspected := []int{}, []int64{}
	curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.curseforge.com" || r.Header.Get("x-api-key") != "private-fixture-key" {
			t.Fatalf("unexpected catalog origin or credentials: %s", r.URL)
		}
		var payload any
		if r.URL.Path == "/v1/mods/search" {
			index, err := strconv.Atoi(r.URL.Query().Get("index"))
			if err != nil || index < 0 || index > len(projects) {
				t.Fatal("invalid index", index)
			}
			limit, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
			if err != nil || limit <= 0 {
				t.Fatal("invalid limit", limit)
			}
			searches = append(searches, index)
			end := min(index+limit, len(projects))
			page := curseForgePagination{index, limit, end - index, len(projects)}
			if pagination != nil {
				pagination(&page)
			}
			payload = map[string]any{"data": projects[index:end], "pagination": page}
		} else if strings.HasPrefix(r.URL.Path, "/v1/mods/") {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) < 5 || parts[4] != "files" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			id, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			file := curseForgeFile{ID: 20, ModID: id, Game: 1, Available: true, Status: 4, Release: 1, Filename: "root.jar", DisplayName: "1", Size: 3, URL: "https://edge.forgecdn.net/files/123/456/root.jar", GameVersions: []string{"1.21.8"}}
			file.Hashes = append(file.Hashes, struct {
				Value string `json:"value"`
				Algo  int    `json:"algo"`
			}{strings.Repeat("a", 40), 1})
			if rejected[id] {
				// Odd rejected projects have only a beta; even ones have an
				// incompatible release. Both are real file-level rejections.
				if id%2 == 1 {
					file.Release = 2
				} else {
					file.GameVersions = []string{"1.20.1"}
				}
			}
			if len(parts) == 5 {
				inspected = append(inspected, id)
				payload = map[string]any{"data": []curseForgeFile{file}, "pagination": curseForgePagination{0, 25, 1, 1}}
			} else if len(parts) == 6 && parts[5] == "20" {
				payload = map[string]any{"data": file}
			} else {
				t.Fatalf("unexpected file path: %s", r.URL.Path)
			}
		} else {
			return base.RoundTrip(r)
		}
		body, err := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, err
	})
	return server, &searches, &inspected
}

func curseForgeRawProjects(count int) []curseForgeProject {
	projects := make([]curseForgeProject, count)
	for i := range projects {
		projects[i] = curseForgeProject{ID: int64(i + 1), Game: 1, Class: 5, Name: "Example", Slug: "example"}
	}
	return projects
}

func TestCurseForgeDiscoverFillsVisiblePageAndResumesRawCursor(t *testing.T) {
	server, searches, inspected := curseForgeDiscoverFixture(t, curseForgeRawProjects(30), map[int64]bool{1: true, 4: true}, nil)
	q := pluginDiscoverQuery{Limit: 12, Compatible: true, Release: true}
	page, err := discoverCurseForgePlugins(context.Background(), server, q)
	if err != nil {
		t.Fatal(err)
	}
	wantPositions := []int{2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	if len(page.Projects) != 12 || !reflect.DeepEqual(page.Positions, wantPositions) || page.End != 14 || page.NextOffset != 14 {
		t.Fatalf("raw cursor lost across top-up: %#v", page)
	}
	if !reflect.DeepEqual(*searches, []int{0, 12}) || len(*inspected) != 14 {
		t.Fatal("unexpected inspection", *searches, *inspected)
	}
	seen := map[string]bool{}
	for i, project := range page.Projects {
		if project.ID != strconv.Itoa(wantPositions[i]) {
			t.Fatal("accepted project/raw position mismatch", project, page.Positions)
		}
		seen[project.ID] = true
	}
	q.Cursor.CurseForgeOffset = page.NextOffset
	next, err := discoverCurseForgePlugins(context.Background(), server, q)
	if err != nil || len(next.Projects) != 12 {
		t.Fatal(next, err)
	}
	if next.Offset != 14 || next.Projects[0].ID != "15" || next.Positions[0] != 15 || next.End != 26 || next.NextOffset != 26 {
		t.Fatal("first uninspected project skipped", next)
	}
	for i, project := range next.Projects {
		if seen[project.ID] || project.ID != strconv.Itoa(15+i) || next.Positions[i] != 15+i {
			t.Fatal("duplicate or skipped raw project", project, next.Positions)
		}
	}
}

func TestCurseForgeDiscoverExhaustionAndInspectionBudget(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		total, rejected, accepted, end, next, batches int
	}{
		{"exhausted", 7, 2, 5, 7, -1, 1},
		{"bounded", 120, 96, 0, 96, 96, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rejected := map[int64]bool{}
			for id := 1; id <= tc.rejected; id++ {
				rejected[int64(id)] = true
			}
			server, searches, inspected := curseForgeDiscoverFixture(t, curseForgeRawProjects(tc.total), rejected, nil)
			page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 12, Compatible: true, Release: true})
			if err != nil || len(page.Projects) != tc.accepted || page.End != tc.end || page.NextOffset != tc.next || len(*inspected) != tc.end || len(*searches) != tc.batches {
				t.Fatal(page, *searches, *inspected, err)
			}
			if len(*inspected) > 8*12 {
				t.Fatal("inspection budget exceeded")
			}
		})
	}
}

func TestCurseForgeDiscoverMalformedPaginationFailsClosed(t *testing.T) {
	for _, field := range []string{"index", "pageSize", "resultCount", "totalCount"} {
		t.Run(field, func(t *testing.T) {
			server, searches, inspected := curseForgeDiscoverFixture(t, curseForgeRawProjects(24), nil, func(p *curseForgePagination) {
				switch field {
				case "index":
					p.Index++
				case "pageSize":
					p.PageSize++
				case "resultCount":
					p.ResultCount--
				case "totalCount":
					p.TotalCount = 0
				}
			})
			page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 12, Compatible: true, Release: true})
			if !errors.Is(err, errCurseForgeCatalog) || len(page.Projects) != 0 || len(*inspected) != 0 || len(*searches) != 1 || page.End != 0 || page.NextOffset != -1 {
				t.Fatal("malformed pagination accepted", page, err)
			}
		})
	}
}

func TestSafeCurseForgeLogoURL(t *testing.T) {
	const full = "https://media.forgecdn.net/avatars/123/456/638123456789012345."
	const thumb = "https://media.forgecdn.net/avatars/thumbnails/123/456/64/64/638123456789012345."
	for _, ext := range []string{"png", "jpg", "jpeg", "gif", "webp"} {
		for _, prefix := range []string{full, thumb} {
			if !safeCurseForgeLogoURL(prefix + ext) {
				t.Error("operational logo rejected", prefix+ext)
			}
		}
	}
	for _, raw := range []string{
		"http://media.forgecdn.net/avatars/123/456/icon.png",
		"https://example.com/avatars/123/456/icon.png",
		"https://edge.forgecdn.net/avatars/123/456/icon.png",
		"https://mediafilez.forgecdn.net/avatars/123/456/icon.png",
		"https://evil.media.forgecdn.net/avatars/123/456/icon.png",
		"https://user@media.forgecdn.net/avatars/123/456/icon.png",
		full + "png?x=1", full + "png?", full + "png#x", full + "png#",
		"https://media.forgecdn.net/avatars/%31%32%33/456/icon.png",
		"https://media.forgecdn.net/avatars/123/456/%69con.png",
		"https://media.forgecdn.net/avatars/123/456/icon%2epng",
		"https://media.forgecdn.net/avatars/123/456/icon\\x.png",
		"https://media.forgecdn.net/avatars/123/../icon.png",
		"https://media.forgecdn.net/avatars/no/456/icon.png",
		"https://media.forgecdn.net/avatars/123/-456/icon.png",
		"https://media.forgecdn.net/avatars//456/icon.png",
		"https://media.forgecdn.net/avatars/123/12345678901234567890/icon.png",
		"https://media.forgecdn.net/avatars/thumbnails/123/456/0/64/icon.png",
		"https://media.forgecdn.net/avatars/thumbnails/123/456/64/0/icon.png",
		"https://media.forgecdn.net/avatars/thumbnails/123/456/2049/64/icon.png",
		"https://media.forgecdn.net/avatars/thumbnails/123/456/64/2049/icon.png",
		"https://media.forgecdn.net/avatars/thumbnails/123/456/x/64/icon.png",
		full + "svg", full + "exe", full + "unknown",
		"https://media.forgecdn.net/avatars/123/456/.hidden.png",
		"https://media.forgecdn.net/avatars/123/456/icon name.png",
		"https://media.forgecdn.net/avatars/123/456/icon.png.exe",
	} {
		if safeCurseForgeLogoURL(raw) {
			t.Error("unsafe logo accepted", raw)
		}
	}
}

func TestCurseForgeCatalogLogoNeverFiltersValidProjects(t *testing.T) {
	const full = "https://media.forgecdn.net/avatars/123/456/icon.png"
	const thumb = "https://media.forgecdn.net/avatars/thumbnails/123/456/64/64/icon.png"
	for _, tc := range []struct {
		name string
		logo *curseForgeAsset
		want string
	}{
		{"thumbnail", &curseForgeAsset{ID: 1, ModID: 1, ThumbnailURL: thumb, URL: full}, thumb},
		{"full fallback", &curseForgeAsset{ID: 1, ModID: 1, ThumbnailURL: "https://evil.example/icon.png", URL: full}, full},
		{"missing", nil, ""},
		{"wrong owner", &curseForgeAsset{ID: 1, ModID: 2, ThumbnailURL: thumb, URL: full}, ""},
		{"unsafe", &curseForgeAsset{ID: 1, ModID: 1, URL: "https://evil.example/icon.png"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projects := curseForgeRawProjects(1)
			projects[0].Logo = tc.logo
			if got := curseForgeProjectLogo(projects[0], 1); got != tc.want {
				t.Fatalf("logo = %q, want %q", got, tc.want)
			}
			server, _, _ := curseForgeDiscoverFixture(t, projects, nil, nil)
			page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 12, Compatible: true, Release: true})
			if err != nil || len(page.Projects) != 1 || page.Projects[0].ID != "1" || page.Projects[0].Version == nil || !page.Projects[0].InstallAvailable || page.Projects[0].IconURL != tc.want {
				t.Fatal("logo changed catalog membership", page, err)
			}
		})
	}
}

func TestCurseForgeDirectInstallAllowedDistribution(t *testing.T) {
	f, server := curseForgeCatalogFixture(t, "fabric")
	// Allowed supplier metadata permits exact review and installation.
	page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 8, Compatible: true, Release: true})
	if err != nil || len(page.Projects) != 1 || page.Projects[0].ID != "10" || page.Projects[0].InstallAvailable || !page.Projects[0].ReviewAvailable {
		t.Fatal(page, err)
	}
	plan, err := resolveProviderPluginInstallPlan(context.Background(), server, f.data, "CurseForge", "10", "20")
	if err != nil || !plan.InstallAvailable {
		t.Fatal(plan, err)
	}
	if err := reviewRootPluginArtifact(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if journal := f.execute(t, plan, false); journal.State != operationSucceeded {
		t.Fatal(journal)
	}
}

func TestCurseForgeSupplierInstallability(t *testing.T) {
	for _, loader := range []string{"fabric", "paper"} {
		for _, state := range []string{"allowed", "forbidden", "unavailable", "absent"} {
			t.Run(loader+"/"+state, func(t *testing.T) {
				f, server := curseForgeCatalogFixture(t, loader)
				base := curseForgeClient.Transport
				downloads := 0
				curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/download-url") {
						downloads++
						t.Fatal("restricted project requested download URL")
					}
					response, err := base.RoundTrip(r)
					if err != nil {
						return response, err
					}
					var body map[string]any
					if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					response.Body.Close()
					modify := func(p map[string]any) {
						delete(p, "allowModDistribution")
						if state != "absent" {
							p["allowModDistribution"] = state != "forbidden"
						}
						p["isAvailable"] = state != "unavailable"
					}
					if r.URL.Path == "/v1/mods/10" {
						modify(body["data"].(map[string]any))
					}
					if r.URL.Path == "/v1/mods/search" {
						for _, p := range body["data"].([]any) {
							modify(p.(map[string]any))
						}
					}
					// Force URL fallback if installation erroneously reaches artifact resolution.
					if r.URL.Path == "/v1/mods/10/files/20" && (state == "forbidden" || state == "unavailable") {
						body["data"].(map[string]any)["downloadUrl"] = ""
					}
					raw, _ := json.Marshal(body)
					response.Body = io.NopCloser(bytes.NewReader(raw))
					return response, nil
				})
				page, err := discoverCurseForgePlugins(context.Background(), server, pluginDiscoverQuery{Limit: 8, Compatible: true, Release: true})
				if err != nil || len(page.Projects) != 1 {
					t.Fatal(page, err)
				}
				allowed := state == "allowed" || state == "absent"
				card := page.Projects[0]
				if (card.ReviewAvailable || card.InstallAvailable) != allowed {
					t.Fatal(card)
				}
				plan, err := resolveProviderPluginInstallPlan(context.Background(), server, f.data, "CurseForge", "10", "20")
				if err != nil || plan.InstallAvailable != allowed {
					t.Fatal(plan, err)
				}
				if !allowed && (!strings.Contains(plan.Compatibility, "CurseForge") || downloads != 0 || f.backup || len(f.commands) != 0) {
					t.Fatal(plan, downloads)
				}
			})
		}
	}
}

func TestCurseForgeFabricRuntimeLoaderIdentityReuse(t *testing.T) {
	for _, rootProvided := range []bool{false, true} {
		t.Run(strconv.FormatBool(rootProvided), func(t *testing.T) {
			f, c := curseForgeCatalogFixture(t, "fabric")
			recordRuntimeComponent(t, f, "fabric", "fabric-api.jar", componentModJarVersion(t, "fabric", "fabric-api", "runtime-version"))
			rootID := "absorbaholic"
			if rootProvided {
				rootID = "fabric-api"
			}
			rootRaw := componentModJar(t, "fabric", rootID)
			depRaw := componentModJar(t, "fabric", "fabric-api")
			makeFile := func(project, id int64, name string, raw []byte) curseForgeFile {
				sum := sha1.Sum(raw)
				file := curseForgeFile{ID: id, ModID: project, Game: 1, Available: true, Filename: name, DisplayName: "1", Size: int64(len(raw)), URL: "https://edge.forgecdn.net/files/1234/567/" + name, GameVersions: []string{"1.21.8", "Fabric"}, Release: 1, Status: 4, Dependencies: []curseForgeDependency{}}
				file.Hashes = append(file.Hashes, struct {
					Value string `json:"value"`
					Algo  int    `json:"algo"`
				}{hex.EncodeToString(sum[:]), 1})
				return file
			}
			root := makeFile(10, 20, "root.jar", rootRaw)
			dep := makeFile(11, 21, "dependency.jar", depRaw)
			if !rootProvided {
				root.Dependencies = []curseForgeDependency{{ModID: 11, Relation: 3}}
			}
			base := curseForgeClient.Transport
			curseForgeClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				var payload any
				switch r.URL.Path {
				case "/v1/mods/10/files/20":
					payload = map[string]any{"data": root}
				case "/v1/mods/11":
					payload = map[string]any{"data": curseForgeProject{ID: 11, Game: 1, Class: 6, Name: "Shared dependency"}}
				case "/v1/mods/11/files":
					payload = map[string]any{"data": []curseForgeFile{dep}, "pagination": curseForgePagination{0, 25, 1, 1}}
				case "/v1/mods/11/files/21":
					payload = map[string]any{"data": dep}
				default:
					return base.RoundTrip(r)
				}
				raw, _ := json.Marshal(payload)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})
			pluginDownloadClient.Transport = pluginTestTransport(func(r *http.Request) (*http.Response, error) {
				raw := rootRaw
				if filepath.Base(r.URL.Path) == "dependency.jar" {
					raw = depRaw
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})
			plan, err := resolveProviderPluginInstallPlan(context.Background(), c, f.data, "CurseForge", "10", "20")
			if err != nil {
				t.Fatal(err)
			}
			if rootProvided {
				if plan.InstallAvailable || plan.Compatibility != runtimeModProvided {
					t.Fatal(plan)
				}
			} else {
				if !plan.InstallAvailable || len(plan.Artifacts) != 2 {
					t.Fatal(plan)
				}
				for _, a := range plan.Artifacts {
					if !a.Requested && (a.Satisfied != runtimeModSatisfied || a.ExistingFilename != "fabric-api.jar" || a.ExistingHash == "") {
						t.Fatal(a)
					}
				}
			}
			if f.backup || len(f.commands) != 0 {
				t.Fatal("read-only review changed runtime")
			}
		})
	}
}
