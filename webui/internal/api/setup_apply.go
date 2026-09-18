package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

const adminSetupApplyPath = "/v1/admin/setup/apply"

var (
	adminSetupApplyClientTimeout = 30 * time.Second
	setupApplyFingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type AdminSetupApplyRequest struct {
	PlanFingerprint string                `json:"plan_fingerprint"`
	Request         AdminSetupPlanRequest `json:"request"`
	SMBPassword     string                `json:"smb_password,omitempty"`
	EULAAccepted    bool                  `json:"eula_accepted"`
}

type AdminSetupApplyResponse struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code,omitempty"`
	Error     string               `json:"error,omitempty"`
	Created   bool                 `json:"created"`
	Operation *PersistentOperation `json:"operation,omitempty"`
}

func (c *Client) AdminSetupApply(ctx context.Context, session string, request AdminSetupApplyRequest) (AdminSetupApplyResponse, error) {
	var out AdminSetupApplyResponse
	if !setupApplyFingerprintPattern.MatchString(request.PlanFingerprint) {
		return out, errors.New("invalid reviewed setup fingerprint")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+adminSetupApplyPath, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminSetupApplyClientTimeout
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
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		if err := decodeAdminSetupApplyResponse(resp.Body, &out); err != nil {
			return out, fmt.Errorf("management API returned invalid setup apply response: %w", err)
		}
		return out, nil
	}
	if err := decodeAdminSetupApplyResponse(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid setup apply response: %w", err)
	}
	return out, &ResponseError{StatusCode: resp.StatusCode, Message: out.Error}
}

func decodeAdminSetupApplyResponse(reader io.Reader, target *AdminSetupApplyResponse) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 32*1024))
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
	if target.Operation != nil {
		if !persistentOperationIDPattern.MatchString(target.Operation.OperationID) {
			return errors.New("invalid operation id in response")
		}
		if target.Operation.PlanFingerprint != "" && !setupApplyFingerprintPattern.MatchString(target.Operation.PlanFingerprint) {
			return errors.New("invalid setup fingerprint in operation response")
		}
	}
	return nil
}
