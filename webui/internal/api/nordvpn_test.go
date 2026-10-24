package api

import "testing"

func TestNordCallbackAtWebUIBoundary(t *testing.T) {
	for _, good := range []string{"nordvpn://login?exchange_token=YWJj%2B%2F%3D", "nordvpn://login?exchange_token=abc", "nordvpn://login?action=login&exchange_token=abc&status=done"} {
		if !ValidNordCallback(good) {
			t.Fatal("valid callback rejected")
		}
	}
	for _, bad := range []string{"https://login?exchange_token=abc", "nordvpn://login?exchange_token=abc&exchange_token=def", "nordvpn://login?exchange_token=abc&action=logout", "nordvpn://login?exchange_token=abc&dashboard=https://evil.test", "nordvpn://login?exchange_token=abc#secret", "nordvpn://login?exchange_token=abc%0a"} {
		if ValidNordCallback(bad) {
			t.Fatal("unsafe callback accepted")
		}
	}
}
