# Minecraft convergence C2 source report

> Historical delivery record. This report describes the C2 convergence snapshot
> at commit `4213527`; its “remaining” lists are not the current project roadmap.
> Use [ARCHITECTURE.md](ARCHITECTURE.md),
> [MINECRAFT_RUNTIME.md](MINECRAFT_RUNTIME.md), and
> [ROADMAP.md](ROADMAP.md) for the current design and status.

Base: `testing`, HEAD `4213527`. The original nine-file partial C2 diff was retained and completed in this working tree. No commit, push, GitHub mutation, package installation, image build or deployment was performed.

## Creation and recovery

First-run Recommended/Advanced Setup keeps its existing wizard. Its normalized runtime request now selects the normal-instance transaction. Appliance preparation retains the existing storage transaction and writes storage/backup destination policy separately from server configuration.

Setup uses `instanceRuntimeBackend.createTracked`; Add Server uses `create`, which delegates to that same function. Both therefore share generation, validation, registration, configuration, environment/Quadlet rendering, firewall ownership, activation journals and recovery. Setup records its generated immutable ID before registration in private `setup-<operation-id>.json` evidence. The server has normal instance data, Start on Boot and backup identity; its backup schedule uses the normal instance schedule backend.

Active new Setup no longer invokes the fixed-runtime helper's Apply/Verify/Commit. It does not create `/etc/justvoxel/minecraft.env`, `/etc/containers/systemd/minecraft.container`, `minecraft.service`, `ContainerName=minecraft`, or the `minecraft.json` identity. `/etc/justvoxel/justvoxel.conf` and `minecraft-backup.env` contain appliance storage policy, not the selected server identity.

New Setup journals identify the instance-based recovery contract. Recovery loads protected evidence, completes bounded existing activation/schedule recovery when necessary, retires only the transaction's generated instance, verifies owned policy/marker removal, and then runs the existing storage rollback. The existing operation transitions release the Setup lock only after verified `rolled_back`. Old deployed Setup recovery continues through its preserved helper and evidence formats.

## Selected operations

Inventory and selectors contain actual immutable `jv-*` identities only. Zero servers is a valid inventory/selector result. Missing operation selection resolves only when exactly one registered runtime exists; ambiguous, invalid, stale and mismatched selections fail closed. Rename and deletion retain ordinary instance behavior; deleting the last server does not recreate one.

Whitelist GET/POST use `/v1/minecraft/instances/{id}/whitelist`; the global whitelist routes and WebUI client methods are gone. The backend requires `--instance`, verifies registry/configuration and committed runtime identity, and uses `minecraft-<id>` / `minecraft-<id>.service`. WebUI and mjust pass explicit context; mjust may resolve its one actual registered server. Existing Java/Bedrock/Floodgate, idempotency, authorization, local-root and audit behavior remains in the existing implementations. Whitelist state updates use the selected configuration transaction, including reversible live RCON state changes.

Version uses `/v1/minecraft/instances/{id}/version/status` and `/version/update`, `/version/update/current`, `/version/update/acknowledge`. Existing `/version` and `/version/verify` remain instance-addressed. The mature planner/updater handles compatibility, reviewed fingerprints, player confirmation, cold backup, verification and rollback. Private runtime publication reuses the normal Go renderer/activation journal. Persistent update journals bind operation identity to instance identity. Operation polling remains available while publication is pending. Reviews submit one selected operation without automatic re-plan/retry loops.

Version pulls use the mature planner's verified digest. The selected private runtime may pin that digest while retaining its public channel/tag. Publication preserves that pin during later settings changes. Updates do not retag or remove shared cached images, protecting stopped siblings' image references as well as their files/data/runtime state. Old records without digest/policy fields remain readable. Same-version definition changes verify the running version before publication and retain bounded activation recovery evidence. The dashboard no longer automatically creates a fixed legacy identity.

