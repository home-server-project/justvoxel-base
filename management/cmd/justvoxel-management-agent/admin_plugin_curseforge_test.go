package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func curseForgeTestDirectory(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root ownership contract requires root fixture")
	}
	return filepath.Join(t.TempDir(), "curseforge")
}
func TestCurseForgePrivateAtomicStorage(t *testing.T) {
	path := curseForgeTestDirectory(t)
	dir, err := openCurseForgeDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if info, _ := dir.Stat(); info.Mode().Perm() != 0700 {
		t.Fatal("directory is not private")
	}
	if present, err := curseForgePresent(dir); present || err != nil {
		t.Fatal(present, err)
	}
	if err := replaceCurseForgeKey(dir, "test-first-key"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(path, curseForgeKeyFile)
	previous, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	if err := replaceCurseForgeKey(dir, "test-replacement-key"); err != nil {
		t.Fatal(err)
	}
	old := make([]byte, 64)
	n, err := previous.Read(old)
	if err != nil || string(old[:n]) != "test-first-key" {
		t.Fatal("replacement modified existing inode")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "test-replacement-key" {
		t.Fatal("replacement failed")
	}
	info, err := os.Lstat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key permissions", err)
	}
	var st unix.Stat_t
	if unix.Stat(target, &st) != nil || st.Uid != 0 || st.Gid != 0 {
		t.Fatal("key ownership")
	}
	entries, _ := os.ReadDir(path)
	if len(entries) != 1 {
		t.Fatal("temporary key retained")
	}
	if strings.Contains(string(supportInventory(filepath.Dir(path))), "test-replacement-key") {
		t.Fatal("secret in diagnostics")
	}
}
func TestCurseForgeRejectsUnsafePaths(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "fifo", "public-file", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			path := curseForgeTestDirectory(t)
			dir, err := openCurseForgeDirectory(path)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			target := filepath.Join(path, curseForgeKeyFile)
			outside := filepath.Join(filepath.Dir(path), "outside")
			if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				err = os.Symlink(outside, target)
			case "directory":
				err = os.Mkdir(target, 0700)
			case "fifo":
				err = unix.Mkfifo(target, 0600)
			case "public-file":
				err = os.WriteFile(target, []byte("unsafe"), 0644)
			case "hardlink":
				err = os.Link(outside, target)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = curseForgePresent(dir); err == nil {
				t.Fatal("unsafe target accepted")
			}
			if replaceCurseForgeKey(dir, "new-test-key") == nil {
				t.Fatal("unsafe target replaced")
			}
			data, _ := os.ReadFile(outside)
			if string(data) != "untouched" {
				t.Fatal("external file changed")
			}
		})
	}
	path := curseForgeTestDirectory(t)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if dir, err := openCurseForgeDirectory(path); err == nil {
		dir.Close()
		t.Fatal("public directory accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(path), path); err != nil {
		t.Fatal(err)
	}
	if dir, err := openCurseForgeDirectory(path); err == nil {
		dir.Close()
		t.Fatal("directory symlink accepted")
	}
}
func TestCurseForgeServiceAndStorageContract(t *testing.T) {
	unit, err := os.ReadFile("../../../system_files/usr/lib/systemd/system/justvoxel-management.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"User=root", "Group=justvoxel-web", "UMask=0007"} {
		if !strings.Contains("\n"+string(unit), "\n"+line+"\n") {
			t.Fatalf("management service contract missing %q", line)
		}
	}
	tmpfiles, err := os.ReadFile("../../../system_files/usr/lib/tmpfiles.d/justvoxel.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+string(tmpfiles), "\nd /var/lib/justvoxel/management/curseforge 0700 root root -\n") {
		t.Fatal("tmpfiles must normalize the known CurseForge directory at boot")
	}
	// Lock the creation-only descriptor repair even when root fixtures cannot run.
	source, err := os.ReadFile("admin_plugin_curseforge.go")
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for _, step := range []string{
		"created := false",
		"unix.Mkdirat(fd, part, 0700)",
		"created = err == nil",
		"unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)",
		"if created {\n\t\tif unix.Fchown(fd, 0, 0) != nil || unix.Fchmod(fd, 0700) != nil {",
		"unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0700",
	} {
		index := strings.Index(string(source), step)
		if index <= last {
			t.Fatalf("creation-only normalization before strict validation missing %q", step)
		}
		last = index
	}
}

