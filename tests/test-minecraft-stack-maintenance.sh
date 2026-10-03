#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
export TMPDIR="${fixture_dir}"
export FIXTURE="${fixture_dir}" DATA_PATH="${fixture_dir}/data" MINECRAFT_VERSION_MODE=recommended MINECRAFT_VERSION=26.2 MINECRAFT_IMAGE_TAG=stable BEDROCK_ENABLED=yes
export MINECRAFT_UID="$(id -u)" MINECRAFT_GID="$(id -g)"
mkdir -p "${DATA_PATH}/plugins" "${fixture_dir}/state"
printf old > "${DATA_PATH}/plugins/Geyser-Spigot.jar"
printf old > "${DATA_PATH}/plugins/floodgate-spigot.jar"
printf unrelated > "${DATA_PATH}/plugins/KeepMe.jar"
printf active > "${fixture_dir}/service"
printf geyser > "${fixture_dir}/geyser.jar"
printf floodgate > "${fixture_dir}/floodgate.jar"
metadata() {
    local project="$1" sha
    sha="$(sha256sum "${fixture_dir}/${project}.jar" | cut -d ' ' -f1)"
    jq -cn --arg project "${project}" --arg sha "${sha}" '{project:$project,version:"2.99.0",build:19,artifact:"fixture.jar",sha256:$sha}'
}
geyser="$(metadata geyser)"; floodgate="$(metadata floodgate)"
fingerprint="sha256:$(printf '%064d' 3)"
digest="sha256:$(printf '%064d' 1)"
jq -cn --arg fingerprint "${fingerprint}" --arg digest "${digest}" --argjson geyser "${geyser}" --argjson floodgate "${floodgate}" \
    '{stack_state:"updates_available",update_available:true,installed:"26.2",selected_candidate:"26.2",geyser_state:"updates_available",floodgate_state:"updates_available",plan_fingerprint:$fingerprint,managed_image_digest:$digest,managed_artifacts:{geyser:$geyser,floodgate:$floodgate}}' > "${fixture_dir}/plan.json"
cat > "${fixture_dir}/common.sh" <<'STUB'
JV_MAINTENANCE_LOCK="${FIXTURE}/lock"
JV_STATE_DIR="${FIXTURE}/state"
JV_PREVIOUS_IMAGE_STATE="${FIXTURE}/state/previous"
JV_PAPER_USER_AGENT=JustVoxel-test
require_root() { :; }
require_config() { :; }
minecraft_image_ref() { printf 'docker.io/itzg/minecraft-server:stable'; }
jv_remote_platform_digest() { printf 'sha256:%064d' 1; }
podman() {
    if [[ $* == *'.Digest'* ]]; then printf 'sha256:%064d' 1
    elif [[ $* == *'.Id'* || $* == *'.Image'* ]]; then printf image-id
    elif [[ $* == *'rcon-cli list'* ]]; then printf 'There are 0 of a max of 10 players online'
    else printf '%s\n' "$*" >> "${FIXTURE}/podman-actions"
    fi
}
systemctl() {
    if [[ $1 == is-active ]]; then [[ $(cat "${FIXTURE}/service") == active ]]; return; fi
    if [[ $1 == stop ]]; then printf stopped > "${FIXTURE}/service"; printf stop >> "${FIXTURE}/actions"; fi
    if [[ $1 == start ]]; then printf active > "${FIXTURE}/service"; printf start >> "${FIXTURE}/actions"; fi
}
write_main_config() { printf config >> "${FIXTURE}/config-actions"; printf '%s' "${MINECRAFT_VERSION}" > "${FIXTURE}/version"; }
render_runtime() { printf '%s' "${MINECRAFT_VERSION}" > "${FIXTURE}/version"; printf render >> "${FIXTURE}/actions"; }
confirm() { echo 'Unexpected interactive confirmation in reviewed flow' >&2; return 1; }
wait_for_rcon() { [[ $(cat "${FIXTURE}/service") == active ]]; }
restorecon() { [[ ${FAIL_ROLLBACK:-no} != yes || ! -e ${FIXTURE}/rollback-started ]]; }
mv() {
    if [[ $* == *'.justvoxel-rollback-'* ]]; then : > "${FIXTURE}/rollback-started"; fi
    if [[ ${FAIL_SECOND:-no} == yes && $* == *'.justvoxel-floodgate.'* ]]; then
        [[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == geyser ]] || return 2
        printf second-failed >> "${FIXTURE}/actions"
        return 1
    fi
    command mv "$@"
}
STUB
sed "s|/usr/libexec/justvoxel/mjust/managed-plugin-files|${repo_root}/mjust/libexec/managed-plugin-files|g" \
    "${repo_root}/mjust/libexec/managed-crossplay.sh" > "${fixture_dir}/managed.sh"
