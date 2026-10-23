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
JV_SETUP_IN_PROGRESS="${FIXTURE}/setup-in-progress"
JV_STATE_DIR="${FIXTURE}/state"
JV_MINECRAFT_IMAGE_REPO=docker.io/itzg/minecraft-server
JV_PAPER_USER_AGENT=JustVoxel-test
require_root() { :; }
require_config() { :; }
install() {
    # Simulate only the privileged state-directory call within this fixture.
    # Keep real install behavior for managed plugin files and directories.
    if [[ $# == 7 && $1 == -d && $2 == -m0700 && $3 == -o && $4 == root && $5 == -g && $6 == root ]]; then
        [[ $7 == "${FIXTURE}/state" && ! -L $7 ]] || return 1
        command install -d -m0700 "$7" || return 1
        printf '%s\n' "$*" >> "${FIXTURE}/state-install"
    else
        command install "$@"
    fi
}
minecraft_image_ref() { printf 'docker.io/itzg/minecraft-server:%s' "${MINECRAFT_IMAGE_TAG}"; }
jv_remote_platform_digest() { printf 'sha256:%064d' 1; }
podman() {
    if [[ ${IMAGE_CHANGE:-no} == yes ]]; then
        case "$1 $2" in
            'pull docker.io/itzg/minecraft-server@sha256:'*) : > "${FIXTURE}/pulled"; printf '%s\n' "$*" >> "${FIXTURE}/podman-actions"; return ;;
            'image inspect')
                if [[ $3 == docker.io/itzg/minecraft-server@* && $3 != "${JV_INSTANCE_IMAGE_REF}" && ! -e ${FIXTURE}/pulled ]]; then return 1; fi
                if [[ $* == *'.Digest'* ]]; then
                    if [[ $3 == old-image || $3 == "${JV_INSTANCE_IMAGE_REF}" ]]; then printf 'sha256:%064d' 2
                    else printf 'sha256:%064d' "${PULLED_DIGEST_NUMBER:-1}"; fi
                elif [[ $3 == "${JV_INSTANCE_IMAGE_REF}" ]]; then printf old-image
                elif [[ $3 == docker.io/itzg/minecraft-server@* ]]; then printf new-image
                else printf old-image; fi
                return 0 ;;
            'inspect minecraft-jv-fixture1') cat "${FIXTURE}/running-image"; return ;;
            'image exists') [[ $3 == older-image ]]; return ;;
            'image rm') printf '%s' "$3" > "${FIXTURE}/image-removed"; return ;;
            'ps -a') [[ ${OLDER_IMAGE_USED:-no} != yes ]] || printf container-id; return 0 ;;
        esac
    fi
    if [[ $* == *'.Digest'* ]]; then printf 'sha256:%064d' 1
    elif [[ $* == *'.Id'* || $* == *'.Image'* ]]; then printf image-id
    elif [[ $* == *'rcon-cli list'* ]]; then printf '%s' "${PLAYERS:-There are 0 of a max of 10 players online}"
    else printf '%s\n' "$*" >> "${FIXTURE}/podman-actions"
    fi
}
systemctl() {
    if [[ $1 == is-active ]]; then [[ $(cat "${FIXTURE}/service") == active ]]; return; fi
    if [[ $1 == stop ]]; then printf stopped > "${FIXTURE}/service"; printf stop >> "${FIXTURE}/actions"; return 0; fi
    if [[ $1 == start ]]; then printf active > "${FIXTURE}/service"; printf start >> "${FIXTURE}/actions";
        if [[ ${IMAGE_CHANGE:-no} == yes ]]; then
            # Start the published digest, independently of its channel alias.
            podman image inspect "$(cat "${FIXTURE}/published-image-ref")" --format '{{.Id}}' > "${FIXTURE}/running-image" || return 1
        fi
        return 0
    fi
}
# Publication now renders atomically in the Management Agent. Model that fixed
# external operation; keep the backend's own publication handoff intact.
fixture_publish() {
    printf '%s' "${MINECRAFT_SERVER_TYPE-paper}" > "${FIXTURE}/software"
    printf '%s' "${MINECRAFT_IMAGE_TAG}" > "${FIXTURE}/image-tag"
    printf config >> "${FIXTURE}/config-actions"
    printf '%s' "${MINECRAFT_VERSION}" > "${FIXTURE}/version"
    render_runtime || return 1
    printf 'docker.io/itzg/minecraft-server@%s' "${JV_UPDATE_IMAGE_DIGEST}" > "${FIXTURE}/published-image-ref"
}
render_runtime() { printf '%s' "${MINECRAFT_SERVER_TYPE-paper}" > "${FIXTURE}/software"; printf '%s' "${MINECRAFT_VERSION}" > "${FIXTURE}/version"; printf render >> "${FIXTURE}/actions";
    if [[ ${FAIL_RENDER:-no} == yes ]]; then return 1; fi
    if [[ ${FAIL_RENDER_ONCE:-no} == yes && ! -e ${FIXTURE}/render-failed ]]; then : > "${FIXTURE}/render-failed"; return 1; fi
}
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
[[ $# -ge 2 && $1 == --instance && $2 == jv-fixture1 ]] || exit 1
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
[[ $# == 3 && $1 == --instance && $2 == jv-fixture1 ]] || exit 1
shift 2
[[ $1 == 26.2 && $(cat "${FIXTURE}/service") == active ]] || exit 1
if [[ ${IMAGE_CHANGE:-no} == yes && $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == old-geyser && $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == old-floodgate ]]; then
    # Verify the restored runtime separately from an injected new-runtime failure.
    [[ $(cat "${FIXTURE}/running-image") == old-image && $(cat "${FIXTURE}/software") == paper ]] || exit 1
    [[ ! -e ${FIXTURE}/image-tag || $(cat "${FIXTURE}/image-tag") == stable ]] || exit 1
    printf rollback-verified >> "${FIXTURE}/actions"
    exit 0
fi
printf verified >> "${FIXTURE}/actions"
[[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == geyser && $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == floodgate ]] || exit 1
[[ ${FAIL_VERIFY:-no} != yes ]]
STUB
chmod +x "${fixture_dir}/status" "${fixture_dir}/backup" "${fixture_dir}/verify"
cat > "${fixture_dir}/context.sh" <<'STUB'
jv_load_instance() {
    [[ $# == 1 && $1 == jv-fixture1 ]] || return 1
    readonly JV_INSTANCE_ID="$1"
    JV_INSTANCE_UNIT="minecraft-${JV_INSTANCE_ID}.service"
    JV_INSTANCE_CONTAINER="minecraft-${JV_INSTANCE_ID}"
    # Scenario inputs model the selected record; fill omitted runtime settings.
    MINECRAFT_SERVER_TYPE="${MINECRAFT_SERVER_TYPE-paper}"
    MINECRAFT_IMAGE_TAG="${MINECRAFT_IMAGE_TAG-stable}"
    MINECRAFT_VERSION_MODE="${MINECRAFT_VERSION_MODE-recommended}"
    MINECRAFT_VERSION="${MINECRAFT_VERSION-26.2}"
    BEDROCK_ENABLED="${BEDROCK_ENABLED-yes}"
    BEDROCK_MANAGED_PLUGINS="${BEDROCK_MANAGED_PLUGINS-yes}"
    DATA_PATH="${DATA_PATH-${FIXTURE}/data}"
    JV_INSTANCE_IMAGE_DIGEST="$(printf 'sha256:%064d' 1)"
    # IMAGE_CHANGE proposes digest 1 against the still-committed digest 2.
    # Target software/channel arguments must never change this loaded identity.
    [[ ${IMAGE_CHANGE:-no} != yes ]] || JV_INSTANCE_IMAGE_DIGEST="$(printf 'sha256:%064d' 2)"
    JV_INSTANCE_IMAGE_REF="docker.io/itzg/minecraft-server@${JV_INSTANCE_IMAGE_DIGEST}"
    if [[ ${IMAGE_CHANGE:-no} == yes ]]; then
        printf '%s' "${MINECRAFT_SERVER_TYPE}" > "${FIXTURE}/software"
        printf '%s' "${JV_INSTANCE_IMAGE_REF}" > "${FIXTURE}/published-image-ref"
    fi
}
STUB
cat > "${fixture_dir}/agent" <<'STUB'
#!/usr/bin/bash
set -euo pipefail
source "${FIXTURE}/common.sh"
[[ $2 == jv-fixture1 ]] || exit 1
case "$1" in
    instance-version-backup) [[ $# == 2 ]] || exit 1; exec "${FIXTURE}/backup" ;;
    instance-version-publish)
        [[ $# == 7 && $7 =~ ^sha256:[a-f0-9]{64}$ ]] || exit 1
        MINECRAFT_SERVER_TYPE=$3 MINECRAFT_IMAGE_TAG=$4 MINECRAFT_VERSION=$5 MINECRAFT_VERSION_MODE=$6
        JV_UPDATE_IMAGE_DIGEST=$7
        fixture_publish ;;
    *) exit 1 ;;
esac
STUB
chmod +x "${fixture_dir}/agent"
sed -n '/^validate_server_type() {/,/^render_runtime_files() {/p' "${repo_root}/mjust/libexec/common.sh" | sed '$d' >> "${fixture_dir}/common.sh"
sed -e "s|^source /usr/libexec/justvoxel/mjust/common.sh$|source ${fixture_dir}/common.sh|" \
    -e '\|^source /usr/libexec/justvoxel/mjust/update-policy.sh$|d' \
    -e "s|^source /usr/libexec/justvoxel/mjust/instance-context.sh$|source ${fixture_dir}/context.sh|" \
    -e "s|/usr/libexec/justvoxel/management-agent|${fixture_dir}/agent|g" \
    -e "s|^source /usr/libexec/justvoxel/mjust/managed-crossplay.sh$|source ${fixture_dir}/managed.sh|" \
    -e "s|/usr/libexec/justvoxel/mjust/admin-version-status-json|${fixture_dir}/status|g" \
    -e "s|/usr/libexec/justvoxel/minecraft-backup|${fixture_dir}/backup|g" \
    -e "s|/usr/libexec/justvoxel/mjust/verify-minecraft-stack|${fixture_dir}/verify|g" \
    "${repo_root}/mjust/libexec/update-minecraft-backend" > "${fixture_dir}/backend"
# Setup exclusion applies before any instance load, plan, backup or publication.
touch "${fixture_dir}/setup-in-progress"
if bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}"; then
    echo 'Maintenance bypassed setup exclusion' >&2; exit 1
fi
[[ ! -e ${fixture_dir}/status-read && ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/config-actions ]]
rm "${fixture_dir}/setup-in-progress"
before="$(sha256sum "${DATA_PATH}/plugins/"*.jar)"
if CORRUPT=yes bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}"; then echo 'Bad checksum applied' >&2; exit 1; fi
[[ $(sha256sum "${DATA_PATH}/plugins/"*.jar) == "${before}" ]]
[[ ! -f ${fixture_dir}/actions && $(cat "${fixture_dir}/service") == active ]]
bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}"
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
    rm -f -- "${fixture_dir}/actions" "${fixture_dir}/podman-actions" "${fixture_dir}/config-actions" "${fixture_dir}/version" "${fixture_dir}/downloads" "${fixture_dir}/rollback-started" "${fixture_dir}/status-read" "${fixture_dir}/image-tag" "${fixture_dir}/pulled" "${fixture_dir}/render-failed"
    printf old-image > "${fixture_dir}/running-image"
    rm -f -- "${fixture_dir}/state-install" "${fixture_dir}/image-removed"
}
assert_preserved() {
    [[ $(sha256sum "${DATA_PATH}/plugins/KeepMe.jar" "${DATA_PATH}/plugins/Geyser-Spigot/config.yml" "${DATA_PATH}/plugins/floodgate/config.yml" "${DATA_PATH}/plugins/floodgate/key.pem" "${DATA_PATH}/plugins/ViaVersion/config.yml") == "${preserved}" ]]
}
for initial in active stopped; do
    reset_runtime "${initial}"
    bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}"
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
            if env "${failure}=yes" bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}"; then
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
    if bash "${fixture_dir}/backend" --instance jv-fixture1 > "${fixture_dir}/output" 2>&1; then
        [[ ${state} != unavailable ]]
    else
        [[ ${state} == unavailable ]]
    fi
    [[ ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/podman-actions && ! -e ${fixture_dir}/config-actions && ! -e ${fixture_dir}/version && ! -e ${fixture_dir}/downloads ]]
    [[ $(sha256sum "${DATA_PATH}/plugins/"*.jar) == "${before}" && $(cat "${fixture_dir}/service") == active ]]
    if bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
    grep -q 'review again' "${fixture_dir}/output"
    [[ ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/config-actions ]]
done
cp "${fixture_dir}/actionable.json" "${fixture_dir}/plan.json"
# A plan becoming non-actionable during preparation is rejected before shutdown.
for state in up_to_date waiting_for_compatibility unavailable; do
    reset_runtime active
    if REFRESH_STATE="${state}" bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
    grep -q 'review again' "${fixture_dir}/output"
    [[ ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/config-actions && $(cat "${fixture_dir}/service") == active ]]
    [[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == old-geyser && $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == old-floodgate ]]
done
# A rollback error must be explicit and must never restart the failed pair.
reset_runtime active
if FAIL_VERIFY=yes FAIL_ROLLBACK=yes bash "${fixture_dir}/backend" --instance jv-fixture1 --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
grep -q 'managed Geyser/Floodgate rollback failed' "${fixture_dir}/output"
grep -q 'operation requires attention' "${fixture_dir}/output"
[[ $(cat "${fixture_dir}/service") == stopped ]]

# Server software switches reuse this same transaction and preserve plugin data.
for direction in 'paper purpur' 'purpur paper'; do
    read -r old target <<< "${direction}"
    reset_runtime active
    MINECRAFT_SERVER_TYPE="${old}" bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type "${target}" --reviewed-stack "${fingerprint}"
    [[ $(cat "${fixture_dir}/software") == "${target}" && $(cat "${fixture_dir}/service") == active ]]
    [[ $(cat "${fixture_dir}/actions") == backuprenderstartverified ]]
    assert_preserved
    reset_runtime active
    if MINECRAFT_SERVER_TYPE="${old}" FAIL_VERIFY=yes bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type "${target}" --reviewed-stack "${fingerprint}"; then
        echo 'Failed software switch accepted' >&2; exit 1
    fi
    [[ $(cat "${fixture_dir}/software") == "${old}" ]]
    assert_preserved
done
for direction in 'paper vanilla' 'purpur vanilla' 'vanilla paper' 'vanilla purpur'; do
    read -r old target <<< "${direction}"
    reset_runtime active
    if MINECRAFT_SERVER_TYPE="${old}" bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type "${target}" --reviewed-stack "${fingerprint}"; then exit 1; fi
    [[ ! -e ${fixture_dir}/actions && ! -e ${fixture_dir}/config-actions ]]
done

# Combined software/channel updates share the cold backup, verification and rollback.
for initial in active stopped; do
    reset_runtime "${initial}"
    MINECRAFT_SERVER_TYPE=paper bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type purpur --image-tag latest --reviewed-stack "${fingerprint}"
    [[ $(cat "${fixture_dir}/software") == purpur && $(cat "${fixture_dir}/image-tag") == latest ]]
    [[ $(cat "${fixture_dir}/service") == "${initial}" ]]
    [[ $(cat "${fixture_dir}/actions") == *backuprenderstartverified* ]]
    assert_preserved
    reset_runtime "${initial}"
    if MINECRAFT_SERVER_TYPE=paper FAIL_VERIFY=yes bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type purpur --image-tag latest --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
    [[ $(cat "${fixture_dir}/software") == paper && $(cat "${fixture_dir}/image-tag") == stable ]]
    assert_preserved
    reset_runtime "${initial}"
    if MINECRAFT_SERVER_TYPE=paper FAIL_VERIFY=yes FAIL_ROLLBACK=yes bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type purpur --image-tag latest --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
    grep -q 'operation requires attention' "${fixture_dir}/output"
    ! grep -q 'maintenance completed' "${fixture_dir}/output"
done
# Verified identical-image channel changes never back up, stop, start, or pull.
jq '.runtime_update_required=false | .geyser_state="up_to_date" | .floodgate_state="up_to_date"' "${fixture_dir}/actionable.json" > "${fixture_dir}/plan.json"
for initial in active stopped; do
    reset_runtime "${initial}"
    # The current managed pair is already verified in this scenario.
    cp "${fixture_dir}/geyser.jar" "${DATA_PATH}/plugins/Geyser-Spigot.jar"
    cp "${fixture_dir}/floodgate.jar" "${DATA_PATH}/plugins/floodgate-spigot.jar"
    bash "${fixture_dir}/backend" --instance jv-fixture1 --image-tag latest --reviewed-stack "${fingerprint}"
    [[ $(cat "${fixture_dir}/image-tag") == latest && $(cat "${fixture_dir}/service") == "${initial}" ]]
    [[ $(cat "${fixture_dir}/actions") != *backup* && $(cat "${fixture_dir}/actions") != *start* && $(cat "${fixture_dir}/actions") != *stop* ]]
    ! grep -q '^pull ' "${fixture_dir}/podman-actions"
done
# Definition-write failures restore the old channel without a restart; an
# unrenderable restored definition is explicitly reported as incomplete recovery.
for failure in FAIL_RENDER_ONCE FAIL_RENDER; do
    for initial in active stopped; do
        reset_runtime "${initial}"
        cp "${fixture_dir}/geyser.jar" "${DATA_PATH}/plugins/Geyser-Spigot.jar"
        cp "${fixture_dir}/floodgate.jar" "${DATA_PATH}/plugins/floodgate-spigot.jar"
        if env "${failure}=yes" bash "${fixture_dir}/backend" --instance jv-fixture1 --image-tag latest --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
        [[ $(cat "${fixture_dir}/image-tag") == stable && $(cat "${fixture_dir}/software") == paper ]]
        [[ $(cat "${fixture_dir}/service") == "${initial}" ]]
        [[ $(cat "${fixture_dir}/actions") != *backup* && $(cat "${fixture_dir}/actions") != *stop* && $(cat "${fixture_dir}/actions") != *start* ]]
        ! grep -q 'Container channel saved' "${fixture_dir}/output"
        if [[ ${failure} == FAIL_RENDER ]]; then grep -q 'recovery is incomplete' "${fixture_dir}/output"
        else ! grep -q 'recovery is incomplete' "${fixture_dir}/output"; fi
    done
done
# Invalid selections and a held maintenance lock cannot mutate configuration.
reset_runtime active
if bash "${fixture_dir}/backend" --instance jv-fixture1 --image-tag '../bad' --reviewed-stack "${fingerprint}"; then exit 1; fi
[[ ! -e ${fixture_dir}/config-actions ]]
(
    exec 8>"${fixture_dir}/lock"
    flock -n 8
    if bash "${fixture_dir}/backend" --instance jv-fixture1 --image-tag latest --reviewed-stack "${fingerprint}"; then exit 1; fi
    [[ ! -e ${fixture_dir}/config-actions && ! -e ${fixture_dir}/actions ]]
)

# Per-instance rollback records preserve shared images, including stopped siblings.
sed -n "/^candidate_previous_id=''/,/^rm -rf --/p" "${repo_root}/mjust/libexec/update-minecraft-backend" | sed '$d' > "${fixture_dir}/image-policy"
for older in older-image previous-image new-image; do
    for used in no yes; do
        (
            JV_INSTANCE_ID=jv-fixture1
            JV_STATE_DIR="${fixture_dir}/state"
            JV_PREVIOUS_IMAGE_STATE="${JV_STATE_DIR}/minecraft-jv-fixture1-previous-image-id"
            old_running_id=previous-image old_local_id=previous-image new_local_id=new-image
            printf '%s\n' "${older}" > "${JV_PREVIOUS_IMAGE_STATE}"
            rm -f -- "${fixture_dir}/image-removed"
            install() { mkdir -p "${JV_STATE_DIR}"; }
            podman() {
                case "$1 $2" in
                    'ps -a') [[ ${used} != yes ]] || printf container-id ;;
                    'image exists') [[ $3 == "${older}" ]] ;;
                    'image rm') printf '%s' "$3" > "${fixture_dir}/image-removed" ;;
                    *) echo 'Unexpected image cleanup command' >&2; return 1 ;;
                esac
            }
            source "${fixture_dir}/image-policy"
            [[ $(cat "${JV_PREVIOUS_IMAGE_STATE}") == previous-image ]]
            [[ ! -e ${fixture_dir}/image-removed ]]
        )
    done
