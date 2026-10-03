#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT
export DATA_PATH="${fixture_dir}/data" MINECRAFT_UID="$(id -u)" MINECRAFT_GID="$(id -g)"
JV_PAPER_USER_AGENT=JustVoxel-test
# Resolve only the fixed cleanup helper to the source checkout.
sed "s|/usr/libexec/justvoxel/mjust/managed-plugin-files|${repo_root}/mjust/libexec/managed-plugin-files|g" \
    "${repo_root}/mjust/libexec/managed-crossplay.sh" > "${fixture_dir}/managed.sh"
source "${fixture_dir}/managed.sh"
mkdir -p "${DATA_PATH}/plugins" "${fixture_dir}/prepared"
printf 'new-geyser' > "${fixture_dir}/geyser.jar"
printf 'new-floodgate' > "${fixture_dir}/floodgate.jar"
geyser_sha="$(sha256sum "${fixture_dir}/geyser.jar" | cut -d ' ' -f1)"
floodgate_sha="$(sha256sum "${fixture_dir}/floodgate.jar" | cut -d ' ' -f1)"
metadata() {
    local project="$1" sha="$2"
    jq -cn --arg project "${project}" --arg sha "${sha}" \
        '{project_id:$project,version:"2.99.0-SNAPSHOT",build:19,channel:"default",downloads:{spigot:{name:"plugin.jar",sha256:$sha}}}'
}
for project in geyser floodgate; do
    sha="${geyser_sha}"
    [[ ${project} == geyser ]] || sha="${floodgate_sha}"
    raw="$(metadata "${project}" "${sha}")"
    parsed="$(jv_crossplay_parse_metadata "${project}" "${raw}")"
    jq -e --arg project "${project}" '.project == $project and .build == 19 and .artifact == "plugin.jar" and .version == "2.99.0-SNAPSHOT"' <<< "${parsed}" >/dev/null
    for filter in 'del(.version)' '.build=0' '.build=1.5' '.channel="experimental"' '.version="feature/new-java"' 'del(.downloads.spigot)' '.downloads.spigot.sha256="bad"' '.downloads.spigot.name="../plugin.jar"' '.project_id="other"'; do
        if jv_crossplay_parse_metadata "${project}" "$(jq -c "${filter}" <<< "${raw}")"; then
            echo "Invalid ${project} metadata was accepted: ${filter}" >&2; exit 1
        fi
    done
    [[ $(jv_crossplay_artifact_state "${project}" '{}') == unavailable ]]
    [[ $(jv_crossplay_artifact_state "${project}" "${parsed}") == updates_available ]]
    cp "${fixture_dir}/${project}.jar" "${DATA_PATH}/plugins/$(jv_crossplay_jar_name "${project}")"
    [[ $(jv_crossplay_artifact_state "${project}" "${parsed}") == up_to_date ]]
    printf old > "${DATA_PATH}/plugins/$(jv_crossplay_jar_name "${project}")"
    [[ $(jv_crossplay_artifact_state "${project}" "${parsed}") == updates_available ]]
    printf '%s\n' "${parsed}" > "${fixture_dir}/prepared/${project}.json"
    cp "${fixture_dir}/${project}.jar" "${fixture_dir}/prepared/${project}.jar"
