package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type DiagnosticLogEntry struct {
	Category      string `json:"category"`
	LogID         string `json:"log_id"`
	OperationType string `json:"operation_type,omitempty"`
	OperationID   string `json:"operation_id,omitempty"`
	State         string `json:"state,omitempty"`
	Timestamp     string `json:"timestamp"`
}

func validDiagnosticLogIdentifier(category, id string) bool {
	return (category == "setup" || category == "migration" || category == "restore" || category == "plugins" || category == "reset" || category == "operation" || category == "support" || category == "network") && persistentOperationIDPattern.MatchString(id)
}

func (c *Client) AdminDiagnosticLogs(ctx context.Context, session string) ([]DiagnosticLogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/admin/logs", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = adminSetupDiagnosticClientTimeout
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return nil, ErrPasswordChangeRequired
	}
	if response.StatusCode != http.StatusOK {
		return nil, readResponseError(response)
	}
	var payload struct {
		Logs []DiagnosticLogEntry `json:"logs"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 512*1024))
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Logs) > 6*200 {
		return nil, errors.New("management API returned too many diagnostic logs")
	}
	for _, entry := range payload.Logs {
		if !validDiagnosticLogIdentifier(entry.Category, entry.LogID) {
			return nil, errors.New("management API returned invalid diagnostic log identifier")
		}
	}
	return payload.Logs, nil
}

func (c *Client) AdminDiagnosticLog(ctx context.Context, session, category, id string) ([]byte, error) {
	if !validDiagnosticLogIdentifier(category, id) {
		return nil, errors.New("invalid diagnostic log identifier")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/admin/logs/"+category+"/"+id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = adminSetupDiagnosticClientTimeout
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return nil, ErrPasswordChangeRequired
	}
	if response.StatusCode != http.StatusOK {
		return nil, readResponseError(response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4*1024*1024 {
		return nil, errors.New("management API returned oversized diagnostic log")
	}
	return data, nil
}

// AdminGenerateSupportReport has no caller-controlled paths, units or command arguments.
func (c *Client) AdminGenerateSupportReport(ctx context.Context, session string) (DiagnosticLogEntry, error) {
	return c.adminGenerateReport(ctx, session, "support")
}

func (c *Client) AdminGenerateNetworkReport(ctx context.Context, session string) (DiagnosticLogEntry, error) {
	return c.adminGenerateReport(ctx, session, "network")
}

func (c *Client) adminGenerateReport(ctx context.Context, session, category string) (DiagnosticLogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/admin/logs/"+category, nil)
	if err != nil {
		return DiagnosticLogEntry{}, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	// Four fixed commands may take four seconds each, plus bounded collection.
	client.Timeout = 25 * time.Second
	if category == "network" {
		client.Timeout = 65 * time.Second
	}
	response, err := client.Do(req)
	if err != nil {
		return DiagnosticLogEntry{}, errors.New("support report is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return DiagnosticLogEntry{}, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return DiagnosticLogEntry{}, ErrPasswordChangeRequired
	}
	if response.StatusCode != http.StatusOK {
		return DiagnosticLogEntry{}, readResponseError(response)
	}
	var payload struct {
		OK  bool               `json:"ok"`
		Log DiagnosticLogEntry `json:"log"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return DiagnosticLogEntry{}, errors.New("invalid support report response")
	}
	_, timestampErr := time.Parse(time.RFC3339Nano, payload.Log.Timestamp)
	if !payload.OK || payload.Log.Category != category || !validDiagnosticLogIdentifier(payload.Log.Category, payload.Log.LogID) || timestampErr != nil || payload.Log.OperationID != "" || payload.Log.OperationType != "" || payload.Log.State != "" {
		return DiagnosticLogEntry{}, errors.New("invalid support report identity")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return DiagnosticLogEntry{}, errors.New("invalid support report response")
	}
	return payload.Log, nil
}
