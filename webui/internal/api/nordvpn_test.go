package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidNordAccessToken(t *testing.T) {
	visible := ""
	for b := byte(0x21); b <= 0x7e; b++ {
		visible += string(b)
	}
	for _, good := range []string{visible, "opaque+/_-.~=token", strings.Repeat("a", 4096)} {
		if !ValidNordAccessToken(good) {
			t.Fatal("visible ASCII rejected")
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 4097), "a b", "a\tb", "a\rb", "a\nb", "a\x00b", "a\x1fb", "a\x7fb", "é", string([]byte{0xff})} {
		if ValidNordAccessToken(bad) {
			t.Fatal("invalid token accepted")
		}
	}
}

func TestNordVPNTokenLoginFixedRequest(t *testing.T) {
	const token = "opaque+/_-.~=token"
	calls := 0
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/network/remote-access/nordvpn" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer session" {
			t.Fatal("wrong fixed endpoint/session")
		}
		deadline, ok := r.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > 35*time.Second {
			t.Fatal("Nord timeout changed")
		}
		var got map[string]string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, map[string]string{"action": "token-login", "token": token}) {
			t.Fatal("token changed or action not fixed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}}
	result, err := client.NordVPNTokenLogin(context.Background(), "session", token)
	if err != nil || !result.OK || calls != 1 {
		t.Fatal("token login failed", err)
	}
}