Backups and Restore use selected instance APIs, including Quick Look and terminal frontends. The Phase 4A/4B archive ownership, metadata/integrity, same-ID-only Restore, stopped/runtime safety and recovery engines are reused. Terminal Restore uses full instance data and supports reconnect/recovery; world-only fixed-server Restore is not a normal-instance mode. Appliance backup storage design is unchanged.

No CSS, wallpaper or wizard/workspace layout redesign was made. The Original card/type disappears and normal instances use existing cards, tabs and controls.

## Source coverage and verification

Source tests were added/updated for first-run normal creation, shared creation machinery, absence of fixed Setup artifacts, zero/one/multiple inventories, actual selectors, stale and mismatched selection, Settings isolation, whitelist exact targeting/permissions/idempotency, Version targeting and sibling isolation, shared-image isolation, selected Backup/Restore, confirmations, deletion of the final server and immutable-ID targeting. Go and Node fixtures no longer assert Original fallback or read-only instance Version behavior. Shell source fixtures assert instance whitelist/backup/player/Restore routes.

Only static checks are run in Codex: gofmt for changed Go, `bash -n` for changed shell, `node --check` for changed JavaScript/CJS, and `git diff --check`. Go tests/vet/build, executable test suites, image checks and VM/visual acceptance are deferred to the user-run Phase 3. These static checks do not prove compilation or runtime correctness. Independent human review remains required.

## Remaining C3 fixed-server dependencies

These are retained and require later convergence; they are not hidden by compatibility routes for C2:

- Plugins: `management/cmd/justvoxel-management-agent/admin_plugin_install.go` and plugin runtime verification still use the fixed service/container contract.
- Import/Export and migration: `admin_migration_import_apply.go` still registers the legacy identity; migration export/import/recovery, data migration and their shell helpers retain fixed runtime paths. See the exact source references below.
- Minecraft/Factory Reset and Start Over: `admin_minecraft_reset.go`, `admin-minecraft-reset-json`, `admin-factory-reset-json`, `minecraft-reset-common.sh`, and `start-over` retain fixed runtime ownership/cleanup rules.
- System update/reboot/shutdown: `runtime/system-update-reboot-worker`, `admin-system-update-reboot-json`, `admin-system-actions-json`, `interrupt-safety.sh`, and the image shutdown-guard unit retain fixed-server assumptions.
- Appliance status/validation and older terminal administration: `web-status-json`, `validate-backend`, `runtime/justvoxel-motd`, the global Minecraft lifecycle/log endpoints, `mjust service`, `mjust logs`, `configuration-api.sh`/legacy configuration, and `common.sh` retain fixed-server dependencies. These interfaces have not been represented as converged C2 instance controls.
- Physical legacy artifacts/helpers: fixed Quadlet/backup templates, `runtime/minecraft-backup`, and `backup-common.sh` remain. C3 has retired the global Restore transaction/discovery/planning workers and `restore-api.sh`; shared Restore validators remain. The audit below records the earlier C2 state.
- Deployed-state recovery: the old Setup runtime transaction helper and diagnostic defaults remain for old journals. `verify-minecraft-stack` retains its existing legacy mode for C3 callers. New Setup and instance Version explicitly use `--instance`.

The following literal-reference audit includes both C3 consumers and intentionally retained old-recovery/default branches. It is a source inventory, not a claim that C3 features work with a newly created normal instance.

