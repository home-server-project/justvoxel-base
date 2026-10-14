package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const AddonUploadLimit = (128 << 20) + (64 << 10)

func ValidAddonKind(kind string) bool {
	switch kind {
	case "placeholderapi_expansion", "geyser_extension", "bluemap_addon", "skript_script", "worldedit_schematic":
		return true
	}
	return false
}

type AddonEntry struct {
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Target   string `json:"target_relative_path"`
	SHA512   string `json:"sha512"`
	Name     string `json:"display_name,omitempty"`
	Identity string `json:"descriptor_identity,omitempty"`
}
type AddonHost struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	Available bool   `json:"available"`
}
type AddonItem struct {
	AddonEntry
	State        string `json:"state"`
	Management   string `json:"management"`
	UpdateStatus string `json:"update_status"`
}
type AddonSection struct {
	Kind        string      `json:"kind"`
	Name        string      `json:"name"`
	Accept      string      `json:"accept"`
	Host        AddonHost   `json:"host"`
	Destination string      `json:"destination"`
	Files       []AddonItem `json:"files"`
}
type AddonInventory struct {
	Server   PluginServerContext `json:"server"`
	Sections []AddonSection      `json:"sections"`
}
type AddonPlan struct {
	AddonEntry
	Size                 int64               `json:"size"`
	Destination          string              `json:"destination"`
	Host                 AddonHost           `json:"host"`
	Server               PluginServerContext `json:"server"`
	RestartRequired      bool                `json:"restart_required"`
	Validation           string              `json:"validation"`
	Online               int                 `json:"online"`
	Players              []string            `json:"players"`
	ConfirmationRequired bool                `json:"confirmation_required"`
	Fingerprint          string              `json:"plan_fingerprint"`
}

func (c *Client) AdminAddonInventory(ctx context.Context, session string) (AddonInventory, error) {
	var out AddonInventory
	err := c.pluginRead(ctx, session, "/v1/admin/plugins/addons", &out)
	return out, err
}

// Body is the bounded streaming WebUI multipart proxy; there is no staging copy.
func (c *Client) AdminAddonUpload(ctx context.Context, session, contentType string, body io.Reader, apply bool) (AddonPlan, PersistentOperationResponse, error) {
	var plan AddonPlan
	var operation PersistentOperationResponse
	path := "/v1/admin/plugins/addons/plan"
	if apply {
		path = "/v1/admin/plugins/addons/install"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://unix"+path, body)
	if err != nil {
		return plan, operation, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	client := *c.http
	client.Timeout = 100 * time.Second
	response, err := client.Do(req)
	if err != nil {
		return plan, operation, err
	}
	defer response.Body.Close()
	if response.StatusCode == 401 {
		return plan, operation, ErrUnauthorized
	}
	if response.StatusCode == 403 {
		return plan, operation, ErrPasswordChangeRequired
	}
	if response.StatusCode != 200 && response.StatusCode != 202 {
		var failure struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&failure)
		message := failure.Error
		if message == "" {
			message = failure.Message
		}
		if message == "" {
			message = "Add-ons upload rejected"
		}
		return plan, operation, &ResponseError{StatusCode: response.StatusCode, Message: message}
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if apply {
		err = decoder.Decode(&operation)
	} else {
		err = decoder.Decode(&plan)
	}
	if err != nil {
		return plan, operation, errors.New("Invalid Add-ons response")
	}
	return plan, operation, nil
}
func (c *Client) AdminAddonRecover(ctx context.Context, session string, request PluginInstallAcknowledgeRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	err := c.pluginPost(ctx, session, "/v1/admin/plugins/addons/recover", request, &out)
	return out, err
}
