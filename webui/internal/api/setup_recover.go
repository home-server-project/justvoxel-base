package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (c *Client) AdminSetupRecover(ctx context.Context, session, operationID string) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	if !persistentOperationIDPattern.MatchString(operationID) {
		return out, errors.New("invalid setup operation id")
	}
	body, err := json.Marshal(struct {
		OperationID   string `json:"operation_id"`
		RetryRecovery bool   `json:"retry_recovery"`
	}{operationID, true})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/admin/setup/recover", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = 60 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return out, ErrPasswordChangeRequired
	}
	if resp.StatusCode != http.StatusOK {
		return out, readResponseError(resp)
	}
	if err := decodePersistentOperationResponse(resp.Body, &out); err != nil || out.Operation == nil || out.Operation.OperationID != operationID {
		return PersistentOperationResponse{}, errors.New("management API returned invalid setup recovery response")
	}
	return out, nil
}
