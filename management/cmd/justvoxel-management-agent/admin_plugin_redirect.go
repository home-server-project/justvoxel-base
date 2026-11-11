package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Follow only explicitly validated CurseForge artifact redirects. Other providers
// retain their existing no-redirect behavior. Never retain Location or URL secrets.
func requestPluginArtifact(ctx context.Context, a pluginInstallArtifact, evidence map[string]string) (*http.Response, error) {
	client := *pluginDownloadClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	current, _ := url.Parse(a.URL) // caller has validated exact official metadata
	key := ""
	if pluginSource(a.Source) == "CurseForge" {
		var err error
		key, err = readCurseForgeKey()
		if err != nil {
			return nil, errors.New("CurseForge download requires a configured API key")
		}
	}
	seen := map[string]bool{}
	for hop := 0; ; hop++ {
		evidence["redirect_hops"] = strconv.Itoa(hop)
		if seen[current.String()] {
			return nil, errors.New("CurseForge artifact redirect loop detected")
		}
		seen[current.String()] = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, errors.New("Unsafe artifact request")
		}
		if key != "" {
			if !safeCurseForgeArtifactURL(current.String()) {
				return nil, errors.New("Unsafe CurseForge artifact destination")
			}
			req.Header.Set("x-api-key", key)
		}
		req.Header.Set("User-Agent", "JustVoxel/PluginWorkspace-Stage2")
		response, err := client.Do(req)
		if err != nil {
			return nil, errors.New("Plugin download failed")
		}
		if pluginSource(a.Source) != "CurseForge" {
			return response, nil
		}
		switch response.StatusCode {
		case 301, 302, 303, 307, 308:
		default:
			return response, nil
		}
		response.Body.Close()
		evidence["http_status"] = strconv.Itoa(response.StatusCode)
		evidence["redirect_source"] = current.Hostname() + current.EscapedPath()
		location := response.Header.Get("Location")
		next, err := url.Parse(location)
		if err != nil || location == "" {
			return nil, errors.New("CurseForge artifact redirect lacks a safe destination")
		}
		next = current.ResolveReference(next)
		// Strip userinfo/query/fragment from diagnostic evidence even on rejection.
		evidence["redirect_destination"] = next.Hostname() + next.EscapedPath()
		if !safeCurseForgeArtifactURL(next.String()) {
			return nil, errors.New("CurseForge artifact redirected outside the trusted CDN domain.")
		}
		if hop >= 5 {
			return nil, errors.New("CurseForge artifact exceeded five redirects")
		}
		current = next
	}
}
