package api

import (
	"context"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type AdminDateTimeState struct {
	Timezone string `json:"timezone"`
	LocalDate string `json:"local_date"`
	LocalTime string `json:"local_time"`
	Automatic bool `json:"automatic"`
	Synchronized bool `json:"synchronized"`
}

type AdminDateTimeChange struct {
	Timezone string `json:"timezone"`
	Automatic bool `json:"automatic"`
	Date string `json:"date,omitempty"`
	Time string `json:"time,omitempty"`
}

func (c *Client) AdminDateTime(ctx context.Context, session string) (AdminDateTimeState, error) {
	var out AdminDateTimeState
	err := c.do(ctx, http.MethodGet, "/v1/admin/date-time", session, nil, &out)
	return out, err
}

func (c *Client) AdminDateTimeApply(ctx context.Context, session string, request AdminDateTimeChange) (AdminDateTimeState, error) {
	var out AdminDateTimeState
	data, err := json.Marshal(request)
	if err != nil { return out, err }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/admin/date-time", bytes.NewReader(data))
	if err != nil { return out, err }
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	client := *c.http
	client.Timeout = 25 * time.Second
	resp, err := client.Do(req)
	if err != nil { return out, fmt.Errorf("management API unavailable: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized { return out, ErrUnauthorized }
	if resp.StatusCode == http.StatusForbidden { return out, ErrPasswordChangeRequired }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return out, readResponseError(resp) }
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}
