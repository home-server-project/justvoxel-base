package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeCurseForgeAPI struct {
	fakeDiscoveryAPI
	configured  bool
	calls       int
	method, key string
}

func (f *fakeCurseForgeAPI) AdminCurseForgeAPI(_ context.Context, session, method, key string) (api.CurseForgeAPIStatus, error) {
	if session != "session-token" {
		return api.CurseForgeAPIStatus{}, api.ErrUnauthorized
	}
	f.calls++
	f.method = method
	f.key = key
	if method == "PUT" {
		f.configured = true
	}
	if method == "DELETE" {
		f.configured = false
	}
	return api.CurseForgeAPIStatus{Configured: f.configured}, nil
}
func TestCurseForgeWorkspaceAuthorizationAndCSRF(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "administrator"} {
		client := &fakeCurseForgeAPI{}
		client.role = role
		app, err := New(client, Config{})
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			body := ""
			if method != "GET" {
				body = "csrf=csrf-token"
			}
			if method == "PUT" {
				body += "&key=test-private-key"
			}
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(method, "http://example/api/plugins/workspace/curseforge-api", body))
			want := 403
			if role == "administrator" {
				want = 200
			}
			if rr.Code != want {
				t.Fatalf("%s %s: %d %s", role, method, rr.Code, rr.Body.String())
			}
			if role == "administrator" {
				var out map[string]any
				if json.Unmarshal(rr.Body.Bytes(), &out) != nil || len(out) != 1 || out["configured"] != (method == "PUT") {
					t.Fatal("presence-only response", rr.Body.String())
				}
				if method == "PUT" && (client.key != "test-private-key" || client.method != method) {
					t.Fatal("key not forwarded")
				}
			}
			if strings.Contains(rr.Body.String(), "test-private-key") {
				t.Fatal("secret echoed")
			}
		}
		if role != "administrator" && client.calls != 0 {
			t.Fatal("unauthorized request forwarded")
		}
	}
	client := &fakeCurseForgeAPI{}
	app, err := New(client, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		for _, body := range []string{"", "csrf=wrong", "key=test-key"} {
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, authenticatedAdminRequest(method, "http://example/api/plugins/workspace/curseforge-api", body))
			if rr.Code != 403 {
				t.Fatal("write without CSRF", rr.Code)
			}
		}
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, httptest.NewRequest(method, "http://example/api/plugins/workspace/curseforge-api", nil))
		if rr.Code != 401 {
			t.Fatal("unauthenticated access", rr.Code)
		}
	}
	for _, tc := range []struct{ method, query, body string }{
		{"GET", "?key=never-allowed", ""}, {"PUT", "", "csrf=csrf-token&key=a&key=b"}, {"DELETE", "", "csrf=csrf-token&key=not-allowed"}, {"PUT", "", "csrf=csrf-token&extra=not-allowed"}, {"PUT", "", "csrf=csrf-token&key=" + url.QueryEscape(strings.Repeat("x", 8193))},
	} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(tc.method, "http://example/api/plugins/workspace/curseforge-api"+tc.query, tc.body))
		if rr.Code != 400 {
			t.Fatal("invalid form accepted", rr.Code)
		}
	}
	if client.calls != 0 {
		t.Fatal("invalid request forwarded")
	}
}
func TestCurseForgeWorkspaceMarkupAndSecretBoundary(t *testing.T) {
	header, _ := assets.ReadFile("templates/header.html")
	script, _ := assets.ReadFile("static/app.js")
	markup, js := string(header), string(script)
	last := -1
	for _, tab := range []string{"discover", "installed", "addons", "curseforge-api"} {
		index := strings.Index(markup, `data-plugins-tab="`+tab+`"`)
		if index <= last {
			t.Fatal("tab order", tab)
		}
		last = index
	}
	for _, want := range []string{"data-plugins-curseforge", "data-plugins-curseforge-section", `data-curseforge-notice-dismiss aria-label="Dismiss CurseForge notice">×</button>`, "CurseForge API", "Apply for CurseForge API key", `rel="noopener noreferrer"`, "••••••••••••••••"} {
		if !strings.Contains(markup, want) {
			t.Fatal("missing markup", want)
		}
	}
	start := strings.Index(js, "  const curseForgeSwitch =")
	end := strings.Index(js[start:], "  const returnToInstalled =") + start
	section := js[start:end]
	for _, want := range []string{`input.type = "password"`, `input.autocomplete = "new-password"`, `input.value = ""`, `showPanel("CurseForge API", true)`, `text("button", "Save")`, `panelAction("Cancel", closePanel, true)`} {
		if !strings.Contains(section, want) {
			t.Fatal("dialog contract", want)
		}
	}
	for _, unsafe := range []string{"localStorage", "sessionStorage", "console.", "alert(", "input.value = status", "?key=", "payload.key"} {
		if strings.Contains(section, unsafe) {
			t.Fatal("secret boundary", unsafe)
		}
	}
	query := js[strings.Index(js, "  const discoverQuery ="):]
	query = strings.SplitN(query, "\n", 2)[0]
	if strings.Contains(strings.ToLower(query), "curseforge") {
		t.Fatal("unsupported provider in catalog query")
	}
}
func TestCurseForgeSwitchNoticeLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	script, _ := assets.ReadFile("static/app.js")
	js := string(script)
	start := strings.Index(js, "  const curseForgeSwitch =")
	end := strings.Index(js[start:], "  const returnToInstalled =") + start
	harness := `
 const assert = require("node:assert/strict");
 const nodes = new Map(); let timerID=0, cleared=[], timers=new Map(), requests=[];
 const setTimeout=(fn,ms)=>{const id=++timerID;timers.set(id,{fn,ms});return id;};
 const clearTimeout=id=>{cleared.push(id);timers.delete(id);};
 const make=()=>({hidden:true,checked:false,disabled:false,listeners:{},addEventListener(type,fn){this.listeners[type]=fn;},setAttribute(){},append(){},dataset:{},style:{},getBoundingClientRect(){return {left:100,right:600,width:250};},parentElement:{getBoundingClientRect(){return {left:100};}},classList:{add(){}},click(){this.listeners.click?.();}});
 const get=key=>{if(!nodes.has(key))nodes.set(key,make());return nodes.get(key);};
 const pluginsDialog={getBoundingClientRect(){return {left:0,right:800};},querySelector:get,addEventListener(type,fn){this[type]=fn;}};
 const document={querySelector(){return {value:"csrf-token"};},createElement:make};
 const panel=make(),panelBody=make();
 const text=()=>make(),panelAction=(label,action)=>{const n=make();n.label=label;n.listeners.click=action;return n;};
 let opened=0; const showPanel=()=>{opened++;},closePanel=()=>{},overlayActions=()=>{};
 let tab="discover";
 const fetch=async (url,options)=>{requests.push({url,options});return {ok:true,status:200,json:async()=>({configured:true})};};
 const queueMicrotask=()=>{};
 ` + js[start:end] + `
 applyCurseForgeStatus({configured:false});
 curseForgeSwitch.checked=true;curseForgeSwitch.listeners.change();
 assert.equal(curseForgeSwitch.checked,false);assert.equal(curseForgeNotice.hidden,false);assert.equal(requests.length,0);
 const first=curseForgeNoticeTimer;assert.equal(timers.get(first).ms,15000);
 curseForgeSwitch.checked=true;curseForgeSwitch.listeners.change();
 assert.ok(cleared.includes(first));assert.notEqual(curseForgeNoticeTimer,first);assert.equal(timers.size,1);
 pluginsDialog.querySelector("[data-curseforge-notice-dismiss]").click();assert.equal(curseForgeNotice.hidden,true);assert.equal(timers.size,0);
 curseForgeSwitch.checked=true;curseForgeSwitch.listeners.change();timers.get(curseForgeNoticeTimer).fn();assert.equal(curseForgeNotice.hidden,true);
 curseForgeSwitch.checked=true;curseForgeSwitch.listeners.change();let prevented=false;
 pluginsDialog.keydown({key:"Escape",preventDefault(){prevented=true;},stopPropagation(){}});assert.equal(prevented,true);assert.equal(curseForgeNotice.hidden,true);
 get('[data-plugins-tab="curseforge-api"]').listeners.click=()=>{tab="curseforge-api";};
 curseForgeSwitch.checked=true;curseForgeSwitch.listeners.change();get("[data-curseforge-notice-configure]").click();
 assert.equal(tab,"curseforge-api");assert.equal(opened,1);assert.equal(curseForgeNotice.hidden,true);
 tab="discover";applyCurseForgeStatus({configured:true});curseForgeSwitch.checked=true;curseForgeSwitch.listeners.change();
 assert.equal(curseForgeSwitch.checked,true);assert.equal(curseForgeNotice.hidden,true);assert.equal(curseForgeInfo.hidden,false);assert.equal(requests.length,0);
 applyCurseForgeStatus({configured:false});assert.equal(curseForgeSwitch.checked,false);assert.equal(curseForgeInfo.hidden,true);
 curseForgeSwitch.checked=true;curseForgeSwitch.listeners.change();assert.equal(curseForgeNotice.hidden,false);
 `
	if out, err := exec.Command(node, "-e", harness).CombinedOutput(); err != nil {
		t.Fatalf("switch lifecycle: %v\n%s", err, out)
	}
}
