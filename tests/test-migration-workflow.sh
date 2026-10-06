#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail(){ echo "FAIL: $*" >&2; exit 1; }
# Static workflow invariants that require the full appliance at runtime.
import_files=(
    "${repo_root}/mjust/libexec/migration-import"
    "${repo_root}/mjust/libexec/migration-import-common.sh"
    "${repo_root}/mjust/libexec/migration-import-plan-source.sh"
    "${repo_root}/mjust/libexec/migration-import-plan-destination.sh"
    "${repo_root}/mjust/libexec/migration-import-activate.sh"
)
import_text="$(cat "${import_files[@]}")"
import_frontend="${repo_root}/mjust/libexec/migration-import"
import_backend="${repo_root}/mjust/libexec/migration-import-backend"
grep -Fq 'migration-api.sh' "${import_frontend}" || fail 'migration Import frontend does not use the shared API helper'
grep -Fq 'jv_migration_import_plan_review' "${import_frontend}" || fail 'migration Import frontend does not plan through the Agent'
grep -Fq 'jv_migration_import_apply' "${import_frontend}" || fail 'migration Import frontend does not apply through the Agent'
grep -Fq 'Attached disk / partition' "${import_frontend}" || fail 'migration Import attached-disk source choice missing'
grep -Fq 'USB / removable storage' "${import_frontend}" || fail 'migration Import removable source choice missing'
grep -Fq 'Configured JustVoxel backup storage' "${import_frontend}" || fail 'migration Import configured-backup source choice missing'
grep -Fq 'Temporary NFS share' "${import_frontend}" || fail 'migration Import NFS source choice missing'
grep -Fq 'Temporary SMB/CIFS share' "${import_frontend}" || fail 'migration Import SMB source choice missing'
grep -Fq 'source_selection_required' "${import_frontend}" || fail 'migration Import transport source-entry review missing'
grep -Fq 'jv_migration_monitor_operation' "${import_frontend}" || fail 'migration Import frontend does not monitor the persistent Agent operation'
grep -Fq 'interrupt-safety.sh' "${import_backend}" || fail 'shared migration import backend does not load interruption safety'
grep -Fq 'JV_MIGRATION_API_MODE' "${import_backend}" || fail 'shared migration Import backend has no non-interactive Agent mode'
for api_file in "${repo_root}/mjust/libexec/migration-import-common.sh" "${repo_root}/mjust/libexec/migration-import-plan-source.sh" "${repo_root}/mjust/libexec/migration-import-plan-destination.sh" "${repo_root}/mjust/libexec/migration-import-activate.sh"; do
    grep -Fq 'JV_MIGRATION_API_MODE' "${api_file}" || fail "Import backend component lacks Agent-mode handling: ${api_file}"
done
export_frontend="${repo_root}/mjust/libexec/migration-export"
exporter="${repo_root}/mjust/libexec/migration-export-backend"
recovery="${repo_root}/mjust/libexec/migration-recover"
recovery_backend="${repo_root}/mjust/libexec/migration-recover-backend"
transport_files=(
    "${repo_root}/mjust/libexec/migration-transport.sh"
    "${repo_root}/mjust/libexec/migration-transport-device.sh"
    "${repo_root}/mjust/libexec/migration-transport-network.sh"
    "${repo_root}/mjust/libexec/migration-transport-ui.sh"
)
transport_text="$(cat "${transport_files[@]}")"
import_source_helper="${repo_root}/mjust/libexec/migration-import-source.sh"
common="${repo_root}/mjust/libexec/common.sh"
migration_common="${repo_root}/mjust/libexec/migration-common.sh"
validate_backend="${repo_root}/mjust/libexec/validate-backend"
template="${repo_root}/templates/config/minecraft.env.in"
menu="${repo_root}/mjust/libexec/menu"
justfile="${repo_root}/mjust/justfile"

