package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminStorageProvisionAPI interface {
	adminDiscoveryAPI
	AdminStorageProvisionDiscover(ctx context.Context, session string) (api.AdminStorageProvisionResponse, error)
	AdminStorageProvisionPlan(ctx context.Context, session string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error)
	AdminStorageProvisionApply(ctx context.Context, session string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error)
}

type storageProvisionCandidateView struct {
	Path       string
	Model      string
	Transport  string
	Size       string
	Free       string
	SystemDisk bool
}

type storageProvisionPageData struct {
	Title           string
	Version         string
	ManagementAPI   string
	CSRF            string
	Identity        api.SessionInfo
	Configuration   api.AdminConfigurationDiscovery
	WholeDisks      []storageProvisionCandidateView
	BlankPartitions []storageProvisionCandidateView
	FreeSpaceDisks  []storageProvisionCandidateView
	Form            api.AdminStorageProvisionRequest
	Plan            *api.AdminStorageProvisionResponse
	PlanDeviceSize  string
	PlanFreeSize    string
	PlanCreateSize  string
	Error           string
	Message         string
}

func (a *App) registerAdminStorageProvisionPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/storage-provision", a.storageProvisionPage)
	mux.HandleFunc("POST /settings/storage-provision/plan", a.storageProvisionPlan)
	mux.HandleFunc("POST /settings/storage-provision/apply", a.storageProvisionApply)
}

func (a *App) storageProvisionPage(w http.ResponseWriter, r *http.Request) {
	a.renderStorageProvisionPage(w, r, api.AdminStorageProvisionRequest{}, nil, "", storageProvisionResultMessage(r))
}

func (a *App) storageProvisionPlan(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.storageProvisionRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseStorageProvisionForm(r, false)
	if err != nil {
		a.renderStorageProvisionPage(w, r, request, nil, err.Error(), "")
		return
	}
	plan, err := client.AdminStorageProvisionPlan(r.Context(), session, request)
	if err != nil {
		a.renderStorageProvisionPage(w, r, request, nil, apiMessage(err, "Could not validate this storage operation."), "")
		return
	}
	a.renderStorageProvisionPage(w, r, request, &plan, "", "")
}

func (a *App) storageProvisionApply(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.storageProvisionRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseStorageProvisionForm(r, true)
	if err != nil {
		a.renderStorageProvisionPage(w, r, request, nil, err.Error(), "")
		return
	}
	result, err := client.AdminStorageProvisionApply(r.Context(), session, request)
	if err != nil {
		a.renderStorageProvisionPage(w, r, request, nil, apiMessage(err, "Could not provision this storage device."), "")
		return
	}
	if !result.Applied {
		a.renderStorageProvisionPage(w, r, request, &result, "Storage provisioning did not complete.", "")
		return
	}
	http.Redirect(w, r, "/settings/storage-provision?result=saved", http.StatusSeeOther)
}

func (a *App) storageProvisionRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminStorageProvisionAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, _, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminStorageProvisionAPI)
	if !ok {
		http.Error(w, "advanced storage provisioning is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) renderStorageProvisionPage(w http.ResponseWriter, r *http.Request, form api.AdminStorageProvisionRequest, plan *api.AdminStorageProvisionResponse, errorMessage, message string) {
	session, client, identity, ok := a.storageProvisionRequest(w, r, false)
	if !ok {
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	data := storageProvisionPageData{
		Title: "Advanced storage", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Configuration: configuration,
		Form: form, Plan: plan, Error: errorMessage, Message: message,
	}
	if configuration.Configured {
		discovery, err := client.AdminStorageProvisionDiscover(r.Context(), session)
		if err != nil {
			data.Error = apiMessage(err, "Advanced storage discovery is unavailable.")
		} else {
			data.WholeDisks = storageProvisionCandidates(discovery.WholeDisks)
			data.BlankPartitions = storageProvisionCandidates(discovery.BlankPartitions)
			data.FreeSpaceDisks = storageProvisionCandidates(discovery.FreeSpaceDisks)
		}
	}
	if plan != nil {
		data.PlanDeviceSize = formatOptionalBytes(plan.Proposed.SizeBytes)
		data.PlanFreeSize = humanBytes(plan.Proposed.FreeMiB * 1024 * 1024)
		data.PlanCreateSize = humanBytes(plan.Proposed.PlannedMiB * 1024 * 1024)
	}
	a.renderAdminDiscovery(w, "storage_provision.html", data)
}

func storageProvisionCandidates(items []api.AdminStorageProvisionCandidate) []storageProvisionCandidateView {
	out := make([]storageProvisionCandidateView, 0, len(items))
	for _, item := range items {
		out = append(out, storageProvisionCandidateView{
			Path: item.Path, Model: item.Model, Transport: item.Transport,
			Size: humanBytes(item.SizeBytes), Free: formatOptionalBytes(item.FreeBytes), SystemDisk: item.SystemDisk,
		})
	}
	return out
}

func parseStorageProvisionForm(r *http.Request, includeConfirmation bool) (api.AdminStorageProvisionRequest, error) {
	var out api.AdminStorageProvisionRequest
	if err := r.ParseForm(); err != nil {
		return out, errors.New("Could not read the storage provisioning form.")
	}
	out.Operation = strings.TrimSpace(r.FormValue("operation"))
	out.Device = strings.TrimSpace(r.FormValue("device"))
	out.MountPoint = strings.TrimSpace(r.FormValue("mount_point"))
	out.Path = strings.TrimSpace(r.FormValue("path"))
	out.SizeGiB = strings.TrimSpace(r.FormValue("size_gib"))
	if out.SizeGiB == "" {
		out.SizeGiB = "all"
	}
	out.Fingerprint = strings.TrimSpace(r.FormValue("fingerprint"))
	if includeConfirmation {
		out.Confirmation = r.FormValue("confirmation")
	}
	if out.Operation == "" || out.Device == "" || out.MountPoint == "" || out.Path == "" {
		return out, errors.New("Choose an operation, device, mount point and backup directory.")
	}
	return out, nil
}

func storageProvisionResultMessage(r *http.Request) string {
	if r.URL.Query().Get("result") == "saved" {
		return "Storage provisioned and activated as the JustVoxel backup destination. Minecraft was not restarted."
	}
	return ""
}
