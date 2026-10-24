package api

import (
	"net/url"
	"regexp"
	"strings"
)

var nordExchangePattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+={0,2}$`)

func ValidNordCallback(value string) bool {
	if len(value) == 0 || len(value) > 2048 || strings.ContainsAny(value, " \t\r\n<>\"'\\") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "nordvpn" || u.Host != "login" || u.Path != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["exchange_token"]) != 1 || len(q.Get("exchange_token")) > 1024 || !nordExchangePattern.MatchString(q.Get("exchange_token")) {
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