# shellcheck disable=SC1090
source "${migration_common}"
available_bytes="$(jv_migration_available_bytes "${repo_root}")" || fail 'migration free-space helper failed on a local path'
[[ ${available_bytes} =~ ^[0-9]+$ ]] || fail 'migration free-space helper did not return a numeric byte count'
(( available_bytes > 0 )) || fail 'migration free-space helper returned no available space'

# Shared compatibility and destination-specific availability, without network access.
api_planner="${repo_root}/mjust/libexec/admin-migration-import-plan-json"
source_planner="${repo_root}/mjust/libexec/migration-import-plan-source.sh"
destination_planner="${repo_root}/mjust/libexec/migration-import-plan-destination.sh"
(
    source "${common}"
    server_supports_plugins paper || fail 'Paper must support plugins'
    server_supports_plugins purpur || fail 'Purpur must support plugins'
    if server_supports_plugins vanilla; then fail 'Vanilla must not support plugins'; fi

    availability_fixture="$(mktemp -d)"
    trap 'rm -rf -- "${availability_fixture}"' EXIT
    curl() { fail 'availability fixture attempted network access'; }
    paper_version_build_channel() {
        printf 'paper %s\n' "$1" >> "${availability_fixture}/calls"
        [[ $1 == 27.1 ]]
    }
    purpur_metadata() {
        printf 'purpur metadata\n' >> "${availability_fixture}/calls"
        printf '%s' '{"versions":["28.3"]}'
    }
    MINECRAFT_SERVER_TYPE=paper
    source_version=27.1
    server_version_available "$source_version" || fail 'Paper availability did not use the destination helper'
    [[ $(cat "${availability_fixture}/calls") == 'paper 27.1' ]] || fail 'wrong Paper availability path'
    if server_version_available 28.3; then fail 'unavailable Paper version accepted'; fi
    rm -f "${availability_fixture}/calls"
    MINECRAFT_SERVER_TYPE=purpur
    source_version=28.3
    server_version_available "$source_version" || fail 'Purpur availability did not use the destination helper'
    [[ $(cat "${availability_fixture}/calls") == 'purpur metadata' ]] || fail 'wrong Purpur availability path'
    if server_version_available 27.1; then fail 'unavailable Purpur version accepted'; fi
)
for planner in "${api_planner}" "${source_planner}"; do
    grep -Fq 'server_supports_plugins "${MINECRAFT_SERVER_TYPE-paper}"' "${planner}" || fail 'planner lacks shared destination compatibility gate'
    grep -Fq 'server_allows_in_place_switch "${MINECRAFT_SERVER_TYPE-paper}" "$(jq -r '\''.minecraft.implementation // "paper"'\'' <<< "${native_manifest}")"' "${planner}" || fail 'planner lacks existing in-place switch compatibility authority'
    grep -Fq 'server_version_available "$source_version"' "${planner}" || fail 'planner lacks destination version availability preflight'
    if grep -Fq 'paper_version_has_stable_build' "${planner}"; then fail 'Import uses a Paper-only availability check'; fi
done
grep -Fxq 'MINECRAFT_VERSION_MODE=pinned' "${destination_planner}" || fail 'Import does not pin the source version'
grep -Fxq 'MINECRAFT_VERSION="${source_version}"' "${destination_planner}" || fail 'Import does not retain the exact source version'

