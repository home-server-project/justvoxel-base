package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const adminSetupDiagnosticSessionPath = "/v1/admin/setup/diagnostics/session"

var adminSetupDiagnosticClientTimeout = 15 * time.Second

type AdminSetupDiagnosticSessionResponse struct {
	OK            bool   `json:"ok"`
	SchemaVersion string `json:"schema_version"`
	SessionID     string `json:"session_id"`
}

type adminSetupDiagnosticEventRequest struct {
	Event  string            `json:"event"`
	Values map[string]string `json:"values"`
}

func (c *Client) AdminSetupDiagnosticSession(ctx context.Context, session, interfaceName string) (AdminSetupDiagnosticSessionResponse, error) {
	var out AdminSetupDiagnosticSessionResponse
	status, err := c.setupDiagnosticJSON(ctx, http.MethodPost, adminSetupDiagnosticSessionPath, session, map[string]string{"interface": interfaceName}, &out)
	if err != nil {
		return out, err
	}
	if status != http.StatusCreated || !out.OK || out.SchemaVersion != "v1" || !persistentOperationIDPattern.MatchString(out.SessionID) {
		return out, errors.New("management API returned invalid setup diagnostic session")
	}
	return out, nil
}

func (c *Client) AdminSetupDiagnosticEvent(ctx context.Context, session, id, event string, values map[string]string) error {
	if !persistentOperationIDPattern.MatchString(id) {
		return errors.New("invalid setup diagnostic id")
	}
	status, err := c.setupDiagnosticJSON(ctx, http.MethodPost, "/v1/admin/setup/diagnostics/"+id+"/event", session, adminSetupDiagnosticEventRequest{Event: event, Values: values}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return errors.New("management API rejected setup diagnostic event")
	}
	return nil
}

func (c *Client) AdminSetupDiagnosticLog(ctx context.Context, session, id string) ([]byte, error) {
	if !persistentOperationIDPattern.MatchString(id) {
		return nil, errors.New("invalid setup diagnostic id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/admin/setup/diagnostics/"+id+"/log", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = adminSetupDiagnosticClientTimeout
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, ErrPasswordChangeRequired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, readResponseError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > 4*1024*1024 {
		return nil, errors.New("management API returned invalid setup diagnostic log")
	}
	return data, nil
}

func (c *Client) setupDiagnosticJSON(ctx context.Context, method, path, session string, body, out any) (int, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = adminSetupDiagnosticClientTimeout
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return resp.StatusCode, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return resp.StatusCode, ErrPasswordChangeRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, readResponseError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return resp.StatusCode, nil
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}
