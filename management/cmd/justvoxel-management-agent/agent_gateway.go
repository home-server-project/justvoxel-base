package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const gatewayBodyLimit = 1 << 20

type gatewayOperation struct {
	Name         string           `json:"name"`
	Method       string           `json:"method"`
	Path         string           `json:"path"`
	MinLevel     int              `json:"min_level"`
	Instance     bool             `json:"instance"`
	AllInstances bool             `json:"all_instances"`
	BodySchema   map[string]any   `json:"body_schema,omitempty"`
	Handler      http.HandlerFunc `json:"-"`
}

// This is the complete external capability policy. A handler can be reached only
// by a catalog entry; neither transport accepts a Management API path.
func (s *server) gatewayOperations() []gatewayOperation {
	ops := []gatewayOperation{}
	add := func(name, method, path string, level int, instance, all bool, h http.HandlerFunc) {
		ops = append(ops, gatewayOperation{Name: name, Method: method, Path: "/api/v1" + path, MinLevel: level, Instance: instance, AllInstances: all, Handler: h, BodySchema: gatewayRequestSchema(name, method)})
	}
	add("appliance_status", "GET", "/status", 1, false, false, s.status)
	add("instances", "GET", "/instances", 1, false, false, s.gatewayInstanceInventory)
	add("instance_status", "GET", "/instances/{id}/status", 1, true, false, s.instanceRuntimeStatus)
	add("instance_configuration", "GET", "/instances/{id}/configuration", 1, true, false, s.instanceRuntimeConfiguration)
	add("players", "GET", "/instances/{id}/players", 1, true, false, s.instanceMaintenanceRead)
	add("logs", "GET", "/instances/{id}/logs", 1, true, false, s.gatewayLogs)
	add("backups", "GET", "/instances/{id}/backups", 1, true, false, s.instanceBackupRead)
	add("backup_settings", "GET", "/instances/{id}/backups/settings", 1, true, false, s.instanceBackupRead)
	add("backup_status", "GET", "/instances/{id}/backups/status", 1, true, false, s.instanceBackupRead)
	add("whitelist", "GET", "/instances/{id}/whitelist", 2, true, false, s.whitelistList)
	add("whitelist_change", "POST", "/instances/{id}/whitelist", 2, true, false, s.whitelistChange)
	for _, action := range []string{"start", "restart", "stop"} {
		level := 2
		if action == "stop" {
			level = 3
		}
		add("instance_"+action, "POST", "/instances/{id}/"+action, level, true, false, func(w http.ResponseWriter, r *http.Request) { s.instanceRuntimeAction(w, r, action) })
	}
	add("backup_now", "POST", "/instances/{id}/backups", 2, true, false, s.instanceBackupNow)
	add("instance_create", "POST", "/instances", 3, false, true, s.instanceRuntimeCreate)
	add("instance_rename", "PUT", "/instances/{id}/name", 3, true, false, s.minecraftInstanceRename)
	add("instance_configure", "PUT", "/instances/{id}/configuration", 3, true, false, s.instanceRuntimeConfigure)
	add("instance_activate", "POST", "/instances/{id}/activate", 3, true, false, s.instanceRuntimeApply)
	add("instance_recover", "POST", "/instances/{id}/recover", 3, true, false, s.instanceRuntimeRecover)
	add("instance_delete", "POST", "/instances/{id}/delete", 3, true, false, s.instanceDelete)
	add("backup_configure", "PUT", "/instances/{id}/backups/settings", 3, true, false, s.instanceBackupConfigure)
	add("restore_plan", "POST", "/instances/{id}/backups/restore/plan", 3, true, false, s.instanceRestorePlan)
	add("restore_apply", "POST", "/instances/{id}/backups/restore/apply", 3, true, false, s.instanceRestoreApply)
	add("restore_status", "GET", "/instances/{id}/backups/restore/status", 3, true, false, s.instanceRestoreStatus)
	add("restore_recover", "POST", "/instances/{id}/backups/restore/recover", 3, true, false, s.instanceRestoreRecover)
	add("version_status", "GET", "/instances/{id}/version/status", 3, true, false, s.adminVersionStatus)
	add("version_update", "POST", "/instances/{id}/version/update", 3, true, false, s.adminMinecraftStackUpdate)
	add("version_update_status", "GET", "/instances/{id}/version/update/current", 3, true, false, s.adminMinecraftStackUpdateCurrent)
	add("container_status", "GET", "/instances/{id}/container/status", 3, true, false, s.adminMinecraftContainerStatus)
	add("container_update", "POST", "/instances/{id}/container/update", 3, true, false, s.adminMinecraftContainerUpdate)
	add("container_update_status", "GET", "/instances/{id}/container/update/current", 3, true, false, s.adminMinecraftContainerUpdateCurrent)
	add("container_update_acknowledge", "POST", "/instances/{id}/container/update/acknowledge", 3, true, false, s.adminMinecraftContainerUpdateAcknowledge)
	add("version_update_acknowledge", "POST", "/instances/{id}/version/update/acknowledge", 3, true, false, s.adminMinecraftStackUpdateAcknowledge)
	add("addons", "GET", "/instances/{id}/plugins/addons", 3, true, false, s.selectedPluginHandler(s.adminAddonInventory))
	add("addon_plan", "POST", "/instances/{id}/plugins/addons/plan", 3, true, false, s.selectedPluginHandler(s.adminAddonPlan))
	add("addon_install", "POST", "/instances/{id}/plugins/addons/install", 3, true, false, s.selectedPluginHandler(s.adminAddonInstall))
	add("addon_recover", "POST", "/instances/{id}/plugins/addons/recover", 3, true, false, s.selectedPluginHandler(s.adminAddonRecover))
	add("plugins", "GET", "/instances/{id}/plugins/installed", 3, true, false, s.selectedPluginHandler(s.adminPluginsInstalled))
	add("plugin_discover", "GET", "/instances/{id}/plugins/discover", 3, true, false, s.selectedPluginHandler(s.adminPluginsDiscover))
	add("plugin_install_plan", "POST", "/instances/{id}/plugins/install/plan", 3, true, false, s.selectedPluginHandler(s.adminPluginInstallPlan))
	add("plugin_install", "POST", "/instances/{id}/plugins/install", 3, true, false, s.selectedPluginHandler(s.adminPluginInstall))
	add("plugin_manage_plan", "POST", "/instances/{id}/plugins/manage/plan", 3, true, false, s.selectedPluginHandler(s.adminPluginManagePlan))
	add("plugin_manage", "POST", "/instances/{id}/plugins/manage", 3, true, false, s.selectedPluginHandler(s.adminPluginManage))
	add("plugin_operation_status", "GET", "/instances/{id}/plugins/operations/{operation}", 3, true, false, s.selectedPluginHandler(s.selectedPluginStatus))
	add("plugin_operation", "GET", "/instances/{id}/plugins/operation", 3, true, false, s.selectedPluginHandler(s.adminPluginOperation))
	add("plugin_install_status", "GET", "/instances/{id}/plugins/install-operation", 3, true, false, s.selectedPluginHandler(s.adminPluginInstallCurrent))
	add("plugin_install_acknowledge", "POST", "/instances/{id}/plugins/install/acknowledge", 3, true, false, s.selectedPluginHandler(s.adminPluginInstallAcknowledge))
	add("plugin_manage_acknowledge", "POST", "/instances/{id}/plugins/manage/acknowledge", 3, true, false, s.selectedPluginHandler(s.adminPluginManageAcknowledge))
	add("export_discover", "GET", "/instances/{id}/migration/export", 3, true, false, s.adminMigrationExportDiscover)
	add("export_plan", "POST", "/instances/{id}/migration/export/plan", 3, true, false, s.adminMigrationExportPlan)
	add("export_apply", "POST", "/instances/{id}/migration/export/apply", 3, true, false, s.adminMigrationExportApply)
	add("import_discover", "GET", "/instances/{id}/migration/import", 3, true, false, s.adminMigrationImportDiscover)
	add("import_plan", "POST", "/instances/{id}/migration/import/plan", 3, true, false, s.adminMigrationImportPlan)
	add("import_apply", "POST", "/instances/{id}/migration/import/apply", 3, true, false, s.adminMigrationImportApply)
	add("import_resolve", "POST", "/instances/{id}/migration/import/resolve", 3, true, false, s.adminMigrationImportResolve)
	add("migration_recovery", "GET", "/instances/{id}/migration/recovery", 3, true, false, s.adminMigrationRecoveryDiscover)
	add("migration_recovery_plan", "POST", "/instances/{id}/migration/recovery/plan", 3, true, false, s.adminMigrationRecoveryPlan)
	add("migration_recovery_apply", "POST", "/instances/{id}/migration/recovery/apply", 3, true, false, s.adminMigrationRecoveryApply)
	add("migration_recovery_resolve", "POST", "/instances/{id}/migration/recovery/resolve", 3, true, false, s.adminMigrationRecoveryResolve)
	add("migration_status", "GET", "/instances/{id}/migration/current-operation", 3, true, false, s.migrationInstanceCurrent)
	add("storage", "GET", "/storage", 3, false, true, s.adminStorage)
	add("storage_action_plan", "POST", "/storage/actions/plan", 3, false, true, s.adminStorageActionPlan)
	add("storage_action_apply", "POST", "/storage/actions/apply", 3, false, true, s.adminStorageActionApply)
	add("storage_mounts", "GET", "/storage/mounts/status", 3, false, true, s.adminStorageMountStatus)
	add("storage_mount_plan", "POST", "/storage/mounts/plan", 3, false, true, s.adminStorageMountPlan)
	add("storage_mount_apply", "POST", "/storage/mounts/apply", 3, false, true, s.adminStorageMountApply)
	add("storage_provision", "GET", "/storage/provision", 3, false, true, s.adminStorageProvisionDiscover)
	add("storage_provision_plan", "POST", "/storage/provision/plan", 3, false, true, s.adminStorageProvisionPlan)
	add("storage_provision_apply", "POST", "/storage/provision/apply", 3, false, true, s.adminStorageProvisionApply)
	add("data_migration", "GET", "/storage/migration", 3, false, true, s.adminDataMigrationDiscover)
	add("data_migration_plan", "POST", "/storage/migration/plan", 3, false, true, s.adminDataMigrationPlan)
	add("data_migration_apply", "POST", "/storage/migration/apply", 3, false, true, s.adminDataMigrationApply)
	add("data_migration_recover", "POST", "/storage/migration/recover", 3, false, true, s.adminDataMigrationRecover)
	add("network", "GET", "/network", 3, false, true, s.networkStatus)
	add("network_remote_access", "GET", "/network/remote-access", 3, false, true, s.networkRemoteAccessStatus)
	add("network_remote_change", "POST", "/network/remote-access/{provider}", 3, false, true, s.networkRemoteAccessChange)
	add("network_ethernet", "POST", "/network/ethernet/{interface}", 3, false, true, s.networkEthernetConfigure)
	add("network_wifi_scan", "POST", "/network/wifi/{interface}/scan", 3, false, true, s.networkWiFiScan)
	add("network_wifi_networks", "GET", "/network/wifi/{interface}/networks", 3, false, true, s.networkWiFiNetworks)
	add("network_wifi_connect", "POST", "/network/wifi/{interface}/connect", 3, false, true, s.networkWiFiConnect)
	add("network_wifi_disconnect", "POST", "/network/wifi/{interface}/disconnect", 3, false, true, s.networkWiFiDisconnect)
	add("network_wifi_radio", "POST", "/network/wifi/radio", 3, false, true, s.networkWiFiRadio)
	add("network_checkpoint", "POST", "/network/checkpoints", 3, false, true, s.networkCheckpointCreate)
	add("network_checkpoint_confirm", "POST", "/network/checkpoints/{id}/confirm", 3, false, true, s.networkCheckpointConfirm)
	add("system_updates", "GET", "/system/updates", 4, false, true, s.adminSystemUpdateStatus)
	add("system_update_check", "POST", "/system/updates/check", 4, false, true, s.adminSystemUpdateCheck)
	add("system_update", "POST", "/system/updates", 4, false, true, s.adminSystemUpdate)
	add("system_actions", "GET", "/system/actions", 4, false, true, s.adminSystemActionsStatus)
	add("system_reboot", "POST", "/system/reboot", 4, false, true, func(w http.ResponseWriter, r *http.Request) { s.adminSystemAction(w, r, "reboot") })
	add("system_poweroff", "POST", "/system/poweroff", 4, false, true, func(w http.ResponseWriter, r *http.Request) { s.adminSystemAction(w, r, "poweroff") })
	add("systemd", "POST", "/system/systemd", 4, false, true, s.gatewaySystemd)
	return ops
}

