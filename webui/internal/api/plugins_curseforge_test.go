package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCurseForgeCredentialTransport(t *testing.T) {
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		c := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != method || r.URL.Path != "/v1/admin/plugins/curseforge-api" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer session" {
				t.Fatal("credential transport boundary")
			}
			if method == "PUT" {
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["key"] != "test-private-key" {
					t.Fatal("write contract")
				}
			} else if r.Body != nil {
				t.Fatal("unexpected credential body")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"configured":true}`))}, nil
		})}}
		out, err := c.AdminCurseForgeAPI(context.Background(), "session", method, "test-private-key")
		if err != nil || !out.Configured {
			t.Fatal(out, err)
		}
		data, err := json.Marshal(out)
		if err != nil || string(data) != `{"configured":true}` {
			t.Fatal("status is not presence-only")
		}
	}
}
