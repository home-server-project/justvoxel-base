#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf -- "${tmp}"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

service_log="${tmp}/systemctl.log"
transaction_dir="${tmp}/.justvoxel-import-test"
mkdir -p "${transaction_dir}/staging-source"

set +e
(
    set -Eeuo pipefail

    # Model an SMB/NFS/archive read failure before activation. Minecraft was
    # running when Import started, but Import has not stopped it and live data
    # has not been modified.
    configured=yes
    live_modified=no
    validated=no
    rollback_attempted=no
    minecraft_was_active=yes
    minecraft_stopped_by_import=no
    transport_started=no
    transaction="${transaction_dir}"
    source_class=itzg-paper
    JV_MIGRATION_SOURCE='//nas/share/paper-data.tar.gz'
    JV_MIGRATION_API_MODE=0

    systemctl() {
        printf '%s\n' "$*" >> "${service_log}"
        return 0
    }
    jv_migration_transport_cleanup() {
        printf '%s\n' transport-cleanup >> "${service_log}"
        return 0
    }

    # shellcheck disable=SC1090
    source "${repo_root}/mjust/libexec/migration-import-common.sh"

    # Stand in for extraction/read failure while staging.
    exit 74
)
rc=$?
set -e

[[ ${rc} -eq 74 ]] || fail "pre-activation failure status changed: ${rc}"
[[ ! -e ${transaction_dir} ]] || fail 'failed pre-activation staging did not discard disposable transaction data'
[[ ! -e ${service_log} ]] || {
    cat "${service_log}" >&2
    fail 'pre-activation staging failure touched Minecraft or transport state unexpectedly'
}

echo 'migration pre-activation failure safety test passed.'