# Exercise the actual API planner with bounded source/configuration fixtures.
# Appliance helpers are replaced only in this temporary test copy.
(
    source "${common}"
    review_fixture="$(mktemp -d)"
    trap 'rm -rf -- "${review_fixture}"' EXIT
    export REVIEW_FIXTURE="${review_fixture}"
    mkdir -p "${review_fixture}/server" "${review_fixture}/helpers"
    REVIEW_CONFIG="${review_fixture}/config"
    REVIEW_QUADLET="${review_fixture}/quadlet"
    touch "$REVIEW_CONFIG" "$REVIEW_QUADLET"
    sed -e '/^source /d' -e '/^require_root$/d' \
        -e '/^case "${1:-discover}" in/,$d' \
        -e 's/JV_CONFIG/REVIEW_CONFIG/g; s/JV_QUADLET/REVIEW_QUADLET/g' \
        -e "s|/usr/libexec/justvoxel/mjust/|${review_fixture}/helpers/|g" \
        "$api_planner" > "${review_fixture}/planner.sh"
    source "${review_fixture}/planner.sh"
    cat > "${review_fixture}/helpers/migration-archive" <<'FIXTURE'
#!/usr/bin/bash
set -euo pipefail
case "$1" in
    inspect) jq -cn --argjson native "$REVIEW_NATIVE" '{expandedBytes:1024,nativeBundle:$native}' ;;
    verify-native) jq -cn --arg software "$REVIEW_SOURCE_TYPE" '{minecraft:{implementation:$software,version:"26.2",onlineMode:true}}' ;;
    detect) jq -cn --arg root "$REVIEW_FIXTURE/server" --arg type "$REVIEW_CANDIDATE_TYPE" '{candidates:[{root:$root,sourceType:$type,minecraftVersion:"26.2",onlineMode:true,maxPlayers:10,javaPortHint:25565,pluginJarCount:0}]}' ;;
    *) exit 1 ;;
esac
FIXTURE
    cat > "${review_fixture}/helpers/validate-data-mount" <<'FIXTURE'
#!/usr/bin/bash
exit 0
FIXTURE
    cat > "${review_fixture}/helpers/web-status-json" <<'FIXTURE'
#!/usr/bin/bash
printf '%s\n' '{"state":"stopped","online":0,"names":[]}'
FIXTURE
    chmod +x "${review_fixture}/helpers/"*
    require_config() { :; }
    jv_migration_import_source_prepare() {
        JV_MIGRATION_IMPORT_SOURCE_PATH="${review_fixture}/server"
        JV_MIGRATION_IMPORT_SOURCE_IDENTITY=source-fixture
    }
    jv_migration_import_source_cleanup() { :; }
    jv_restore_validate_data_layout() { :; }
    jv_migration_check_candidate_port() { :; }
    # A rejected software plan must never ask for version availability.
    server_version_available() {
        [[ $MINECRAFT_SERVER_TYPE != vanilla ]] || fail 'incompatible plan checked destination version availability'
        printf '%s\n' "$MINECRAFT_SERVER_TYPE" >> "${review_fixture}/availability"
    }
    DATA_PATH="${review_fixture}/server" BACKUP_PATH="${review_fixture}/backups"
    JAVA_PORT=25565 BEDROCK_PORT=19132 BEDROCK_ENABLED=no
    JAVA_MEMORY=4G CONTAINER_MEMORY=6G TIMEZONE=UTC MINECRAFT_IMAGE_TAG=stable
    MINECRAFT_UID=1001 MINECRAFT_GID=1001 BACKUP_KEEP=7
    BACKUP_SCHEDULE='*-*-* 04:30:00' BACKUP_TIMER_ENABLED=yes
    review_request="$(jq -cn --arg path "$DATA_PATH" '{source:{kind:"local",path:$path},destination:{java_port:25565,bedrock_port:19132,backup_keep:7,backup_automatic:true,storage:{},backups:{}}}')"
    for source_software in paper purpur; do
        export REVIEW_SOURCE_TYPE="$source_software" REVIEW_NATIVE=true REVIEW_CANDIDATE_TYPE=itzg-paper
        MINECRAFT_SERVER_TYPE=vanilla
        rejected="$(plan <<< "$review_request")"
        jq -e --arg source "$source_software" --arg data "$DATA_PATH" '
            .ok==false and .code=="unsupported_server_type" and
            .normalized.source.server_type==$source and .normalized.source.minecraft_version=="26.2" and
            .normalized.source.online_mode=="online" and .normalized.source.max_players==10 and
            .normalized.destination.server_type=="vanilla" and .normalized.destination.data_path==$data and
            .normalized.destination.java_memory=="4G" and
            .requirements==null and .context==null and .plan_fingerprint==null
        ' >/dev/null <<< "$rejected" || fail 'incompatible plan lost review facts or became Apply-ready'
        [[ ! -e ${review_fixture}/availability ]] || fail 'rejected plan checked version availability'
        for destination_software in paper purpur; do
            MINECRAFT_SERVER_TYPE="$destination_software"
            compatible="$(plan <<< "$review_request")"
            jq -e --arg source "$source_software" --arg destination "$destination_software" '
                .ok==true and .normalized.source.server_type==$source and
                .normalized.destination.server_type==$destination and
                .requirements.import_confirmation_required==true and
                .requirements.vanilla_confirmation_required==false and .context!=null
            ' >/dev/null <<< "$compatible" || fail 'compatible Paper/Purpur planning changed'
            [[ $(cat "${review_fixture}/availability") == "$destination_software" ]] || fail 'compatible plan skipped version availability'
            rm "${review_fixture}/availability"
        done
    done
    export REVIEW_SOURCE_TYPE=vanilla REVIEW_NATIVE=false REVIEW_CANDIDATE_TYPE=vanilla
    MINECRAFT_SERVER_TYPE=paper
    guarded="$(plan <<< "$review_request")"
    jq -e '
        .ok==true and .normalized.source.server_type=="vanilla" and
        .normalized.destination.server_type=="paper" and
        .requirements.vanilla_confirmation_required==true and
        any(.warnings[]; .code=="vanilla_conversion") and .context!=null
    ' >/dev/null <<< "$guarded" || fail 'guarded Vanilla to Paper planning changed'
)

