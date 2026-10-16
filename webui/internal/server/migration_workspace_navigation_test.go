package server

import (
	"errors"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func migrationNavigationURLMatches(raw, path, workspace, tab, operation string) bool {
	destination, err := url.Parse(raw)
	if err != nil || destination.Path != path {
		return false
	}
	query := destination.Query()
	for key, want := range map[string]string{"workspace": workspace, "tab": tab, "operation": operation, "server": "jv-a1b2c3"} {
		if query.Get(key) != want || (want == "" && query.Has(key)) {
			return false
		}
	}
	return true
}

func migrationMarkupHasURL(markup, path, workspace, tab, operation string) bool {
	for _, match := range regexp.MustCompile(`href="([^"]*)"`).FindAllStringSubmatch(markup, -1) {
		if migrationNavigationURLMatches(html.UnescapeString(match[1]), path, workspace, tab, operation) {
			return true
		}
	}
	return false
}

func TestMigrationWorkspaceImportReviewOnlyContinuesForHumanInput(t *testing.T) {
	for _, tc := range []struct {
		code  string
		input string
	}{
		{code: "source_selection_required", input: `name="source_path"`},
		{code: "multiple_roots", input: `name="selected_root"`},
		{code: "source_version_required", input: `name="source_version"`},
		{code: "stale_root", input: `name="selected_root"`},
	} {
		t.Run(tc.code, func(t *testing.T) {
			client := &fakeServerMigrationImportAPI{
				fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: importMediaFixture(), importDiscovery: importDiscoveryFixture(true)},
				plan: api.AdminMigrationImportPlanResponse{
					SchemaVersion: "v1", Code: tc.code, Error: "Agent requires source input.",
					SourceEntries: []api.AdminMigrationImportSourceEntry{{Path: "server.tar.gz", Kind: "archive"}},
					Candidates:    []api.AdminMigrationImportCandidate{{RootRelative: "server", SourceType: "paper"}},
				},
				planErr: &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "Agent requires source input."},
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/workspace/migration/import/review", configuredImportRequestValues("local")))
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Continue review") || !strings.Contains(page.Body.String(), tc.input) {
				t.Fatalf("interactive %s response = %d: %s", tc.code, page.Code, page.Body.String())
			}
		})
	}
}

func TestMigrationWorkspaceImportDeterministicPlanHasNoContinueLoop(t *testing.T) {
	for _, route := range []string{"review", "apply"} {
		t.Run(route, func(t *testing.T) {
			client := &fakeServerMigrationImportAPI{
				fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: importMediaFixture(), importDiscovery: importDiscoveryFixture(true)},
				plan:                   api.AdminMigrationImportPlanResponse{SchemaVersion: "v1", Code: "port_conflict", Error: "Agent says Java port 25565 is already in use."},
				planErr:                &api.ResponseError{StatusCode: http.StatusBadRequest, Message: "generic plan failure"},
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			values := configuredImportRequestValues("local")
			if route == "apply" {
				values.Set("plan_fingerprint", serverMigrationFingerprint)
				values.Set("import_confirmation", "IMPORT")
			}
			page := httptestResponse(app, importWebRequest(http.MethodPost, "http://example/workspace/migration/import/"+route, values))
			body := page.Body.String()
			if page.Code != http.StatusBadRequest || !strings.Contains(body, client.plan.Error) || !migrationMarkupHasURL(body, "/", "migration", "import", "") || strings.Contains(body, "Continue review") {
				t.Fatalf("deterministic %s response = %d: %s", route, page.Code, body)
			}
			if client.applyCalls != 0 {
				t.Fatalf("deterministic replan reached Apply: %d", client.applyCalls)
			}
		})
	}
}

