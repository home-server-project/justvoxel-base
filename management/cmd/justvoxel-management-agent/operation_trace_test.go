package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func traceTestOperation(t *testing.T, store *operationStore, kind string) string {
	t.Helper()
	id, err := newOperationID()
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.operations[id] = operationJournal{OperationID: id, OperationType: kind}
	store.mu.Unlock()
	return id
}
func TestOperationTraceAuthorizationAndScope(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeSetup)
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		s.operations = store
		mux := http.NewServeMux()
		registerAdminOperationRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/operations/"+id+"/trace", ""))
		want := http.StatusForbidden
		if role == roleAdministrator {
			want = http.StatusOK
		}
		if rr.Code != want {
			t.Fatalf("%s: %d", role, rr.Code)
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = store
	mux := http.NewServeMux()
	registerAdminOperationRoutes(mux, s)
	for _, kind := range []string{operationTypeSetup, operationTypeMinecraftReset, operationTypeFactoryReset, operationTypeRestore} {
		op := traceTestOperation(t, store, kind)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/operations/"+op+"/trace?path=/etc/shadow&type=setup", ""))
		want := http.StatusOK
		if kind == operationTypeRestore {
			want = http.StatusNotFound
		}
		if rr.Code != want {
			t.Fatalf("%s: %d", kind, rr.Code)
		}
		if want == http.StatusOK && (rr.Body.Len() != 0 || rr.Header().Get("Cache-Control") != "no-store" || rr.Header().Get("Content-Type") != "text/plain; charset=utf-8") {
			t.Fatal("missing trace did not return safe empty text")
		}
	}
	for _, item := range []struct {
		id   string
		code int
	}{{"invalid", 400}, {"12345678-1234-4123-8123-123456789abc", 404}} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, "/v1/admin/operations/"+item.id+"/trace", ""))
		if rr.Code != item.code {
			t.Fatalf("%s: %d", item.id, rr.Code)
		}
	}
	if _, err := store.readTrace("../../etc/shadow"); err == nil {
		t.Fatal("arbitrary path accepted")
	}
}
func TestOperationTraceSanitization(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeSetup)
	input := "\x1b[31m$ /usr/libexec/justvoxel/mjust/validate-backend --first-run\x1b[0m\n\\x1b[32mOK: /var/lib/justvoxel/minecraft docker.io/itzg/minecraft-server:stable /dev/sda\npassword=hidden token=abc smb_password=share-secret\nAuthorization: Bearer private-value\nCookie: session=secret-cookie\nhttps://user:secret@example.test/path\nknown-secret\x00\x07\n{\"stdin\":\"must not appear\"}"
	if err := store.appendTrace(id, input, []string{"known-secret"}); err != nil {
		t.Fatal(err)
	}
	data, err := store.readTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"hidden", "abc", "share-secret", "private-value", "secret-cookie", "user:secret", "known-secret", "must not appear", "\x1b", "\\x1b", "\x00", "\x07"} {
		if strings.Contains(text, secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
	for _, good := range []string{"/usr/libexec/justvoxel/mjust/validate-backend --first-run", "/var/lib/justvoxel/minecraft", "docker.io/itzg/minecraft-server:stable", "/dev/sda"} {
		if !strings.Contains(text, good) {
			t.Fatalf("lost %q", good)
		}
	}
}
func TestOperationTraceBoundsAndPrivateFiles(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeSetup)
	for i := 0; i < 80; i++ {
		if err := store.appendTrace(id, strings.Repeat("a", 32*1024), nil); err != nil {
			t.Fatal(err)
		}
	}
	data, err := store.readTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > operationTraceLimit {
		t.Fatal("unbounded trace")
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if len(line)+1 > operationTraceLineLimit {
			t.Fatal("unbounded line")
		}
	}
	path := filepath.Join(store.baseDir, "logs", "live-traces", id+".trace")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("trace is not private")
	}
	target := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.readTrace(id); err == nil {
		t.Fatal("trace symlink followed")
	}
	if err := store.appendTrace(id, "corruption", nil); err == nil {
		t.Fatal("trace symlink written")
	}
	content, _ := os.ReadFile(target)
	if string(content) != "unchanged" {
		t.Fatal("target changed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.readTrace(id); err == nil {
		t.Fatal("directory symlink followed")
	}
}
func TestOperationTraceIngestionAndIsolation(t *testing.T) {
	store := openTestOperationStore(t)
	a := traceTestOperation(t, store, operationTypeSetup)
	b := traceTestOperation(t, store, operationTypeMinecraftReset)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { operationTraceContext{store, a, nil}.ingest(reader); reader.Close(); close(done) }()
	fmt.Fprintln(writer, "STAGE Runtime")
	fmt.Fprintln(writer, strings.Repeat("x", operationTraceLineLimit+20)+"password=discard")
	fmt.Fprintln(writer, "$ systemctl start minecraft.service")
	writer.Close()
	<-done
	if err := store.appendTrace(b, "independent", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("stale")); err == nil {
		t.Fatal("closed pipe writable")
	}
	data, _ := store.readTrace(a)
	other, _ := store.readTrace(b)
	if !strings.Contains(string(data), "systemctl start minecraft.service") || strings.Contains(string(data), "discard") {
		t.Fatal("ingestion failed")
	}
	if strings.Contains(string(other), "systemctl") || strings.Contains(string(other), "stale") {
		t.Fatal("cross-operation trace corruption")
	}
}
func TestOperationTraceHelperProcess(t *testing.T) {
	if os.Getenv("JV_TRACE_TEST_HELPER") != "1" {
		return
	}
	file := os.NewFile(3, "inherited trace")
	fmt.Fprintln(file, "STAGE Runtime")
	fmt.Fprintln(file, "$ systemctl daemon-reload")
	fmt.Fprintln(file, "OK: configured")
	file.Close()
	fmt.Print(`{"response":"not a trace"}`)
	os.Exit(0)
}
func TestOperationTraceTopLevelOrdering(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeSetup)
	ctx := withOperationTrace(context.Background(), store, id)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOperationTraceHelperProcess$")
	// This environment selects a harmless child fixture, never a host service.
	t.Setenv("JV_TRACE_TEST_HELPER", "1")
	finish, err := attachOperationTrace(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	output, runErr := cmd.Output()
	finish(runErr)
	if runErr != nil || !strings.Contains(string(output), "not a trace") {
		t.Fatal("JSON stdout changed")
	}
	data, err := store.readTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	command := strings.Index(text, "$ "+cmd.Path)
	stage := strings.Index(text, "STAGE Runtime")
	result := strings.LastIndex(text, "[exit 0]")
	if command < 0 || stage <= command || result <= stage || strings.Contains(text, "not a trace") {
		t.Fatal("helper trace ordering or JSON boundary changed")
	}
}

func TestOperationTraceCredentialArguments(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeFactoryReset)
	cmd := exec.Command("/fixed/fixture-helper", "--password", "private hash with spaces", "--unlock", "voxel")
	finish, err := attachOperationTrace(withOperationTrace(context.Background(), store, id), cmd)
	if err != nil {
		t.Fatal(err)
	}
	// Inspect transport setup without executing the fixture or any host command.
	finish(nil)
	data, err := store.readTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "private hash") || !strings.Contains(text, "--password [redacted]") || !strings.Contains(text, "--unlock voxel") {
		t.Fatal("credential argument redaction lost safe command context")
	}
}

func TestOperationTraceSeparateFromDiagnostics(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeSetup)
	if err := store.appendTrace(id, "$ systemctl start minecraft.service", nil); err != nil {
		t.Fatal(err)
	}
	logs, err := store.listDiagnosticLogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs {
		if entry.LogID == id {
			t.Fatal("live trace was listed as a forensic diagnostic log")
		}
	}
}
