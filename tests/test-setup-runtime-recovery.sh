#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf -- "${fixture}"' EXIT
helper="${repo_root}/mjust/libexec/admin-setup-runtime-transaction-json"
# Load only production functions; never call appliance services or host Podman.
source "${repo_root}/mjust/libexec/operation-trace.sh"
source <(sed -n '/^_a55_evidence_set() {/,/^action=/{ /^action=/d; p; }' "${helper}")
A53_TRANSACTION_ROOT="${fixture}/transactions"
op=12345678-1234-4123-8123-123456789abc
fp="sha256:$(printf 'a%.0s' {1..64})"
A53_TX_DIR="${A53_TRANSACTION_ROOT}/${op}"
A53_MANIFEST="${A53_TX_DIR}/manifest.json"
mkdir -p -m0700 "${A53_TX_DIR}"
chmod 0700 "${A53_TRANSACTION_ROOT}"
# Ownership is mocked so these source tests can run without root.
stat() { if [[ $1 == -c && $2 == %u ]]; then printf '0\n'; else command stat "$@"; fi; }
identity="$(jq -cn --arg op "${op}" --arg fp "${fp}" '{schema_version:"v1",operation_id:$op,plan_fingerprint:$fp}')"
request="$(jq -c '. + {server:{motd:"Minecraft",max_players:20,bedrock_enabled:false,timezone:"UTC"},minecraft:{server_type:"paper",java_memory:"2G",container_memory:"3G",java_port:25565,bedrock_port:19132,image_tag:"testing",version_policy:"recommended",version:"26.2",game_mode:"survival",minecraft_uid:1000,minecraft_gid:1000},storage:{type:"system",path:"/var/lib/justvoxel/minecraft"},backups:{type:"system",path:"/var/lib/justvoxel/backups",automatic:false,schedule:"daily",keep:7}}' <<< "${identity}")"
reset_manifest() {
    jq -cn --argjson identity "${identity}" --argjson request "${request}" '$identity + {storage_request:($identity + {storage:$request.storage,backups:$request.backups}),runtime_request:$request,directories_created:[]}' > "${A53_MANIFEST}"
    chmod 0600 "${A53_MANIFEST}"
}
reset_manifest
_a55_recover_request <<< "${identity}"
[[ $(jq -r '.minecraft.java_port' <<< "${A55_REQUEST}") == 25565 ]]
# Evidence cannot be replaced with live values or another operation's request.
for change in \
    '.runtime_request.operation_id = "00000000-0000-4000-8000-000000000000"' \
    '.runtime_request.plan_fingerprint = "sha256:bad"' \
    '.runtime_request.storage.path = "/var/lib/unrelated"' \
    '.runtime_request.storage.expected_uuid = "changed"' \
    '.storage_request.plan_fingerprint = "sha256:bad"' \
    '.runtime_request.minecraft.java_port = 0' \
    '.runtime_request.backups.mount_point = "/var/mnt/other"' \
    '.runtime_request.server.secret = "forbidden"' \
    'del(.runtime_request)' \
    'del(.storage_request)'; do
    reset_manifest
    jq "${change}" "${A53_MANIFEST}" > "${fixture}/changed"
    cat "${fixture}/changed" > "${A53_MANIFEST}"
    if _a55_recover_request <<< "${identity}"; then echo "Unsafe recovery accepted: ${change}" >&2; exit 1; fi
done
reset_manifest
chmod 0644 "${A53_MANIFEST}"
if _a55_recover_request <<< "${identity}"; then echo 'Public recovery evidence accepted' >&2; exit 1; fi
reset_manifest
mv "${A53_MANIFEST}" "${fixture}/outside"
ln -s "${fixture}/outside" "${A53_MANIFEST}"
if _a55_recover_request <<< "${identity}"; then echo 'Symlink evidence accepted' >&2; exit 1; fi
rm "${A53_MANIFEST}"
reset_manifest