func TestMigrationWorkspaceProgressBrowserRedirectAndInternalFragment(t *testing.T) {
	client := &fakeServerMigrationAPI{operation: &api.PersistentOperation{
		OperationID: serverMigrationOperationID, OperationType: "migration_recovery", State: "needs_attention", Status: "Recovery needs review.",
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	address := "http://example/workspace/migration/progress/" + serverMigrationOperationID
	browser := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, address, ""))
	if browser.Code != http.StatusSeeOther || !migrationNavigationURLMatches(browser.Header().Get("Location"), "/", "migration", "", serverMigrationOperationID) {
		t.Fatalf("browser progress response = %d, location %q", browser.Code, browser.Header().Get("Location"))
	}
	fragmentRequest := authenticatedAdminRequest(http.MethodGet, address, "")
	fragmentRequest.Header.Set(migrationWorkspaceFragmentHeader, "1")
	fragment := httptestResponse(app, fragmentRequest)
	if fragment.Code != http.StatusOK || !strings.Contains(fragment.Body.String(), "data-migration-workspace-root") || !migrationMarkupHasURL(fragment.Body.String(), "/", "migration", "recovery", "") || !strings.Contains(fragment.Body.String(), "Review Migration Recovery") {
		t.Fatalf("internal progress response = %d: %s", fragment.Code, fragment.Body.String())
	}
	if !migrationMarkupHasURL(regexp.MustCompile(`<a\b[^>]*\bid="server-migration-back-link"[^>]*>Back</a>`).FindString(fragment.Body.String()), "/", "migration", "", "") || !strings.Contains(fragment.Body.String(), `hidden>Back</a>`) {
		t.Fatal("needs-attention progress exposes looping Server Migration navigation")
	}
}

func TestMigrationWorkspaceApplyRedirectRendersCanonicalProgress(t *testing.T) {
	operation := &api.PersistentOperation{
		OperationID: serverMigrationOperationID, OperationType: "migration_export", State: "queued", Status: "Export queued.",
	}
	client := &fakeServerMigrationExportAPI{
		fakeServerMigrationAPI: fakeServerMigrationAPI{exportDiscovery: exportDiscoveryFixture(), operation: operation},
		plan:                   exportPlanFixture("local", false),
		apply:                  api.AdminMigrationApplyResponse{OK: true, Operation: operation},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"csrf": {"csrf-token"}, "kind": {"local"}, "path": {"/srv/migrations"},
		"filename": {"justvoxel-migration-test.tar.gz"}, "plan_fingerprint": {serverMigrationFingerprint},
		"export_confirmation": {"EXPORT"},
	}
	request := exportWebRequest(http.MethodPost, "http://example/workspace/migration/export/apply", values)
	request.Header.Set(migrationWorkspaceFragmentHeader, "1")
	started := httptestResponse(app, request)
	progressURL := "/workspace/migration/progress/" + serverMigrationOperationID
	if started.Code != http.StatusSeeOther || !migrationNavigationURLMatches(started.Header().Get("Location"), progressURL, "", "", "") {
		t.Fatalf("Migration Apply response = %d, location %q: %s", started.Code, started.Header().Get("Location"), started.Body.String())
	}
	progressRequest := authenticatedAdminRequest(http.MethodGet, "http://example"+started.Header().Get("Location"), "")
	progressRequest.Header.Set(migrationWorkspaceFragmentHeader, "1")
	fragment := httptestResponse(app, progressRequest)
	if fragment.Code != http.StatusOK || !strings.Contains(fragment.Body.String(), "data-migration-workspace-root") || !strings.Contains(fragment.Body.String(), serverMigrationOperationID) {
		t.Fatalf("redirected progress fragment = %d: %s", fragment.Code, fragment.Body.String())
	}

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	render := strings.Index(js, "const renderMigrationMarkup =")
	if render < 0 {
		t.Fatal("Migration fragment renderer or submission handler is missing")
	}
	relativeSubmit := strings.Index(js[render:], "root.addEventListener(\"submit\"")
	if relativeSubmit < 0 {
		t.Fatal("Migration fragment renderer or submission handler is missing")
	}
	submit := render + relativeSubmit
	for _, want := range []string{
		`const renderedURL = new URL(responseURL, window.location.href);`,
		`renderedURL.origin === window.location.origin`,
		`renderedURL.pathname.match(/^\/workspace\/migration\/progress\/([A-Za-z0-9_-]+)$/)`,
		`if (progress) updateMigrationLocation("", progress[1]);`,
	} {
		if !strings.Contains(js[render:submit], want) {
			t.Fatalf("redirected progress URL contract missing %q", want)
		}
	}
	if !strings.Contains(js[submit:], `renderMigrationMarkup(responseMarkup, response.url || action.pathname);`) {
		t.Fatal("Migration submission does not render the final redirected response URL")
	}
}

