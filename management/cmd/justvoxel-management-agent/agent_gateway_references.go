package main

import (
	"database/sql"
	"errors"
	"strings"
)

// Private paths are never part of the external contract. Discovery returns
// connection-bound opaque selectors which the Agent resolves before handing a
// typed request to the existing operation validator. They confer no permission.
var gatewayPathFields = map[string]bool{
	"mountpoints": true, "system_disks": true,
	"path": true, "device": true, "mount_point": true, "current_mount_point": true,
	"created_device": true, "mountpoint": true, "parent": true, "archive_path": true,
	"data_path": true, "backup_path": true, "destination": true, "source": true,
	"root": true, "root_relative": true, "selected_root": true, "expected_source": true,
	"data_source": true, "data_destination": true, "backup_destination": true, "backup_source": true,
}

func (s *webUIStore) gatewayReference(connectionID, value string) (string, error) {
	var ref string
	err := s.db.QueryRow(`SELECT reference FROM gateway_references WHERE connection_id=? AND value=?`, connectionID, value).Scan(&ref)
	if err == nil {
		return ref, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	ref, err = gatewayRandom("jvref_")
	if err != nil {
		return "", err
	}
	if _, err = s.db.Exec(`INSERT INTO gateway_references(connection_id,reference,value) VALUES(?,?,?) ON CONFLICT(connection_id,value) DO NOTHING`, connectionID, ref, value); err != nil {
		return "", err
	}
	err = s.db.QueryRow(`SELECT reference FROM gateway_references WHERE connection_id=? AND value=?`, connectionID, value).Scan(&ref)
	return ref, err
}
func (s *webUIStore) gatewayResolveReferences(id string, value any) (any, error) {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "jvref_") {
			var path string
			if err := s.db.QueryRow(`SELECT value FROM gateway_references WHERE connection_id=? AND reference=?`, id, v).Scan(&path); err != nil {
				return nil, errors.New("unknown operation reference")
			}
			return path, nil
		}
	case []any:
		for i, item := range v {
			resolved, err := s.gatewayResolveReferences(id, item)
			if err != nil {
				return nil, err
			}
			v[i] = resolved
		}
	case map[string]any:
		for key, item := range v {
			resolved, err := s.gatewayResolveReferences(id, item)
			if err != nil {
				return nil, err
			}
			v[key] = resolved
		}
	}
	return value, nil
}
func (s *webUIStore) gatewayPublicResult(id string, value any) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			if !gatewayPublicFields[key] {
				continue
			}
			if paths, ok := item.([]any); ok && gatewayPathFields[key] {
				safePaths := []any{}
				for _, value := range paths {
					if path, ok := value.(string); ok && path != "" {
						ref, err := s.gatewayReference(id, path)
						if err != nil {
							return nil, err
						}
						safePaths = append(safePaths, ref)
					} else {
						safe, err := s.gatewayPublicResult(id, value)
						if err != nil {
							return nil, err
						}
						safePaths = append(safePaths, safe)
					}
				}
				out[key] = safePaths
				continue
			}
			if path, ok := item.(string); ok && gatewayPathFields[key] && path != "" && (strings.Contains(path, "/") || (key != "source" && key != "destination")) {
				ref, err := s.gatewayReference(id, path)
				if err != nil {
					return nil, err
				}
				out[key] = ref
				continue
			}
			safe, err := s.gatewayPublicResult(id, item)
			if err != nil {
				return nil, err
			}
			out[key] = safe
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			safe, err := s.gatewayPublicResult(id, item)
			if err != nil {
				return nil, err
			}
			out = append(out, safe)
		}
		return out, nil
	default:
		return gatewayPublicValue(value), nil
	}
}
