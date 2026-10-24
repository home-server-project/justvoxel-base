package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fileBrowserHealthTransport func(*http.Request) (*http.Response, error)

func (transport fileBrowserHealthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestFileBrowserHealthUsesBaseURLAndRejectsRedirects(t *testing.T) {
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	for _, status := range []int{http.StatusOK, http.StatusMovedPermanently, http.StatusNoContent, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var paths []string
			http.DefaultTransport = fileBrowserHealthTransport(func(req *http.Request) (*http.Response, error) {
				paths = append(paths, req.URL.RequestURI())
				code := status
				// Quantum redirects the old root probe instead of reporting health.
				if req.URL.Path == "/health" {
					code = http.StatusMovedPermanently
				}
				if req.URL.String() != "http://127.0.0.1:61209/files/health" || req.Method != http.MethodGet {
					t.Errorf("unexpected health request: %s %s", req.Method, req.URL)
				}
				return &http.Response{
					StatusCode: code,
					Header:     http.Header{"Location": []string{"/files/"}},
					Body:       io.NopCloser(strings.NewReader(`{"message":"ok"}`)),
					Request:    req,
				}, nil
			})
			if ready := fileBrowserHealth(context.Background()); ready != (status == http.StatusOK) {
				t.Fatalf("health for HTTP %d = %v", status, ready)
			}
			if !reflect.DeepEqual(paths, []string{"/files/health"}) {
				t.Fatalf("health must directly probe /files/health without following redirects: %v", paths)
			}
		})
	}
}

func TestFileBrowserAvailabilityAndLifecycle(t *testing.T) {
	s, store := systemMonitorProfileTestServer(t, roleAdministrator)
	got, err := store.readFileBrowserAvailability()
	if err != nil || got != "on-demand" {
		t.Fatalf("fresh availability = %q, %v", got, err)
	}
	old := fileBrowserCommand
	t.Cleanup(func() { fileBrowserCommand = old })
	var calls []string
	fileBrowserCommand = func(_ context.Context, cmd string, args ...string) ([]byte, error) {
		calls = append(calls, cmd+" "+strings.Join(args, " "))
		return nil, nil
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.setFileBrowserAvailability(ctx, "always"); err != nil {
			t.Fatal(err)
		}
		if err := s.stopFileBrowser(ctx); err != nil {
			t.Fatal(err)
		}
	}
	expected := []string{"systemctl enable " + fileBrowserUnit, "systemctl --no-block start " + fileBrowserUnit, "systemctl enable " + fileBrowserUnit, "systemctl --no-block start " + fileBrowserUnit}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("always lifecycle = %v", calls)
	}
	got, err = store.readFileBrowserAvailability()
	if err != nil || got != "always" {
		t.Fatalf("persisted availability = %q, %v", got, err)
	}
	calls = nil
	if err := s.setFileBrowserAvailability(ctx, "on-demand"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "systemctl disable "+fileBrowserUnit {
		t.Fatalf("mode change killed open workspace: %v", calls)
	}
	if err := startFileBrowser(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.stopFileBrowser(ctx); err != nil {
		t.Fatal(err)
	}
	if calls[2] != "systemctl stop "+fileBrowserUnit {
		t.Fatalf("on-demand close = %v", calls)
	}
	if err := s.setFileBrowserAvailability(ctx, "both"); err == nil {
		t.Fatal("invalid enum accepted")
	}
	if _, err := store.db.Exec(`UPDATE filebrowser_settings SET availability = '' WHERE id = 1`); err == nil {
		t.Fatal("database allowed neither mode")
	}
}

func TestFileBrowserModeSaveFailureCanBeRetried(t *testing.T) {
	s, store := systemMonitorProfileTestServer(t, roleAdministrator)
	old := fileBrowserCommand
	t.Cleanup(func() { fileBrowserCommand = old })
	fileBrowserCommand = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("systemd unavailable")
	}
	if err := s.setFileBrowserAvailability(context.Background(), "always"); err == nil {
		t.Fatal("failure hidden")
	}
	if got, err := store.readFileBrowserAvailability(); err != nil || got != "always" {
		t.Fatalf("desired mode lost: %q, %v", got, err)
	}
	calls := 0
	fileBrowserCommand = func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil }
	if err := s.setFileBrowserAvailability(context.Background(), "always"); err != nil || calls != 2 {
		t.Fatalf("retry failed: %v, %d", err, calls)
	}
}