# Exercise the managed-container ownership decision without a Podman daemon.
port_fixture="$(mktemp -d)"
trap 'rm -rf -- "${port_fixture}"' EXIT
cat > "${port_fixture}/podman" <<'EOF'
#!/usr/bin/bash
case "$1 $2 $3 $4 $5" in
    'inspect --type container --format {{.State.Running}}')
        [[ $6 == minecraft && ${FAKE_MINECRAFT_STATE:-missing} != missing ]] || exit 1
        printf '%s\n' "${FAKE_MINECRAFT_STATE}"
        ;;
    'port minecraft 19132/udp  ')
        [[ ${FAKE_MINECRAFT_MAPPING:-missing} != missing ]] || exit 1
        printf '%s\n' "${FAKE_MINECRAFT_MAPPING}"
        ;;
    *) exit 1 ;;
esac
EOF
cat > "${port_fixture}/ss" <<'EOF'
#!/usr/bin/bash
if [[ ${FAKE_UDP_LISTENER:-no} == yes && $2 == -lun ]]; then
    printf '%s\n' 'UNCONN 0 0 0.0.0.0:19132 0.0.0.0:*'
fi
EOF
chmod 0755 "${port_fixture}/podman" "${port_fixture}/ss"
export PATH="${port_fixture}:${PATH}"
export FAKE_MINECRAFT_STATE=running FAKE_MINECRAFT_MAPPING=0.0.0.0:19132 FAKE_UDP_LISTENER=yes
FAKE_MINECRAFT_STATE=true
jv_migration_running_minecraft_owns_port udp 19132 || fail 'running minecraft UDP mapping was not recognized'
if jv_migration_running_minecraft_owns_port tcp 19132; then fail 'UDP mapping was recognized as TCP'; fi
FAKE_MINECRAFT_STATE=false
if jv_migration_running_minecraft_owns_port udp 19132; then fail 'stopped minecraft was recognized as owning a port'; fi
FAKE_MINECRAFT_STATE=missing
if jv_migration_running_minecraft_owns_port udp 19132; then fail 'missing minecraft was recognized as owning a port'; fi
FAKE_MINECRAFT_STATE=true FAKE_MINECRAFT_MAPPING=0.0.0.0:19133
if jv_migration_running_minecraft_owns_port udp 19132; then fail 'different host mapping was recognized as requested port'; fi
FAKE_MINECRAFT_MAPPING=0.0.0.0:19132
jv_migration_check_import_bedrock_port 19132 yes 19132 || fail 'enabled current Bedrock port was rejected'
jv_migration_check_import_bedrock_port 19132 no 19133 || fail 'running managed Bedrock port was rejected after cross-play disable'
FAKE_MINECRAFT_MAPPING=0.0.0.0:19133
if jv_migration_check_import_bedrock_port 19132 no 19132 2>/dev/null; then fail 'unrelated Bedrock listener was allowed'; fi
grep -Fq 'jv_migration_check_import_bedrock_port "$bedrock_port" "$BEDROCK_ENABLED" "$BEDROCK_PORT"' "${repo_root}/mjust/libexec/admin-migration-import-plan-json" || fail 'API Import planner does not use shared Bedrock check'
grep -Fq 'jv_migration_check_import_bedrock_port "${BEDROCK_PORT}" "${old_bedrock_enabled}" "${old_bedrock}"' "${repo_root}/mjust/libexec/migration-import-plan-destination.sh" || fail 'CLI Import planner does not use shared Bedrock check'

