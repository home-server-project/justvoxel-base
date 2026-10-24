package api

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var nordExchangePattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+={0,2}$`)

func ValidNordCallback(value string) bool {
	if len(value) == 0 || len(value) > 2048 || strings.ContainsAny(value, "<>\"'\\#") || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	httpsSuccess := strings.HasPrefix(value, "https://nordaccount.com/product/nordvpn/login/success?") &&
		u.Scheme == "https" && u.Host == "nordaccount.com" && u.Path == "/product/nordvpn/login/success" && u.RawPath == ""
	customCallback := strings.HasPrefix(value, "nordvpn://") && u.Scheme == "nordvpn" && u.Host == "login" && u.Path == ""
	if !httpsSuccess && !customCallback {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	token := q.Get("exchange_token")
	if err != nil || len(q["exchange_token"]) != 1 || len(token) > 1024 || !nordExchangePattern.MatchString(token) || (httpsSuccess && len(q) != 1) {
		return false
	}
	for key, values := range q {
		if len(values) != 1 {
			return false
		}
		switch key {
		case "exchange_token":
		case "action":
			if values[0] != "login" {
				return false
			}
		case "status":
			if values[0] != "done" {
				return false
			}
		default:
			return false
		}
	}
	return true
}
