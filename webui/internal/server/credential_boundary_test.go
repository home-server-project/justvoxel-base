package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasswordCredentialBoundary(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "forced"}[forced], func(t *testing.T) {
			app, err := New(newAuthFlowAPI(), Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://example/password", nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
			req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
			if forced {
				req.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "1"})
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("GET /password returned %d: %s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			for _, want := range []string{
				`action="/password"`, `name="csrf" value="csrf-token"`,
				`name="current_password"`, `name="new_password"`, `name="confirm_password"`,
				`minlength="8"`, "Password requirements:", "local console", "SSH password login",
				`/static/password.js`,
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("password boundary missing %q", want)
				}
			}
			for _, forbidden := range []string{`app-header`, `/static/app.js`, `Control Center`, `data-workspace-window`} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("password boundary includes %q", forbidden)
				}
			}
		})
	}
}

func TestSeparateWebUIPasswordCredentialCopy(t *testing.T) {
	fake := newAuthFlowAPI()
	fake.authStatus.Mode = "separate"
	app, err := New(fake, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/password", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Separate WebUI password") ||
		!strings.Contains(rr.Body.String(), "Linux/SSH password is unchanged") {
		t.Fatalf("separate password explanation missing: %d %s", rr.Code, rr.Body.String())
	}
}

func TestFactoryResetCompletionBoundaryMarker(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	without := httptest.NewRecorder()
	app.Handler().ServeHTTP(without, httptest.NewRequest(http.MethodGet, "http://example/factory-reset-complete", nil))
	if without.Code != http.StatusSeeOther || without.Header().Get("Location") != "/login" {
		t.Fatalf("unmarked completion page returned %d %q", without.Code, without.Header().Get("Location"))
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/factory-reset-complete", nil)
	req.AddCookie(&http.Cookie{Name: factoryResetCompleteCookie, Value: "1"})
	marked := httptest.NewRecorder()
	app.Handler().ServeHTTP(marked, req)
	if marked.Code != http.StatusOK {
		t.Fatalf("marked completion page returned %d", marked.Code)
	}
	body := marked.Body.String()
	for _, want := range []string{"JustVoxel has been fully reset", "Username", "voxel", "Password", "Continue to sign in", "immediately require a new administrator password"} {
		if !strings.Contains(body, want) {
			t.Fatalf("completion boundary missing %q", want)
		}
	}
	for _, forbidden := range []string{`app-header`, `/static/app.js`, `Control Center`, `data-workspace-window`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("completion boundary includes %q", forbidden)
		}
	}
	if strings.Count(body, `href="/login"`) != 1 {
		t.Fatal("completion page must have one continuation")
	}
}

func TestNormalLoginClearsFactoryResetCompletionMarker(t *testing.T) {
	app, err := New(newAuthFlowAPI(), Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/login", nil)
	req.AddCookie(&http.Cookie{Name: factoryResetCompleteCookie, Value: "1"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login returned %d", rr.Code)
	}
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == factoryResetCompleteCookie && cookie.Path == "/factory-reset-complete" && cookie.MaxAge < 0 {
			return
		}
	}
	t.Fatal("normal login did not clear completion marker")
}

func TestFactoryResetCompletionAppContract(t *testing.T) {
	source, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(source)
	for _, want := range []string{
		`if (operation.operation_type === "factory_reset" && operation.state === "succeeded") {`,
		`showFactoryResetComplete();`,
		`factoryResetPollOperationID === activeFactoryResetOperationID`,
		`url === "/api/system/workspace/reset/operations/" + factoryResetPollOperationID`,
		`window.location.replace("/factory-reset-complete")`,
		`window.location.assign("/login")`,
		`!["needs_attention", "resolved", "rolled_back"].includes(operation.state)`,
		`operation.operation_type === "minecraft_reset" && operation.state === "succeeded"`,
		`window.dispatchEvent(new Event("justvoxel:minecraft-reset-complete"))`,
	} {
		if !strings.Contains(app, want) {
			t.Fatalf("reset completion contract missing %q", want)
		}
	}
	if strings.Index(app, `if (operation.operation_type === "factory_reset" && operation.state === "succeeded") {`) >
		strings.Index(app, `if (operation.operation_type === "minecraft_reset" && operation.state === "succeeded") {`) {
		t.Fatal("factory success handoff must precede Minecraft success handoff")
	}
	fetchStart := strings.Index(app, "const systemFetchJSON = async")
	if fetchStart < 0 {
		t.Fatal("system fetch boundary missing")
	}
	fetchEnd := strings.Index(app[fetchStart:], "const systemNotice =")
	if fetchEnd < 0 {
		t.Fatal("system fetch boundary missing")
	}
	fetch := app[fetchStart : fetchStart+fetchEnd]
	completion := strings.Index(fetch, "showFactoryResetComplete();")
	login := strings.Index(fetch, `window.location.assign("/login")`)
	if completion < 0 || login < 0 || completion > login ||
		!strings.Contains(fetch, `if (factoryResetPollOperationID && factoryResetPollOperationID === activeFactoryResetOperationID`) {
		t.Fatal("only the known Factory Reset poll may turn 401 into completion")
	}
}

func TestPasswordScriptStaysCredentialOnly(t *testing.T) {
	source, err := assets.ReadFile("static/password.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	for _, want := range []string{`input[type="password"]`, "password-reveal-button", "password-strength", `input.name === "new_password"`} {
		if !strings.Contains(script, want) {
			t.Fatalf("password script missing %q", want)
		}
	}
	for _, forbidden := range []string{"app.js", "fetch(", "setInterval(", "setTimeout(", "localStorage", "sessionStorage"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("password script includes %q", forbidden)
		}
	}
}
