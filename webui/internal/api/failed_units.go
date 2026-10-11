package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const failedUnitOutputLimit = 256 * 1024

var failedUnitNamePattern = regexp.MustCompile(`^(?:[A-Za-z0-9_:.-]|\\x[0-9A-Fa-f]{2})+(?:@(?:[A-Za-z0-9_:.@-]|\\x[0-9A-Fa-f]{2})*)?\.(?:service|timer|mount|socket|path|target|automount|swap|device|scope|slice)$`)

func ValidFailedUnitName(unit string) bool {
	return len(unit) <= 255 && failedUnitNamePattern.MatchString(unit)
}

type FailedUnit struct {
	Unit        string `json:"unit"`
	Description string `json:"description"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
}

func (c *Client) failedUnitRequest(ctx context.Context, session, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/admin/systemd/failed"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = 8 * time.Second
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("failed unit diagnostics are unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return nil, ErrPasswordChangeRequired
	}
	if response.StatusCode != http.StatusOK {
		// Keep status without forwarding privileged response details.
		return nil, &ResponseError{StatusCode: response.StatusCode, Message: "Failed unit diagnostics are unavailable."}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, failedUnitOutputLimit+1))
	if err != nil || len(data) > failedUnitOutputLimit {
		return nil, errors.New("failed unit diagnostics are unavailable")
	}
	return data, nil
}

func (c *Client) AdminFailedUnits(ctx context.Context, session string) ([]FailedUnit, error) {
	data, err := c.failedUnitRequest(ctx, session, "")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Units []FailedUnit `json:"failed_units"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Units == nil || len(payload.Units) > 100 {
		return nil, errors.New("invalid failed unit list")
	}
	seen := make(map[string]bool)
	for _, unit := range payload.Units {
		if !ValidFailedUnitName(unit.Unit) || unit.ActiveState != "failed" || len(unit.Description) > 4096 || len(unit.SubState) > 64 || seen[unit.Unit] {
			return nil, errors.New("invalid failed unit list")
		}
		seen[unit.Unit] = true
	}
	return payload.Units, nil
}

func (c *Client) AdminFailedUnitJournal(ctx context.Context, session, unit string) ([]byte, error) {
	if !ValidFailedUnitName(unit) {
		return nil, errors.New("invalid failed unit identifier")
	}
	return c.failedUnitRequest(ctx, session, "/"+url.PathEscape(unit)+"/journal?limit=60")
}
