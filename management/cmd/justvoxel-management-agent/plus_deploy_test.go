package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func plusDeploymentFixture(t *testing.T) plusDeploymentPaths {
	t.Helper()
	base := t.TempDir()
	template, err := filepath.Abs("../../../templates/plus")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(template, "stack.env.in")); err != nil {
		t.Fatalf("Plus deployment tests require repository templates: %v", err)
	}
	p := plusDeploymentPaths{Configuration: filepath.Join(base, "configuration"), Preparation: filepath.Join(base, "preparation"), Templates: template, DefaultData: filepath.Join(base, "data"), Socket: filepath.Join(base, "socket"), CABundle: filepath.Join(base, "ca.crt"), UnitOverride: filepath.Join(base, "units")}
	if err := os.WriteFile(p.Socket, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CABundle, []byte("fixture host CA\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mockPlusOwnership(t *testing.T) {
	t.Helper()
	old := setPlusOwnership
	setPlusOwnership = func(string, int, int) error { return nil }
	t.Cleanup(func() { setPlusOwnership = old })
}

func TestPlusRuntimeSecretsTLSAndRetryPreserveAdministratorChoices(t *testing.T) {
	mockPlusOwnership(t)
	p := plusDeploymentFixture(t)
	r := validPlusSetupRequest()
	r.Username = "admin"
	r.UseTLS = true
	r.Certificate, r.PrivateKey = plusSetupCertificate(t, time.Now(), r.Host)
	if err := preparePlusRuntime(p, r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"database.env", "panel.env", "drydock.env", "stack.env", "wings"} {
		info, err := os.Stat(filepath.Join(p.Configuration, name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if name == "wings" {
			mode = 0700
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("unsafe mode on %s", name)
		}
	}
	drydock, _ := os.ReadFile(filepath.Join(p.Configuration, "drydock.env"))
	if !strings.Contains(string(drydock), "$argon2id$v=19$") || strings.Contains(string(drydock), r.Password) {
		t.Fatal("Drydock credentials not hashed")
	}
	persistent, _ := os.ReadFile(filepath.Join(p.DefaultData, "panel/var/.env"))
	if !strings.Contains(string(persistent), "APP_KEY=base64:") {
		t.Fatal("Panel APP_KEY not precreated")
	}
	nginx, _ := os.ReadFile(filepath.Join(p.DefaultData, "panel/nginx/panel.conf"))
	if !strings.Contains(string(nginx), "listen 443 ssl;") || !strings.Contains(string(nginx), r.Host+"/privkey.pem") {
		t.Fatal("TLS not configured")
	}
	env, _ := os.ReadFile(filepath.Join(p.Configuration, "stack.env"))
	if !strings.Contains(string(env), "PANEL_HTTP_BIND_ADDRESS=127.0.0.1") {
		t.Fatal("TLS setup exposed HTTP on all interfaces")
	}
	database, _ := os.ReadFile(filepath.Join(p.Configuration, "database.env"))
	chosen := []byte("administrator image selection\n")
	if err := os.WriteFile(filepath.Join(p.Configuration, "compose.yaml"), chosen, 0660); err != nil {
		t.Fatal(err)
	}
	if err := preparePlusRuntime(p, r); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(filepath.Join(p.Configuration, "database.env"))
	if string(unchanged) != string(database) {
		t.Fatal("retry changed database credentials")
	}
	compose, _ := os.ReadFile(filepath.Join(p.Configuration, "compose.yaml"))
	if string(compose) != string(chosen) {
		t.Fatal("retry overwrote administrator image selections")
	}
}

func TestPlusRuntimeRejectsSymlinksAndExistingData(t *testing.T) {
	mockPlusOwnership(t)
	p := plusDeploymentFixture(t)
	r := validPlusSetupRequest()
	if err := os.Mkdir(p.DefaultData, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.DefaultData, "existing"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := preparePlusRuntime(p, r); err == nil {
		t.Fatal("unmanaged existing data adopted")
	}
	path := filepath.Join(filepath.Dir(p.DefaultData), "linked")
	if err := os.Symlink(p.DefaultData, path); err != nil {
		t.Fatal(err)
	}
	if err := ensurePlusDirectory(filepath.Join(path, "child"), 0700, -1, -1); err == nil {
		t.Fatal("directory symlink followed")
	}
	link := filepath.Join(p.DefaultData, "link")
	if err := os.Symlink(filepath.Join(p.DefaultData, "existing"), link); err != nil {
		t.Fatal(err)
	}
	if err := writePlusFile(link, []byte("replace"), 0600, -1, -1); err == nil {
		t.Fatal("file symlink followed")
	}
}

func TestPlusDeploymentSelectionOrderCredentialsAndReadiness(t *testing.T) {
	mockPlusOwnership(t)
	oldCommand, oldHTTP, oldMounts := runPlusDeploymentCommand, checkPlusHTTP, readPlusSetupMounts
	t.Cleanup(func() {
		runPlusDeploymentCommand = oldCommand
		checkPlusHTTP = oldHTTP
		readPlusSetupMounts = oldMounts
	})
	readPlusSetupMounts = func(context.Context) ([]plusSetupMount, error) { return nil, nil }
	checkPlusHTTP = func(context.Context, plusSetupRequest, int, string) bool { return true }
	for _, components := range []plusSetupComponents{{Database: true, Cache: true, Panel: true, Wings: true, Drydock: true}, {Drydock: true}} {
		p := plusDeploymentFixture(t)
		r := validPlusSetupRequest()
		r.Username = "admin"
		r.Components = components
		if err := savePlusSetup(p.Preparation, r); err != nil {
			t.Fatal(err)
		}
		calls := []string{}
		bootstrapped := false
		runPlusDeploymentCommand = func(_ context.Context, command string, args []string, input []byte) ([]byte, error) {
			call := command + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if strings.Contains(call, r.Password) {
				t.Fatal("password in command arguments")
			}
			if strings.Contains(call, "network inspect") {
				return []byte(`[{"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.20.0.0/16","Gateway":"172.20.0.1"}]}}]`), nil
			}
			if strings.Contains(call, "exec -T --user 0 panel php") {
				var payload map[string]any
				if err := json.Unmarshal(input, &payload); err != nil {
					t.Fatal(err)
				}
				if payload["action"] == "verify" {
					if !bootstrapped {
						t.Fatal("verified before bootstrap")
					}
					return []byte(`{"ok":true}`), nil
				}
				if payload["password"] != r.Password {
					t.Fatal("password not passed through stdin")
				}
				bootstrapped = true
				return []byte(`{"ok":true,"node_id":1,"configuration":{"uuid":"fixture-node","token":"fixture-token","token_id":"fixture-id","api":{"ssl":{"enabled":false}},"system":{"data":"fixture"},"remote":"http://panel.example.com:8081"}}`), nil
			}
			if strings.Contains(call, "ps --all --quiet") {
				return []byte(strings.Repeat("a", 64)), nil
			}
			if command == "docker" && len(args) > 0 && args[0] == "inspect" {
				return []byte("running healthy"), nil
			}
			return nil, nil
		}
		if err := runPlusDeployment(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		status, err := readPlusDeploymentStatus(p)
		if err != nil || status.State != "succeeded" {
			t.Fatal("deployment not complete")
		}
		if _, err := os.Stat(filepath.Join(p.Preparation, "setup.json")); !os.IsNotExist(err) {
			t.Fatal("temporary credentials retained")
		}
		combined := strings.Join(calls, "\n")
		if components.Panel {
			if strings.Index(combined, "up --detach database") > strings.Index(combined, "up --detach panel") {
				t.Fatal("Panel started before database")
			}
			if strings.Index(combined, "exec -T --user 0 panel php") > strings.Index(combined, "up --detach wings") {
				t.Fatal("Wings started before node credentials")
			}
			wings, _ := os.ReadFile(filepath.Join(p.Configuration, "wings/config.yml"))
			if !strings.Contains(string(wings), "justvoxel-plus-games") || !strings.Contains(string(wings), p.DefaultData+"/wings") {
				t.Fatal("Wings persistence or network missing")
			}
		} else {
			for _, service := range []string{"panel", "database", "cache", "wings"} {
				if strings.Contains(combined, "up --detach "+service) {
					t.Fatalf("disabled %s started", service)
				}
			}
		}
		public, _ := os.ReadFile(filepath.Join(p.Configuration, "deployed"))
		if strings.Contains(string(public), r.Password) || strings.Contains(string(public), "fixture-token") {
			t.Fatal("credentials in public state")
		}
	}
}

func TestPlusDeploymentFailureRetainsDataWithoutEnableOrRetry(t *testing.T) {
	mockPlusOwnership(t)
	p := plusDeploymentFixture(t)
	r := validPlusSetupRequest()
	r.Username = "admin"
	if err := savePlusSetup(p.Preparation, r); err != nil {
		t.Fatal(err)
	}
	oldCommand, oldMounts := runPlusDeploymentCommand, readPlusSetupMounts
	t.Cleanup(func() { runPlusDeploymentCommand = oldCommand; readPlusSetupMounts = oldMounts })
	readPlusSetupMounts = func(context.Context) ([]plusSetupMount, error) { return nil, nil }
	pulls := 0
	runPlusDeploymentCommand = func(_ context.Context, command string, args []string, _ []byte) ([]byte, error) {
		call := command + " " + strings.Join(args, " ")
		if strings.Contains(call, "enable") {
			t.Fatal("failed deployment enabled startup")
		}
		if len(args) > 0 && args[len(args)-1] == "pull" {
			pulls++
			return []byte("sensitive diagnostic"), errors.New("private registry details")
		}
		return nil, nil
	}
	if err := runPlusDeployment(context.Background(), p); err == nil || strings.Contains(err.Error(), "private registry") {
		t.Fatal("pull failure hidden or diagnostic leaked")
	}
	if pulls != 1 {
		t.Fatalf("expected exactly one image pull attempt, got %d", pulls)
	}
	if _, err := os.Stat(filepath.Join(p.Configuration, "deployed")); !os.IsNotExist(err) {
		t.Fatal("failed deployment marked configured")
	}
	if _, err := os.Stat(filepath.Join(p.Preparation, "setup.json")); err != nil {
		t.Fatal("failed deployment lost retry credentials")
	}
	status, err := readPlusDeploymentStatus(p)
	if err != nil || status.State != "failed" || status.Stage != "pull" {
		t.Fatalf("failure not reported: %+v %v", status, err)
	}
}

func TestPlusDeploymentLockPublicURLsAndRoles(t *testing.T) {
	p := plusDeploymentFixture(t)
	lock, err := plusPreparationLock(p.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := plusPreparationLock(p.Preparation); err == nil {
		other.Close()
		t.Fatal("simultaneous setup allowed")
	}
	lock.Close()
	if err := ensurePlusDirectory(p.Configuration, 0700, -1, -1); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"javascript:alert(1)", "https://user:password@example.com", "https://example.com/?token=secret"} {
		data, _ := json.Marshal(plusDeploymentStatus{State: "succeeded", PanelURL: address})
		if err := writePlusFile(filepath.Join(p.Configuration, "deployed"), data, 0644, -1, -1); err != nil {
			t.Fatal(err)
		}
		if _, err := plusPublicApplications(p); err == nil {
			t.Fatal("unsafe launcher URL accepted")
		}
	}
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		s.plus = true
		rr := httptest.NewRecorder()
		s.plusSetupDeploy(rr, surfaceRequest(http.MethodPost, "/v1/admin/plus/setup/deploy", `{}`))
		if rr.Code != http.StatusForbidden {
			t.Fatal("non-admin deployed stack")
		}
	}
}

func TestPlusStatusUsesCompletedDeploymentAndDoesNotExposeSecrets(t *testing.T) {
	p := plusDeploymentFixture(t)
	if err := ensurePlusDirectory(p.Configuration, 0700, -1, -1); err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(plusDeploymentStatus{State: "succeeded", PanelURL: "https://panel.example.com:8443", DrydockURL: "https://panel.example.com:3000"})
	if err := writePlusFile(filepath.Join(p.Configuration, "deployed"), public, 0644, -1, -1); err != nil {
		t.Fatal(err)
	}
	data, err := plusStatusWithApplications([]byte(`{"plus":{"configured":false,"docker":"Running"},"system":{"health":"Healthy"}}`), p)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Plus struct {
			Configured bool
			Docker     string
			PanelURL   string `json:"panel_url"`
		}
	}
	if err := json.Unmarshal(data, &result); err != nil || !result.Plus.Configured || result.Plus.Docker != "Running" || result.Plus.PanelURL == "" {
		t.Fatal("Plus status did not adopt deployment addresses")
	}
}

func TestPlusHTTPReadinessVerifiesSuppliedTLSCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	address, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(address.Port())
	request := plusSetupRequest{UseTLS: true, Host: "example.com", Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))}
	if !plusHTTPReady(context.Background(), request, port, "/health") {
		t.Fatal("supplied trusted certificate rejected")
	}
	request.Host = "wrong.example.net"
	if plusHTTPReady(context.Background(), request, port, "/health") {
		t.Fatal("wrong hostname accepted")
	}
	request.Host = "example.com"
	request.Certificate = ""
	if plusHTTPReady(context.Background(), request, port, "/health") {
		t.Fatal("untrusted certificate accepted")
	}
}