# Durable transactions still dispatch to the original guarded rollback action.
(
    _a55_recover_request <<< "${identity}"
    [[ ${A55_LEGACY_RECOVERY} == false ]]
    _a55_rollback_action() { printf 'durable rollback\n' > "${fixture}/durable-dispatch"; }
    _a55_legacy_recover_action() { echo 'Durable transaction used legacy recovery' >&2; exit 99; }
    _a55_recover_action
    [[ -s ${fixture}/durable-dispatch ]]
)

# Legacy evidence matches commit 24b086b: neither request was persisted.
# Exercise the real compatibility
# verification and existing storage recovery without host service/container calls.
(
    source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-common.sh"
    source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-apply.sh"
    source "${repo_root}/mjust/libexec/admin-setup-storage-transaction-actions.sh"
    source "${repo_root}/mjust/libexec/minecraft-reset-common.sh"
    A53_TX_DIR="${A53_TRANSACTION_ROOT}/${op}"
    A53_MANIFEST="${A53_TX_DIR}/manifest.json"
    A55_CGROUP_ROOT="${fixture}/cgroups"
    A55_EVIDENCE='{}'
    A55_SETUP_MARKER="${fixture}/legacy-marker"
    A55_RUNTIME_FILES=()
    for name in justvoxel.conf minecraft.env minecraft-backup.env minecraft.container minecraft-backup.service minecraft-backup.timer; do
        A55_RUNTIME_FILES+=("${fixture}/${name}")
    done
    A53_DEFAULT_DATA_PATH="${fixture}/legacy-data"
    A53_DEFAULT_BACKUP_PATH="${fixture}/legacy-backups"
    mkdir -p "${A53_DEFAULT_DATA_PATH}" "${A53_DEFAULT_BACKUP_PATH}"
    printf 'preserved world\n' > "${A53_DEFAULT_DATA_PATH}/world.dat"
    printf 'preserved backup\n' > "${A53_DEFAULT_BACKUP_PATH}/backup.zip"
    JV_MAINTENANCE_LOCK="${fixture}/legacy-maintenance.lock"
    A53_FSTAB="${fixture}/legacy-fstab"
    A54_SMB_CREDENTIALS="${fixture}/legacy-credentials"
    fixture_active_state=inactive main_pid=0 control_pid=0 container_rc=1 nested=false mount_error=false storage_invalid=false
    failing_unit=minecraft.service
    selected_mounted=false selected_source=/dev/fixture selected_uuid=fixture-uuid
    mountpoint() { [[ ${selected_mounted} == true ]]; }
    jv_exact_mount_identity() {
        case "$1" in SOURCE) printf '%s\n' "${selected_source}" ;; UUID) printf '%s\n' "${selected_uuid}" ;; esac
    }
    umount() { [[ $* == "-- ${fixture}/legacy-mount" ]] || exit 99; selected_mounted=false; }
    systemctl() {
        [[ $* != 'daemon-reload' ]] || return 0 # Existing storage fstab rollback.
        [[ $1 == show ]] || { echo 'Unexpected runtime mutation' >&2; exit 99; }
        case "$2" in
            --property=ActiveState) if [[ ${*: -1} == "${failing_unit}" ]]; then printf '%s\n' "${fixture_active_state}"; else printf 'inactive\n'; fi ;;
            --property=MainPID) printf '%s\n' "${main_pid}" ;;
            --property=ControlPID) printf '%s\n' "${control_pid}" ;;
            --property=ControlGroup) printf '\n' ;;
            *) return 1 ;;
        esac
    }
    podman() { [[ $* == 'container exists minecraft' ]] || exit 99; return "${container_rc}"; }
    firewall-cmd() { echo 'Legacy recovery must not change unidentified firewall rules' >&2; exit 99; }
    jv_reset_remove_active_configuration() { exit 99; }
    jv_reset_remove_minecraft_container() { exit 99; }
    jv_reset_delete_internal_data() { exit 99; }
    validate_storage_path() { [[ ${storage_invalid} == false ]]; }
    storage_validate_mountpoint_path() { [[ ${1-} == "${fixture}/legacy-mount" ]]; }
    # Exercise the local legacy validator, including every critical mount scope.
    _a55_legacy_validate_mountpoint "${fixture}/legacy-mount"
    for critical_mount in / /boot /boot/efi /var /var/tmp /var/lib/containers /var/lib/containers/storage /etc /etc/justvoxel /sysroot /sysroot/var; do
        if _a55_legacy_validate_mountpoint "${critical_mount}"; then
            echo "Critical legacy mountpoint accepted: ${critical_mount}" >&2
            exit 1
        fi
    done
    findmnt() {
        [[ ${mount_error} == false ]] || return 2
        case "$*" in
            '-rn -o TARGET') [[ ${nested} == false ]] || printf '%s/nested\n' "${A53_DEFAULT_DATA_PATH}" ;;
            '-n -o TARGET,FSTYPE --target '*) printf '/ xfs\n' ;;
            '-n -o SOURCE,FSTYPE,TARGET --target '*) printf '/dev/fixture xfs /\n' ;;
            '-n -o FSTYPE --target '*) printf 'xfs\n' ;;
            *) return 1 ;;
        esac
        return 0
    }
    # Paths are in a temporary fixture; use the real canonical/inode/mount checks.
    jv_reset_data_scope() { printf 'internal\n'; }
    legacy_manifest() {
        printf '# original fstab\n' > "${A53_TX_DIR}/fstab.before"
        chmod 0600 "${A53_TX_DIR}/fstab.before"
        printf '# transaction fstab\n' > "${A53_FSTAB}"
        # Restore fixture ownership without requiring root; private evidence ownership is mocked above.
        jq -cn --argjson identity "${identity}" --arg data "${A53_DEFAULT_DATA_PATH}" --arg backups "${A53_DEFAULT_BACKUP_PATH}" \
          --arg mount "${fixture}/legacy-mount" --arg before "$(_a53_file_sha256 "${A53_TX_DIR}/fstab.before")" --arg after "$(_a53_file_sha256 "${A53_FSTAB}")" \
          --arg uid "$(command stat -c '%u' -- "${A53_FSTAB}")" --arg gid "$(command stat -c '%g' -- "${A53_FSTAB}")" '
          $identity + {phase:"storage_verified",
          runtime:{java_firewall_preexisting:false,bedrock_firewall_preexisting:true,minecraft_started:true,timer_activated:false,marker:true,empty_data_identity:null},
          mounts_by_transaction:[{mountpoint:$mount,source:"/dev/fixture",uuid:"fixture-uuid"}],directories_created:[$data,$backups],
          fstab_existed:true,fstab_changed:true,fstab_before_sha256:$before,fstab_after_sha256:$after,fstab_mode:"644",fstab_uid:$uid,fstab_gid:$gid,
          credentials_existed:false,credentials_changed:false,credentials_before_sha256:"absent",credentials_after_sha256:"",credentials_mode:"",credentials_uid:"",credentials_gid:"",
          rollback:{state:"not_started",result:""}}' > "${A53_MANIFEST}"
        chmod 0600 "${A53_MANIFEST}"
    }
    legacy_manifest
    jq -e '(has("runtime_request") | not) and (has("storage_request") | not) and .phase == "storage_verified" and .rollback.state == "not_started"' "${A53_MANIFEST}" >/dev/null
    selected_mounted=true
    _a55_recover_request <<< "${identity}"
    [[ ${A55_LEGACY_RECOVERY} == true && -z ${A55_REQUEST} ]]
    _a55_recover_action > "${fixture}/legacy-result"
    jq -e '.ok == true and .phase == "runtime_rolled_back" and .evidence.recovery_mode == "legacy_verified_clean"' "${fixture}/legacy-result" >/dev/null
    jq -e '.runtime_rollback_verified == true and .runtime_recovery.firewall == "preserved_unknown_ports" and (has("runtime_request") | not) and (has("storage_request") | not)' "${A53_MANIFEST}" >/dev/null
    # The original storage helper accepts the verified runtime handoff.
    a53_recover_action <<< "${identity}" > "${fixture}/legacy-storage-result"
    jq -e '.ok == true and .phase == "storage_rolled_back" and .rollback_result == "rolled_back"' "${fixture}/legacy-storage-result" >/dev/null
    jq -e '.rollback.state == "succeeded" and .phase == "storage_rolled_back"' "${A53_MANIFEST}" >/dev/null
    [[ ${selected_mounted} == false ]]
    cmp "${A53_TX_DIR}/fstab.before" "${A53_FSTAB}"
    [[ $(cat "${A53_DEFAULT_DATA_PATH}/world.dat") == 'preserved world' && $(cat "${A53_DEFAULT_BACKUP_PATH}/backup.zip") == 'preserved backup' ]]

    # Every managed file, including dangling links, blocks verification.
    for file in "${A55_RUNTIME_FILES[@]}" "${A55_SETUP_MARKER}"; do
        legacy_manifest
        ln -s "${fixture}/missing" "${file}"
        _a55_recover_request <<< "${identity}"
        _a55_recover_action > "${fixture}/legacy-result"
        jq -e '.ok == false and .phase == "runtime_rollback" and (.error | contains("runtime file remains"))' "${fixture}/legacy-result" >/dev/null
        jq -e '.runtime_rollback_verified != true' "${A53_MANIFEST}" >/dev/null
        rm "${file}"
    done
    for failing_unit in minecraft.service minecraft-backup.service minecraft-backup.timer; do
        for fixture_active_state in active activating deactivating unknown; do
            legacy_manifest
            _a55_recover_request <<< "${identity}"
            _a55_recover_action > "${fixture}/legacy-result"
            jq -e '.ok == false and .phase == "runtime_rollback"' "${fixture}/legacy-result" >/dev/null
        done
    done
    fixture_active_state=inactive
    for unsafe in main-pid control-pid cgroup container podman-error nested mount-error storage changed-source changed-uuid changed-inode maintenance; do
        legacy_manifest
        case "${unsafe}" in
            main-pid) main_pid=42 ;;
            control-pid) control_pid=42 ;;
            cgroup) mkdir -p "${A55_CGROUP_ROOT}/system.slice/minecraft.service"; printf 'populated 1\n' > "${A55_CGROUP_ROOT}/system.slice/minecraft.service/cgroup.events" ;;
            container) container_rc=0 ;;
            podman-error) container_rc=125 ;;
            nested) nested=true ;;
            mount-error) mount_error=true ;;
            storage) storage_invalid=true ;;
            changed-source|changed-uuid)
                jq --arg mount "${fixture}/legacy-mount" '.mounts_by_transaction = [{mountpoint:$mount,source:"/dev/fixture",uuid:"fixture-uuid"}]' "${A53_MANIFEST}" > "${fixture}/changed"
                cat "${fixture}/changed" > "${A53_MANIFEST}"
                selected_mounted=true
                if [[ ${unsafe} == changed-source ]]; then selected_source=/dev/other; else selected_uuid=other-uuid; fi
                ;;
            changed-inode)
                DATA_PATH="${A53_DEFAULT_DATA_PATH}" BACKUP_PATH="${A53_DEFAULT_BACKUP_PATH}" DATA_MOUNT_POINT='' DATA_EXPECTED_UUID='' DATA_EXPECTED_SOURCE=''
                recorded="$(_a55_data_identity)"
                jq --argjson recorded "${recorded}" '.runtime.empty_data_identity = $recorded' "${A53_MANIFEST}" > "${fixture}/changed"
                cat "${fixture}/changed" > "${A53_MANIFEST}"
                mv "${DATA_PATH}" "${DATA_PATH}-original"; mkdir "${DATA_PATH}"
                ;;
            maintenance) exec 9>&-; exec 8>"${JV_MAINTENANCE_LOCK}"; flock -n 8 ;;
        esac
        _a55_recover_request <<< "${identity}"
        _a55_recover_action > "${fixture}/legacy-result"
        jq -e '.ok == false and .phase == "runtime_rollback" and (.error | length > 0)' "${fixture}/legacy-result" >/dev/null
        jq -e '.runtime_rollback_verified != true and .rollback.state == "not_started"' "${A53_MANIFEST}" >/dev/null
        case "${unsafe}" in
            changed-inode) rmdir "${DATA_PATH}"; mv "${DATA_PATH}-original" "${DATA_PATH}" ;;
            maintenance) flock -u 8; exec 8>&- ;;
        esac
        main_pid=0 control_pid=0 container_rc=1 nested=false mount_error=false storage_invalid=false
        selected_source=/dev/fixture selected_uuid=fixture-uuid
        rm -rf "${A55_CGROUP_ROOT}"
        # The same preserved manifest is retryable after the condition clears.
        _a55_recover_request <<< "${identity}"
        _a55_recover_action > "${fixture}/legacy-result"
        jq -e '.ok == true and .phase == "runtime_rolled_back"' "${fixture}/legacy-result" >/dev/null
        selected_mounted=false
    done
    for change in '.operation_id = "wrong"' '.plan_fingerprint = "wrong"' '.schema_version = "wrong"' 'del(.runtime)' '.runtime.marker = "true"' '.runtime_request = null' \
        '.phase = "runtime_verified"' '.rollback.state = "succeeded"' '.rollback.result = "rolled_back"' '.runtime.marker = false' \
        '.rollback = null' 'del(.rollback)' 'del(.rollback.result)'; do
        legacy_manifest
        jq "${change}" "${A53_MANIFEST}" > "${fixture}/changed"
        cat "${fixture}/changed" > "${A53_MANIFEST}"
        if _a55_recover_request <<< "${identity}"; then echo "Invalid legacy evidence accepted: ${change}" >&2; exit 1; fi
    done
)
reset_manifest