func TestCurseForgeDirectoryGroupOwnership(t *testing.T) {
	path := curseForgeTestDirectory(t)
	parent := filepath.Dir(path)
	// A setgid parent reproduces non-root group inheritance without changing
	// the test process identity or requiring the appliance's group database.
	if err := os.Chown(parent, 0, 12345); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, os.ModeSetgid|0700); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(parent, "inherited-group")
	if err := os.Mkdir(probe, 0700); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if unix.Stat(probe, &st) != nil || st.Gid != 12345 {
		t.Fatal("fixture did not reproduce non-root group inheritance")
	}
	dir, err := openCurseForgeDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(int(dir.Fd()), &st); err != nil {
		dir.Close()
		t.Fatal(err)
	}
	dir.Close()
	if st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0700 {
		t.Fatal("new directory was not normalized to root:root 0700")
	}
	if err := os.Chown(path, 0, 12345); err != nil {
		t.Fatal(err)
	}
	if dir, err := openCurseForgeDirectory(path); err == nil {
		dir.Close()
		t.Fatal("existing directory with unsafe group accepted")
	}
	if unix.Stat(path, &st) != nil || st.Gid != 12345 {
		t.Fatal("runtime repaired an existing directory")
	}
}

func TestCurseForgeKeyValidation(t *testing.T) {
	if !validCurseForgeKey("$2a$10$exampleExampleExampleExampleExampleExampleExampleExample") {
		t.Fatal("synthetic bcrypt-looking API credential rejected")
	}
	for _, key := range []string{"", " \t ", strings.Repeat("x", 1025), "secret\nvalue", "secret\rvalue", "secret\x00value", "secret\u2028value"} {
		if validCurseForgeKey(key) {
			t.Fatal("invalid credential accepted")
		}
	}
	if !validCurseForgeKey(strings.Repeat("x", 1024)) {
		t.Fatal("bounded key rejected")
	}
}
func TestCurseForgeAPIStatusSaveRemove(t *testing.T) {
	var logs bytes.Buffer
	originalLogWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(originalLogWriter) })
	old := curseForgeDirectory
	curseForgeDirectory = curseForgeTestDirectory(t)
	t.Cleanup(func() { curseForgeDirectory = old })
	s := surfaceTestServer(t, roleAdministrator)
	mux := http.NewServeMux()
	registerAdminPluginRoutes(mux, s)
	for _, tc := range []struct {
		method, body string
		configured   bool
	}{
		{"GET", "", false}, {"PUT", `{"key":"test-private-credential"}`, true}, {"GET", "", true}, {"DELETE", "", false}, {"GET", "", false},
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(tc.method, "/v1/admin/plugins/curseforge-api", tc.body))
		var out map[string]any
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &out) != nil || len(out) != 1 || out["configured"] != tc.configured {
			t.Fatalf("presence-only contract: %d %s", rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "test-private-credential") {
			t.Fatal("secret echoed")
		}
	}
	if _, err := os.Lstat(filepath.Join(curseForgeDirectory, curseForgeKeyFile)); !os.IsNotExist(err) {
		t.Fatal("key remains after removal")
	}
	for _, body := range []string{`{"key":""}`, `{"key":" \t"}`, `{"key":"secret\nvalue"}`, `{"key":"` + strings.Repeat("x", 1025) + `"}`, `{"key":"valid","extra":true}`} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest("PUT", "/v1/admin/plugins/curseforge-api", body))
		if rr.Code != 400 || strings.Contains(rr.Body.String(), "secret") {
			t.Fatal("invalid input or secret boundary", rr.Code)
		}
	}
	if strings.Contains(logs.String(), "test-private-credential") || strings.Contains(logs.String(), "secret") {
		t.Fatal("credential in logs")
	}
}
func TestCurseForgeAPIAuthorization(t *testing.T) {
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			rr := httptest.NewRecorder()
			s.adminCurseForgeAPI(rr, surfaceRequest(method, "/v1/admin/plugins/curseforge-api", `{"key":"test-key"}`))
			if rr.Code != 403 {
				t.Fatal("non-administrator access", rr.Code)
			}
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		rr := httptest.NewRecorder()
		s.adminCurseForgeAPI(rr, httptest.NewRequest(method, "/v1/admin/plugins/curseforge-api", nil))
		if rr.Code != 401 {
			t.Fatal("unauthenticated access", rr.Code)
		}
	}
}
