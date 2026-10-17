#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
configure="${repo_root}/mjust/libexec/configure"
maximum="${repo_root}/mjust/libexec/configure-max-players"
helper="${repo_root}/mjust/libexec/configuration-api.sh"
selection="${repo_root}/mjust/libexec/instance-selection.sh"
discovery="${repo_root}/mjust/libexec/admin-discovery-json"
agent="${repo_root}/management/cmd/justvoxel-management-agent"
selected="${repo_root}/webui/internal/server/selected_server.go"
for frontend in "${configure}" "${maximum}"; do
    grep -Fq 'instance_id="$(jv_select_registered_instance "${instance_id}")" || exit 1' "${frontend}"
    grep -Fq 'readonly instance_id' "${frontend}"
    grep -Fq 'jv_config_get "${instance_id}"' "${frontend}"
    grep -Fq 'jv_config_apply_payload "${instance_id}" "${payload}"' "${frontend}"
    if grep -Eq 'require_config|minecraft.env|minecraft.container|\.instances\[0\]|Original' "${frontend}"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
done
grep -Fq 'mjust/configure-max-players --instance "${instance_id}"' "${configure}"
grep -Fq 'mjust/update-minecraft --instance "${instance_id}"' "${configure}"
grep -Fq 'mjust/data-migration || true' "${configure}"
grep -Fq 'jv_config_backup_get "${instance_id}"' "${configure}"
grep -Fq 'jv_config_backup_put "${instance_id}" "${payload}"' "${configure}"
grep -Fq 'GET "/v1/minecraft/instances/${id}/configuration"' "${helper}"
grep -Fq 'PUT "/v1/minecraft/instances/${id}/configuration"' "${helper}"
grep -Fq 'GET "/v1/minecraft/instances/${id}/backups/settings"' "${helper}"
grep -Fq 'PUT "/v1/minecraft/instances/${id}/backups/settings"' "${helper}"
grep -Fq '.id == $id and (.runtime | type == "object")' "${helper}"
grep -Fq '.ok == true and .id == $id' "${helper}"
grep -Fq 'jv_config_validate_id "${id}" || return 1' "${helper}"
grep -Fq "jq -c '{runtime:.runtime}'" "${helper}"
if grep -RE '/v1/admin/configuration/(plan|apply)' "${repo_root}/mjust"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
if grep -R -E --include='*.go' 'POST /v1/admin/configuration/(plan|apply)|registerAdminConfigurationRoutes|runAdminConfigurationHelper' "${agent}"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
if grep -Eq 'configuration_change_json|plan\)|apply\)|podman|minecraft.service' "${discovery}"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
grep -Fq 'GET /v1/admin/configuration' "${agent}/admin_discovery.go"
grep -Fq 'GET /v1/admin/storage' "${agent}/admin_discovery.go"
grep -Fq 'GET /v1/admin/setup-defaults' "${agent}/admin_discovery.go"
grep -Fq 'APPLIANCE_CONFIGURED:-no' "${discovery}"
grep -Fq 'configured:true,minecraft:{},backup:' "${discovery}"
grep -Fq 'MinecraftInstanceConfiguration' "${selected}"
grep -Fq 'SetMinecraftInstanceConfiguration' "${selected}"
if grep -Eq 'AdminConfigurationPlan|AdminConfigurationApply' "${selected}"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
# Exercise the shared selector with inventory fixtures, never host API calls.
selector="$(sed 's|/usr/libexec/justvoxel/mjust/api-client GET /v1/minecraft/instances/runtime|fixture_inventory|' "${selection}")"
eval "${selector}"
fixture_inventory() { printf '%s' "${fixture_inventory_json}"; }
fixture_inventory_json='{"instances":[]}'
if jv_select_registered_instance >/dev/null 2>&1; then exit 1; fi
fixture_inventory_json='{"instances":[{"id":"jv-family1"}]}'
[[ $(jv_select_registered_instance) == jv-family1 ]]
fixture_inventory_json='{"instances":[{"id":"jv-family1"},{"id":"jv-sibling1"}]}'
if jv_select_registered_instance >/dev/null 2>&1; then exit 1; fi
[[ $(jv_select_registered_instance jv-sibling1) == jv-sibling1 ]]
if jv_select_registered_instance family >/dev/null 2>&1; then exit 1; fi
if jv_select_registered_instance jv-absent1 >/dev/null 2>&1; then exit 1; fi
# Drive runtime/backup helpers with API fixtures and inspect every addressed ID.
source "${helper}"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
jv_config_request() {
    printf '%s %s\n' "$1" "$2" >> "${fixture_dir}/calls"
    case "$1 $2" in
        'GET /v1/minecraft/instances/jv-sibling1/configuration') printf '%s' "${configuration_response}" ;;
        'GET /v1/minecraft/instances/jv-sibling1/status') printf '%s' "${status_response}" ;;
        'GET /v1/minecraft/instances/jv-sibling1/backups/settings') echo '{"enabled":true,"time":"04:30","retention":7}' ;;
        'PUT /v1/minecraft/instances/jv-sibling1/configuration')
            jq -e '.runtime.max_players == 12 and .runtime.whitelist == true' <<<"$3" >/dev/null || return 1
            echo '{"ok":true,"id":"jv-sibling1"}' ;;
        'PUT /v1/minecraft/instances/jv-sibling1/backups/settings')
            jq -e '. == {enabled:false,time:"05:00",retention:9}' <<<"$3" >/dev/null || return 1
            echo '{"ok":true,"id":"jv-sibling1"}' ;;
        *) return 1 ;;
    esac
}
configuration_response='{"id":"jv-sibling1","runtime":{"max_players":10,"whitelist":true}}'
configuration="$(jv_config_get jv-sibling1)"
payload="$(jv_config_payload <<<"${configuration}" | jq -c '.runtime.max_players=12')"
status_response='{"id":"jv-sibling1","state":"running"}'
if jv_config_apply_payload jv-sibling1 "${payload}"; then exit 1; fi
if grep -q '^PUT ' "${fixture_dir}/calls"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
status_response='{"id":"jv-family1","state":"stopped"}'
if jv_config_apply_payload jv-sibling1 "${payload}"; then exit 1; fi
if grep -q '^PUT ' "${fixture_dir}/calls"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
status_response='{"id":"jv-sibling1","state":"stopped"}'
jv_config_apply_payload jv-sibling1 "${payload}"
jv_config_backup_get jv-sibling1 >/dev/null
jv_config_backup_put jv-sibling1 '{"enabled":false,"time":"05:00","retention":9}'
for configuration_response in 'bad JSON' '{"id":"jv-family1","runtime":{}}' '{"id":"jv-sibling1","runtime":[]}'; do
    if jv_config_get jv-sibling1; then exit 1; fi
done
if grep -q '/jv-family1/' "${fixture_dir}/calls"; then echo "ERROR: forbidden configuration path found." >&2; exit 1; fi
echo 'Selected-instance configuration regression checks passed.'
