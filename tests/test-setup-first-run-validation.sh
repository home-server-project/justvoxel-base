#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Source the actual classifier, never the privileged validator entrypoint.
source <(sed -n '/^validate_failed_units() {/,/^}/p' "${repo_root}/mjust/libexec/validate-backend")
pass() { :; }
fail() { failures=$((failures + 1)); diagnostics+="FAIL: $*"$'\n'; }
warn() { warnings=$((warnings + 1)); diagnostics+="WARN: $*"$'\n'; }
systemctl() {
    if [[ $1 == --failed ]]; then
        [[ ${enumeration_fails} == no ]] || return 1
        [[ -z ${failed_unit} ]] || printf '%s loaded failed failed fixture\n' "${failed_unit}"
    elif [[ $* == *--property=LoadState* ]]; then
        printf 'loaded\n'
    elif [[ $* == *--property=Where* ]]; then
        printf '%s\n' "${mount_where}"
    else
        [[ ${dependencies_fail} == no ]] || return 1
        # Exercise an indirect critical dependency as well as direct Minecraft.
        if [[ ${critical} == yes && $2 == minecraft.service ]]; then printf 'fixture-required.service\n'; fi
        if [[ ${critical} == yes && $2 == fixture-required.service ]]; then printf '%s\n' "${failed_unit}"; fi
    fi
    return 0
}
systemd-escape() {
    case "$3" in
        /var/mnt/justvoxel-backup) printf 'var-mnt-justvoxel-backup.mount\n' ;;
        /var/mnt/justvoxel-data) printf 'var-mnt-justvoxel-data.mount\n' ;;
        *) return 1 ;;
    esac
}
reset_case() {
    failures=0 warnings=0 diagnostics=''
    DATA_PATH=/var/lib/justvoxel/minecraft BACKUP_PATH=/var/lib/justvoxel/backups
    DATA_MOUNT_POINT='' BACKUP_MOUNT_POINT=''
    failed_unit=var-mnt-justvoxel-backup.mount mount_where=/var/mnt/justvoxel-backup
    critical=no dependencies_fail=no enumeration_fails=no
}
reset_case
validate_failed_units first-run
[[ $failures == 0 && $warnings == 1 && $diagnostics == *'unrelated failed appliance mount'* ]]
reset_case
BACKUP_MOUNT_POINT=/var/mnt/justvoxel-backup BACKUP_PATH=/var/mnt/justvoxel-backup/backups
validate_failed_units first-run
[[ $failures == 1 && $diagnostics == *'selected setup storage mount'* ]]
reset_case
failed_unit=var-mnt-justvoxel-data.mount mount_where=/var/mnt/justvoxel-data
DATA_MOUNT_POINT=/var/mnt/justvoxel-data DATA_PATH=/var/mnt/justvoxel-data/minecraft
validate_failed_units first-run
[[ $failures == 1 ]]
reset_case
failed_unit=minecraft.service
validate_failed_units first-run
[[ $failures == 1 && $diagnostics == *'required setup unit'* ]]
reset_case
critical=yes
validate_failed_units first-run
[[ $failures == 1 && $diagnostics == *'required setup unit'* ]]
reset_case
validate_failed_units global
[[ $failures == 1 && $warnings == 0 ]]
reset_case
mount_where=/unexpected
validate_failed_units first-run
[[ $failures == 1 && $diagnostics == *'identity could not be verified'* ]]
reset_case
failed_unit=unknown.service
validate_failed_units first-run
[[ $failures == 1 && $diagnostics == *'cannot be classified'* ]]
reset_case
dependencies_fail=yes
validate_failed_units first-run
[[ $failures == 1 && $diagnostics == *'dependencies'* ]]
reset_case
enumeration_fails=yes
validate_failed_units first-run
[[ $failures == 1 && $diagnostics == *'could not enumerate'* ]]

# Exercise real first-run verification with healthy startup and classifier-backed
# validation. No system services or disk operations run: every command is mocked.
source <(sed -n '/^_a55_verify_action() {/,/^}/p' "${repo_root}/mjust/libexec/admin-setup-runtime-transaction-json" |
    sed -e 's|/usr/libexec/justvoxel/mjust/validate-backend|mock_validate_backend|g' \
        -e 's|/usr/libexec/justvoxel/mjust/verify-minecraft-stack|mock_verify_stack|g')
_a55_load_values() { BEDROCK_ENABLED=no; MINECRAFT_VERSION=fixture; }
minecraft_image_ref() { printf 'fixture:stable\n'; }
podman() { return 0; }
mock_verify_stack() { return 0; }
_a55_wait_for_rcon() { return 0; }
_a55_manifest_update() { return 0; }
_a55_evidence_set() { if [[ $1 == final_validation ]]; then final_validation=$2; fi; }
_a55_evidence_set_bounded() { if [[ $1 == final_validation_output ]]; then final_output=$2; fi; }
_a55_json() { verified_phase=$3; }
# Add only the start/restart counter cases to the existing classifier mock.
eval "$(declare -f systemctl | sed 's/systemctl/classifier_systemctl/')"
systemctl() {
    case "$1" in
        show) if [[ $* == *NRestarts* ]]; then printf '0\n'; else classifier_systemctl "$@"; fi ;;
        start) return 0 ;;
        *) classifier_systemctl "$@" ;;
    esac
}
mock_validate_backend() {
    [[ $1 == --first-run ]] || return 1
    validate_failed_units first-run
    printf '%s' "${diagnostics}"
    (( failures == 0 ))
}
A55_EVIDENCE='{}'
reset_case
_a55_verify_action
[[ $final_validation == passed && $verified_phase == runtime_verified && $final_output == *'unrelated failed appliance mount'* ]]
reset_case
BACKUP_PATH=/var/mnt/justvoxel-backup/backups BACKUP_MOUNT_POINT=/var/mnt/justvoxel-backup
if _a55_verify_action; then echo 'Selected failed mount accepted' >&2; exit 1; fi
[[ $final_validation == failed && $final_output == *'selected setup storage mount'* ]]
# Existing test-setup-runtime-repair.sh exercises actual verify failure followed
# by guarded runtime rollback; setup_runtime_transaction_test.go checks the
# runtime-then-storage rollback orchestration and failures needing attention.
echo 'First-run failed-unit policy regression checks passed.'