func TestFileBrowserAdministratorOnly(t *testing.T) {
	for _, role := range []principalRole{roleViewer, roleOperator, roleAdministrator} {
		s, _ := systemMonitorProfileTestServer(t, role)
		mux := http.NewServeMux()
		registerFileBrowserRoutes(mux, s)
		for _, action := range []string{"", "/start", "/stop", "/availability"} {
			method := http.MethodPost
			if action == "" {
				method = http.MethodGet
			}
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, systemMonitorProfileRequest(method, "/v1/admin/filebrowser"+action, map[string]string{"availability": "always"}))
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s %s returned %d", role, action, rr.Code)
			}
		}
	}
}

// Expected policy is independent of automatic source discovery and mount rendering.
func representativeFileBrowserSources() []fileBrowserSource {
	return []fileBrowserSource{
		{Host: "/approved/minecraft", Path: "/data/minecraft/jv-abc123", Name: "Minecraft Servers", NonRecursive: true},
		{Host: "/approved/backups", Path: "/data/backups/1", Name: "Backups", NonRecursive: true},
		{Host: "/var/mnt", Path: "/data/mounted/var-mnt", Name: "Mounted storage", Propagation: "rslave"},
		{Host: "/mnt", Path: "/data/mounted/mnt", Name: "Mounts", Propagation: "rslave"},
		{Host: "/media", Path: "/data/mounted/media", Name: "Media", Propagation: "rslave"},
		{Host: "/run/media", Path: "/data/mounted/run-media", Name: "Removable media", Propagation: "rslave"},
		{Host: "/usr", Path: "/data/system/usr", Name: "System /usr (read-only)", ReadOnly: true},
		{Host: "/boot", Path: "/data/system/boot", Name: "System /boot (read-only)", ReadOnly: true},
	}
}

func TestFileBrowserContainerContract(t *testing.T) {
	sources := representativeFileBrowserSources()
	args := fileBrowserContainerArgs(sources)
	joined := strings.Join(args, " ")
	for _, want := range []string{fileBrowserImage, "127.0.0.1:61209:8080", "label=disable", "no-new-privileges", "--cap-drop=ALL", "--cap-add=DAC_OVERRIDE", "--user 1000:1000"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	var mounts []string
	var capabilities []string
	for i, arg := range args {
		if arg == "--mount" {
			mounts = append(mounts, args[i+1])
		}
		if strings.HasPrefix(arg, "--cap-add") {
			capabilities = append(capabilities, arg)
		}
	}
	if !reflect.DeepEqual(capabilities, []string{"--cap-add=DAC_OVERRIDE"}) {
		t.Fatalf("unexpected capabilities: %v", capabilities)
	}
	wantMounts := []string{
		"type=volume,source=justvoxel-filebrowser-state,target=/home/filebrowser/data",
		"type=bind,source=" + fileBrowserRuntimeConfig + ",target=/etc/filebrowser/config.yaml,ro=true",
		"type=bind,source=/approved/minecraft,target=/data/minecraft/jv-abc123,bind-nonrecursive=true",
		"type=bind,source=/approved/backups,target=/data/backups/1,bind-nonrecursive=true",
		"type=bind,source=/var/mnt,target=/data/mounted/var-mnt,bind-propagation=rslave",
		"type=bind,source=/mnt,target=/data/mounted/mnt,bind-propagation=rslave",
		"type=bind,source=/media,target=/data/mounted/media,bind-propagation=rslave",
		"type=bind,source=/run/media,target=/data/mounted/run-media,bind-propagation=rslave",
		"type=bind,source=/usr,target=/data/system/usr,ro=true",
		"type=bind,source=/boot,target=/data/system/boot,ro=true",
	}
	if !reflect.DeepEqual(mounts, wantMounts) {
		t.Fatalf("mount policy changed: got %v, want %v", mounts, wantMounts)
	}
	for _, forbidden := range []string{":Z", ":z", "--privileged", "--network=host", "--device", "0.0.0.0:61209", "source=/,", "source=/etc,"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("unsafe container argument %q", forbidden)
		}
	}
	base, err := os.ReadFile("../../../system_files/usr/share/justvoxel/filebrowser/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderFileBrowserConfig(base, sources)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(out, &config); err != nil {
		t.Fatal(err)
	}
	server := config["server"].(map[string]any)
	entries := server["sources"].([]any)
	if len(entries) != len(sources) {
		t.Fatal("wrong source count")
	}
	for i, raw := range entries {
		entry := raw.(map[string]any)
		if entry["path"] != sources[i].Path || entry["name"] != sources[i].Name {
			t.Fatalf("source identity changed: %v", entry)
		}
		source := entry["config"].(map[string]any)
		readOnly := i >= 6
		if source["readOnly"] != readOnly || source["private"] != true || source["defaultEnabled"] != true || source["defaultUserScope"] != "/" {
			t.Fatalf("incorrect source policy: %v", source)
		}
		if !reflect.DeepEqual(source["rules"], []any{map[string]any{"ignoreSymlinks": true}}) {
			t.Fatal("unexpected source rules")
		}
		wantPermissions := map[string]any{"view": true, "download": true, "configured": true, "create": !readOnly, "modify": !readOnly, "delete": !readOnly}
		if !reflect.DeepEqual(source["defaultPermissions"], wantPermissions) {
			t.Fatalf("incorrect permissions for %s: %v", sources[i].Host, source["defaultPermissions"])
		}
	}
}