type gatewayCall struct {
	Operation  string            `json:"operation"`
	InstanceID string            `json:"instance_id,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	Query      map[string]string `json:"query,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
}
type gatewayResult struct {
	Status int             `json:"status"`
	Data   json.RawMessage `json:"data"`
}

func registerGatewayRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/gateway/catalog", s.gatewayCatalog)
	mux.HandleFunc("POST /v1/gateway/execute", s.gatewayExecute)
	mux.HandleFunc("GET /v1/gateway/metadata", s.gatewayMetadata)
	mux.HandleFunc("GET /v1/admin/gateway", s.gatewayAdmin)
	mux.HandleFunc("POST /v1/admin/gateway", s.gatewayAdmin)
}
func gatewayBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}
func (s *server) gatewayCatalog(w http.ResponseWriter, r *http.Request) {
	s.gatewayMu.RLock()
	defer s.gatewayMu.RUnlock()

	c, e := s.store.gatewayAuthenticate(gatewayBearer(r))
	if e != nil {
		writeError(w, 401, "external authentication required")
		return
	}
	ops := []gatewayOperation{}
	for _, op := range s.gatewayOperations() {
		if c.Level >= op.MinLevel && (!op.AllInstances || c.AllInstances) {
			ops = append(ops, op)
		}
	}
	if e = s.store.gatewayActivity(c.ID); e != nil {
		writeError(w, 503, "gateway unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"operations": ops, "routes": s.gatewayOperations()})
}
func (s *server) gatewayMetadata(w http.ResponseWriter, r *http.Request) {
	snap, e := s.store.gatewaySnapshot()
	if e != nil {
		writeError(w, 503, "gateway unavailable")
		return
	}
	resources := map[string][]string{}
	if snap.Enabled {
		for _, c := range snap.Connections {
			if c.Enabled && c.OAuth != nil && (c.ExpiresAt == nil || s.store.now().Before(*c.ExpiresAt)) {
				duplicate := false
				for _, issuer := range resources[c.OAuth.Audience] {
					if issuer == c.OAuth.Issuer {
						duplicate = true
					}
				}
				if !duplicate {
					resources[c.OAuth.Audience] = append(resources[c.OAuth.Audience], c.OAuth.Issuer)
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"resources": resources})
}
func gatewayAllowed(c gatewayConnection, op gatewayOperation, call gatewayCall) bool {
	if c.Level < op.MinLevel || (op.AllInstances && !c.AllInstances) {
		return false
	}
	if op.Instance && !c.allowsInstance(call.InstanceID) {
		return false
	}
	// Nested immutable IDs (for example migration source/target) cannot evade scope.
	var body any
	if len(call.Body) > 0 && json.Unmarshal(call.Body, &body) != nil {
		return false
	}
	var check func(any) bool
	check = func(v any) bool {
		switch t := v.(type) {
		case string:
			if instanceIDPattern.MatchString(t) {
				return c.allowsInstance(t)
			}
		case []any:
			for _, v := range t {
				if !check(v) {
					return false
				}
			}
		case map[string]any:
			for _, v := range t {
				if !check(v) {
					return false
				}
			}
		}
		return true
	}
	return check(body)
}

var gatewayParameterPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$`)

func (s *server) gatewayExecute(w http.ResponseWriter, r *http.Request) {
	s.gatewayMu.RLock()
	defer s.gatewayMu.RUnlock()

	c, e := s.store.gatewayAuthenticate(gatewayBearer(r))
	if e != nil {
		writeError(w, 401, "external authentication required")
		return
	}
	var call gatewayCall
	if !decodeGatewayJSON(w, r, &call) {
		return
	}
	var op *gatewayOperation
	for _, candidate := range s.gatewayOperations() {
		if candidate.Name == call.Operation {
			op = &candidate
			break
		}
	}
	if op == nil {
		writeError(w, 404, "unknown gateway operation")
		return
	}
	actor := session{Username: c.ID + " " + c.Name, Role: roleViewer, AuthSource: authSourceExternal, ConnectionID: c.ID, ConnectionLevel: c.Level}
	if c.Level == 2 {
		actor.Role = roleOperator
	}
	if c.Level >= 3 {
		actor.Role = roleAdministrator
	}
	mutating := op.Method != "GET"
	audit := func(success bool) bool {
		if mutating {
			if err := s.store.recordAuditEvent(actor, "external_"+op.Name, c.ID, success, ""); err != nil {
				writeError(w, 503, "external operation audit unavailable")
				return false
			}
		}
		return true
	}
	if !gatewayAllowed(c, *op, call) {
		if !audit(false) {
			return
		}
		writeError(w, 403, "connection permission or instance scope denied")
		return
	}
	body := call.Body
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	var input any
	if json.Unmarshal(body, &input) != nil {
		if !audit(false) {
			return
		}
		writeError(w, 400, "invalid operation body")
		return
	}
	input, e = s.store.gatewayResolveReferences(c.ID, input)
	if e != nil {
		if !audit(false) {
			return
		}
		writeError(w, 400, "invalid operation reference")
		return
	}
	body, e = json.Marshal(input)
	if e != nil {
		writeError(w, 400, "invalid operation body")
		return
	}
	path := op.Path
	parameters := map[string]string{}
	for k, v := range call.Parameters {
		parameters[k] = v
	}
	if op.Instance {
		parameters["id"] = call.InstanceID
	}
	for key, value := range parameters {
		if !strings.Contains(path, "{"+key+"}") || !gatewayParameterPattern.MatchString(value) {
			if !audit(false) {
				return
			}
			writeError(w, 400, "invalid operation parameter")
			return
		}
		path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(value))
	}
	if strings.Contains(path, "{") {
		if !audit(false) {
			return
		}
		writeError(w, 400, "missing operation parameter")
		return
	}
	query := url.Values{}
	for key, value := range call.Query {
		switch key {
		case "limit", "format", "query", "source", "project_id", "version", "platform", "type", "offset", "q", "policy", "bedrock", "server_type", "kind", "transaction":
		default:
			if !audit(false) {
				return
			}
			writeError(w, 400, "unsupported query parameter")
			return
		}
		if len(value) > 256 {
			if !audit(false) {
				return
			}
			writeError(w, 400, "query too long")
			return
		}
		query.Set(key, value)
	}
	req, _ := http.NewRequestWithContext(context.WithValue(r.Context(), gatewayPrincipalKey{}, actor), op.Method, "http://gateway"+path, bytes.NewReader(body))
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Content-Type", "application/json")
	for k, v := range parameters {
		req.SetPathValue(k, v)
	}
	capture := &gatewayResponse{header: make(http.Header), status: 200}
	if mutating {
		if e = s.store.recordAuditEvent(actor, "external_attempt_"+op.Name, c.ID, true, ""); e != nil {
			writeError(w, 503, "external operation audit unavailable")
			return
		}
	}
	op.Handler(capture, req)
	if capture.overflow {
		if !audit(false) {
			return
		}
		writeError(w, 502, "operation result exceeds gateway limit")
		return
	}
	var data any
	if json.Unmarshal(capture.body.Bytes(), &data) != nil {
		if !audit(false) {
			return
		}
		writeError(w, 502, "operation returned an unsupported response")
		return
	}
	safe, e := s.store.gatewayPublicResult(c.ID, data)
	if e != nil {
		if !audit(false) {
			return
		}
		writeError(w, 503, "operation result unavailable")
		return
	}
	if capture.status >= 400 {
		safe = map[string]any{"error": map[string]string{"code": "operation_failed", "message": "Appliance operation could not complete; review its state in JustVoxel."}, "result": safe}
	}
	if !audit(capture.status < 400) {
		return
	}
	if capture.status < 400 {
		if e = s.store.gatewayActivity(c.ID); e != nil {
			writeError(w, 503, "gateway activity could not be recorded")
			return
		}
	}
	raw, e := json.Marshal(safe)
	if e != nil {
		writeError(w, 502, "invalid operation result")
		return
	}
	writeJSON(w, 200, gatewayResult{capture.status, raw})
}

type gatewayResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *gatewayResponse) Header() http.Header  { return w.header }
func (w *gatewayResponse) WriteHeader(code int) { w.status = code }
func (w *gatewayResponse) Write(b []byte) (int, error) {
	if w.body.Len()+len(b) > gatewayBodyLimit {
		w.overflow = true
		return 0, errors.New("gateway response too large")
	}
	return w.body.Write(b)
}

// An allowlist, rather than copying private backend models. Unknown new backend
// fields stay private until deliberately added to this external contract.
var gatewayPublicFields = func() map[string]bool {
	fields := strings.Fields(`
		accepted action action_confirmed actions active active_connection active_state addons addresses
		administrator_reset_required allowed apiVersion applied archive archive_id archive_name archive_path
		archive_published archive_size_bytes author authors autoconnect autoconnect_priority available
		available_channel available_profile_uuids avatarUrl backup backup_action backup_automatic backup_available
		backup_before_shutdown backup_configured backup_daily_time backup_destination backup_id backup_keep
		backup_limit backup_mode backup_path backup_schedule backup_smb_password_required backup_source
		backup_usable backup_used backups bedrockPortHint bedrock_enabled bedrock_managed_plugins bedrock_port
		bedrock_port_hint bedrock_supported bedrock_tcp_port bedrock_udp_end bedrock_udp_start bitrate_kbps
		blank_partitions booted bssid cachedUpdate can_firmware_reboot can_poweroff can_reboot can_recover can_retry
		can_update candidate_channel candidate_type candidates capacity capacity_bytes carrier changed channel
		check_state checked checkpoint checkpoint_id checks code compatibility compatible completed_at
		config_identity config_sha256 configuration configuration_sha256 configured configured_backup_available
		configured_image_tag configured_server_type configured_version confirm_destructive confirm_players
		confirmation confirmation_required connected connection_id connection_state connectivity container_memory
		context cooldown_seconds count countdown_seconds created createdAt created_at created_device
		crossplay_compatible crossplay_enabled current current_mount_point current_software current_version cursor
		data_action data_bytes data_destination data_identity data_path data_source date_published defaults
		delete_after_export dependencies dependency_type description destination destination_available
		destination_identity destination_kind destructive destructive_confirmation_required device devices dhcp
		difficulty digest directories directory_created disk display_name display_state dns downloadOnly downloaded
		downloads duration_seconds effects efi enabled enabled_on_boot enabled_supported end enforceWhitelist
		enforce_whitelist entries error errors estimated_bytes eula_acceptance_required eula_accepted expanded_bytes
		expected_source expected_uuid expires_at export_confirmation_required file_name filename files filesystem
		filesystem_free_bytes filesystem_size_bytes filesystem_type filesystem_usage_known filesystem_used_bytes
		finalizable finalize_confirmation_required fingerprint finished finished_at firmware firmware_reboot flags
		floodgateEnabled floodgate_state force format_allowed free free_bytes free_end free_mib free_space_disks
		free_spaces free_start frequency_mhz gameMode game_mode game_versions gateway geyserAuthType geyserEnabled
		geyser_state geyser_supported_version had_service had_timer hangar hangar_first hangar_offset
		hardware_address health hidden hits hostname icon_project_id icon_url id image imageDigest image_state
		image_tag import_confirmation_required install_available installed installed_state instance_id instances
		integrity integrity_validation_required intent interface interface_name interfaces ip ip_interface ipv4
		ipv4_address ipv4_dns ipv4_gateway ipv4_method ipv4_prefix ipv6 ipv6_method items javaPortHint java_enabled
		java_memory java_port java_port_hint justvoxel keep key_management kind known label label_method last
		last_attempt last_success latest levelName limit lines load_state loader_version loaders local_address
		local_port logs mac maintenance maintenance_state major_minor managed managed_crossplay_available management
		management_api max maxPlayers max_bitrate_kbps max_players meshnet message metadata metadata_error
		metadata_status min minecraft minecraftVersion minecraft_state minecraft_version mode model modrinth
		modrinth_offset motd mount_options mount_point mount_point_removed mounted mountpoint mountpoints mounts mtu
		name names namespace needs_attention netbird network networking_enabled networks next next_cursor normalized
		normalized_options normalized_request offset ok online onlineMode online_mode
		online_mode_confirmation_required operation operation_id operation_supported operation_type operations
		operator_usage optional options original originally_running os_status_available output overwrite owner
		ownership_verified pagination paper_channel paper_supported parent partial partition partition_size_bytes
		partition_table partitions partitions_supported path pending pending_checkpoints percent persistence phase
		plan plan_fingerprint plan_id planned_end planned_mib platformDependencies players
		players_confirmation_required plugin pluginDependencies pluginJarCount pluginJars plugin_count plugins
		plugins_confirmation_required policy port port_mode ports poweroff preserve previous profile_uuid profiles
		progress projectId project_id projects proposed protocol provider providers provision_allowed public
		public_address public_port public_settings quick reachable readOnly read_only reason reboot reboot_required
		recommended recoverable recovery recovery_required release remote_access removable replacement requested
		required required_bytes requirements restart restart_limit restart_required restart_used restore
		restore_action restore_available restore_mode result results retention retention_count retry_after_seconds
		role rollback rollback_available rollback_required root root_relative routes running runtime runtime_state
		runtime_update_required runtime_validation_required safety schedule schedule_enabled schedule_time schema
		schema_version scope security selected selected_candidate selected_root selections server server_id
		server_software server_supported server_type service service_active service_enabled service_state settings
		sha256 signal size size_bytes size_gib slug smb_password_required software source sourceType source_action
		source_available source_class source_entries source_id source_identity source_kind source_kinds
		source_revalidation_on_apply source_type source_version sources speed speed_mbps ssid stack_state stage
		staged staged_update start start_on_boot started started_at state state_identity status status_message
		status_unavailable storage strength sub_state success suggested_filename summary supported system
		system_disk system_disks tailscale target target_action target_available_bytes target_display
		target_filesystem target_id target_identity target_kind target_kinds target_label target_type
		target_validation_on_apply targets time timeout_seconds timer timer_enabled timestamp timezone title total
		total_bytes total_hits transaction transaction_id transaction_state transactions transport tunnels type
		unconfigured_validation_required unit unit_file_state units unmount_allowed update_available update_status
		updated_at updating used used_bytes uuid validation vanilla_confirmation_required variant verify version
		version_id version_number version_policy version_type versions viaversion_state warnings webui webui_version
		whitelist whitelistEnabled whitelist_enabled whole_disks wireless wireless_enabled
	`)
	m := map[string]bool{}
	for _, key := range fields {
		m[key] = true
	}
	return m
}()
var gatewayCredentialText = regexp.MustCompile(`jvapi_[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
var gatewayPrivatePath = regexp.MustCompile(`(?:^|[\s"'(\[])/[^\s"'<>]*`)

func gatewayPublicValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range t {
			if gatewayPublicFields[k] {
				out[k] = gatewayPublicValue(v)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, v := range t {
			out = append(out, gatewayPublicValue(v))
		}
		return out
	case string:
		if len(t) > 65536 {
			t = t[:65536]
		}
		return gatewayCredentialText.ReplaceAllString(gatewayPrivatePath.ReplaceAllString(gatewayDiagnosticText(t), "[private path]"), "[credential omitted]")
	default:
		return v
	}
}
func (s *server) gatewayInstanceInventory(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(gatewayPrincipalKey{}).(session)
	snapshot, err := s.store.gatewaySnapshot()
	if err != nil {
		writeError(w, 503, "inventory unavailable")
		return
	}
	var c gatewayConnection
	for _, item := range snapshot.Connections {
		if item.ID == actor.ConnectionID {
			c = item
		}
	}
	items, err := minecraftInstances.listInstances(minecraftLegacyNameSoftware(r))
	if err != nil {
		writeError(w, 503, "inventory unavailable")
		return
	}
	out := []minecraftRegisteredInstance{}
	for _, item := range items {
		if c.allowsInstance(item.ID) {
			out = append(out, item)
		}
	}
	writeJSON(w, 200, map[string]any{"instances": out})
}
func (s *server) gatewayLogs(w http.ResponseWriter, r *http.Request) {
	// Only this curated read uses Operator log access; no browser role is changed.
	actor, ok := r.Context().Value(gatewayPrincipalKey{}).(session)
	if !ok {
		writeError(w, 403, "external principal required")
		return
	}
	if actor.Role == roleViewer {
		actor.Role = roleOperator
	}
	s.minecraftLogs(w, r.WithContext(context.WithValue(r.Context(), gatewayPrincipalKey{}, actor)))
}
func (s *server) gatewayAdmin(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePrimaryAdministrator(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		snap, e := s.store.gatewaySnapshot()
		if e != nil {
			writeError(w, 503, "gateway unavailable")
			return
		}
		writeJSON(w, 200, snap)
		return
	}
	s.gatewayMu.Lock()
	defer s.gatewayMu.Unlock()
	if s.operations != nil {
		current, err := s.operations.currentFactoryReset()
		if err != nil {
			writeError(w, 503, "Factory Reset state unavailable")
			return
		}
		if current != nil && operationIsCurrent(current.State) {
			writeError(w, 409, "Resolve or complete Factory Reset before configuring connections")
			return
		}
	}
	var request struct {
		Action       string            `json:"action"`
		ID           string            `json:"id"`
		Enabled      bool              `json:"enabled"`
		Connection   gatewayConnection `json:"connection"`
		Acknowledged bool              `json:"acknowledged"`
		Slider       int               `json:"slider"`
	}
	if !decodeGatewayJSON(w, r, &request) {
		return
	}
	var err error
	var token string
	var c gatewayConnection
	switch request.Action {
	case "enable":
		err = s.store.gatewaySetEnabled(request.Enabled, actor)
	case "create", "apply":
		c, token, err = s.store.gatewaySave(request.Connection, request.Action == "create", request.Acknowledged && request.Slider == 100, actor)
	case "rotate", "revoke", "delete", "reset-allowance":
		token, err = s.store.gatewayCredentialAction(request.ID, request.Action, actor)
	default:
		writeError(w, 400, "invalid gateway action")
		return
	}
	if err != nil {
		writeError(w, 400, "Connection change could not be saved. Check settings and Level 4 confirmation.")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "connection": c, "token": token})
}

// The gateway envelope has its own bounded strict decoder. Backend handlers keep
// their existing request limits and validation.
func decodeGatewayJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, gatewayBodyLimit))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		writeError(w, 400, "invalid gateway request")
		return false
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		writeError(w, 400, "invalid gateway request")
		return false
	}
	return true
}

func gatewayDiagnosticText(text string) string {
	lines := strings.Split(instanceDiagnosticText([]byte(text), ""), "\n")
	inKey := false
	for i, line := range lines {
		upper := strings.ToUpper(line)
		lower := strings.ToLower(line)
		if strings.Contains(upper, "-----BEGIN") {
			inKey = true
		}
		if inKey || strings.Contains(lower, "private key") || strings.Contains(lower, "authorization:") || (strings.Contains(lower, "floodgate") && strings.Contains(lower, "key")) {
			lines[i] = "[private diagnostic line omitted]"
		}
		if strings.Contains(upper, "-----END") {
			inKey = false
		}
	}
	return strings.Join(lines, "\n")
}
