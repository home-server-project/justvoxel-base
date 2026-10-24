package api

import (
	"strings"
	"testing"
)

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

func TestNordHTTPSCompletionStrictValidation(t *testing.T) {
	for _, good := range []string{
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=YWJj%2B%2F%3D",
		"nordvpn://login?exchange_token=abc",
		"nordvpn://login?action=login&exchange_token=abc&status=done",
	} {
		if !ValidNordCallback(good) {
			t.Fatal("valid completion rejected")
		}
	}
	for _, bad := range []string{
		"http://nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"https://www.nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"https://my.nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"https://api.nordvpn.com/product/nordvpn/login/success?exchange_token=abc",
		"https://nordaccount.com.evil.test/product/nordvpn/login/success?exchange_token=abc",
		"https://evil.nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"https://nordaccount.com:443/product/nordvpn/login/success?exchange_token=abc",
		"https://user:password@nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"https://nordaccount.com/wrong?exchange_token=abc",
		"https://nordaccount.com/product/nordvpn/login/%73uccess?exchange_token=abc",
		"https://nordaccount.com/product/nordvpn/login/success/?exchange_token=abc",
		"https://nordaccount.com/product/nordvpn/login/other/../success?exchange_token=abc",
		"https://nordaccount.com/product/nordvpn/login%2fsuccess?exchange_token=abc",
		"https://nordaccount.com/product/nordvpn/login/success",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc&exchange_token=def",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc&extra=value",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc&action=login",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc#fragment",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc#",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%0a",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%20",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%3B",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc%ZZ",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc+def",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc\r\n",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc def",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=abc\u0000",
		"HTTPS://nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"//nordaccount.com/product/nordvpn/login/success?exchange_token=abc",
		"nordvpn:login?exchange_token=abc",
		"nordvpn://login:443?exchange_token=abc",
		"nordvpn://login?exchange_token=abc&status=bad",
		"nordvpn://login?exchange_token=abc&action=login&action=login",
		"nordvpn://login?exchange_token=abc&extra=value",
		"nordvpn://login/path?exchange_token=abc",
		"nordvpn://user@login?exchange_token=abc",
		"nordvpn://login?exchange_token=abc#fragment",
		"nordvpn://login?exchange_token=abc&exchange_token=def",
		"nordvpn://login?exchange_token=abc%0a",
		"https://nordaccount.com/product/nordvpn/login/success?exchange_token=" + strings.Repeat("a", 1025),
		"nordvpn://login?exchange_token=" + strings.Repeat("a", 1025),
		strings.Repeat("a", 2049),
	} {
		if ValidNordCallback(bad) {
			t.Fatal("unsafe completion accepted")
		}
	}
}
