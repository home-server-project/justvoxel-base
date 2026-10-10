#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf -- "${fixture}"' EXIT
helper="${repo_root}/mjust/libexec/admin-setup-runtime-transaction-json"
# Load only production functions; never call appliance services or host Podman.
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
