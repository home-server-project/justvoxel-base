#!/usr/bin/bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf -- "${scratch}"' EXIT
mkdir -p "${scratch}/bin"

cat >"${scratch}/common.sh" <<'EOF'
JV_CONFIG_DIR="${JV_TEST_ROOT}/config"
JV_CONFIG="${JV_CONFIG_DIR}/minecraft.conf"
JV_QUADLET="${JV_TEST_ROOT}/minecraft.container"
JV_BACKUP_SERVICE="${JV_TEST_ROOT}/minecraft-backup.service"
JV_BACKUP_TIMER="${JV_TEST_ROOT}/minecraft-backup.timer"
JV_PREVIOUS_IMAGE_STATE="${JV_TEST_ROOT}/previous-image"
require_root() { :; }
validate_server_type() { case "$1" in paper|purpur|vanilla) return 0 ;; *) return 1 ;; esac; }
source "${JV_TEST_REPO}/mjust/libexec/minecraft-reset-common.sh"
jv_reset_path_scope() { printf 'unknown\n'; }
jv_reset_data_scope() { jv_reset_path_scope "$1"; }
# Factory reset also targets a fixed appliance backup path; leave the host untouched.
jv_reset_delete_tree_same_filesystem() { :; }
EOF

cat >"${scratch}/bin/podman" <<'EOF'
#!/usr/bin/bash
if [[ $1 == rm && $2 == --force && $3 == --ignore && $4 == minecraft ]]; then
    exit 1
fi
if [[ $1 == container && $2 == exists && $3 == minecraft ]]; then
    exit "${JV_TEST_EXISTS_RC}"
fi
exit 125
EOF
cat >"${scratch}/bin/systemctl" <<'EOF'
#!/usr/bin/bash
[[ $1 == is-active ]] && exit 1
exit 0
EOF
cat >"${scratch}/bin/firewall-cmd" <<'EOF'
#!/usr/bin/bash
exit 0
EOF
chmod +x "${scratch}/bin/"*

for mode in minecraft factory; do
    sed "s@source /usr/libexec/justvoxel/mjust/common.sh@source ${scratch}/common.sh@; /source \/usr\/libexec\/justvoxel\/mjust\/minecraft-reset-common.sh/d" \
        "${repo}/mjust/libexec/admin-${mode}-reset-json" >"${scratch}/${mode}-reset"

    for exists_rc in 1 0 125; do
        root="${scratch}/${mode}-${exists_rc}"
        mkdir -p "${root}/config"
        cat >"${root}/config/minecraft.conf" <<EOF
DATA_PATH="${root}/data"
BACKUP_PATH="${root}/backups"
JAVA_PORT=25565
BEDROCK_ENABLED=no
BEDROCK_PORT=19132
EOF
        export JV_TEST_REPO="${repo}" JV_TEST_ROOT="${root}" JV_TEST_EXISTS_RC="${exists_rc}"
        if output="$(PATH="${scratch}/bin:${PATH}" bash "${scratch}/${mode}-reset" apply --confirm-players)"; then
            status=0
        else
            status=$?
        fi

        if [[ ${exists_rc} == 1 ]]; then
            [[ ${status} == 0 ]] || { echo "ERROR: ${mode} reset stopped after confirmed container absence: ${output}" >&2; exit 1; }
            jq -e '.ok == true' <<<"${output}" >/dev/null
            [[ ! -e ${root}/config/minecraft.conf ]] || { echo "ERROR: ${mode} reset did not remove active configuration." >&2; exit 1; }
        else
            [[ ${status} != 0 ]] || { echo "ERROR: ${mode} reset accepted remaining or unverified container." >&2; exit 1; }
            jq -e '.code == "container_cleanup_failed"' <<<"${output}" >/dev/null
            [[ -e ${root}/config/minecraft.conf ]] || { echo "ERROR: ${mode} reset cleared configuration after container cleanup failure." >&2; exit 1; }
        fi
    done
done

echo 'Reset container cleanup race regression OK'
