#!/usr/bin/bash
# Fixture-only coverage of the real guard. No live system services or containers.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "${test_root}"' EXIT
export TEST_ROOT="${test_root}" TEST_AGENT_ALIVE=yes TEST_AGENT_EXE=/usr/libexec/justvoxel/management-agent
id=jv-a1b2c3
op=12345678-1234-4123-8123-123456789abc
fp=sha256:$(printf '%064d' 0)
mkdir -p "${test_root}/bin" "${test_root}/etc/justvoxel/instances/${id}" "${test_root}/etc/containers/systemd" \
    "${test_root}/var/lib/justvoxel/instances" "${test_root}/var/lib/justvoxel-instance-runtime" \
    "${test_root}/run/justvoxel/setup-instance-start" "${test_root}/proc/1234"
chmod 0700 "${test_root}/etc/justvoxel/instances" "${test_root}/etc/justvoxel/instances/${id}" \
    "${test_root}/var/lib/justvoxel/instances" "${test_root}/var/lib/justvoxel-instance-runtime" "${test_root}/run/justvoxel/setup-instance-start"
real_stat=$(command -v stat)
cat > "${test_root}/bin/stat" <<STUB
#!/usr/bin/bash
if [[ \$2 == '%u:%g:%a' ]]; then
    printf '0:0:%s\n' "\$("${real_stat}" -c '%a' -- "\${!#}")"
else
    exec "${real_stat}" "\$@"
fi
STUB
real_readlink=$(command -v readlink)
cat > "${test_root}/bin/readlink" <<STUB
#!/usr/bin/bash
if [[ \${!#} == "\${TEST_ROOT}/proc/1234/exe" ]]; then
    printf '%s\n' "\${TEST_AGENT_EXE}"
else
    exec "${real_readlink}" "\$@"
fi
STUB
chmod 0755 "${test_root}/bin/"*
export PATH="${test_root}/bin:${PATH}"
# The fake proc tree and kill function model a live Agent without creating one.
{
    printf 'kill() { [[ ${TEST_AGENT_ALIVE} == yes && $* == "-0 1234" ]]; }\n'
    sed -e "s|/etc/justvoxel/instances|${test_root}/etc/justvoxel/instances|g" \
        -e "s|/etc/containers/systemd|${test_root}/etc/containers/systemd|g" \
        -e "s|/var/lib/justvoxel/instances|${test_root}/var/lib/justvoxel/instances|g" \
        -e "s|/var/lib/justvoxel-instance-runtime|${test_root}/var/lib/justvoxel-instance-runtime|g" \
        -e "s|/run/justvoxel|${test_root}/run/justvoxel|g" \
        -e "s|/proc/|${test_root}/proc/|g" -e 's/\$EUID == 0 \&\& //' \
        "${repo_root}/mjust/libexec/validate-instance-runtime-commit"
} > "${test_root}/guard"
dir="${test_root}/etc/justvoxel/instances/${id}"
printf 'private environment\n' > "${dir}/minecraft.env"
printf '[Container]\n' > "${test_root}/etc/containers/systemd/minecraft-${id}.container"
env_hash=$(sha256sum "${dir}/minecraft.env"); env_hash=${env_hash%% *}
quadlet_hash=$(sha256sum "${test_root}/etc/containers/systemd/minecraft-${id}.container"); quadlet_hash=${quadlet_hash%% *}
jq -n --arg id "${id}" --arg env "${env_hash}" --arg quadlet "${quadlet_hash}" \
    '{schema_version:1,runtime:{id:$id},environment_sha256:$env,quadlet_sha256:$quadlet}' > "${dir}/config.json"
evidence="${test_root}/var/lib/justvoxel-instance-runtime/setup-${op}.json"
jq -n --arg id "${id}" --arg op "${op}" --arg fp "${fp}" --slurpfile config "${dir}/config.json" \
    '{schema:2,request:{instance_only:true,operation_id:$op,plan_fingerprint:$fp},record:$config[0],rolled_back:false,publication:{stage:"runtime_prepared"}}' > "${evidence}"
permit="${test_root}/run/justvoxel/setup-instance-start/setup-instance-start-${id}.json"
# starttime is field 22, or field 20 after the closing command parenthesis.
printf '1234 (management agent) S' > "${test_root}/proc/1234/stat"
for ((i=0;i<18;i++)); do printf ' 0' >> "${test_root}/proc/1234/stat"; done
printf ' 99\n' >> "${test_root}/proc/1234/stat"
chmod 0600 "${dir}/"* "${test_root}/etc/containers/systemd/"* "${evidence}"
reject() { if bash "${test_root}/guard" "${id}" >/dev/null 2>&1; then echo "FAIL: $*" >&2; exit 1; fi; }
permit_write() {
    local hash
    hash=$(sha256sum "${evidence}"); hash=${hash%% *}
    jq -n --arg id "${id}" --arg op "${op}" --arg fp "${fp}" --arg hash "${hash}" \
        '{schema:1,id:$id,operation_id:$op,plan_fingerprint:$fp,evidence_sha256:$hash,agent_pid:1234,agent_start:"99"}' > "${permit}"
    chmod 0600 "${permit}"
}
reject 'persistent evidence alone admitted provisional startup'
permit_write
bash "${test_root}/guard" "${id}"
TEST_AGENT_ALIVE=no reject 'dead Agent admitted startup'
TEST_AGENT_EXE=/usr/bin/other reject 'wrong process admitted startup'
for change in '.id="jv-wrong1"' '.operation_id="00000000-0000-4000-8000-000000000000"' '.agent_start="100"' '.plan_fingerprint="bad"'; do
    permit_write
    jq "${change}" "${permit}" > "${permit}.tmp"; cat "${permit}.tmp" > "${permit}"
    reject "mismatching permit: ${change}"
done
permit_write
chmod 0644 "${permit}"; reject 'nonprivate permit'; chmod 0600 "${permit}"
mv "${permit}" "${permit}.saved"; ln -s "${permit}.saved" "${permit}"; reject 'symlink permit'; rm "${permit}"
for change in '.schema=1' '.rolled_back=true' '.publication.stage="verified"' '.record.runtime.id="jv-wrong1"'; do
    cp "${evidence}" "${evidence}.saved"
    jq "${change}" "${evidence}.saved" > "${evidence}"
    permit_write
    reject "mismatching evidence: ${change}"
    cat "${evidence}.saved" > "${evidence}"
done
permit_write
for gate in pending factory-reset restore-journal data-migration; do
    printf '{}\n' > "${dir}/${gate}.json"; chmod 0600 "${dir}/${gate}.json"
    reject "conflicting ${gate} evidence"; rm "${dir}/${gate}.json"
done
printf '{}\n' > "${test_root}/var/lib/justvoxel-instance-runtime/delete-${id}.json"
reject 'Delete conflict'; rm "${test_root}/var/lib/justvoxel-instance-runtime/delete-${id}.json"
printf 'changed\n' >> "${dir}/minecraft.env"; reject 'wrong committed hash'
printf 'private environment\n' > "${dir}/minecraft.env"
# Ordinary committed instances still require exact private identity and hashes,
# and never need a Setup permit or persistent Setup evidence.
rm "${permit}" "${evidence}"
printf '{"id":"%s"}\n' "${id}" > "${test_root}/var/lib/justvoxel/instances/${id}.json"
chmod 0600 "${test_root}/var/lib/justvoxel/instances/${id}.json"
bash "${test_root}/guard" "${id}"
printf '{"id":"jv-wrong1"}\n' > "${test_root}/var/lib/justvoxel/instances/${id}.json"
reject 'wrong normal identity'
printf 'PASS: exact live Setup permit and committed runtime guard\n'
