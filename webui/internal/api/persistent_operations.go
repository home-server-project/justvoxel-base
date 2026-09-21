package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var persistentOperationIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type PersistentOperationRollback struct {
	State  string `json:"state"`
	Result string `json:"result,omitempty"`
}

type PersistentOperation struct {
	SchemaVersion   string                      `json:"schema_version"`
	OperationID     string                      `json:"operation_id"`
	OperationType   string                      `json:"operation_type"`
	PlanFingerprint string                      `json:"plan_fingerprint"`
	State           string                      `json:"state"`
	Stage           string                      `json:"stage"`
	Status          string                      `json:"status"`
	StartedAt       string                      `json:"started_at"`
	UpdatedAt       string                      `json:"updated_at"`
	FinishedAt      string                      `json:"finished_at,omitempty"`
	InterruptedAt   string                      `json:"interrupted_at,omitempty"`
	Rollback        PersistentOperationRollback `json:"rollback"`
}

type PersistentOperationResponse struct {
	Operation *PersistentOperation `json:"operation"`
}

func (c *Client) AdminOperation(ctx context.Context, session, id string) (PersistentOperationResponse, error) {
	if !persistentOperationIDPattern.MatchString(id) {
		return PersistentOperationResponse{}, errors.New("invalid operation id")
	}
	return c.getPersistentOperation(ctx, session, "/v1/admin/operations/"+id)
}

func (c *Client) AdminCurrentSetupOperation(ctx context.Context, session string) (PersistentOperationResponse, error) {
	return c.getPersistentOperation(ctx, session, "/v1/admin/setup/current-operation")
}

func (c *Client) AdminCurrentRestoreOperation(ctx context.Context, session string) (PersistentOperationResponse, error) {
	return c.getPersistentOperation(ctx, session, "/v1/admin/restore/current-operation")
}

func (c *Client) AdminCurrentDataMigrationOperation(ctx context.Context, session string) (PersistentOperationResponse, error) {
	return c.getPersistentOperation(ctx, session, "/v1/admin/data-migration/current-operation")
}

func (c *Client) getPersistentOperation(ctx context.Context, session, path string) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)
	resp, err := c.http.Do(req)
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	if err := decodePersistentOperationResponse(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid operation response: %w", err)
	}
	return out, nil
}

func decodePersistentOperationResponse(reader io.Reader, target *PersistentOperationResponse) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	if target.Operation != nil && !persistentOperationIDPattern.MatchString(target.Operation.OperationID) {
		return errors.New("invalid operation id in response")
	}
	return nil
}
