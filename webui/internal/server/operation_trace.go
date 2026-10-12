package server

import (
	"context"
	"net/http"
)

type operationTraceAPI interface {
	AdminOperationTrace(context.Context, string, string) ([]byte, error)
}

func (a *App) operationTrace(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !setupOperationIDPattern.MatchString(id) {
		writeSystemWorkspaceError(w, 400, "Invalid operation identifier.")
		return
	}
	client, ok := a.api.(operationTraceAPI)
	if !ok {
		writeSystemWorkspaceError(w, 503, "Operation trace is unavailable.")
		return
	}
	data, err := client.AdminOperationTrace(r.Context(), session, id)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Operation trace is unavailable.")
		return
	}
	if len(data) > 1024*1024 {
		writeSystemWorkspaceError(w, 502, "Operation trace is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