```text
templates/systemd/minecraft-backup.service.in:3:After=local-fs.target network-online.target minecraft.service
templates/quadlets/minecraft.container.in:9:ContainerName=minecraft
templates/quadlets/minecraft.container.in:13:EnvironmentFile=/etc/justvoxel/minecraft.env
runtime/minecraft-backup:128:        metadata_server_version="$(podman exec minecraft rcon-cli 'version' 2>/dev/null | head -n1 || true)"
runtime/minecraft-backup:129:        metadata_geyser_version="$(podman exec minecraft rcon-cli 'geyser version' 2>/dev/null | head -n1 || true)"
runtime/minecraft-backup:131:        metadata_server_version="$(podman exec minecraft mc-monitor status --json 2>/dev/null | jq -er '.server_info.version.name | select(type == "string")' || true)"
templates/config/minecraft-backup.env.in:4:MINECRAFT_SERVICE=minecraft.service
runtime/system-update-reboot-worker:19:    if [[ ${was_active} == yes ]] && ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
runtime/system-update-reboot-worker:20:        systemctl start minecraft.service >/dev/null 2>&1 || true
runtime/system-update-reboot-worker:29:if [[ ${was_active} == yes ]] && systemctl is-active --quiet minecraft.service 2>/dev/null; then
runtime/justvoxel-motd:123:if [[ -f /etc/containers/systemd/minecraft.container ]]; then
management/cmd/justvoxel-management-agent/admin_minecraft_reset.go:1064:	for _, path := range []string{"/etc/justvoxel/justvoxel.conf", "/etc/justvoxel/minecraft.env", "/etc/justvoxel/minecraft-backup.env", "/etc/containers/systemd/minecraft.container", "/etc/systemd/system/minecraft-backup.service", "/etc/systemd/system/minecraft-backup.timer", "/var/lib/justvoxel/state/minecraft-previous-image-id"} {
management/cmd/justvoxel-management-agent/admin_minecraft_reset.go:1079:	out, err := pluginInstallCommand(ctx, "systemctl", "show", "--property=ActiveState", "--value", "minecraft.service")
system_files/usr/lib/systemd/system/justvoxel-minecraft-shutdown-guard.service:4:After=minecraft.service
management/cmd/justvoxel-management-agent/minecraft.go:124:		_ = s.store.recordAuditEvent(sess, "minecraft_restart", "minecraft.service", false, "operator restart eligibility check failed")
management/cmd/justvoxel-management-agent/minecraft.go:130:		_ = s.store.recordAuditEvent(sess, "minecraft_restart", "minecraft.service", false, contextText)
management/cmd/justvoxel-management-agent/minecraft.go:171:			_ = s.store.recordAuditEvent(sess, "minecraft_restart", "minecraft.service", false, contextText)
management/cmd/justvoxel-management-agent/admin_plugin_install.go:994:	out, err := pluginInstallCommand(ctx, "systemctl", "is-active", "minecraft.service")
management/cmd/justvoxel-management-agent/admin_plugin_install.go:1005:	_, err := pluginInstallCommand(ctx, "systemctl", action, "minecraft.service")
management/cmd/justvoxel-management-agent/operator_restart.go:113:	return g.acceptTarget(actor, "minecraft.service")
management/cmd/justvoxel-management-agent/operator_surfaces.go:20:	return exec.CommandContext(ctx, "journalctl", "--no-pager", "-u", "minecraft.service", "-n", strconv.Itoa(limit), "-o", outputMode).CombinedOutput()
mjust/libexec/validate-backend:68:    local -a pending=(minecraft.service minecraft-backup.service minecraft-backup.timer basic.target sysinit.target local-fs.target)
mjust/libexec/validate-backend:299:if systemctl cat minecraft.service >/dev/null 2>&1; then
mjust/libexec/validate-backend:300:    pass 'minecraft.service is generated from the local Quadlet'
mjust/libexec/validate-backend:302:    fail 'minecraft.service was not generated'
mjust/libexec/validate-backend:317:if systemctl is-active --quiet minecraft.service; then
mjust/libexec/validate-backend:318:    pass 'minecraft.service is active'
mjust/libexec/validate-backend:323:        if players="$(podman exec minecraft rcon-cli 'list' 2>/dev/null)"; then
mjust/libexec/validate-backend:334:            if geyser="$(podman exec minecraft rcon-cli 'geyser version' 2>/dev/null)"; then
mjust/libexec/validate-backend:350:    fail 'minecraft.service is not active'
mjust/libexec/migration-export-backend:31:        systemctl start minecraft.service || rc=1
mjust/libexec/migration-export-backend:108:if systemctl is-active --quiet minecraft.service; then
mjust/libexec/migration-export-backend:111:        server_reported="$(podman exec minecraft rcon-cli 'version' 2>/dev/null | head -n1 || true)"
mjust/libexec/migration-export-backend:112:        geyser_reported="$(podman exec minecraft rcon-cli 'geyser version' 2>/dev/null | head -n1 || true)"
mjust/libexec/migration-export-backend:114:        server_reported="$(podman exec minecraft mc-monitor status --json 2>/dev/null | jq -er '.server_info.version.name | select(type == "string")' || true)"
mjust/libexec/migration-export-backend:116:    if server_supports_plugins; then plugins_reported="$(podman exec minecraft rcon-cli 'plugins' 2>/dev/null | head -n1 || true)"; fi
mjust/libexec/migration-export-backend:120:    systemctl stop minecraft.service
mjust/libexec/migration-export-backend:121:    if systemctl is-active --quiet minecraft.service; then
mjust/libexec/migration-export-backend:287:    systemctl start minecraft.service
mjust/libexec/backup-common.sh:61:    MINECRAFT_SERVICE="${MINECRAFT_SERVICE:-minecraft.service}"
mjust/libexec/admin-setup-runtime-transaction-json:15:    /etc/justvoxel/minecraft.env
mjust/libexec/admin-setup-runtime-transaction-json:17:    /etc/containers/systemd/minecraft.container
mjust/libexec/admin-setup-runtime-transaction-json:195:    jv_trace_run systemctl cat minecraft.service >/dev/null 2>&1 && return 1
mjust/libexec/admin-setup-runtime-transaction-json:309:        if jv_trace_run podman exec minecraft rcon-cli list >/dev/null 2>&1 &&
mjust/libexec/admin-setup-runtime-transaction-json:310:            jv_trace_run systemctl is-active --quiet minecraft.service; then
mjust/libexec/admin-setup-runtime-transaction-json:313:        restarts="$(jv_trace_run systemctl show minecraft.service --property=NRestarts --value)" || return 1
mjust/libexec/admin-setup-runtime-transaction-json:345:    restart_baseline="$(jv_trace_run systemctl show minecraft.service --property=NRestarts --value)" || return 1
mjust/libexec/admin-setup-runtime-transaction-json:347:    jv_trace_command systemctl start minecraft.service
mjust/libexec/admin-setup-runtime-transaction-json:348:    output="$(systemctl start minecraft.service 2>&1)"
mjust/libexec/admin-setup-runtime-transaction-json:380:        jv_trace_command podman exec minecraft rcon-cli 'geyser version'
mjust/libexec/admin-setup-runtime-transaction-json:381:        output="$(podman exec minecraft rcon-cli 'geyser version' 2>&1)"
mjust/libexec/admin-setup-runtime-transaction-json:440:    if ! jv_trace_run systemctl stop minecraft.service >/dev/null 2>&1; then
mjust/libexec/admin-setup-runtime-transaction-json:442:        [[ $(jv_trace_run systemctl show -p LoadState --value minecraft.service 2>/dev/null) == not-found ]] || failure=1
mjust/libexec/admin-setup-runtime-transaction-json:444:    if jv_trace_run systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-setup-runtime-transaction-json:484:    jv_trace_run systemctl is-active --quiet minecraft.service || { _a55_json false false runtime_commit 'Minecraft is not active.'; return 0; }
mjust/libexec/admin-setup-runtime-transaction-json:648:    for unit in minecraft.service minecraft-backup.service minecraft-backup.timer; do
mjust/libexec/admin-restore-transaction-json:38:    podman exec minecraft rcon-cli 'list' 2>/dev/null || true
mjust/libexec/admin-restore-transaction-json:50:    if systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-restore-transaction-json:56:            systemctl stop minecraft.service >/dev/null 2>&1 || rollback_ok=no
mjust/libexec/admin-restore-transaction-json:88:        if systemctl start minecraft.service >/dev/null 2>&1             && /usr/libexec/justvoxel/mjust/restore-runtime-validate >/dev/null 2>&1; then
mjust/libexec/admin-restore-transaction-json:122:    if [[ ${minecraft_stopped_by_restore} == yes ]] && ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-restore-transaction-json:123:        systemctl start minecraft.service >/dev/null 2>&1 || cleanup_ok=no
mjust/libexec/admin-restore-transaction-json:258:if systemctl is-active --quiet minecraft.service; then
mjust/libexec/admin-restore-transaction-json:295:if systemctl is-active --quiet minecraft.service; then
mjust/libexec/admin-restore-transaction-json:365:systemctl start minecraft.service >/dev/null
mjust/libexec/verify-minecraft-stack:13:    JV_INSTANCE_UNIT=minecraft.service
mjust/libexec/admin-discovery-json:225:        output="$(timeout 10 podman exec minecraft rcon-cli 'whitelist on' 2>/dev/null)" || return 1
mjust/libexec/admin-discovery-json:228:        output="$(timeout 10 podman exec minecraft rcon-cli 'whitelist off' 2>/dev/null)" || return 1
mjust/libexec/admin-discovery-json:490:    if [[ ${mode} == apply && ${memory_restart_required} == true ]] && systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-discovery-json:548:        if [[ ${old_whitelist_enabled} != "${new_whitelist_enabled}" ]] && systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-discovery-json:561:                    if ! timeout 15 systemctl stop minecraft.service || systemctl is-active --quiet minecraft.service; then
mjust/libexec/admin-discovery-json:576:            if ! systemctl restart minecraft.service || ! systemctl is-active --quiet minecraft.service; then
mjust/libexec/admin-discovery-json:584:                systemctl restart minecraft.service >/dev/null 2>&1 || true
mjust/libexec/web-status-json:54:    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/web-status-json:111:            if ! systemctl --no-block start minecraft.service; then
mjust/libexec/web-status-json:154:                if ! systemctl --no-block start minecraft.service; then
mjust/libexec/web-status-json:249:        if systemctl is-failed --quiet minecraft.service 2>/dev/null; then
mjust/libexec/web-status-json:251:        elif systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/web-status-json:272:            active_us="$(systemctl show minecraft.service -p ActiveEnterTimestampMonotonic --value 2>/dev/null || true)"
mjust/libexec/web-status-json:278:                geyser="$(podman exec minecraft rcon-cli 'geyser version' 2>/dev/null | head -n1 || true)"
mjust/libexec/web-status-json:360:        details_systemd="$(systemctl --no-pager --full status minecraft.service 2>&1 || true)"
mjust/libexec/start-over:19:keep_dropin=/run/systemd/system/minecraft.service.d/justvoxel-keep-container.conf
mjust/libexec/start-over:77:    rmdir /run/systemd/system/minecraft.service.d 2>/dev/null || true
mjust/libexec/start-over:85:if systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/restore-runtime-validate:9:if ! systemctl is-active --quiet minecraft.service; then
mjust/libexec/restore-runtime-validate:10:    echo 'ERROR: minecraft.service is not active after restore.' >&2
mjust/libexec/restore-runtime-validate:19:players="$(podman exec minecraft rcon-cli 'list' 2>/dev/null)" || { echo 'ERROR: Minecraft player query failed after restore.' >&2; exit 1; }
mjust/libexec/minecraft-reset-common.sh:171:    jv_trace_run systemctl reset-failed minecraft.service minecraft-backup.service minecraft-backup.timer >/dev/null 2>&1 || true
mjust/libexec/migration-recover-backend:101:        if systemctl is-active --quiet minecraft.service 2>/dev/null; then echo 'ERROR: Minecraft is active after a fresh-import rollback.' >&2; exit 1; fi
mjust/libexec/migration-import-common.sh:24:    systemctl stop minecraft.service >/dev/null 2>&1 || true
mjust/libexec/migration-import-common.sh:58:        if systemctl start minecraft.service; then
mjust/libexec/migration-import-common.sh:63:                    systemctl stop minecraft.service || rollback_ok=no
mjust/libexec/migration-import-common.sh:116:            if systemctl start minecraft.service >/dev/null 2>&1; then
mjust/libexec/migration-import-activate.sh:3:query_players() { podman exec minecraft rcon-cli 'list' 2>/dev/null || true; }
mjust/libexec/migration-import-activate.sh:4:if [[ ${configured} == yes ]] && systemctl is-active --quiet minecraft.service; then
mjust/libexec/migration-import-activate.sh:108:systemctl start minecraft.service
mjust/libexec/migration-common.sh:179:/etc/justvoxel/minecraft.env
mjust/libexec/migration-common.sh:181:/etc/containers/systemd/minecraft.container
mjust/libexec/migration-common.sh:252:    systemctl stop minecraft.service >/dev/null 2>&1 || true
mjust/libexec/migration-common.sh:285:    systemctl stop minecraft.service >/dev/null 2>&1 || true
mjust/libexec/menu:7:[[ -e /etc/containers/systemd/minecraft.container ]] && configured=yes
mjust/libexec/menu:325:    [[ -e /etc/containers/systemd/minecraft.container ]] && configured=yes || configured=no
mjust/libexec/interrupt-safety.sh:7:        jv_trace_run podman exec minecraft rcon-cli 'list' 2>/dev/null
mjust/libexec/interrupt-safety.sh:9:        podman exec minecraft rcon-cli 'list' 2>/dev/null
mjust/libexec/interrupt-safety.sh:23:    if systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/interrupt-safety.sh:45:    podman exec minecraft rcon-cli "say Server shutting down in ${seconds} ${unit}" >/dev/null 2>&1 || true
mjust/libexec/interrupt-safety.sh:49:    if declare -F jv_trace_command >/dev/null; then jv_trace_stage "SERVICE Stopping Minecraft"; jv_trace_command systemctl --no-block stop minecraft.service; fi
mjust/libexec/interrupt-safety.sh:51:    if systemctl --no-block stop minecraft.service; then
mjust/libexec/interrupt-safety.sh:120:    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/interrupt-safety.sh:161:    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/interrupt-safety.sh:208:    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/interrupt-safety.sh:212:    players="$(timeout 5s podman exec minecraft rcon-cli 'list' 2>/dev/null || true)"
mjust/libexec/interrupt-safety.sh:237:    if ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/common.sh:9:readonly JV_QUADLET=/etc/containers/systemd/minecraft.container
mjust/libexec/common.sh:692:        if podman exec minecraft rcon-cli 'list' >/dev/null 2>&1 &&
mjust/libexec/common.sh:693:            systemctl is-active --quiet minecraft.service; then
mjust/libexec/common.sh:713:    systemctl stop minecraft.service
mjust/libexec/common.sh:718:    systemctl start minecraft.service
mjust/libexec/admin-version-status-json:9:JV_INSTANCE_UNIT=minecraft.service
mjust/libexec/admin-system-update-reboot-json:59:    if systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-system-actions-json:51:    systemctl is-active --quiet minecraft.service 2>/dev/null && minecraft_state=running
mjust/libexec/admin-system-actions-json:157:        [[ ${was_active} == yes ]] && systemctl start minecraft.service >/dev/null 2>&1 || true
management/cmd/justvoxel-management-agent/setup_runtime_evidence.go:85:	container, unit, timer := "minecraft", "minecraft.service", "minecraft-backup.timer"
management/cmd/justvoxel-management-agent/setup_runtime_evidence.go:86:	env, quadlet := "/etc/justvoxel/minecraft.env", "/etc/containers/systemd/minecraft.container"
management/cmd/justvoxel-management-agent/setup_runtime_evidence.go:157:	unit := "minecraft.service"
management/cmd/justvoxel-management-agent/setup_runtime_evidence.go:210:	unit := "minecraft.service"
mjust/libexec/admin-minecraft-reset-json:106:if jv_trace_run systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-factory-reset-json:189:if [[ ${configured} == true ]] && jv_trace_run systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-factory-reset-json:239:    jv_trace_run systemctl reset-failed minecraft.service minecraft-backup.service minecraft-backup.timer >/dev/null 2>&1 || true
mjust/libexec/admin-factory-reset-json:244:   jv_trace_run systemctl is-active --quiet minecraft.service 2>/dev/null ||
mjust/libexec/admin-migration-recovery-transaction-json:22:        ! systemctl is-active --quiet minecraft.service 2>/dev/null || { echo "Minecraft became active during fresh recovery" >&2; exit 1; }
mjust/libexec/admin-data-migration-transaction-json:72:    systemctl start minecraft.service >/dev/null 2>&1 || return 1
mjust/libexec/admin-data-migration-transaction-json:79:    if systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-data-migration-transaction-json:80:        systemctl stop minecraft.service >/dev/null 2>&1 || rollback_ok=no
mjust/libexec/admin-data-migration-transaction-json:94:        systemctl start minecraft.service >/dev/null 2>&1 || rollback_ok=no
mjust/libexec/admin-data-migration-transaction-json:131:    if [[ ${minecraft_stopped_by_migration} == yes ]] && ! systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-data-migration-transaction-json:335:if systemctl is-active --quiet minecraft.service; then
mjust/libexec/admin-data-migration-transaction-json:383:if systemctl is-active --quiet minecraft.service 2>/dev/null; then
mjust/libexec/admin-data-migration-transaction-json:421:    systemctl start minecraft.service >/dev/null 2>&1
mjust/libexec/admin-data-migration-transaction-json:426:    ! systemctl is-active --quiet minecraft.service 2>/dev/null
mjust/libexec/admin-migration-export-transaction-json:47:        if systemctl start minecraft.service >/dev/null 2>&1 \
mjust/libexec/admin-migration-export-transaction-json:226:if systemctl is-active --quiet minecraft.service; then
mjust/libexec/admin-migration-export-transaction-json:237:        server_reported="$(podman exec minecraft rcon-cli 'version' 2>/dev/null | head -n1 || true)"
mjust/libexec/admin-migration-export-transaction-json:238:        geyser_reported="$(podman exec minecraft rcon-cli 'geyser version' 2>/dev/null | head -n1 || true)"
mjust/libexec/admin-migration-export-transaction-json:240:        server_reported="$(podman exec minecraft mc-monitor status --json 2>/dev/null | jq -er '.server_info.version.name | select(type == "string")' || true)"
mjust/libexec/admin-migration-export-transaction-json:266:    systemctl start minecraft.service
mjust/libexec/configuration-api.sh:20:    response="$("${JV_CONFIG_API_CLIENT}" GET /v1/admin/configuration)"
mjust/libexec/configuration-api.sh:93:    plan="$(jv_config_post /v1/admin/configuration/plan "${payload}")" || return 1
mjust/libexec/configuration-api.sh:111:    response="$(jv_config_post /v1/admin/configuration/apply "${payload}")" || return 1
mjust/libexec/configuration-api.sh:126:        response="$(jv_config_post /v1/admin/configuration/apply "${payload}")" || return 1
mjust/libexec/logs:18:response="$("${api_client}" GET '/v1/logs/minecraft?limit=100&format=cat')"
mjust/libexec/service:24:    response="$(printf '{"confirm_players":%s}\n' "${confirm_players}" | "${api_client}" POST "/v1/minecraft/${action}" --data)"
```

