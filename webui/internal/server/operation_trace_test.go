package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeOperationTraceAPI struct {
	*fakeDiagnosticLogsAPI
	id      string
	session string
	trace   []byte
	err     error
	calls   int
}

func (f *fakeOperationTraceAPI) AdminOperationTrace(_ context.Context, session, id string) ([]byte, error) {
	f.id = id
	f.session = session
	f.calls++
	return f.trace, f.err
}
func TestOperationTraceProxy(t *testing.T) {
	client := &fakeOperationTraceAPI{fakeDiagnosticLogsAPI: &fakeDiagnosticLogsAPI{fakeAPI: &fakeAPI{}, role: "administrator"}, trace: []byte("$ systemctl start minecraft.service\n[exit 0]\n")}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	id := "12345678-1234-4123-8123-123456789abc"
	request := func(id string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/operations/"+id+"/trace?path=/etc/shadow&kind=factory_reset&command=rm", ""))
		return rr
	}
	rr := request(id)
	if rr.Code != 200 || rr.Body.String() != string(client.trace) || client.id != id || client.session == "" {
		t.Fatal("trace proxy changed authoritative ID, session, or text")
	}
	if rr.Header().Get("Content-Type") != "text/plain; charset=utf-8" || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe trace headers")
	}
	if client.reads != 0 {
		t.Fatal("trace read diagnostic log")
	}
	for _, role := range []string{"viewer", "operator"} {
		client.role = role
		if request(id).Code != 403 {
			t.Fatal("non-Administrator allowed")
		}
	}
	client.role = "administrator"
	if request("malformed").Code != 400 || client.calls != 1 {
		t.Fatal("invalid ID forwarded")
	}
	client.trace = nil
	if request(id).Code != 200 {
		t.Fatal("empty trace rejected")
	}
	client.err = api.ErrUnauthorized
	if request(id).Code != 401 {
		t.Fatal("authentication loss changed")
	}
	client.err = api.ErrPasswordChangeRequired
	if request(id).Code != 403 {
		t.Fatal("password boundary changed")
	}
	client.err = nil
	client.trace = []byte(strings.Repeat("a", 1024*1024+1))
	if request(id).Code != 502 {
		t.Fatal("unbounded trace")
	}
}
