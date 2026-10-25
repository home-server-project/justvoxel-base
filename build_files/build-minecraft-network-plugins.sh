#!/usr/bin/bash
# Invoked by the image workflows, never by appliance runtime.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
output="${repo_root}/build_artifacts/minecraft-network"
if ! command -v java >/dev/null 2>&1; then
    echo 'ERROR: Minecraft Network plugins require Java 25. Use the image workflow Java setup before invoking this helper.' >&2
    exit 1
fi
if ! java_properties="$(java -XshowSettings:properties -version 2>&1)"; then
    echo 'ERROR: Unable to run Java. Minecraft Network plugins require a working Java 25 installation.' >&2
    exit 1
fi
if ! grep -Eq '^[[:space:]]*java.specification.version = 25$' <<<"${java_properties}"; then
    echo 'ERROR: Minecraft Network plugins require Java 25.' >&2
    exit 1
fi
work="$(mktemp -d)"
trap 'rm -rf -- "${work}"' EXIT

# Acquire the exact verified LimboAPI compatibility pin on every image build.
python3 "${repo_root}/build_files/resolve-limboapi-release.py" "${output}"
limbo_jar="${output}/limboapi.jar"

# Velocity internals are a temporary compile-only input, never a runtime artifact.
velocity_dependencies="${work}/velocity"
python3 "${repo_root}/build_files/resolve-velocity-release.py" "${velocity_dependencies}"
mapfile -t velocity_release < <(python3 - "${velocity_dependencies}/velocity-release.json" <<'PY'
import json
import sys
with open(sys.argv[1]) as source:
    release = json.load(source)
print(release['version'])
print(release['build'])
print(release['filename'])
PY
)
if (( ${#velocity_release[@]} != 3 )); then
    echo 'ERROR: Missing Velocity compile dependency metadata.' >&2
    exit 1
fi
velocity_version="${velocity_release[0]}"
velocity_build="${velocity_release[1]}"
velocity_server_jar="${velocity_dependencies}/${velocity_release[2]}"
printf 'Velocity compile API: %s build %s STABLE\n' "${velocity_version}" "${velocity_build}"

# Standalone Gradle distribution: no LimboAPI source checkout or upstream wrapper.
# Gradle 9.1 supports running on Java 25; verify its official distribution checksum.
gradle_version=9.1.0
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    "https://services.gradle.org/distributions/gradle-${gradle_version}-bin.zip" -o "${work}/gradle.zip"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    "https://services.gradle.org/distributions/gradle-${gradle_version}-bin.zip.sha256" -o "${work}/gradle.sha256"
gradle_sha="$(cat "${work}/gradle.sha256")"
[[ "${gradle_sha}" =~ ^[0-9a-f]{64}$ ]] || exit 1
printf '%s  %s\n' "${gradle_sha}" "${work}/gradle.zip" | sha256sum --check --status
unzip -q "${work}/gradle.zip" -d "${work}"

one_jar() {
    local directory="$1" selected
    local -a jars=()
    mapfile -t jars < <(find "${directory}" -maxdepth 1 -type f -name '*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' -print)
    if (( ${#jars[@]} != 1 )); then
        echo 'ERROR: Missing or ambiguous Minecraft Network JAR output.' >&2
        return 1
    fi
    selected="${jars[0]}"
    [[ -s "${selected}" ]] || return 1
    printf '%s\n' "${selected}"
}
"${work}/gradle-${gradle_version}/bin/gradle" --no-daemon -p "${repo_root}/velocity-lobby" \
    --project-cache-dir "${work}/gradle-project-cache" \
    "-PbuildOutputDir=${work}/gradle-build" \
    "-PlimboApiJar=${limbo_jar}" "-PvelocityVersion=${velocity_version}" \
    "-PvelocityServerJar=${velocity_server_jar}" clean test jar
lobby_jar="$(one_jar "${work}/gradle-build/libs")"
python3 - "${limbo_jar}" "${lobby_jar}" <<'PY'
import json
import sys
import zipfile
for path, identity in zip(sys.argv[1:], ['limboapi', 'justvoxel-lobby']):
    with zipfile.ZipFile(path) as jar:
        metadata = json.loads(jar.read('velocity-plugin.json'))
        if metadata['id'] != identity or jar.testzip() is not None:
            raise ValueError('invalid plugin artifact')
        if identity == 'justvoxel-lobby' and any(name.startswith(('net/elytrium/', 'com/velocitypowered/', 'org/geysermc/')) for name in jar.namelist()):
            raise ValueError('runtime dependency must not be bundled')
PY
mkdir -p "${output}"
install -m0644 "${lobby_jar}" "${output}/justvoxel-lobby.jar"
(cd "${output}" && sha256sum limboapi.jar justvoxel-lobby.jar > SHA256SUMS)