recovery_transaction="${repo_root}/mjust/libexec/admin-migration-recovery-transaction-json"
validator_line='        /usr/libexec/justvoxel/mjust/restore-runtime-validate >&2'
grep -Fxq "${validator_line}" "${recovery_transaction}" || fail 'recovery validator stdout is not isolated from JSON events'
cat > "${port_fixture}/restore-runtime-validate" <<'EOF'
#!/usr/bin/bash
echo 'RCON: checked'
echo 'Minecraft: checked'
[[ ${FAKE_VALIDATOR_FAIL:-no} == no ]]
EOF
chmod 0755 "${port_fixture}/restore-runtime-validate"
validator_call="${validator_line/\/usr\/libexec\/justvoxel\/mjust\/restore-runtime-validate/${port_fixture}\/restore-runtime-validate}"
bash -e -c "printf '%s\\n' '{\"event\":\"progress\"}'; ${validator_call}; printf '%s\\n' '{\"event\":\"result\"}'" > "${port_fixture}/recovery.out" 2> "${port_fixture}/recovery.err" || fail 'successful recovery validator fixture failed'
[[ $(wc -l < "${port_fixture}/recovery.out") == 2 ]] || fail 'validator diagnostics contaminated recovery JSON stdout'
jq -e '.event == "progress" or .event == "result"' "${port_fixture}/recovery.out" >/dev/null || fail 'recovery fixture emitted non-JSON stdout'
grep -Fq 'RCON: checked' "${port_fixture}/recovery.err" || fail 'validator diagnostics were not preserved on stderr'
if FAKE_VALIDATOR_FAIL=yes bash -e -c "${validator_call}; printf '%s\\n' '{\"event\":\"result\"}'" > "${port_fixture}/failed.out" 2> "${port_fixture}/failed.err"; then fail 'validator failure did not fail recovery'; fi
[[ ! -s "${port_fixture}/failed.out" ]] || fail 'failed validator emitted recovery result'

for text in \
    'Type IMPORT to continue:' \
    'The source eula.txt, if present, is NOT accepted' \
    'plugins are executable server code' \
    'jv_migration_snapshot_runtime' \
    'jv_migration_rollback_data' \
    'jv_migration_assert_fresh_selinux_path' \
    'jv_migration_remove_fresh_selinux_rule' \
    'restore-runtime-validate' \
    'jv_stop_minecraft_adaptive' \
    '/usr/libexec/justvoxel/mjust/validate' \
    'JUSTVOXEL_REGENERATE_RCON=1' \
    'online-mode=false' \
    'MINECRAFT_VERSION_MODE=pinned' \
    'mjust migration-recover'; do
    grep -Fq "${text}" <<< "${import_text}" || fail "import workflow invariant missing: ${text}"