func TestMigrationWorkspacePagesRedirectBrowserAndServeInternalFragments(t *testing.T) {
	base := fakeServerMigrationAPI{
		exportDiscovery: exportDiscoveryFixture(),
		importDiscovery: importDiscoveryFixture(true),
		recovery:        recoveryDiscoveryFixture(),
	}
	for _, tc := range []struct {
		path   string
		tab    string
		client API
	}{
		{path: "/workspace/migration", tab: "", client: &fakeServerMigrationAPI{exportDiscovery: base.exportDiscovery, importDiscovery: base.importDiscovery, recovery: base.recovery}},
		{path: "/workspace/migration/export", tab: "export", client: &fakeServerMigrationExportAPI{fakeServerMigrationAPI: base}},
		{path: "/workspace/migration/import", tab: "import", client: &fakeServerMigrationImportAPI{fakeServerMigrationAPI: base, storage: importStorageFixture()}},
		{path: "/workspace/migration/recovery", tab: "recovery", client: &fakeServerMigrationRecoveryAPI{fakeServerMigrationAPI: base}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			app, err := New(tc.client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			address := "http://example" + tc.path
			browser := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, address, ""))
			if browser.Code != http.StatusSeeOther || !migrationNavigationURLMatches(browser.Header().Get("Location"), "/", "migration", tc.tab, "") {
				t.Fatalf("browser response = %d, location %q", browser.Code, browser.Header().Get("Location"))
			}
			request := authenticatedAdminRequest(http.MethodGet, address, "")
			request.Header.Set(migrationWorkspaceFragmentHeader, "1")
			fragment := httptestResponse(app, request)
			if fragment.Code != http.StatusOK || !strings.Contains(fragment.Body.String(), "data-migration-workspace-root") {
				t.Fatalf("internal fragment response = %d: %s", fragment.Code, fragment.Body.String())
			}
		})
	}
}

func TestMigrationWorkspaceExplicitURLStateContract(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, want := range []string{
		`const migrationRequested = migrationDestination.get("workspace") === "migration"`,
		`const destination = new URL(globalThis.justVoxelSelectedURL("/?workspace=migration"), window.location.href);`,
		`if (operation) destination.searchParams.set("operation", operation);`,
		`else if (Object.hasOwn(tabURLs, tab)) destination.searchParams.set("tab", tab);`,
		`if (window.location.pathname + window.location.search !== nextURL) {`,
		`window.history.replaceState(window.history.state, "", nextURL);`,
		`updateMigrationLocation(tab, operation);`,
		`updateMigrationLocation(currentTab);`,
		`const destination = new URLSearchParams(window.location.search);`,
		`const requestedMigrationOperation = migrationRequested ? destination.get("operation") : "";`,
		`const requestedMigrationTab = migrationRequested ? destination.get("tab") : "";`,
		`loadMigration("/workspace/migration/progress/" + encodeURIComponent(requestedMigrationOperation))`,
		`currentTab = requestedMigrationTab;`,
		`if (!migrationRequested && !readWorkspaceWindowState("migration").open) return;`,
		`"X-JustVoxel-Migration-Fragment": "1"`,
		`destination.searchParams.get("workspace") === "migration"`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("Migration explicit URL contract missing %q", want)
		}
	}
	if strings.Contains(js, `const requestedMigrationOperation = migrationRequested ? migrationDestination.get("operation")`) {
		t.Fatal("Migration reopen still uses the initial operation query")
	}
	canonicalLink := strings.Index(js, `updateMigrationLocation(tab, operation);`)
	operationLoad := strings.Index(js, `if (operation) await loadMigration("/workspace/migration/progress/" + encodeURIComponent(operation));`)
	if canonicalLink < 0 || operationLoad < 0 || canonicalLink > operationLoad {
		t.Fatal("canonical Migration links must update history before loading the fragment")
	}
}

