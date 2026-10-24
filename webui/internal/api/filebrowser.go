package api

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type FileBrowserStatus struct {
	Availability string `json:"availability"`
	Ready        bool   `json:"ready"`
	State        string `json:"state"`
}

func (c *Client) AdminFileBrowser(ctx context.Context, session string) (FileBrowserStatus, error) {
	var out FileBrowserStatus
	err := c.do(ctx, http.MethodGet, "/v1/admin/filebrowser", session, nil, &out)
	return out, err
}
func (c *Client) AdminFileBrowserAction(ctx context.Context, session, action, availability string) error {
	switch action {
	case "start", "stop", "availability":
	default:
		return fmt.Errorf("invalid File Browser action")
	}
	client := *c.http
	client.Timeout = 40 * time.Second
	caller := &Client{http: &client}
	return caller.do(ctx, http.MethodPost, "/v1/admin/filebrowser/"+action, session, map[string]string{"availability": availability}, nil)
}
