package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
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
	for _, want := range []string{`input[type="password"]:not([data-password-ui="off"])`, "password-reveal-button", "password-strength", `input.name === "new_password"`} {
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

func TestPasswordScriptOptOutAndRealPasswords(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := assets.ReadFile("static/password.js")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise both enhancers with real password-field markup, preserving the
	// standalone CurseForge opt-out and dashboard shared enhancement.
	harness := `
const assert = require("node:assert/strict");
const shared = process.argv[2] === "shared";
const make = () => ({children: [], listeners: {}, attributes: {},
 appendChild(child) {this.children.push(child);},
 insertBefore(child) {this.children.push(child);},
 after(child) {this.afterNode = child;},
 setAttribute(key, value) {this.attributes[key] = value;},
 addEventListener(type, fn) {this.listeners[type] = fn;},
 classList: {
  states: new Set(),
  toggle(name, enabled) {if (enabled) this.states.add(name); else this.states.delete(name);},
  contains(name) {return this.states.has(name);}
 }});
const inputs = JSON.parse(process.argv[1]).map(attributes => Object.assign(make(), {
 type: "password", name: attributes.name || "", minLength: 8, value: "", attributes,
 dataset: {}, autocomplete: attributes.name === "new_password" ? "new-password" : "",
 parentNode: make()
}));
const document = {
 createElement: make,
 querySelectorAll(selector) {
  assert.equal(selector, shared
   ? 'input[type="password"]:not([data-password-reveal-ready])'
   : 'input[type="password"]:not([data-password-ui="off"])');
  return inputs.filter(input => shared
   ? input.type === "password" && !input.dataset.passwordRevealReady
   : input.attributes["data-password-ui"] !== "off");
 }
};
` + "\n__PASSWORD_ENHANCER__\n" + `
for (const input of inputs) {
 if (!shared && input.attributes["data-password-ui"] === "off") {
  assert.equal(input.parentNode.children.length, 0);
  assert.equal(input.afterNode, undefined);
  assert.deepEqual(input.listeners, {});
  assert.equal(input.type, "password");
  continue;
 }
 const shell = input.parentNode.children[0];
 assert.equal(shell.className, "password-field-shell");
 const button = shell.children[1];
 assert.equal(button.className, "password-reveal-button");
 assert.equal(button.type, "button");
 assert.equal(input.type, "password");
 assert.equal(button.attributes["aria-label"], "Show password");
 assert.equal(button.attributes.title, undefined);
 assert.equal(button.classList.contains("is-showing"), false);
 assert.ok(button.innerHTML.includes('<circle cx="12" cy="12" r="2.8"></circle>'));
 assert.ok(button.innerHTML.includes('<path class="password-reveal-slash" d="M4 4l16 16"></path>'));
 button.listeners.click(); assert.equal(input.type, "text");
 assert.equal(button.classList.contains("is-showing"), true);
 assert.equal(button.attributes["aria-label"], "Hide password");
 assert.equal(button.attributes.title, undefined);
 button.listeners.click(); assert.equal(input.type, "password");
 assert.equal(button.classList.contains("is-showing"), false);
 assert.equal(button.attributes["aria-label"], "Show password");
 assert.equal(button.attributes.title, undefined);
 if (shared) {
  enhancePasswordFields(document);
  assert.equal(input.parentNode.children.length, 1);
  assert.equal(shell.children.length, 2);
 }
 if (input.name === "new_password") {
  assert.equal(shell.afterNode.textContent, "Strength: Not acceptable");
  input.value = "SyntheticPassword42!"; input.listeners.input();
  assert.equal(shell.afterNode.textContent, "Strength: Strong");
 } else {
  assert.equal(shell.afterNode, undefined);
  assert.equal(input.listeners.input, undefined);
 }
}
`
	var fields []map[string]string
	for _, template := range []string{"templates/login.html", "templates/password.html", "templates/header.html"} {
		markup, err := assets.ReadFile(template)
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range strings.Split(string(markup), "<input ")[1:] {
			field := strings.SplitN(part, ">", 2)[0]
			if !strings.Contains(field, `type="password"`) {
				continue
			}
			attributes := map[string]string{}
			for _, name := range []string{"name", "data-password-ui"} {
				if _, value, found := strings.Cut(field, name+`="`); found {
					attributes[name] = strings.SplitN(value, `"`, 2)[0]
				}
			}
			if strings.Contains(field, "data-curseforge-key") && attributes["data-password-ui"] != "off" {
				t.Fatal("CurseForge input did not opt out")
			}
			if attributes["name"] != "" && attributes["data-password-ui"] == "off" {
				t.Fatal("real password field opted out")
			}
			fields = append(fields, attributes)
		}
	}
	if len(fields) != 5 {
		t.Fatalf("expected login, three change-password fields, and CurseForge field; got %d", len(fields))
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	app, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	_, enhancer, found := strings.Cut(string(app), "const enhancePasswordFields = ")
	if !found {
		t.Fatal("shared password enhancer missing")
	}
	enhancer, _, found = strings.Cut(enhancer, "\n};")
	if !found {
		t.Fatal("shared password enhancer boundary missing")
	}
	shared := "const enhancePasswordFields = " + enhancer + "\n};\nenhancePasswordFields(document);"
	standaloneIcon := strings.SplitN(strings.SplitN(string(source), "button.innerHTML = ", 2)[1], ";", 2)[0]
	sharedIcon := strings.SplitN(strings.SplitN(shared, "button.innerHTML = ", 2)[1], ";", 2)[0]
	if standaloneIcon != sharedIcon {
		t.Fatal("password enhancers must use the same icon")
	}
	for name, script := range map[string]string{"standalone": string(source), "shared": shared} {
		t.Run(name, func(t *testing.T) {
			program := strings.Replace(harness, "__PASSWORD_ENHANCER__", script, 1)
			if out, err := exec.Command(node, "-e", program, string(encoded), name).CombinedOutput(); err != nil {
				t.Fatalf("password enhancement: %v\n%s", err, out)
			}
		})
	}
}

func TestPasswordRevealStyleContract(t *testing.T) {
	source, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(source)
	for _, want := range []string{
		"top:50%;", "transform:translateY(-50%);", "border:1px solid transparent;border-radius:50%;background:transparent;",
		".password-reveal-slash{display:none}",
		".password-reveal-button.is-showing .password-reveal-slash{display:block}",
		".password-reveal-button:focus-visible{outline:2px solid #78c99d;outline-offset:0}",
		".password-reveal-button svg{width:19px;height:19px;fill:none;stroke:currentColor;stroke-width:1.8}",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("password reveal styling missing %q", want)
		}
	}
	for _, rule := range strings.Split(css, "}") {
		selector, declarations, found := strings.Cut(rule, "{")
		if !found || !strings.Contains(selector, ".password-reveal-button") {
			continue
		}
		if strings.Contains(declarations, "translateY(-42%)") {
			t.Fatal("password reveal retains the old vertical offset")
		}
		if strings.Contains(selector, ":hover") || strings.Contains(selector, ":focus-visible") || strings.Contains(selector, ".is-showing") {
			if strings.Contains(declarations, "background:") || strings.Contains(declarations, "border-color:") {
				t.Fatal("password reveal state adds a background or border box")
			}
		}
	}
}
