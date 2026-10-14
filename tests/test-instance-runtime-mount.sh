#!/usr/bin/bash
# Temporary-directory tests only. No Podman/systemd or host mount operations.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "${test_root}"' EXIT
mkdir -p "${test_root}/bin" "${test_root}/disk/minecraft-instances/jv-a1b2c3/data"
cat > "${test_root}/common.sh" <<'STUB'
require_root() { :; }
jv_exact_mount_identity() {
    [[ $2 == "${TEST_MOUNT}" ]] || return 1
    case "$1" in UUID) printf '%s\n' "${TEST_UUID}" ;; SOURCE) printf '%s\n' '/dev/test-data' ;; *) return 1 ;; esac
}
# Any accidentally introduced global configuration read must fail the test.
require_config() { exit 90; }
STUB
cat > "${test_root}/bin/mountpoint" <<'STUB'
#!/usr/bin/bash
[[ ${TEST_MOUNTED} == yes && ${!#} == "${TEST_MOUNT}" ]]
STUB
chmod 0755 "${test_root}/bin/mountpoint"
# Substitute only the immutable source location in the temporary helper copy.
sed "s|source /usr/libexec/justvoxel/mjust/common.sh|source ${test_root}/common.sh|" \
    "${repo_root}/mjust/libexec/validate-instance-data-mount" > "${test_root}/helper"
export PATH="${test_root}/bin:${PATH}"
export TEST_MOUNT="${test_root}/disk" TEST_UUID=test-uuid TEST_MOUNTED=yes
data_path="${TEST_MOUNT}/minecraft-instances/jv-a1b2c3/data"
check() { bash "${test_root}/helper" jv-a1b2c3 "${TEST_MOUNT}" "${data_path}" "$@"; }
reject() {
    if check "$@" >/dev/null 2>&1; then
        echo 'FAIL: unsafe instance mount passed validation' >&2
        exit 1
    fi
}
check '' '' ''
check "${TEST_MOUNT}" test-uuid /dev/test-data
reject "${TEST_MOUNT}" wrong-uuid /dev/test-data
reject "${TEST_MOUNT}" test-uuid /dev/wrong-data
reject "${TEST_MOUNT}" '' ''
reject '' test-uuid ''
TEST_MOUNTED=no
reject "${TEST_MOUNT}" test-uuid /dev/test-data
TEST_MOUNTED=yes
if bash "${test_root}/helper" jv-d4e5f6 "${TEST_MOUNT}" "${data_path}" '' '' '' >/dev/null 2>&1; then
    echo 'FAIL: another instance data path passed validation' >&2; exit 1
fi
mv "${data_path}" "${data_path}-original"
ln -s "${data_path}-original" "${data_path}"
reject '' '' ''
rm "${data_path}"
reject '' '' ''
[[ -d ${data_path}-original && ! -e ${data_path} ]]
printf 'PASS: instance-aware mount validation\n'
