package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReadResponseErrorPreservesMessage(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":"Password is too simple"}`)),
	}
	err := readResponseError(resp)
	message, ok := ErrorMessage(err)
	if !ok {
		t.Fatalf("expected response error message, got %T: %v", err, err)
	}
	if message != "Password is too simple" {
		t.Fatalf("unexpected message %q", message)
	}
}

func TestReadResponseErrorHidesUnstructuredBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader("internal proxy detail")),
	}
	err := readResponseError(resp)
	if _, ok := ErrorMessage(err); ok {
		t.Fatalf("unstructured backend body was exposed: %v", err)
	}
}
