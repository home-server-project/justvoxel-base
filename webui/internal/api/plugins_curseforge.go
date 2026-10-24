package api

import (
	"context"
	"net/http"
)

// CurseForgeAPIStatus is deliberately presence-only, including write responses.
type CurseForgeAPIStatus struct {
	Configured bool `json:"configured"`
}

func (c *Client) AdminCurseForgeAPI(ctx context.Context, session, method, key string) (CurseForgeAPIStatus, error) {
	var out CurseForgeAPIStatus
	var body any
	if method == http.MethodPut {
		body = struct {
			Key string `json:"key"`
		}{key}
	}
	err := c.do(ctx, method, "/v1/admin/plugins/curseforge-api", session, body, &out)
	return out, err
}
