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

func TestReadResponseErrorPreservesOnlyValidDiagnosticID(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	for _, candidate := range []string{id, "../unsafe", ""} {
		resp := &http.Response{StatusCode: http.StatusConflict, Body: io.NopCloser(strings.NewReader(`{"error":"Provider review failed","diagnostic_id":"` + candidate + `"}`))}
		err, ok := readResponseError(resp).(*ResponseError)
		if !ok || err.Message != "Provider review failed" {
			t.Fatal(err)
		}
		want := ""
		if candidate == id {
			want = id
		}
		if err.DiagnosticID != want {
			t.Fatal("unsafe or missing diagnostic ID", err.DiagnosticID)
		}
	}
}