func TestAutomaticFileBrowserSources(t *testing.T) {
	root := t.TempDir()
	directory, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	symlink, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	regular, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	want := representativeFileBrowserSources()[2:]
	for _, scenario := range []string{"all directories", "symlink", "missing", "regular file", "inaccessible"} {
		t.Run(scenario, func(t *testing.T) {
			var visited []string
			sources, err := automaticFileBrowserSources(func(path string) (os.FileInfo, error) {
				visited = append(visited, path)
				switch scenario {
				case "symlink":
					return symlink, nil
				case "missing":
					return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrNotExist}
				case "regular file":
					return regular, nil
				case "inaccessible":
					return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrPermission}
				default:
					return directory, nil
				}
			})
			if scenario == "inaccessible" {
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("lost inspection error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(visited, []string{"/var/mnt", "/mnt", "/media", "/run/media", "/usr", "/boot"}) {
				t.Fatalf("automatic root allowlist changed: %v", visited)
			}
			if scenario == "all directories" {
				if !reflect.DeepEqual(sources, want) {
					t.Fatalf("automatic source policy: got %#v, want %#v", sources, want)
				}
			} else if len(sources) != 0 {
				t.Fatalf("accepted %s roots: %#v", scenario, sources)
			}
			for _, source := range sources {
				for _, forbidden := range []string{"/", "/etc", "/root", "/home", "/proc", "/sys", "/dev", "/run", "/var", "/var/lib", "/var/lib/containers", "/var/lib/justvoxel", "/var/lib/justvoxel-web", "/var/lib/justvoxel-instance-runtime"} {
					if source.Host == forbidden {
						t.Fatalf("exposed forbidden root: %s", source.Host)
					}
				}
			}
		})
	}
}

