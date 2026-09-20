package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"time"
)

const adminRestoreDiscoveryHelper = "/usr/libexec/justvoxel/mjust/admin-restore-discovery-json"

var (
	adminRestoreDiscoveryTimeout = 15 * time.Second
	adminRestoreBackupIDPattern  = regexp.MustCompile(`^minecraft-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}\.tar\.gz$`)
)

var runAdminRestoreDiscoveryHelper = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, adminRestoreDiscoveryHelper).CombinedOutput()
}

type adminRestoreMetadataMinecraft struct {
	VersionMode           string `json:"version_mode"`
	ConfiguredVersion     string `json:"configured_version"`
	ServerReportedVersion string `json:"server_reported_version,omitempty"`
}

type adminRestoreMetadataBedrock struct {
	Enabled               bool   `json:"enabled"`
	GeyserReportedVersion string `json:"geyser_reported_version,omitempty"`
	FloodgateConfigured   bool   `json:"floodgate_configured"`
}

type adminRestoreMetadataJustVoxel struct {
	Variant string `json:"variant,omitempty"`
}

type adminRestoreBackupMetadata struct {
	CreatedAt string                          `json:"created_at,omitempty"`
	Minecraft adminRestoreMetadataMinecraft   `json:"minecraft"`
	Bedrock   adminRestoreMetadataBedrock     `json:"bedrock"`
	JustVoxel adminRestoreMetadataJustVoxel   `json:"justvoxel"`
}

type adminRestoreBackup struct {
	ID             string                      `json:"id"`
	CreatedAt      string                      `json:"created_at"`
	SizeBytes      uint64                      `json:"size_bytes"`
	MetadataStatus string                      `json:"metadata_status"`
	Metadata       *adminRestoreBackupMetadata `json:"metadata,omitempty"`
}

type adminRestoreBackupsResponse struct {
	Backups []adminRestoreBackup `json:"backups"`
}

func registerAdminRestoreRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/restore/backups", s.adminRestoreBackups)
}

func (s *server) adminRestoreBackups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), adminRestoreDiscoveryTimeout)
	defer cancel()
	output, err := runAdminRestoreDiscoveryHelper(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "restore backup discovery is unavailable")
		return
	}

	result, err := decodeAdminRestoreBackups(output)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "restore backup discovery returned invalid data")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeAdminRestoreBackups(output []byte) (adminRestoreBackupsResponse, error) {
	var result adminRestoreBackupsResponse
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return result, errors.New("unexpected trailing JSON value")
		}
		return result, err
	}
	if result.Backups == nil {
		result.Backups = []adminRestoreBackup{}
	}

	seen := make(map[string]struct{}, len(result.Backups))
	for i := range result.Backups {
		backup := &result.Backups[i]
		if !adminRestoreBackupIDPattern.MatchString(backup.ID) {
			return result, errors.New("invalid restore backup id")
		}
		if _, exists := seen[backup.ID]; exists {
			return result, errors.New("duplicate restore backup id")
		}
		seen[backup.ID] = struct{}{}
		if _, err := time.Parse(time.RFC3339, backup.CreatedAt); err != nil {
			return result, errors.New("invalid restore backup timestamp")
		}
		switch backup.MetadataStatus {
		case "valid":
			if backup.Metadata == nil {
				return result, errors.New("valid restore metadata is missing")
			}
		case "missing", "invalid":
			if backup.Metadata != nil {
				return result, errors.New("non-valid restore metadata must be omitted")
			}
		default:
			return result, errors.New("invalid restore metadata status")
		}
	}
	return result, nil
}
