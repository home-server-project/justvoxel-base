package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDateTimeProfileMigrationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	// Construct a deployed version-2 store without altering migration definitions.
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range webUIStoreMigrations[:2] {
		for _, statement := range migration.statements {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(?, ?, '1970-01-01T00:00:00Z')`, migration.version, migration.name); err != nil {
			t.Fatal(err)
		}
	}
	user, err := (&webUIStore{db: db, now: time.Now}).createWebUser("viewer", webUserRoleViewer, "viewer-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.readDateTimeProfile()
	if err != nil || profile != (dateTimeProfile{"12", "mdy"}) {
		t.Fatalf("migration default: %#v, %v", profile, err)
	}
	want := dateTimeProfile{"24", "dmy"}
	if err := store.writeDateTimeProfile(want); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	got, err := reopened.readDateTimeProfile()
	if err != nil || got != want {
		t.Fatalf("reopened profile: %#v, %v", got, err)
	}
	if got, err := reopened.webUserByID(user.ID); err != nil || got.Username != "viewer" {
		t.Fatalf("existing identity lost: %#v %v", got, err)
	}
}

func TestDateTimeFormatsAreValidatedBeforeHostCalls(t *testing.T) {
	old := runTimedatectl
	defer func() { runTimedatectl = old }()
	runTimedatectl = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("invalid format reached host clock")
		return nil, nil
	}
	for _, profile := range []dateTimeProfile{{"", "mdy"}, {"23", "mdy"}, {"12", ""}, {"24", "ymd"}} {
		s := surfaceTestServer(t, roleAdministrator)
		if err := s.store.writeDateTimeProfile(profile); err == nil {
			t.Fatalf("stored invalid profile %#v", profile)
		}
		rr := httptest.NewRecorder()
		request := map[string]any{"timezone": "UTC", "automatic": true, "time_format": profile.TimeFormat, "date_format": profile.DateFormat}
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", string(data)))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid format: %d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestDateTimeFormatsAreSharedThroughSessionOnly(t *testing.T) {
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		t.Run(string(role), func(t *testing.T) {
			s := surfaceTestServer(t, role)
			want := dateTimeProfile{"24", "dmy"}
			if err := s.store.writeDateTimeProfile(want); err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			s.sessionStatus(rr, surfaceRequest(http.MethodGet, "/v1/session", ""))
			var got map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if rr.Code != http.StatusOK || got["time_format"] != "24" || got["date_format"] != "dmy" {
				t.Fatalf("shared profile: %d %#v", rr.Code, got)
			}
			for _, privileged := range []string{"timezone", "automatic", "local_date", "local_time"} {
				if _, exists := got[privileged]; exists {
					t.Fatalf("shared session exposes %s", privileged)
				}
			}
			if role != roleAdministrator {
				rr = httptest.NewRecorder()
				s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", `{"timezone":"UTC","automatic":true,"time_format":"12","date_format":"mdy"}`))
				if rr.Code != http.StatusForbidden {
					t.Fatalf("format mutation: %d", rr.Code)
				}
				got, err := s.store.readDateTimeProfile()
				if err != nil || got != want {
					t.Fatalf("unauthorized write: %#v %v", got, err)
				}
			}
		})
	}
}

func TestDateTimeFormatApplyDoesNotMutateHostClock(t *testing.T) {
	old := runTimedatectl
	defer func() { runTimedatectl = old }()
	runTimedatectl = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] != "show" {
			t.Fatalf("format change mutated host clock: %#v", args)
		}
		return []byte("Timezone=UTC\nNTP=yes\nNTPSynchronized=yes\n"), nil
	}
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", `{"timezone":"UTC","automatic":true,"time_format":"24","date_format":"dmy"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rr.Code, rr.Body.String())
	}
	var state adminDateTimeState
	if err := json.Unmarshal(rr.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.TimeFormat != "24" || state.DateFormat != "dmy" {
		t.Fatalf("saved state: %#v", state)
	}
	// Older callers omitting display fields preserve the appliance profile.
	rr = httptest.NewRecorder()
	s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", `{"timezone":"UTC","automatic":true}`))
	got, err := s.store.readDateTimeProfile()
	if rr.Code != http.StatusOK || err != nil || got != (dateTimeProfile{"24", "dmy"}) {
		t.Fatalf("legacy apply: %d %#v %v", rr.Code, got, err)
	}
}