done
grep -Fq 'migration-api.sh' "${export_frontend}" || fail 'migration Export frontend does not use shared API helper'
grep -Fq "stat -Lc 'local:%d:%i'" "${repo_root}/mjust/libexec/admin-migration-export-plan-json" || fail 'Export local target identity still depends on mutable directory metadata'
grep -Fq "stat -Lc '%d:%i' \"\$DATA_PATH\"" "${repo_root}/mjust/libexec/admin-migration-export-plan-json" || fail 'Export data identity still depends on mutable world size/mtime'
if grep -Fq "local:%d:%i:%Y" "${repo_root}/mjust/libexec/admin-migration-export-plan-json"; then fail 'Export local target identity includes mutable mtime'; fi
if grep -Fq "%d:%i:%s:%Y" "${repo_root}/mjust/libexec/admin-migration-export-plan-json"; then fail 'Export data identity includes mutable size/mtime'; fi
for text in '.partial' 'verify-native' 'flock -n' 'jv_player_check_before_interrupt' 'sync -f' 'mv -- "${partial}"'; do
    grep -Fq "${text}" "${exporter}" || fail "shared export backend invariant missing: ${text}"
done
for fs in ext4 xfs btrfs vfat exfat; do
    grep -Fq "${fs}" <<< "${transport_text}" || fail "temporary media allowlist missing ${fs}"
