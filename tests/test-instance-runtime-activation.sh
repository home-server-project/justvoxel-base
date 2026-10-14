#!/usr/bin/bash
# Temporary fixtures only. No systemd, Podman, firewall, mounts, or worlds run.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "${test_root}"' EXIT
mkdir -p "${test_root}/bin" "${test_root}/etc/justvoxel/instances/jv-a1b2c3" "${test_root}/etc/containers/systemd" "${test_root}/disk" "${test_root}/var/lib/justvoxel/instances"
chmod 0700 "${test_root}/var/lib/justvoxel/instances" "${test_root}/etc/justvoxel/instances" "${test_root}/etc/justvoxel/instances/jv-a1b2c3"
export TEST_ROOT="${test_root}"
# Fake ownership only: preserve real mode checks so unsafe permissions fail on
# both root and unprivileged CI. All copied helpers use only temporary paths.
real_stat="$(command -v stat)"
cat > "${test_root}/bin/stat" <<STUB
#!/usr/bin/bash
if [[ \$2 == '%u:%g:%a' ]]; then
    mode="\$("${real_stat}" -c '%a' -- "\${!#}")" || exit 1
    printf '0:0:%s\n' "\${mode}"
elif [[ \$2 == '%u:%a' ]]; then
    mode="\$("${real_stat}" -c '%a' -- "\${!#}")" || exit 1
    printf '0:%s\n' "\${mode}"
elif [[ \$2 == '%u' ]]; then
    printf '0\n'
elif [[ \$2 == '%u:%g' ]]; then
    if [[ \${TEST_ROOT_OWNER:-no} == yes && \${!#} == */data && ! -e \${TEST_ROOT}/owner-completed ]]; then
        printf '0:0\n'
    else
        printf '1000:1000\n'
    fi
else
    exec "${real_stat}" "\$@"
fi
STUB
cat > "${test_root}/common.sh" <<'STUB'
require_root() { :; }
require_config() { exit 91; }
jv_exact_mount_identity() { printf '%s\n' test-uuid; }
server_version_available() { [[ $1 == 1.21.4 ]]; }
paper_version_has_stable_build() { [[ $1 == 1.21.4 ]]; }
resolve_latest_server_version() { printf '%s\n' 1.21.4; }
STUB
cat > "${test_root}/bin/mountpoint" <<'STUB'
#!/usr/bin/bash
[[ ${TEST_MOUNTED:-no} == yes && ${!#} == "${TEST_ROOT}/disk" ]]
STUB
cat > "${test_root}/bin/df" <<'STUB'
#!/usr/bin/bash
printf 'Filesystem 1024-blocks Used Available Capacity Mounted\nfixture 5000000 0 4000000 0%% fixture\n'
STUB
cat > "${test_root}/bin/chown" <<'STUB'
#!/usr/bin/bash
# Ownership checks are stubbed; no host account or labels change.
if [[ ${!#} == */data && ${TEST_ROOT_OWNER:-no} == yes ]]; then
    [[ ${TEST_FAIL_CHOWN:-no} != yes ]] || exit 1
    touch "${TEST_ROOT}/owner-completed"
fi
exit 0
STUB
cat > "${test_root}/bin/sync" <<'STUB'
#!/usr/bin/bash
exit 0
STUB
chmod 0755 "${test_root}/bin/"*
export PATH="${test_root}/bin:${PATH}"
sed -e "s|/etc/justvoxel/instances|${test_root}/etc/justvoxel/instances|g" \
    -e "s|/etc/containers/systemd|${test_root}/etc/containers/systemd|g" \
    -e "s|/var/lib/justvoxel/instances|${test_root}/var/lib/justvoxel/instances|g" \
    -e 's/\$EUID == 0 \&\& //' \
    "${repo_root}/mjust/libexec/validate-instance-runtime-commit" > "${test_root}/guard"
sed "s|source /usr/libexec/justvoxel/mjust/common.sh|source ${test_root}/common.sh|" \
    "${repo_root}/mjust/libexec/instance-runtime-prerequisites" > "${test_root}/storage"
printf '{"id":"jv-a1b2c3"}\n' > "${test_root}/var/lib/justvoxel/instances/jv-a1b2c3.json"
chmod 0600 "${test_root}/var/lib/justvoxel/instances/jv-a1b2c3.json"
dir="${test_root}/etc/justvoxel/instances/jv-a1b2c3"
env_file="${dir}/minecraft.env"
quadlet="${test_root}/etc/containers/systemd/minecraft-jv-a1b2c3.container"
printf 'RCON_PASSWORD=private-test-value\n' > "${env_file}"
printf '[Container]\nContainerName=minecraft-jv-a1b2c3\n' > "${quadlet}"
env_hash="$(sha256sum -- "${env_file}")"; env_hash=${env_hash%% *}
quadlet_hash="$(sha256sum -- "${quadlet}")"; quadlet_hash=${quadlet_hash%% *}
jq -n --arg env "${env_hash}" --arg quadlet "${quadlet_hash}" \
    '{schema_version:1,runtime:{id:"jv-a1b2c3"},environment_sha256:$env,quadlet_sha256:$quadlet}' > "${dir}/config.json"
chmod 0600 "${dir}/config.json" "${env_file}" "${quadlet}"
reject_guard() {
    if bash "${test_root}/guard" "$@" >"${test_root}/output" 2>&1; then
        echo 'FAIL: unsafe runtime commitment accepted' >&2; exit 1
    fi
    if rg -q private-test-value "${test_root}/output"; then
        echo 'FAIL: secret leaked from runtime guard' >&2; exit 1
    fi
}
bash "${test_root}/guard" jv-a1b2c3
reject_guard invalid
printf '{}' > "${dir}/pending.json"
reject_guard jv-a1b2c3
rm -- "${dir}/pending.json"
chmod 0644 "${env_file}"
reject_guard jv-a1b2c3
chmod 0600 "${env_file}"
mv -- "${env_file}" "${env_file}.original"
ln -s -- "${env_file}.original" "${env_file}"
reject_guard jv-a1b2c3
rm -- "${env_file}"
mv -- "${env_file}.original" "${env_file}"
printf 'uncommitted\n' >> "${quadlet}"
reject_guard jv-a1b2c3

data="${test_root}/disk/minecraft-instances/jv-a1b2c3/data"
check_storage() { bash "${test_root}/storage" "$1" jv-a1b2c3 "${test_root}/disk" "${data}" "$2" "$3" "$4" 1000 1000; }
reject_storage() {
    if check_storage "$@" >/dev/null 2>&1; then echo 'FAIL: unsafe storage accepted' >&2; exit 1; fi
}
check_storage preflight-new '' '' ''
[[ ! -e ${data} ]]
reject_storage validate '' '' ''
TEST_MOUNTED=no
export TEST_MOUNTED
reject_storage provision "${test_root}/disk" test-uuid ''
[[ ! -e ${data} ]]
TEST_MOUNTED=yes
reject_storage provision "${test_root}/disk" wrong-uuid ''
[[ ! -e ${data} ]]
check_storage provision "${test_root}/disk" test-uuid ''
check_storage validate "${test_root}/disk" test-uuid ''
printf 'existing world\n' > "${data}/world.dat"
reject_storage preflight-new '' '' ''
check_storage provision '' '' ''
[[ $(cat "${data}/world.dat") == 'existing world' ]]
mv -- "${data}" "${data}.original"
ln -s -- "${data}.original" "${data}"
reject_storage validate '' '' ''
[[ -f ${data}.original/world.dat ]]
bash "${test_root}/storage" version paper 1.21.4 default
if bash "${test_root}/storage" version vanilla 0.0.0 default >/dev/null 2>&1; then echo 'FAIL: unavailable version accepted' >&2; exit 1; fi
if bash "${test_root}/storage" version paper LATEST default >/dev/null 2>&1; then echo 'FAIL: unsupported latest channel accepted' >&2; exit 1; fi
# Exercise recovery of our own empty directory after a failed initial chown.
rm -- "${data}"
data="${test_root}/disk/minecraft-instances/jv-d4e5f6/data"
export TEST_ROOT_OWNER=yes TEST_FAIL_CHOWN=yes
if bash "${test_root}/storage" provision jv-d4e5f6 "${test_root}/disk" "${data}" '' '' '' 1000 1000 >/dev/null 2>&1; then
    echo 'FAIL: failed data ownership assignment was ignored' >&2; exit 1
fi
[[ -f ${data%/data}/.provision.json && -d ${data} ]]
TEST_FAIL_CHOWN=no
# Existing nonempty data must remain protected even with the marker.
printf 'existing data' > "${data}/world.dat"
if bash "${test_root}/storage" provision jv-d4e5f6 "${test_root}/disk" "${data}" '' '' '' 1000 1000 >/dev/null 2>&1; then
    echo 'FAIL: nonempty directory ownership was repaired' >&2; exit 1
fi
[[ $(cat "${data}/world.dat") == 'existing data' ]]
rm -- "${data}/world.dat"
bash "${test_root}/storage" provision jv-d4e5f6 "${test_root}/disk" "${data}" '' '' '' 1000 1000
[[ -f ${test_root}/owner-completed && ! -e ${data%/data}/.provision.json ]]
printf 'PASS: private instance commitment, interrupted activation, and storage safety\n'