func TestMigrationWorkspaceImportAttentionActionsAndResolvedNavigation(t *testing.T) {
	backAnchor := regexp.MustCompile(`<a\b[^>]*\bid="server-migration-back-link"[^>]*>Back</a>`)
	hiddenAttribute := regexp.MustCompile(`\shidden(?:\s|>)`)
	for _, surface := range []string{"workspace"} {
		t.Run(surface, func(t *testing.T) {
			for _, state := range []string{"validating", "running", "verifying", "needs_attention", "resolved", "succeeded", "rolled_back"} {
				t.Run(state, func(t *testing.T) {
					client := &fakeServerMigrationAPI{operation: &api.PersistentOperation{
						OperationID: serverMigrationOperationID, OperationType: "migration_import", State: state,
					}}
					app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
					if err != nil {
						t.Fatal(err)
					}
					path := "/workspace/migration/progress/"
					request := authenticatedAdminRequest(http.MethodGet, "http://example"+path+serverMigrationOperationID, "")
					request.Header.Set(migrationWorkspaceFragmentHeader, "1")
					page := httptestResponse(app, request)
					body := page.Body.String()
					if page.Code != http.StatusOK || !strings.Contains(body, "Download Import log") || !strings.Contains(body, "/api/system/workspace/logs/operation/"+serverMigrationOperationID+"?download=1") {
						t.Fatalf("Import progress actions missing: %d %s", page.Code, body)
					}
					wantBack := state == "succeeded" || state == "rolled_back" || state == "resolved"
					backLink := backAnchor.FindString(body)
					if backLink == "" || !migrationMarkupHasURL(backLink, "/", "migration", "", "") || hiddenAttribute.MatchString(backLink) == wantBack {
						t.Fatalf("%s progress Back navigation incorrect: %s", state, body)
					}
					if strings.Contains(body, ">Server Migration</a>") {
						t.Fatalf("progress navigation still labelled Server Migration: %s", body)
					}
					if state == "succeeded" {
						dashboardLink := regexp.MustCompile(`<a\b[^>]*\bid="server-migration-dashboard-link"[^>]*>Open dashboard</a>`).FindString(body)
						if !migrationMarkupHasURL(dashboardLink, "/", "", "", "") || hiddenAttribute.MatchString(dashboardLink) {
							t.Fatalf("succeeded progress missing visible dashboard action: %s", body)
						}
					}
					if state == "needs_attention" {
						recoveryLink := regexp.MustCompile(`<a\b[^>]*\bid="server-migration-recovery-link"[^>]*>Review Migration Recovery</a>`).FindString(body)
						if !migrationMarkupHasURL(recoveryLink, "/", "migration", "recovery", "") || hiddenAttribute.MatchString(recoveryLink) {
							t.Fatalf("needs-attention progress actions loop or omit Recovery: %s", body)
						}
					}
				})
			}
		})
	}
}

