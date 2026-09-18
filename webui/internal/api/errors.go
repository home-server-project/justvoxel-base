package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ResponseError struct {
	StatusCode int
	Message    string
}

func (e *ResponseError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("management API HTTP %d", e.StatusCode)
}

func ErrorMessage(err error) (string, bool) {
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || strings.TrimSpace(responseErr.Message) == "" {
		return "", false
	}
	return responseErr.Message, true
}

func readResponseError(resp *http.Response) error {
	limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(limited, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
		return &ResponseError{StatusCode: resp.StatusCode, Message: payload.Error}
	}
	return &ResponseError{StatusCode: resp.StatusCode}
}
