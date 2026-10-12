package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

func (c *Client) AdminOperationTrace(ctx context.Context, session, id string) ([]byte, error) {
	if !persistentOperationIDPattern.MatchString(id) {
		return nil, errors.New("invalid operation id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/admin/operations/"+id+"/trace", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Accept", "text/plain")
	client := *c.http
	client.Timeout = 8 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("operation trace is unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, ErrPasswordChangeRequired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &ResponseError{StatusCode: resp.StatusCode, Message: "Operation trace is unavailable."}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, errors.New("operation trace is unavailable")
	}
	return data, nil
}