done
grep -Fq 'NTFS removable media is not supported' <<< "${transport_text}" || fail 'NTFS refusal is missing'
grep -Fq 'jv_migration_import_source_prepare' "${import_source_helper}" || fail 'shared Import source resolver is missing'
grep -Fq 'jv_migration_import_source_entries_json' "${import_source_helper}" || fail 'shared Import source candidate discovery is missing'
grep -Fq 'jv_migration_import_source_cleanup' "${import_source_helper}" || fail 'shared Import source cleanup is missing'
bash -n "${import_source_helper}" || fail 'shared Import source resolver failed bash syntax validation'
grep -Fq 'source_kinds:["local","backup","device","nfs","smb"]' "${repo_root}/mjust/libexec/admin-migration-import-plan-json" || fail 'Import discovery does not advertise full source transport parity'
grep -Fq 'JV_MIGRATION_IMPORT_SOURCE_IDENTITY' "${repo_root}/mjust/libexec/admin-migration-import-transaction-json" || fail 'Import execution does not revalidate remounted source identity'
if grep -Fq 'admin-migration-import-plan-json plan' "${repo_root}/mjust/libexec/admin-migration-import-transaction-json"; then fail 'Import transaction must not re-plan the archive after Agent Apply review'; fi
if grep -Fq 'postplan=' "${repo_root}/mjust/libexec/admin-migration-import-transaction-json"; then fail 'Import transaction must not post-failure re-plan or retry the archive'; fi
if grep -Fq 'jv_migration_source_identity "${JV_MIGRATION_SOURCE}"' "${repo_root}/mjust/libexec/migration-import-activate.sh"; then fail 'Import activation must not re-read the original source after staging'; fi
grep -Fq 'jv_migration_api_result "${preactivation_outcome}" pre-activation' "${repo_root}/mjust/libexec/migration-import-common.sh" || fail 'Import backend does not report bounded pre-activation failure directly'
activation="${repo_root}/mjust/libexec/migration-import-activate.sh"
grep -Fq 'restore-runtime-validate "${source_version}"' "${activation}" || fail 'Import does not verify the concrete source version'
if grep -Eq 'rcon-cli.*version' "${activation}" "${repo_root}/mjust/libexec/restore-runtime-validate"; then fail 'Import/restore bypasses readiness-aware version verification'; fi
grep -Fq 'verify-minecraft-stack "${1:-${MINECRAFT_VERSION}}"' "${repo_root}/mjust/libexec/restore-runtime-validate" || fail 'restore does not use the authoritative version verifier'
import_transaction="${repo_root}/mjust/libexec/admin-migration-import-transaction-json"
if grep -Eq '/usr/libexec/justvoxel/mjust/(restore-runtime-validate|validate|validate-backend)([[:space:]]|$)' "${import_transaction}"; then fail 'API wrapper repeats backend validation'; fi
# Backend success must pass through the journal's verifying state without validation work.
import_success_branch="$(sed -n '/^[[:space:]]*succeeded)/,/^[[:space:]]*;;/p' "${import_transaction}")"
[[ $(grep -c 'emit_progress verifying import_verify ' "${import_transaction}") == 1 ]] || fail 'Import transaction must emit exactly one verifying event'
grep -Fq 'emit_progress verifying import_verify ' <<< "${import_success_branch}" || fail 'Import backend success does not finalize the verifying state'
if grep -Eq 'restore-runtime-validate|validate-backend|(^|[^[:alnum:]_-])validate([^[:alnum:]_-]|$)' <<< "${import_success_branch}"; then fail 'Import success branch repeats backend validation'; fi
execute_line="$(grep -n '^emit_progress running import_execute ' "${import_transaction}" | cut -d: -f1)"
verify_line="$(grep -n '^[[:space:]]*emit_progress verifying import_verify ' "${import_transaction}" | cut -d: -f1)"
result_line="$(grep -n '^[[:space:]]*emit_result succeeded "\$status"$' "${import_transaction}" | cut -d: -f1)"
[[ -n ${execute_line} && -n ${verify_line} && -n ${result_line} ]] || fail 'Import transaction success event sequence is incomplete'
(( execute_line < verify_line && verify_line < result_line )) || fail 'Import transaction must emit running, verifying, then succeeded'
if grep -Fq 'Migration and Minecraft-version upgrade remain separate' "${activation}"; then fail 'Import completion retains stale version-upgrade wording'; fi
runtime_line="$(grep -n '^/usr/libexec/justvoxel/mjust/restore-runtime-validate ' "${activation}" | cut -d: -f1)"
validation_line="$(grep -n '^JUSTVOXEL_RUNTIME_ALREADY_VALIDATED=1 JUSTVOXEL_MAINTENANCE_LOCK_HELD=1 /usr/libexec/justvoxel/mjust/validate-backend$' "${activation}" | cut -d: -f1)"
validated_line="$(grep -n '^jv_migration_write_state .* validated ' "${activation}" | cut -d: -f1)"
success_line="$(grep -n '^jv_migration_api_result succeeded validated ' "${activation}" | cut -d: -f1)"
(( runtime_line < validation_line && validation_line < validated_line && validated_line < success_line )) || fail 'backend success precedes appliance validation/validated state'
rollback="${repo_root}/mjust/libexec/migration-import-common.sh"
rollback_runtime_line="$(grep -n 'if /usr/libexec/justvoxel/mjust/restore-runtime-validate' "${rollback}" | cut -d: -f1)"
rollback_validation_line="$(grep -n '&& JUSTVOXEL_RUNTIME_ALREADY_VALIDATED=1 JUSTVOXEL_MAINTENANCE_LOCK_HELD=1 /usr/libexec/justvoxel/mjust/validate-backend; then' "${rollback}" | cut -d: -f1)"
(( rollback_runtime_line < rollback_validation_line )) || fail 'rollback must verify runtime before full appliance validation'
grep -Fq 'if [[ ${JUSTVOXEL_RUNTIME_ALREADY_VALIDATED:-0} == 1 ]]; then' "${validate_backend}" || fail 'full validation lacks explicit runtime-only skip'
if grep -Fq 'storage_write_network_fstab' <<< "${transport_text}"; then fail 'temporary migration transport must not write fstab'; fi
grep -Fq 'JV_MIGRATION_SMB_CREDENTIALS="${JV_MIGRATION_TRANSPORT_ROOT}/smb.credentials"' <<< "${transport_text}" || fail 'temporary SMB credentials are not under /run transport state'
grep -Fq 'chmod 0600 "${JV_MIGRATION_SMB_CREDENTIALS}"' <<< "${transport_text}" || fail 'temporary SMB credentials are not mode 0600'
for token in GAME_MODE DIFFICULTY WHITELIST_ENABLED ENFORCE_WHITELIST; do
    grep -Fq "@@${token}@@" "${template}" || fail "Minecraft template missing migration-safe token ${token}"