func TestFileBrowserRejectsPrivateAndSymlinkRoots(t *testing.T) {
	// A runner may set TMPDIR inside a protected home/configuration tree.
	// Use an ordinary external directory without weakening production checks.
	t.Setenv("TMPDIR", "/tmp")
	for _, root := range []string{
		"/", "/etc", "/var", "/var/lib", "/var/lib/justvoxel", "/var/lib/justvoxel/webui",
		"/var/lib/justvoxel/instances", "/var/lib/justvoxel/management", "/var/lib/justvoxel/management/operations",
		"/var/lib/justvoxel/config-backups", "/var/lib/justvoxel/state", "/var/lib/justvoxel/exports",
		"/var/lib/justvoxel-instance-runtime", "/var/lib/justvoxel-web", "/root/.ssh", "/home/voxel/.ssh",
	} {
		if safeFileBrowserRoot(root) == nil {
			t.Fatalf("accepted private root %s", root)
		}
	}
	root := t.TempDir()
	if err := safeFileBrowserRoot(root); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if safeFileBrowserRoot(link) == nil {
		t.Fatal("accepted symlink source")
	}
	if safeFileBrowserRoot(filepath.Join(link, "data")) == nil {
		t.Fatal("accepted symlink source ancestor")
	}
}

func TestFileBrowserSourcesWithoutMinecraft(t *testing.T) {
	b := &instanceRuntimeBackend{
		root:     filepath.Join(t.TempDir(), "private"),
		quadlets: t.TempDir(),
	}
	oldBackend, oldDiscovery := instanceRuntimes, discoverAutomaticFileBrowserSources
	t.Cleanup(func() {
		instanceRuntimes, discoverAutomaticFileBrowserSources = oldBackend, oldDiscovery
	})
	instanceRuntimes = b
	want := []fileBrowserSource{
		{Host: "/mnt", Path: "/data/mounted/mnt", Name: "Mounts", Propagation: "rslave"},
	}
	discoverAutomaticFileBrowserSources = func() ([]fileBrowserSource, error) {
		return want, nil
	}
	if records, err := b.records(); err != nil || len(records) != 0 {
		t.Fatalf("expected no configured Minecraft sources: %#v, %v", records, err)
	}
	sources, err := fileBrowserSources(context.Background())
	if err != nil {
		if strings.Contains(err.Error(), "No configured Minecraft data is available") {
			t.Fatalf("automatic sources still require Minecraft: %v", err)
		}
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("automatic sources = %#v, want %#v", sources, want)
	}
}

func TestFileBrowserSourcesUseAuthoritativePaths(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	for _, scenario := range []string{"legacy backups", "configured backups", "broad data root", "broad backup root", "private backup root"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			b := f.backend
			oldBackend, oldBackupCommand := instanceRuntimes, instanceBackupStorageCommand
			t.Cleanup(func() {
				instanceRuntimes, instanceBackupStorageCommand = oldBackend, oldBackupCommand
			})
			instanceRuntimes = b
			instanceBackupStorageCommand = func(context.Context, string, ...string) ([]byte, error) {
				return nil, nil
			}
			backupRoot := f.snapshot.BackupPath
			if scenario == "configured backups" {
				backupRoot = filepath.Join(t.TempDir(), "selected-backups")
			}
			if err := os.MkdirAll(backupRoot, 0700); err != nil {
				t.Fatal(err)
			}
			for _, config := range f.configs {
				record, err := b.readRecord(config.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(record.Storage.DataPath, 0750); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "configured backups":
					record.Backups = &instanceRuntimeBackupStorage{Type: "system", Root: backupRoot, Path: filepath.Join(backupRoot, config.ID)}
				case "broad data root":
					record.Storage.DataPath = record.Storage.Root
				case "broad backup root":
					f.snapshot.BackupPath = "/var/lib/justvoxel"
				case "private backup root":
					f.snapshot.BackupPath = "/var/lib/justvoxel/management"
				}
				if err := b.writeJSON(filepath.Join(b.root, config.ID, "config.json"), record, true); err != nil {
					t.Fatal(err)
				}
			}
			sources, err := fileBrowserSources(context.Background())
			if strings.Contains(scenario, "root") {
				if err == nil {
					t.Fatalf("accepted unsafe sources: %#v", sources)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			automatic, err := automaticFileBrowserSources(os.Lstat)
			if err != nil {
				t.Fatal(err)
			}
			if len(sources) != len(f.configs)+1+len(automatic) {
				t.Fatalf("unexpected sources: %#v", sources)
			}
			for _, source := range sources[:len(f.configs)] {
				if !source.NonRecursive || source.ReadOnly || source.Propagation != "" {
					t.Fatalf("curated source policy changed: %#v", source)
				}
				id := filepath.Base(source.Path)
				if source.Host != filepath.Join(f.snapshot.StorageRoot, "minecraft-instances", id, "data") {
					t.Fatalf("exposed storage outside exact instance data: %#v", source)
				}
			}
			if source := sources[len(f.configs)]; source.Host != backupRoot || !source.NonRecursive || source.ReadOnly || source.Propagation != "" {
				t.Fatalf("backup source = %#v, want %s", source, backupRoot)
			}
			if !reflect.DeepEqual(sources[len(f.configs)+1:], automatic) {
				t.Fatal("automatic sources were not appended to curated sources")
			}
		})
	}
}

func TestFileBrowserMigrationPreservesDeployedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range webUIStoreMigrations[:3] {
		for _, statement := range migration.statements {
			if _, err = db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(?, ?, '1970-01-01T00:00:00Z')`, migration.version, migration.name); err != nil {
			t.Fatal(err)
		}
	}
	user, err := (&webUIStore{db: db, now: time.Now}).createWebUser("existing-viewer", webUserRoleViewer, "existing-viewer-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.readFileBrowserAvailability(); err != nil || got != "on-demand" {
		t.Fatalf("migration default: %q, %v", got, err)
	}
	if _, err = store.db.Exec(`UPDATE filebrowser_settings SET availability = 'always' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err = store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if got, err := reopened.readFileBrowserAvailability(); err != nil || got != "always" {
		t.Fatalf("reopened setting: %q, %v", got, err)
	}
	if got, err := reopened.webUserByID(user.ID); err != nil || got.Username != "existing-viewer" {
		t.Fatalf("existing user lost: %#v, %v", got, err)
	}
	if profile, err := reopened.readDateTimeProfile(); err != nil || profile != (dateTimeProfile{"12", "mdy"}) {
		t.Fatalf("existing profile lost: %#v, %v", profile, err)
	}
}

func TestFileBrowserStatusReportsHealth(t *testing.T) {
	s, _ := systemMonitorProfileTestServer(t, roleAdministrator)
	sess := s.sessions["token"]
	sess.Username = systemAdminUsername
	s.sessions["token"] = sess
	oldCommand, oldHealth := fileBrowserCommand, fileBrowserHealth
	t.Cleanup(func() { fileBrowserCommand, fileBrowserHealth = oldCommand, oldHealth })
	fileBrowserCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte("active\n"), nil }
	for _, ready := range []bool{false, true} {
		fileBrowserHealth = func(context.Context) bool { return ready }
		rr := httptest.NewRecorder()
		s.fileBrowserGet(rr, systemMonitorProfileRequest("GET", "/v1/admin/filebrowser", nil))
		var status fileBrowserStatus
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &status) != nil || status.Ready != ready || status.State != "active" || status.Availability != "on-demand" {
			t.Fatalf("unexpected readiness: %d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestFileBrowserPrimaryAdministratorEndpointsAccepted(t *testing.T) {
	s, _ := systemMonitorProfileTestServer(t, roleAdministrator)
	sess := s.sessions["token"]
	sess.Username = systemAdminUsername
	s.sessions["token"] = sess
	oldCommand, oldHealth := fileBrowserCommand, fileBrowserHealth
	t.Cleanup(func() { fileBrowserCommand, fileBrowserHealth = oldCommand, oldHealth })
	fileBrowserCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte("active\n"), nil }
	fileBrowserHealth = func(context.Context) bool { return true }
	mux := http.NewServeMux()
	registerFileBrowserRoutes(mux, s)
	for _, action := range []string{"", "/start", "/stop", "/availability"} {
		method := "POST"
		if action == "" {
			method = "GET"
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, systemMonitorProfileRequest(method, "/v1/admin/filebrowser"+action, map[string]string{"availability": "on-demand"}))
		if rr.Code != 200 {
			t.Fatalf("%s: %d %s", action, rr.Code, rr.Body.String())
		}
	}
}