cat >> "${fixture_dir}/managed.sh" <<'STUB'
jv_crossplay_metadata() { jq -c ".managed_artifacts.$1" "${FIXTURE}/plan.json"; }
curl() {
    local output='' previous='' argument
    for argument in "$@"; do [[ ${previous} != -o ]] || output="${argument}"; previous="${argument}"; done
    local url="${*: -1}"
    printf download >> "${FIXTURE}/downloads"
    if [[ ${url} == */projects/geyser/* ]]; then cp "${FIXTURE}/geyser.jar" "${output}"
    else
        if [[ ${CORRUPT:-no} == yes ]]; then printf corrupt > "${output}"; else cp "${FIXTURE}/floodgate.jar" "${output}"; fi
    fi
}
STUB
cat > "${fixture_dir}/status" <<'STUB'
#!/usr/bin/bash
if [[ ${REFRESH_STATE:-} != '' && -e ${FIXTURE}/status-read ]]; then
    jq --arg state "${REFRESH_STATE}" '.stack_state=$state | .update_available=false' "${FIXTURE}/plan.json"
else
    : > "${FIXTURE}/status-read"
    cat "${FIXTURE}/plan.json"
fi
STUB
cat > "${fixture_dir}/backup" <<'STUB'
#!/usr/bin/bash
printf backup >> "${FIXTURE}/actions"
printf stopped > "${FIXTURE}/service"
STUB
cat > "${fixture_dir}/verify" <<'STUB'
#!/usr/bin/bash
[[ $1 == 26.2 && $(cat "${FIXTURE}/service") == active ]] || exit 1
printf verified >> "${FIXTURE}/actions"
[[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == geyser && $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == floodgate ]] || exit 1
[[ ${FAIL_VERIFY:-no} != yes ]]
STUB
chmod +x "${fixture_dir}/status" "${fixture_dir}/backup" "${fixture_dir}/verify"
sed -e "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    -e '\|^source /usr/libexec/justvoxel/mjust/update-policy.sh$|d' \
    -e "s|^source /usr/libexec/justvoxel/mjust/managed-crossplay.sh$|source ${fixture_dir}/managed.sh|" \
    -e "s|/usr/libexec/justvoxel/mjust/admin-version-status-json|${fixture_dir}/status|g" \
    -e "s|/usr/libexec/justvoxel/minecraft-backup|${fixture_dir}/backup|g" \
    -e "s|/usr/libexec/justvoxel/mjust/verify-minecraft-stack|${fixture_dir}/verify|g" \
    "${repo_root}/mjust/libexec/update-minecraft-backend" > "${fixture_dir}/backend"
before="$(sha256sum "${DATA_PATH}/plugins/"*.jar)"
if CORRUPT=yes bash "${fixture_dir}/backend" --reviewed-stack "${fingerprint}"; then echo 'Bad checksum applied' >&2; exit 1; fi
[[ $(sha256sum "${DATA_PATH}/plugins/"*.jar) == "${before}" ]]
[[ ! -f ${fixture_dir}/actions && $(cat "${fixture_dir}/service") == active ]]
bash "${fixture_dir}/backend" --reviewed-stack "${fingerprint}"
[[ $(cat "${fixture_dir}/version") == 26.2 ]]
[[ $(cat "${fixture_dir}/actions") == backuprenderstartverified ]]
[[ $(cat "${fixture_dir}/service") == active ]]
[[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == geyser ]]
[[ $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == floodgate ]]
[[ $(cat "${DATA_PATH}/plugins/KeepMe.jar") == unrelated ]]
[[ ! -f ${fixture_dir}/podman-actions ]]

# Every scenario gets the same original pair and preserved configuration/key data.
mkdir -p "${DATA_PATH}/plugins/Geyser-Spigot" "${DATA_PATH}/plugins/floodgate" "${DATA_PATH}/plugins/ViaVersion"
printf geyser-config > "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
printf floodgate-config > "${DATA_PATH}/plugins/floodgate/config.yml"
printf identity > "${DATA_PATH}/plugins/floodgate/key.pem"
printf via-config > "${DATA_PATH}/plugins/ViaVersion/config.yml"
preserved="$(sha256sum "${DATA_PATH}/plugins/KeepMe.jar" "${DATA_PATH}/plugins/Geyser-Spigot/config.yml" "${DATA_PATH}/plugins/floodgate/config.yml" "${DATA_PATH}/plugins/floodgate/key.pem" "${DATA_PATH}/plugins/ViaVersion/config.yml")"
reset_runtime() {
    printf old-geyser > "${DATA_PATH}/plugins/Geyser-Spigot.jar"
    printf old-floodgate > "${DATA_PATH}/plugins/floodgate-spigot.jar"
    printf '%s' "$1" > "${fixture_dir}/service"
    rm -f -- "${fixture_dir}/actions" "${fixture_dir}/podman-actions" "${fixture_dir}/config-actions" "${fixture_dir}/version" "${fixture_dir}/downloads" "${fixture_dir}/rollback-started" "${fixture_dir}/status-read"
}
assert_preserved() {
    [[ $(sha256sum "${DATA_PATH}/plugins/KeepMe.jar" "${DATA_PATH}/plugins/Geyser-Spigot/config.yml" "${DATA_PATH}/plugins/floodgate/config.yml" "${DATA_PATH}/plugins/floodgate/key.pem" "${DATA_PATH}/plugins/ViaVersion/config.yml") == "${preserved}" ]]
}
for initial in active stopped; do
    reset_runtime "${initial}"
    bash "${fixture_dir}/backend" --reviewed-stack "${fingerprint}"
    [[ $(cat "${fixture_dir}/service") == "${initial}" ]]
    [[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == geyser && $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == floodgate ]]
    [[ $(cat "${fixture_dir}/actions") == *startverified* ]]
    assert_preserved
    for failure in FAIL_SECOND FAIL_VERIFY; do
        for absent in neither geyser floodgate; do
            reset_runtime "${initial}"
            case "${absent}" in
                geyser) rm -- "${DATA_PATH}/plugins/Geyser-Spigot.jar" ;;
                floodgate) rm -- "${DATA_PATH}/plugins/floodgate-spigot.jar" ;;
            esac
            if env "${failure}=yes" bash "${fixture_dir}/backend" --reviewed-stack "${fingerprint}"; then
                echo "Failed maintenance accepted: ${failure}" >&2; exit 1
            fi
            expected_service="${initial}"
            if [[ ${initial} == active && ${absent} != neither ]]; then expected_service=stopped; fi
            [[ $(cat "${fixture_dir}/service") == "${expected_service}" ]]
            if [[ ${absent} == geyser ]]; then [[ ! -e ${DATA_PATH}/plugins/Geyser-Spigot.jar ]]
            else [[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == old-geyser ]]; fi
            if [[ ${absent} == floodgate ]]; then [[ ! -e ${DATA_PATH}/plugins/floodgate-spigot.jar ]]
            else [[ $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == old-floodgate ]]; fi
            for name in Geyser-Spigot.jar floodgate-spigot.jar; do
                if [[ -e ${DATA_PATH}/plugins/${name} ]]; then
                    [[ $(stat -c '%u:%g' "${DATA_PATH}/plugins/${name}") == "${MINECRAFT_UID}:${MINECRAFT_GID}" ]]
                fi
            done
            assert_preserved
        done
    done
done
# Backend independently refuses non-actionable plans, even with a matching review.
cp "${fixture_dir}/plan.json" "${fixture_dir}/actionable.json"
for state in up_to_date waiting_for_compatibility unavailable; do
    jq --arg state "${state}" '.stack_state=$state | .update_available=false' "${fixture_dir}/actionable.json" > "${fixture_dir}/plan.json"
    reset_runtime active
    before="$(sha256sum "${DATA_PATH}/plugins/"*.jar)"
    if bash "${fixture_dir}/backend" > "${fixture_dir}/output" 2>&1; then
        [[ ${state} != unavailable ]]
    else
        [[ ${state} == unavailable ]]
    fi
    [[ ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/podman-actions && ! -e ${fixture_dir}/config-actions && ! -e ${fixture_dir}/version && ! -e ${fixture_dir}/downloads ]]
    [[ $(sha256sum "${DATA_PATH}/plugins/"*.jar) == "${before}" && $(cat "${fixture_dir}/service") == active ]]
    if bash "${fixture_dir}/backend" --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
    grep -q 'review again' "${fixture_dir}/output"
    [[ ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/config-actions ]]
done
cp "${fixture_dir}/actionable.json" "${fixture_dir}/plan.json"
# A plan becoming non-actionable during preparation is rejected before shutdown.
for state in up_to_date waiting_for_compatibility unavailable; do
    reset_runtime active
    if REFRESH_STATE="${state}" bash "${fixture_dir}/backend" --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
    grep -q 'review again' "${fixture_dir}/output"
    [[ ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/config-actions && $(cat "${fixture_dir}/service") == active ]]
    [[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == old-geyser && $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == old-floodgate ]]
done
# A rollback error must be explicit and must never restart the failed pair.
reset_runtime active
if FAIL_VERIFY=yes FAIL_ROLLBACK=yes bash "${fixture_dir}/backend" --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
grep -q 'managed Geyser/Floodgate rollback failed' "${fixture_dir}/output"
grep -q 'operation requires attention' "${fixture_dir}/output"
[[ $(cat "${fixture_dir}/service") == stopped ]]