done
printf unrelated > "${DATA_PATH}/plugins/KeepMe.jar"
mkdir -p "${DATA_PATH}/plugins/Geyser-Spigot" "${DATA_PATH}/plugins/floodgate" "${DATA_PATH}/plugins/ViaVersion"
printf 'auth-type: floodgate\n' > "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
printf 'identity-key' > "${DATA_PATH}/plugins/floodgate/key.pem"
printf 'floodgate-config' > "${DATA_PATH}/plugins/floodgate/config.yml"
printf 'via-config' > "${DATA_PATH}/plugins/ViaVersion/config.yml"
# A bad SECOND download must not replace the good installed FIRST component.
printf corrupt > "${fixture_dir}/prepared/floodgate.jar"
before="$(sha256sum "${DATA_PATH}/plugins/"*.jar)"
if jv_crossplay_install "${fixture_dir}/prepared"; then echo 'Bad checksum installed' >&2; exit 1; fi
[[ $(sha256sum "${DATA_PATH}/plugins/"*.jar) == "${before}" ]]
cp "${fixture_dir}/floodgate.jar" "${fixture_dir}/prepared/floodgate.jar"
restorecon() { :; }
jv_crossplay_install "${fixture_dir}/prepared"
jv_crossplay_verify "${fixture_dir}/prepared"
[[ $(cat "${DATA_PATH}/plugins/KeepMe.jar") == unrelated ]]
[[ $(cat "${DATA_PATH}/plugins/floodgate/key.pem") == identity-key ]]
[[ $(cat "${DATA_PATH}/plugins/floodgate/config.yml") == floodgate-config ]]
[[ $(cat "${DATA_PATH}/plugins/ViaVersion/config.yml") == via-config ]]
grep -q 'auth-type: floodgate' "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
# Refreshing a stale Geyser must not rewrite an already current Floodgate.
floodgate_inode="$(stat -c '%i' "${DATA_PATH}/plugins/floodgate-spigot.jar")"
printf stale > "${DATA_PATH}/plugins/Geyser-Spigot.jar"
rm -rf -- "${fixture_dir}/prepared/previous"
rm -f -- "${fixture_dir}/prepared/replacement-started"
jv_crossplay_install "${fixture_dir}/prepared"
jv_crossplay_verify "${fixture_dir}/prepared"
[[ $(stat -c '%i' "${DATA_PATH}/plugins/floodgate-spigot.jar") == "${floodgate_inode}" ]]
grep -Fqx 'MODRINTH_PROJECTS=viaversion' "${repo_root}/templates/config/minecraft.env.in"
# Real descriptor discovery recognizes renamed managed JARs, not substrings.
python3 - "${DATA_PATH}/plugins" <<'PY'
import os, sys, zipfile
root = sys.argv[1]
for name, main in (
    ("imported-bridge.jar", "org.geysermc.geyser.platform.spigot.GeyserSpigotPlugin"),
    ("imported-auth.jar", "org.geysermc.floodgate.SpigotPlugin"),
    ("imported-via.jar", "com.viaversion.viaversion.ViaVersionPlugin"),
    ("NotGeyser-floodgate-ViaVersion.jar", "example.UnrelatedPlugin"),
):
    with zipfile.ZipFile(os.path.join(root, name), "w") as jar:
        jar.writestr("plugin.yml", "name: Fixture\nmain: " + main + "\n")
PY
"${repo_root}/mjust/libexec/managed-plugin-files" "${DATA_PATH}/plugins" all ''
for name in Geyser-Spigot.jar floodgate-spigot.jar imported-bridge.jar imported-auth.jar imported-via.jar; do
    [[ ! -e ${DATA_PATH}/plugins/${name} ]]
done
[[ -f ${DATA_PATH}/plugins/NotGeyser-floodgate-ViaVersion.jar && -f ${DATA_PATH}/plugins/KeepMe.jar ]]
[[ -f ${DATA_PATH}/plugins/floodgate/key.pem && -f ${DATA_PATH}/plugins/ViaVersion/config.yml ]]
# Exercise actual import ownership selection independently of host transport.
selected='{"geyserEnabled":true,"geyserAuthType":"floodgate","floodgateEnabled":true,"pluginJars":["old-geyser.jar","old-floodgate.jar"]}'
source_bedrock_manifest=''
source <(sed -n '/^source_geyser=/,/^# Destination-specific/p' "${repo_root}/mjust/libexec/migration-import-plan-destination.sh")
[[ ${BEDROCK_ENABLED} == yes && ${BEDROCK_MANAGED_PLUGINS} == yes ]]
# Staged normalization preserves imported configuration/key content except port.
source "${repo_root}/mjust/libexec/migration-common.sh"
printf 'online-mode=true\nserver-port=12345\n' > "${DATA_PATH}/server.properties"
printf 'bedrock:\n  port: 20000\nauth-type: floodgate\ncustom-setting: preserve\n' > "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
jv_migration_normalize_staged_runtime_files "${DATA_PATH}" yes
grep -q 'port: 19132' "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
grep -q 'custom-setting: preserve' "${DATA_PATH}/plugins/Geyser-Spigot/config.yml"
[[ $(cat "${DATA_PATH}/plugins/floodgate/key.pem") == identity-key ]]
[[ $(cat "${DATA_PATH}/plugins/floodgate/config.yml") == floodgate-config ]]
[[ $(cat "${DATA_PATH}/plugins/ViaVersion/config.yml") == via-config ]]
# Download preparation uses resolved immutable version/build URLs and leaves
# installed files untouched on checksum/API failure.
curl() {
    local output='' argument previous=''
    for argument in "$@"; do
        [[ ${previous} != -o ]] || output="${argument}"
        previous="${argument}"
    done
    local url="${*: -1}"
    case "${url}" in
        */projects/geyser/versions/latest/builds/latest) metadata geyser "${geyser_sha}" ;;
        */projects/floodgate/versions/latest/builds/latest) metadata floodgate "${floodgate_sha}" ;;
        */projects/geyser/versions/2.99.0-SNAPSHOT/builds/19/downloads/spigot) cp "${fixture_dir}/geyser.jar" "${output}" ;;
        */projects/floodgate/versions/2.99.0-SNAPSHOT/builds/19/downloads/spigot) printf corrupt > "${output}" ;;
        *) return 1 ;;
    esac
}
if jv_crossplay_prepare "${fixture_dir}/prepared"; then echo 'Bad downloaded checksum accepted' >&2; exit 1; fi
[[ -f ${DATA_PATH}/plugins/KeepMe.jar ]]
curl() { return 1; }
if jv_crossplay_prepare "${fixture_dir}/prepared"; then echo 'Offline download accepted' >&2; exit 1; fi
[[ $(jv_crossplay_artifact_state geyser '{}') == unavailable ]]