func TestMigrationWorkspacePolledAttentionHidesLoopingNavigation(t *testing.T) {
	styles, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(styles), `[data-server-migration-operation]:has(#server-migration-operation-attention:not([hidden])) .migration-progress-navigation{display:none}`) {
		t.Fatal("polled needs-attention progress does not hide looping navigation")
	}
	markup, err := assets.ReadFile("templates/migration_workspace_progress.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `class="button-link secondary migration-progress-navigation" id="server-migration-back-link" href="/?workspace=migration&amp;server={{.ServerID}}"`) {
		t.Fatal("normal Migration navigation is not connected to the polled attention styling")
	}

	script, err := assets.ReadFile("static/server-migration-operation.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	if !strings.Contains(js, `const backLink = root.querySelector("#server-migration-back-link");`) {
		t.Fatal("migration operation renderer does not select Back navigation")
	}
	render := strings.Index(js, "const render = (operation) => {")
	poll := strings.Index(js, "const poll = async () => {")
	if render < 0 || poll <= render {
		t.Fatal("migration operation render/poll path is missing")
	}
	if !strings.Contains(js[:render], `const terminalState = (state) => ["succeeded", "rolled_back", "needs_attention", "resolved"].includes(state);`) || !strings.Contains(js[render:poll], `if (terminalState(operation.state)) finished = true;`) {
		t.Fatal("migration operation renderer does not stop polling for resolved state")
	}
	if !strings.Contains(js[:render], `if (state === "resolved") return "Resolved";`) {
		t.Fatal("migration operation renderer does not label resolved state as Resolved")
	}
	for _, want := range []string{
		`const succeeded = operation.state === "succeeded";`,
		`const rolledBack = operation.state === "rolled_back";`,
		`const resolved = operation.state === "resolved";`,
		`if (backLink) backLink.hidden = !(succeeded || rolledBack || resolved);`,
	} {
		if !strings.Contains(js[render:poll], want) {
			t.Fatalf("dynamic Migration Back navigation contract missing %q", want)
		}
	}
	if !strings.Contains(js[poll:], "render(payload.operation);") {
		t.Fatal("migration poll does not render the current operation")
	}
}

func TestStandaloneServerMigrationUIRetired(t *testing.T) {
	app, err := New(&fakeServerMigrationAPI{}, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		paths  []string
		status int
	}{
		{http.MethodGet, []string{
			"/settings/server-migration",
			"/settings/server-migration/export",
			"/settings/server-migration/import",
			"/settings/server-migration/recovery",
			"/settings/server-migration/progress/operation-id",
			"/api/server-migration/progress/operation-id",
		}, http.StatusNotFound},
		{http.MethodPost, []string{
			"/settings/server-migration/export/review",
			"/settings/server-migration/export/apply",
			"/settings/server-migration/import/review",
			"/settings/server-migration/import/apply",
			"/settings/server-migration/recovery/review",
			"/settings/server-migration/recovery/apply",
			"/settings/server-migration/recovery/resolve",
		}, http.StatusMethodNotAllowed},
	} {
		for _, path := range tc.paths {
			t.Run(tc.method+" "+path, func(t *testing.T) {
				response := httptestResponse(app, authenticatedAdminRequest(tc.method, "http://example"+path, ""))
				if response.Code != tc.status {
					t.Fatalf("status = %d, want %d", response.Code, tc.status)
				}
			})
		}
	}
	for _, name := range []string{
		"server_migration.html", "server_migration_export.html", "server_migration_export_review.html",
		"server_migration_import.html", "server_migration_import_review.html", "server_migration_progress.html",
		"server_migration_recovery.html", "server_migration_recovery_review.html",
	} {
		if _, err := assets.ReadFile("templates/" + name); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("retired template %s: expected absence, got %v", name, err)
		}
	}
	for _, tc := range []struct{ template, action string }{
		{"migration_workspace_export.html", "/workspace/migration/export/review"},
		{"migration_workspace_export_review.html", "/workspace/migration/export/apply"},
		{"migration_workspace_import.html", "/workspace/migration/import/review"},
		{"migration_workspace_import_review.html", "/workspace/migration/import/apply"},
		{"migration_workspace_recovery.html", "/workspace/migration/recovery/review"},
		{"migration_workspace_recovery_review.html", "/workspace/migration/recovery/apply"},
		{"migration_workspace_recovery.html", "/workspace/migration/recovery/resolve"},
		{"migration_workspace_progress.html", "/api/workspace/migration/progress/"},
	} {
		markup, err := assets.ReadFile("templates/" + tc.template)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(markup), tc.action) {
			t.Fatalf("%s missing current action %q", tc.template, tc.action)
		}
	}
}