## Exact changed-file manifest

77 changed/new files, including the initial partial C2 files and this report.

- `docs/MINECRAFT-CONVERGENCE-C2.md`
- `docs/MJUST.md`
- `docs/RESTORE.md`
- `management/cmd/justvoxel-management-agent/admin_minecraft_stack_update.go`
- `management/cmd/justvoxel-management-agent/admin_minecraft_stack_update_test.go`
- `management/cmd/justvoxel-management-agent/admin_setup_apply.go`
- `management/cmd/justvoxel-management-agent/admin_setup_apply_test.go`
- `management/cmd/justvoxel-management-agent/admin_setup_recover.go`
- `management/cmd/justvoxel-management-agent/admin_version_status.go`
- `management/cmd/justvoxel-management-agent/admin_version_status_test.go`
- `management/cmd/justvoxel-management-agent/main.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_activation.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_activation_test.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_backups.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_lifecycle.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_maintenance.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_runtime.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_runtime_api.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_version.go`
- `management/cmd/justvoxel-management-agent/minecraft_instance_version_test.go`
- `management/cmd/justvoxel-management-agent/operation_store.go`
- `management/cmd/justvoxel-management-agent/operator_surfaces.go`
- `management/cmd/justvoxel-management-agent/operator_surfaces_idempotent_test.go`
- `management/cmd/justvoxel-management-agent/operator_surfaces_test.go`
- `management/cmd/justvoxel-management-agent/setup_diagnostics.go`
- `management/cmd/justvoxel-management-agent/setup_instance_transaction.go`
- `management/cmd/justvoxel-management-agent/setup_instance_transaction_test.go`
- `management/cmd/justvoxel-management-agent/setup_runtime_evidence.go`
- `management/cmd/justvoxel-management-agent/setup_runtime_transaction.go`
- `management/cmd/justvoxel-management-agent/setup_runtime_transaction_test.go`
- `mjust/justfile`
- `mjust/libexec/admin-discovery-json`
- `mjust/libexec/admin-version-status-json`
- `mjust/libexec/backup`
- `mjust/libexec/instance-context.sh`
- `mjust/libexec/instance-runtime-prerequisites`
- `mjust/libexec/instance-selection.sh`
- `mjust/libexec/menu`
- `mjust/libexec/players`
- `mjust/libexec/restore`
- `mjust/libexec/update-minecraft`
- `mjust/libexec/update-minecraft-backend`
- `mjust/libexec/verify-minecraft-stack`
- `mjust/libexec/whitelist`
- `mjust/libexec/whitelist-backend`
- `tests/minecraft-instance-workspace.test.cjs`
- `tests/test-bedrock-whitelist.sh`
- `tests/test-mjust-api-foundation.sh`
- `tests/test-restore.sh`
- `tests/version-workspace.test.cjs`
- `webui/internal/api/minecraft_instances.go`
- `webui/internal/api/operations.go`
- `webui/internal/api/persistent_operations.go`
- `webui/internal/api/version_stack_test.go`
- `webui/internal/api/version_status.go`
- `webui/internal/server/add_server_wizard.go`
- `webui/internal/server/backups_test_support_test.go`
- `webui/internal/server/backups_workspace_safety_test.go`
- `webui/internal/server/backups_workspace_test.go`
- `webui/internal/server/date_time_version_api_test.go`
- `webui/internal/server/date_time_version_ui_contract_test.go`
- `webui/internal/server/minecraft_instance_test.go`
- `webui/internal/server/minecraft_servers.go`
- `webui/internal/server/minecraft_servers_test.go`
- `webui/internal/server/minecraft_workspace.go`
- `webui/internal/server/minecraft_workspace_test.go`
- `webui/internal/server/role_pages.go`
- `webui/internal/server/selected_backups.go`
- `webui/internal/server/selected_server.go`
- `webui/internal/server/selected_server_test.go`
- `webui/internal/server/server.go`
- `webui/internal/server/static/app.js`
- `webui/internal/server/static/version-workspace.js`
- `webui/internal/server/templates/minecraft_server_overview.html`
- `webui/internal/server/version_software_ui_test.go`
- `webui/internal/server/version_stack_test.go`
- `webui/internal/server/version_workspace.go`

Final static checks: gofmt on 51 Go files; bash -n on 17 shell files; node --check on 4 JavaScript/CJS files; git diff --check. All passed. No executable test suite or Go build-cache command was run.