# Exercise the startup entrypoint in separate processes with local artifacts and
# deterministic failures. Keep the real install, preserve and rollback functions.
mkdir -p "${fixture_dir}/start"
cat > "${fixture_dir}/start/common.sh" <<'SH'
require_root() { :; }
require_config() { :; }
JV_PAPER_USER_AGENT=JustVoxel-test
mktemp() {
    if (( $# == 1 )) && [[ $1 == -d ]]; then
        local directory
        directory="$(command mktemp -d "${CROSSPLAY_TEST_CASE}/staging.XXXXXX")" || return 1
        printf '%s\n' "${directory}" > "${CROSSPLAY_TEST_CASE}/staging-path"
        printf '%s\n' "${directory}"
    else
        command mktemp "$@"
    fi
}
SH
cat > "${fixture_dir}/start/managed.sh" <<'SH'
source "${CROSSPLAY_TEST_FIXTURE}/managed.sh"
curl() {
    printf '%s\n' "${*: -1}" >> "${CROSSPLAY_TEST_CASE}/upstream-calls"
    local output='' argument previous='' project sha
    for argument in "$@"; do
        [[ ${previous} != -o ]] || output="${argument}"
        previous="${argument}"
    done
    case "${*: -1}" in
        */projects/geyser/*) project=geyser ;;
        */projects/floodgate/*) project=floodgate ;;
        *) return 1 ;;
    esac
    if [[ -z ${output} ]]; then
        sha="$(sha256sum "${CROSSPLAY_TEST_FIXTURE}/${project}.jar" | cut -d ' ' -f1)"
        jq -cn --arg project "${project}" --arg sha "${sha}" \
            '{project_id:$project,version:"2.99.0-SNAPSHOT",build:19,channel:"default",downloads:{spigot:{name:"plugin.jar",sha256:$sha}}}'
    elif [[ ${CROSSPLAY_TEST_SCENARIO} == prepare-failure && ${project} == floodgate ]]; then
        printf corrupt > "${output}"
    else
        cp "${CROSSPLAY_TEST_FIXTURE}/${project}.jar" "${output}"
    fi
}
install() {
    local argument staging
    staging="$(cat "${CROSSPLAY_TEST_CASE}/staging-path")"
    for argument in "$@"; do
        if [[ ${CROSSPLAY_TEST_SCENARIO} == install-failure || ${CROSSPLAY_TEST_SCENARIO} == rollback-failure ]]; then
            if [[ ${argument} == "${staging}/floodgate.jar" ]]; then
                # Prove that live replacement of the first component occurred.
                cmp "${CROSSPLAY_TEST_FIXTURE}/geyser.jar" "${DATA_PATH}/plugins/Geyser-Spigot.jar" || return 1
                : > "${CROSSPLAY_TEST_CASE}/partial-install"
                return 1
            fi
        fi
        if [[ ${CROSSPLAY_TEST_SCENARIO} == rollback-failure && ${argument} == "${staging}/previous/geyser.jar" ]]; then
            : > "${CROSSPLAY_TEST_CASE}/rollback-attempted"
            return 1
        fi
    done
    command install "$@"
}
restorecon() { :; }
if [[ ${CROSSPLAY_TEST_SCENARIO} == verify-failure ]]; then
    jv_crossplay_verify() {
        cmp "${CROSSPLAY_TEST_FIXTURE}/geyser.jar" "${DATA_PATH}/plugins/Geyser-Spigot.jar" || return 1
        cmp "${CROSSPLAY_TEST_FIXTURE}/floodgate.jar" "${DATA_PATH}/plugins/floodgate-spigot.jar" || return 1
        : > "${CROSSPLAY_TEST_CASE}/verification-attempted"
        return 1
    }
fi
SH
sed -e "s|/usr/libexec/justvoxel/mjust/common.sh|${fixture_dir}/start/common.sh|" \
    -e "s|/usr/libexec/justvoxel/mjust/managed-crossplay.sh|${fixture_dir}/start/managed.sh|" \
    "${repo_root}/mjust/libexec/managed-crossplay-start" > "${fixture_dir}/start/helper"

start_case() {
    local label="$1" scenario="$2" geyser_state="$3" floodgate_state="$4" bedrock="${5:-yes}"
    local case_dir="${fixture_dir}/start/${label}" data project state path protected_before staging status=0
    data="${case_dir}/data"
    mkdir -p "${data}" "${case_dir}/before"
    cp -a "${DATA_PATH}/plugins" "${data}/plugins"
    for project in geyser floodgate; do
        state="${geyser_state}"
        [[ ${project} == geyser ]] || state="${floodgate_state}"
        path="${data}/plugins/$(jv_crossplay_jar_name "${project}")"
        rm -f -- "${path}"
        case "${state}" in
            old) printf 'old-%s' "${project}" > "${path}" ;;
            empty) : > "${path}" ;;
            absent) continue ;;
            *) return 1 ;;
        esac
        cp "${path}" "${case_dir}/before/${project}.jar"
    done
    protected_before="$(sha256sum "${data}/plugins/KeepMe.jar" \
        "${data}/plugins/NotGeyser-floodgate-ViaVersion.jar" \
        "${data}/plugins/Geyser-Spigot/config.yml" "${data}/plugins/floodgate/config.yml" \
        "${data}/plugins/floodgate/key.pem" "${data}/plugins/ViaVersion/config.yml")"
    DATA_PATH="${data}" BEDROCK_ENABLED="${bedrock}" CROSSPLAY_TEST_FIXTURE="${fixture_dir}" \
        CROSSPLAY_TEST_CASE="${case_dir}" CROSSPLAY_TEST_SCENARIO="${scenario}" \
        bash "${fixture_dir}/start/helper" > "${case_dir}/output" 2>&1 || status=$?
    [[ $(sha256sum "${data}/plugins/KeepMe.jar" \
        "${data}/plugins/NotGeyser-floodgate-ViaVersion.jar" \
        "${data}/plugins/Geyser-Spigot/config.yml" "${data}/plugins/floodgate/config.yml" \
        "${data}/plugins/floodgate/key.pem" "${data}/plugins/ViaVersion/config.yml") == "${protected_before}" ]]
    case "${scenario}" in
        existing|disabled)
            [[ ${status} == 0 && ! -e ${case_dir}/upstream-calls && ! -e ${case_dir}/staging-path ]]
            ;;
        success)
            [[ ${status} == 0 ]]
            for project in geyser floodgate; do
                cmp "${fixture_dir}/${project}.jar" "${data}/plugins/$(jv_crossplay_jar_name "${project}")"
            done
            [[ $(wc -l < "${case_dir}/upstream-calls") == 4 ]]
            ;;
        *) [[ ${status} != 0 ]] ;;
    esac
    case "${scenario}" in
        install-failure|rollback-failure) [[ -f ${case_dir}/partial-install ]] ;;
        verify-failure) [[ -f ${case_dir}/verification-attempted ]] ;;
    esac
    if [[ ${scenario} != existing && ${scenario} != disabled ]]; then
        [[ -f ${case_dir}/staging-path ]]
    fi
    if [[ -f ${case_dir}/staging-path ]]; then
        staging="$(cat "${case_dir}/staging-path")"
        if [[ ${scenario} == rollback-failure ]]; then
            [[ -d ${staging} && -f ${staging}/replacement-started && -f ${case_dir}/rollback-attempted ]]
            cmp "${case_dir}/before/geyser.jar" "${staging}/previous/geyser.jar"
            cmp "${case_dir}/before/floodgate.jar" "${staging}/previous/floodgate.jar"
            grep -q 'ERROR: Managed cross-play rollback failed; appliance state requires attention' "${case_dir}/output"
            grep -Fq "${staging}" "${case_dir}/output"
        else
            [[ ! -e ${staging} ]]
        fi
    fi
    if [[ ${scenario} != success && ${scenario} != rollback-failure ]]; then
        for project in geyser floodgate; do
            path="${data}/plugins/$(jv_crossplay_jar_name "${project}")"
            if [[ -f ${case_dir}/before/${project}.jar ]]; then
                cmp "${case_dir}/before/${project}.jar" "${path}"
            else
                [[ ! -e ${path} && ! -L ${path} ]]
            fi
        done
    fi
}
start_case prepare prepare-failure old absent
start_case first-install success absent absent
# An empty canonical JAR triggers first-setup recovery even when both paths exist.
start_case restore-both install-failure old empty
start_case restore-geyser-absent install-failure absent old
start_case restore-floodgate-absent install-failure old absent
start_case restore-both-absent install-failure absent absent
start_case verify-restore-both verify-failure old empty
start_case verify-restore-absent verify-failure absent old
start_case preserve-recovery rollback-failure old empty
start_case preserve-existing existing old old
start_case bedrock-disabled disabled absent absent no