func TestMigrationScriptsUseWorkspaceInitializationOnly(t *testing.T) {
	for _, kind := range []string{"export", "import", "operation"} {
		name := map[string]string{"export": "Export", "import": "Import", "operation": "Operation"}[kind]
		script, err := assets.ReadFile("static/server-migration-" + kind + ".js")
		if err != nil {
			t.Fatal(err)
		}
		js := string(script)
		for _, want := range []string{"window.JustVoxelServerMigration" + name, "initServerMigration" + name} {
			if !strings.Contains(js, want) {
				t.Fatalf("%s script missing %q", kind, want)
			}
		}
		for _, forbidden := range []string{
			"requestStandalone" + name + "SMBPassword", "workspaceManaged", "DOMContentLoaded", "initServerMigration" + name + "(document)",
		} {
			if strings.Contains(js, forbidden) {
				t.Fatalf("%s script retains %q", kind, forbidden)
			}
		}
		if kind == "operation" && !strings.Contains(js, `const terminalState = (state) => ["succeeded", "rolled_back", "needs_attention", "resolved"].includes(state);`) {
			t.Fatal("resolved must remain a terminal operation state")
		}
	}
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"Export", "Import", "Operation"} {
		for _, want := range []string{
			`await loadMigrationScript("/static/server-migration-` + strings.ToLower(kind) + `.js", () => Boolean(window.JustVoxelServerMigration` + kind + `?.init));`,
			`window.JustVoxelServerMigration` + kind + `?.init(root);`,
		} {
			if !strings.Contains(string(script), want) {
				t.Fatalf("app.js missing current initialization %q", want)
			}
		}
	}
}

func TestMigrationWorkspaceExportTerminalDoneAndLogLinks(t *testing.T) {
	doneAnchor := regexp.MustCompile(`<a\b[^>]*\bid="server-migration-dashboard-link"[^>]*>Done</a>`)
	hiddenAttribute := regexp.MustCompile(`\shidden(?:\s|>)`)
	for _, state := range []string{"running", "succeeded", "rolled_back", "failed", "needs_attention"} {
		t.Run(state, func(t *testing.T) {
			client := &fakeServerMigrationAPI{operation: &api.PersistentOperation{
				OperationID: serverMigrationOperationID, OperationType: "migration_export", State: state,
			}}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			request := authenticatedAdminRequest(http.MethodGet, "http://example/workspace/migration/progress/"+serverMigrationOperationID, "")
			request.Header.Set(migrationWorkspaceFragmentHeader, "1")
			page := httptestResponse(app, request)
			body := page.Body.String()
			if page.Code != http.StatusOK {
				t.Fatalf("progress = %d: %s", page.Code, body)
			}
			done := doneAnchor.FindString(body)
			if done == "" || !migrationMarkupHasURL(done, "/", "migration", "export", "") || hiddenAttribute.MatchString(done) == (state == "succeeded") {
				t.Fatalf("Export Done action incorrect for %s: %s", state, done)
			}
			if strings.Contains(body, ">Open dashboard</a>") {
				t.Fatal("Export still exposes stale dashboard navigation")
			}
			if !strings.Contains(body, `workspace=system&amp;tab=logs&amp;log_group=migration&amp;log_id=`+serverMigrationOperationID) || !strings.Contains(body, "Details available on System -&gt; Logs -&gt; Migration.") {
				t.Fatal("Export lacks its Migration log destination")
			}
		})
	}
}