done
# Same-image operations leave the existing rollback generation in place.
(
    JV_INSTANCE_ID=jv-fixture1
    JV_STATE_DIR="${fixture_dir}/state" JV_PREVIOUS_IMAGE_STATE="${fixture_dir}/state/minecraft-jv-fixture1-previous-image-id"
    old_running_id=new-image old_local_id=new-image new_local_id=new-image
    printf rollback-image > "${JV_PREVIOUS_IMAGE_STATE}"
    podman() { echo 'Same-image operation attempted image deletion' >&2; return 1; }
    source "${fixture_dir}/image-policy"
    [[ $(cat "${JV_PREVIOUS_IMAGE_STATE}") == rollback-image ]]
)

# Existing minecraft-backup alone applies administrator retention. Exercise its
# production retention block against fixture archives and matching metadata.
sed -n '/^mapfile -t minecraft_archives/,/^if (( minecraft_restart_required ))/p' "${repo_root}/runtime/minecraft-backup" | sed '$d' > "${fixture_dir}/retention-policy"
for keep in 1 2 4; do
    (
        minecraft_instance_backup_path="${fixture_dir}/archives-${keep}"
        mkdir -p "${minecraft_instance_backup_path}"
        BACKUP_KEEP="${keep}"
        for index in 1 2 3; do
            archive="${minecraft_instance_backup_path}/minecraft-2026-10-0${index}-120000.tar.gz"
            printf fixture > "${archive}"; printf metadata > "${archive}.meta.json"
            touch -d "2026-10-0${index} 12:00:00 UTC" "${archive}"
        done
        printf unrelated > "${minecraft_instance_backup_path}/keep.txt"
        source "${fixture_dir}/retention-policy"
        [[ ${BACKUP_KEEP} == "${keep}" ]]
        for index in 1 2 3; do
            archive="${minecraft_instance_backup_path}/minecraft-2026-10-0${index}-120000.tar.gz"
            if (( index > 3 - keep )); then [[ -e ${archive} && -e ${archive}.meta.json ]]
            else [[ ! -e ${archive} && ! -e ${archive}.meta.json ]]; fi
        done
        [[ $(cat "${minecraft_instance_backup_path}/keep.txt") == unrelated ]]
    )