# Drive the real apply function. Rendering is the first meaningful mutation;
# the full request and initial rollback identity must already be durable there.
_a55_load_values() { DATA_PATH="${fixture}/data"; }
write_main_config() { return 0; }
_a55_record_initial_state() { _a55_manifest_update '.runtime = {empty_data_identity:{path:"fixture",directory:"1:2",mount:"fixture"}}'; }
A55_SETUP_MARKER="${fixture}/marker"
A55_EVIDENCE='{}'
render_runtime_files() {
    jq -e --argjson request "${request}" '.runtime_request == $request and .runtime.empty_data_identity.directory == "1:2"' "${A53_MANIFEST}" >/dev/null
    [[ $(command stat -c '%a' "${A53_MANIFEST}") == 600 ]]
    touch "${fixture}/render-reached"
    return 1 # Simulate interruption at rendering; evidence must survive.
}
install() { command mkdir -p -- "${@: -1}"; }
_a55_recover_request <<< "${identity}"
if _a55_apply_action; then echo 'Interrupted rendering was accepted' >&2; exit 1; fi
[[ -f ${fixture}/render-reached ]]
_a55_recover_request <<< "${identity}"
jq -e '.runtime_request.minecraft.java_port == 25565 and .runtime.empty_data_identity != null' "${A53_MANIFEST}" >/dev/null

# Filesystem write failure must prevent rendering and marker creation.
rm -f "${A55_SETUP_MARKER}"
_a55_manifest_update() { return 1; }
render_runtime_files() { echo 'Rendered after evidence persistence failed' >&2; exit 99; }
if _a55_apply_action; then echo 'Evidence write failure accepted' >&2; exit 1; fi
[[ ! -e ${A55_SETUP_MARKER} ]]
echo 'Runtime recovery evidence checks passed.'
