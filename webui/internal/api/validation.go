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

const adminValidationPath = "/v1/admin/validation"

var adminValidationClientTimeout = 50 * time.Second

type AdminValidationResponse struct {
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

func (c *Client) AdminValidation(ctx context.Context, session string) (AdminValidationResponse, error) {
	var out AdminValidationResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+adminValidationPath, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminValidationClientTimeout
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	if err := decodeAdminValidationResponse(resp.Body, &out); err != nil {
		return AdminValidationResponse{}, fmt.Errorf("management API returned invalid validation response: %w", err)
	}
	if (out.OK && out.ExitCode != 0) || (!out.OK && out.ExitCode != 1) {
		return AdminValidationResponse{}, errors.New("management API returned inconsistent validation result")
	}
	return out, nil
}

func decodeAdminValidationResponse(reader io.Reader, target *AdminValidationResponse) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 128*1024))
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
	return nil
}