done
# The transaction delegates retention; it adds no archive deletion or overrides.
! grep -Eq 'BACKUP_KEEP=|minecraft_archives|Removing expired backup|\.tar\.gz' "${repo_root}/mjust/libexec/update-minecraft-backend"
grep -Fq '/usr/libexec/justvoxel/management-agent instance-version-backup "${JV_INSTANCE_ID}"' "${repo_root}/mjust/libexec/update-minecraft-backend"

# Pulling a different proposed image must verify the remote platform digest before
# committing either selection, and retain the previous runtime identity.
cp "${fixture_dir}/actionable.json" "${fixture_dir}/plan.json"
for initial in active stopped; do
    for used in no yes; do
        reset_runtime "${initial}"
        printf older-image > "${fixture_dir}/state/minecraft-jv-fixture1-previous-image-id"
        if ! IMAGE_CHANGE=yes OLDER_IMAGE_USED="${used}" bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type purpur --image-tag latest --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then
            cat "${fixture_dir}/output" >&2
            echo 'FAIL: selected-instance image-change maintenance failed' >&2
            exit 1
        fi
        [[ $(cat "${fixture_dir}/software") == purpur && $(cat "${fixture_dir}/image-tag") == latest ]]
        [[ $(cat "${fixture_dir}/state/minecraft-jv-fixture1-previous-image-id") == old-image && $(cat "${fixture_dir}/service") == "${initial}" ]]
        [[ $(cat "${fixture_dir}/state-install") == "-d -m0700 -o root -g root ${fixture_dir}/state" ]]
        [[ $(cat "${fixture_dir}/actions") == *backuprenderstartverified* ]]
        grep -q 'maintenance completed and runtime verified' "${fixture_dir}/output"
        grep -Fxq "pull docker.io/itzg/minecraft-server@${digest}" "${fixture_dir}/podman-actions"
        [[ ! -e ${fixture_dir}/image-removed ]]
        assert_preserved
    done
    for failure in 'PULLED_DIGEST_NUMBER=3' 'FAIL_SECOND=yes' 'FAIL_VERIFY=yes' 'FAIL_RENDER_ONCE=yes'; do
        reset_runtime "${initial}"
        printf rollback-image > "${fixture_dir}/state/minecraft-jv-fixture1-previous-image-id"
        if env IMAGE_CHANGE=yes "${failure}" bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type purpur --image-tag latest --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
        [[ $(cat "${fixture_dir}/software") == paper ]]
        [[ $(cat "${fixture_dir}/service") == "${initial}" ]]
        [[ $(cat "${fixture_dir}/state/minecraft-jv-fixture1-previous-image-id") == rollback-image && ! -e ${fixture_dir}/image-removed && ! -e ${fixture_dir}/state-install ]]
        grep -Fxq "pull docker.io/itzg/minecraft-server@${digest}" "${fixture_dir}/podman-actions"
        # Recovery restores the committed digest; shared image tags are untouched.
        ! grep -q '^tag ' "${fixture_dir}/podman-actions"
        if [[ ${failure} == PULLED_DIGEST_NUMBER=3 ]]; then
            # This must fail on digest verification, before config mutation.
            grep -q 'pulled Minecraft image does not match' "${fixture_dir}/output"
            [[ ! -e ${fixture_dir}/config-actions && ! -e ${fixture_dir}/image-tag ]]
        else
            [[ $(cat "${fixture_dir}/image-tag") == stable && $(cat "${fixture_dir}/config-actions") == configconfig ]]
            if [[ ${failure} == FAIL_SECOND=yes ]]; then [[ $(cat "${fixture_dir}/actions") == *second-failed* ]]; fi
            [[ $(cat "${DATA_PATH}/plugins/Geyser-Spigot.jar") == old-geyser && $(cat "${DATA_PATH}/plugins/floodgate-spigot.jar") == old-floodgate ]]
        fi
        if [[ ${initial} == active ]]; then
            if [[ $(cat "${fixture_dir}/running-image") != old-image ]]; then
                echo 'FAIL: active runtime recovery did not restore old-image' >&2
                printf 'Actual running-image: %s\n' "$(cat "${fixture_dir}/running-image")" >&2
                if [[ -e ${fixture_dir}/output ]]; then cat "${fixture_dir}/output" >&2; fi
                exit 1
            fi
        fi
        if grep -q 'operation requires attention' "${fixture_dir}/output"; then
            echo 'FAIL: image-change recovery reported operation requires attention' >&2
            cat "${fixture_dir}/output" >&2
            exit 1
        fi
        ! grep -q 'maintenance completed' "${fixture_dir}/output"
        assert_preserved
    done
    reset_runtime "${initial}"
    printf rollback-image > "${fixture_dir}/state/minecraft-jv-fixture1-previous-image-id"
    if IMAGE_CHANGE=yes FAIL_VERIFY=yes FAIL_ROLLBACK=yes bash "${fixture_dir}/backend" --instance jv-fixture1 --server-type purpur --image-tag latest --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
    grep -q 'managed Geyser/Floodgate rollback failed' "${fixture_dir}/output"
    grep -q 'operation requires attention' "${fixture_dir}/output"
    [[ $(cat "${fixture_dir}/software") == paper && $(cat "${fixture_dir}/image-tag") == stable && $(cat "${fixture_dir}/service") == stopped ]]
    [[ $(cat "${fixture_dir}/actions") == *startverifiedstop* && $(cat "${fixture_dir}/actions") != *rollback-verified* ]]
    [[ $(cat "${fixture_dir}/state/minecraft-jv-fixture1-previous-image-id") == rollback-image && ! -e ${fixture_dir}/image-removed && ! -e ${fixture_dir}/state-install ]]
    ! grep -q 'maintenance completed' "${fixture_dir}/output"
    assert_preserved
done
# Player consent is checked again before disruption; no implicit retry or stop.
reset_runtime active
if PLAYERS='There are 2 of a max of 10 players online' bash "${fixture_dir}/backend" --instance jv-fixture1 --image-tag latest --reviewed-stack "${fingerprint}" > "${fixture_dir}/output" 2>&1; then exit 1; fi
[[ ! -e ${fixture_dir}/config-actions && ! -e ${fixture_dir}/actions ]]
bash "${fixture_dir}/backend" --instance jv-fixture1 --image-tag latest --reviewed-stack "${fingerprint}" --confirm-players
[[ $(cat "${fixture_dir}/image-tag") == latest ]]