done
grep -Fq 'GAME_MODE="${GAME_MODE:-survival}"' "${common}" || fail 'backward-compatible game-mode default missing'
grep -Fq 'WHITELIST_ENABLED="${WHITELIST_ENABLED:-yes}"' "${common}" || fail 'backward-compatible whitelist default missing'
grep -Fq 'JUSTVOXEL_REGENERATE_RCON' "${common}" || fail 'RCON regeneration support missing'

grep -Fq 'migration-api.sh' "${recovery}" || fail 'migration Recovery frontend does not use shared API helper'
grep -Fq 'jv_migration_recovery_plan' "${recovery}" || fail 'migration Recovery frontend does not plan through the Agent'
grep -Fq 'jv_migration_recovery_apply' "${recovery}" || fail 'migration Recovery frontend does not apply through the Agent'
grep -Fq 'rolled-back-fresh' "${recovery_backend}" || fail 'fresh unconfigured rollback recovery is not supported'
grep -Fq '/usr/libexec/justvoxel/mjust/restore-runtime-validate' "${recovery_backend}" || fail 'configured migration recovery must validate the restored Minecraft runtime'
grep -Fq 'jv_migration_runtime_paths' "${recovery_backend}" || fail 'fresh migration recovery does not prove generated runtime files are absent'
grep -Fq 'FINALIZE ROLLBACK' "${recovery_backend}" || fail 'migration recovery destructive confirmation missing'
grep -Fq 'jv_migration_clear_recovery' "${recovery_backend}" || fail 'migration recovery registry cleanup missing'

grep -Fq "warn '/dev/zram0 is not available" "${validate_backend}" || fail 'missing advisory zram-disabled validation path'
grep -Fq "warn 'zram0 is not active as swap" "${validate_backend}" || fail 'missing advisory zram-inactive validation path'
if grep -Fq "fail '/dev/zram0 is not available" "${validate_backend}"; then fail 'zram absence must not be a fatal appliance validation failure'; fi
if grep -Fq "fail 'zram0 is not active as swap" "${validate_backend}"; then fail 'zram inactivity must not be a fatal appliance validation failure'; fi

grep -Fq 'Migration' "${menu}" || fail 'Migration TUI missing'
if grep -Fq 'Import existing Minecraft server' "${menu}"; then fail 'fresh-appliance menu must not expose an import entry'; fi
grep -Fq "jui_choose 'Migration' 'Export server' 'Import server'" "${menu}" || fail 'Migration submenu import entry missing'
grep -Fq "'Import server') run_and_pause /usr/bin/mjust import" "${menu}" || fail 'Migration submenu import entry does not dispatch to mjust import'
grep -Fq 'Recover / finalize interrupted import' "${menu}" || fail 'migration recovery entry missing from TUI'
grep -Fq 'mjust export' "${menu}" || fail 'export command not discoverable in TUI'
grep -Fq 'mjust import' "${menu}" || fail 'import command not discoverable in TUI'
grep -Fq 'mjust migration-recover' "${menu}" || fail 'migration recovery command not discoverable in TUI'
grep -Fq 'export destination=""' "${justfile}" || fail 'mjust export recipe missing'
grep -Fq 'import source=""' "${justfile}" || fail 'mjust import recipe missing'
grep -Fq 'migration-recover transaction=""' "${justfile}" || fail 'mjust migration-recover recipe missing'

echo 'migration workflow invariant tests passed.'
