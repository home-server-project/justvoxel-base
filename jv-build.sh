#!/usr/bin/bash
# Human-run dev-vm build. Never deploys to or modifies the JustVoxel VM.
# Keep build/validation commands aligned with .github/workflows/build-testing.yml.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
repo_root="$PWD"
base_image=ghcr.io/home-server-project/home-server-base-10:stable
repository=192.168.125.100:5000/justvoxel-base
registry_api=http://192.168.125.100:5000/v2/justvoxel-base
builder=localhost/justvoxel-management-builder:local
config_dir="${HOME}/.config/justvoxel-local"
cache_dir="${HOME}/.cache/justvoxel-local"
builder_state="${cache_dir}/management-builder-base"

stage() { printf '\n== %s ==\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

stage 'Check local build prerequisites'
for command in git go node python3 rg podman skopeo jq cosign curl sudo flock sha256sum; do
    command -v "${command}" >/dev/null || fail "Missing required tool: ${command} (no packages are installed by this script)."
done
[[ -f "${repo_root}/.go-version" && -r "${repo_root}/.go-version" ]] || fail 'Missing readable .go-version file.'
repo_go="$(tr -d '[:space:]' < "${repo_root}/.go-version")"
[[ ${repo_go} =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail '.go-version must contain an exact stable Go version (major.minor.patch).'
export GOTOOLCHAIN="go${repo_go}"
go_version="$(go env GOVERSION)" || fail "Cannot use Go toolchain ${GOTOOLCHAIN}; a modern bootstrap Go command and access to its toolchain cache/download are required."
[[ ${go_version} == "${GOTOOLCHAIN}" ]] || fail "Go selected ${go_version}; expected ${GOTOOLCHAIN}."
printf 'Go toolchain: %s (.go-version)\n' "${go_version}"
for file in cosign.pub "${config_dir}/cosign.key" "${config_dir}/cosign.pub" "${config_dir}/ManagementAgentBuilder.Containerfile"; do
    [[ -r "${file}" ]] || fail "Missing readable prerequisite: ${file}"
done
mkdir -p "${cache_dir}"
exec 9>"${cache_dir}/build.lock"
flock -n 9 || fail 'Another local JustVoxel build is running.'
sudo -v
work_dir="$(mktemp -d "${cache_dir}/build.XXXXXX")"
cleanup() { sudo rm -rf -- "${work_dir}"; }
trap cleanup EXIT
source_commit="$(git rev-parse HEAD)"
immutable_tag="dev-$(date -u +%Y%m%d-%H%M%S)-${source_commit:0:7}"

base_digest_now() {
    local digest
    digest="$(skopeo inspect --raw "docker://${base_image}" | sha256sum | cut -d ' ' -f 1)" || return 1
    [[ "${digest}" =~ ^[0-9a-f]{64}$ ]] || fail 'Invalid Home Server Base digest.'
    printf '%s\n' "${digest}"
}
assert_base_unchanged() {
    [[ "$(base_digest_now)" == "${base_digest}" ]] || fail 'Home Server Base stable moved during the build; rerun ./jv-build.sh.'
}

stage 'Verify Home Server Base stable and refresh the reusable builder'
# Verify the exact manifest as well as the channel, preventing a tag-movement race.
cosign verify --new-bundle-format=false --key ./cosign.pub "${base_image}" >/dev/null
base_digest="$(base_digest_now)"
base_identity="${base_image}@sha256:${base_digest}"
cosign verify --new-bundle-format=false --key ./cosign.pub "${base_identity}" >/dev/null
builder_exists=0
if sudo podman image exists "${builder}"; then
    builder_exists=1
else
    status=$?
    [[ ${status} == 1 ]] || exit "${status}"
fi
stored_base=''
if [[ -f "${builder_state}" ]]; then stored_base="$(cat "${builder_state}")"; fi
if [[ ${builder_exists} == 0 || "${stored_base}" != "${base_digest}" ]]; then
    # Keep the local signing keys out of the builder's build context.
    mkdir "${work_dir}/builder-context"
    sudo podman build --pull=always --tag "${builder}" \
        --file "${config_dir}/ManagementAgentBuilder.Containerfile" "${work_dir}/builder-context"
    assert_base_unchanged
    printf '%s\n' "${base_digest}" >"${work_dir}/management-builder-base"
    mv "${work_dir}/management-builder-base" "${builder_state}"
fi

stage 'Source validation: shell, mjust and WebUI JavaScript (testing CI)'
syntax_failures=()
test_failures=()
test_count=0
check_syntax() {
  local script="$1" shebang
  shebang="$(head -n 1 "${script}")" || return 1
  if [[ "${shebang}" =~ ^'#!'[[:space:]]*([^[:space:]]*/)?(env[[:space:]]+)?python3([.][0-9]+)?([[:space:]]|$) ]]; then
    python3 -B -c 'import ast, pathlib, sys; ast.parse(pathlib.Path(sys.argv[1]).read_bytes(), filename=sys.argv[1])' "${script}"
  elif [[ "${shebang}" =~ ^'#!'[[:space:]]*([^[:space:]]*/)?(env[[:space:]]+)?(bash|sh|dash)([[:space:]]|$) ]] || [[ "${shebang}" != '#!'* ]]; then
    # Files without a shebang include sourced shell helpers.
    bash -n "${script}"
  else
    printf 'Unsupported interpreter: %s (%s)\n' "${script}" "${shebang}" >&2
    return 1
  fi
}
for script in \
  build_files/*.sh \
  build_files/validate/*.sh \
  mjust/bin/mjust \
  mjust/libexec/* \
  runtime/* \
  tests/*.sh; do
  [[ -f "${script}" ]] || continue
  printf 'Checking syntax: %s\n' "${script}"
  if check_syntax "${script}"; then
    printf 'PASS syntax: %s\n' "${script}"
  else
    printf 'FAIL syntax: %s\n' "${script}" >&2
    syntax_failures+=("${script}")
  fi
done
for test_script in tests/test-*.sh; do
  [[ -f "${test_script}" ]] || continue
  test_count=$((test_count + 1))
  printf 'Running test: %s\n' "${test_script}"
  # Real ownership checks need root inside a rootless user namespace.
  test_command=(bash "${test_script}")
  case "${test_script}" in
    tests/test-instance-delete.sh|tests/test-instance-backups.sh|tests/test-instance-restore.sh|tests/test-legacy-backup-restore-exclusion.sh)
      test_command=(podman unshare bash "${test_script}")
      ;;
  esac
  if "${test_command[@]}"; then
    printf 'PASS: %s\n' "${test_script}"
  else
    test_status=$?
    printf 'FAIL: %s (exit %s)\n' "${test_script}" "${test_status}" >&2
    test_failures+=("${test_script}")
  fi
done
if (( test_count == 0 )); then
  echo 'ERROR: no shell regression scripts found.' >&2
  exit 1
fi
if (( ${#syntax_failures[@]} > 0 )); then
  printf 'Syntax checks failed:\n' >&2
  printf '  %s\n' "${syntax_failures[@]}" >&2
fi
if (( ${#test_failures[@]} > 0 )); then
  printf 'Regression tests failed:\n' >&2
  printf '  %s\n' "${test_failures[@]}" >&2
fi
if (( ${#syntax_failures[@]} > 0 || ${#test_failures[@]} > 0 )); then
  exit 1
fi

for script in webui/internal/server/static/*.js; do
  [[ -f "${script}" ]] || continue
  node --check "${script}"
done

stage 'Build WebUI from the current working tree'
./build_files/build-webui.sh "${source_commit}" "${repo_root}/build_artifacts/webui"
webui_version="$(jq -er '.version | select(type == "string" and length > 0)' build_artifacts/webui/webui-release.json)"
jq -e --arg commit "${source_commit}" '.source_commit == $commit and .management_api == "v1"' \
    build_artifacts/webui/webui-release.json >/dev/null

stage 'Resolve Management Agent working-tree inputs'
# Same dependency set as testing CI, but read actual files, including untracked
# non-ignored files. Include path, executable mode and symlink target, not mtimes.
input_identity="$(python3 - "${base_identity}" <<'HASH_INPUTS'
import hashlib
import os
import stat
import subprocess
import sys

paths = [".go-version", "management", "templates", "mjust", "system_files",
         "runtime/system-update-reboot-worker", "build_files/build-common.sh",
         "build_files/build-management-agent.sh",
         "build_files/management-agent-package.Containerfile",
         "tests/run-pam-integration-alma.sh"]
entries = subprocess.check_output(
    ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", *paths])
h = hashlib.sha256()
for path in sorted(set(entries.split(b"\0")) - {b""}):
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        continue  # Tracked deletions are absent from the working-tree identity.
    if stat.S_ISLNK(info.st_mode):
        mode, content = b"120000", os.readlink(path)
    elif stat.S_ISREG(info.st_mode):
        mode = b"100755" if info.st_mode & 0o111 else b"100644"
        with open(path, "rb") as source:
            content = source.read()
    else:
        raise SystemExit("Unsupported Management input: " + os.fsdecode(path))
    h.update(mode + b" " + path + b"\0" + hashlib.sha256(content).digest())
h.update(sys.argv[1].encode() + b"\0management-api=v1\0")
print(h.hexdigest())
HASH_INPUTS
)"
agent_cache="${cache_dir}/management/${input_identity}"
mkdir -p build_artifacts/management
management_result=rebuilt
if [[ -s "${agent_cache}/management-agent" && -x "${agent_cache}/management-agent" &&
      -f "${agent_cache}/management-agent.sha256" ]] &&
    (cd "${agent_cache}" && sha256sum --status -c management-agent.sha256); then
    stage 'Reuse validated Management Agent binary'
    install -m0755 "${agent_cache}/management-agent" build_artifacts/management/management-agent
    management_result=reused
else
    stage 'Build and validate Management Agent in the reusable builder'
    mkdir -p "${cache_dir}/go-build" "${cache_dir}/go-path"
    mkdir "${work_dir}/management"
    sudo podman run --rm --pull=never --security-opt=label=disable \
        -e JUSTVOXEL_MANAGEMENT_SKIP_DNF=1 \
        -v "${cache_dir}/go-build:/tmp/go-build-cache" \
        -v "${cache_dir}/go-path:/tmp/go-path" \
        -v "${repo_root}:/repo:ro" \
        -v "${repo_root}/system_files/usr/libexec/justvoxel:/system_files/usr/libexec/justvoxel:ro" \
        -v "${repo_root}/system_files/etc/pam.d/justvoxel:/etc/pam.d/justvoxel:ro" \
        -v "${repo_root}/tests/run-pam-integration-alma.sh:/run-pam-integration-alma.sh:ro" \
        -v "${work_dir}/management:/out" \
        "${builder}" /bin/bash /repo/build_files/build-management-agent.sh
    assert_base_unchanged
    sudo chmod 0755 "${work_dir}/management/management-agent"
    install -m0755 "${work_dir}/management/management-agent" build_artifacts/management/management-agent
    mkdir -p "${agent_cache}"
    install -m0755 build_artifacts/management/management-agent "${agent_cache}/management-agent.tmp"
    mv "${agent_cache}/management-agent.tmp" "${agent_cache}/management-agent"
    (cd "${agent_cache}" && sha256sum management-agent >management-agent.sha256)
fi

stage 'Build complete JustVoxel Base image'
assert_base_unchanged
sudo podman build --pull=always --security-opt=label=disable --cap-add=all --device /dev/fuse \
    --build-arg "HOME_SERVER_BASE_IMAGE=${base_image}" \
    --build-arg JUSTVOXEL_BASE_REPOSITORY=ghcr.io/home-server-project/justvoxel-base \
    --build-arg JUSTVOXEL_VM_REPOSITORY=ghcr.io/home-server-project/justvoxel-vm \
    --label "org.opencontainers.image.revision=${source_commit}" \
    --label "org.opencontainers.image.created=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --label "io.home-server-project.justvoxel.webui.version=${webui_version}" \
    --label io.home-server-project.justvoxel.management-api=v1 \
    --target justvoxel-base --tag localhost/justvoxel-base:unrechunked --file Containerfile .
assert_base_unchanged

stage 'Rechunk complete image (target 127 layers, hard limit 128)'
mkdir "${work_dir}/rechunk"
chmod 1777 "${work_dir}/rechunk"
sudo podman run --rm --privileged \
    -v /var/lib/containers:/var/lib/containers -v "${work_dir}/rechunk:/var/tmp" \
    --entrypoint /usr/libexec/bootc-base-imagectl localhost/justvoxel-base:unrechunked \
    rechunk --max-layers 127 localhost/justvoxel-base:unrechunked localhost/justvoxel-base:local-dev
layer_count="$(sudo skopeo inspect --raw containers-storage:localhost/justvoxel-base:local-dev | jq -r '.layers | length')"
[[ "${layer_count}" =~ ^[0-9]+$ ]]
(( layer_count > 0 && layer_count <= 128 ))

stage 'Validate completed image: common health, Glances and bootc lint'
sudo podman run --rm \
  --entrypoint /bin/bash \
  localhost/justvoxel-base:local-dev \
  -c '
    set -euo pipefail
    /usr/libexec/justvoxel/health/common
    /usr/bin/glances -w --disable-webui --bind 127.0.0.1 --port 61208 --config /etc/glances/glances.conf >/tmp/glances-runtime.log 2>&1 &
    glances_pid=$!
    cleanup_glances() {
        kill "${glances_pid}" >/dev/null 2>&1 || true
        wait "${glances_pid}" >/dev/null 2>&1 || true
    }
    trap cleanup_glances EXIT
    ready=0
    for _ in $(seq 1 20); do
        if curl -fsS --max-time 1 http://127.0.0.1:61208/api/4/all | jq -e "has(\"cpu\") and has(\"mem\")" >/dev/null; then
            ready=1
            break
        fi
        sleep 0.5
    done
    if [[ "${ready}" -ne 1 ]]; then
        cat /tmp/glances-runtime.log >&2
        exit 1
    fi
    cleanup_glances
    trap - EXIT
    rm -f /tmp/glances-runtime.log /tmp/glances-root.log
    bootc container lint --fatal-warnings
  '

stage 'Push immutable image, sign and verify its exact digest'
# Check registry availability and refuse to overwrite an immutable tag.
skopeo list-tags --tls-verify=false "docker://${repository}" >"${work_dir}/tags.json"
jq -e --arg tag "${immutable_tag}" '(.Tags // []) | index($tag) == null' "${work_dir}/tags.json" >/dev/null
sudo podman push --tls-verify=false --digestfile "${work_dir}/digest" \
    localhost/justvoxel-base:local-dev "docker://${repository}:${immutable_tag}"
digest="$(cat "${work_dir}/digest")"
[[ "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]
cosign sign -y --allow-http-registry --new-bundle-format=false --use-signing-config=false \
    --tlog-upload=false --key "${config_dir}/cosign.key" "${repository}@${digest}"
cosign verify --allow-http-registry --new-bundle-format=false --insecure-ignore-tlog=true \
    --key "${config_dir}/cosign.pub" "${repository}@${digest}" >/dev/null

stage 'Publish verified image as :dev'
# Copy the signed remote digest verbatim; moving :dev cannot trigger conversion.
skopeo copy --src-tls-verify=false --dest-tls-verify=false --preserve-digests \
    "docker://${repository}@${digest}" "docker://${repository}:dev"
[[ "$(skopeo inspect --tls-verify=false --no-tags --format '{{.Digest}}' "docker://${repository}:dev")" == "${digest}" ]]

stage 'Retain newest five immutable dev images'
# Registry v2 deletes by digest, not tag. Resolve everything before deleting and
# protect every digest also referenced by a retained, moving or unrelated tag.
skopeo list-tags --tls-verify=false "docker://${repository}" >"${work_dir}/tags.json"
jq -r '(.Tags // []) | map(select(startswith("dev-"))) | sort | reverse | .[5:][]' \
    "${work_dir}/tags.json" >"${work_dir}/expired-tags"
declare -A protected_digests=()
declare -A expired_digests=()
jq -r '(.Tags // [])[]' "${work_dir}/tags.json" >"${work_dir}/all-tags"
while IFS= read -r tag; do
    tag_digest="$(skopeo inspect --tls-verify=false --no-tags --format '{{.Digest}}' "docker://${repository}:${tag}")"
    [[ "${tag_digest}" =~ ^sha256:[0-9a-f]{64}$ ]]
    if grep -Fxq -- "${tag}" "${work_dir}/expired-tags"; then
        expired_digests["${tag_digest}"]="${tag}"
    else
        protected_digests["${tag_digest}"]=1
    fi
done <"${work_dir}/all-tags"
for tag_digest in "${!expired_digests[@]}"; do
    if [[ -n "${protected_digests[${tag_digest}]:-}" ]]; then
        printf 'Keep %s: manifest also has a protected tag.\n' "${expired_digests[${tag_digest}]}"
        continue
    fi
    curl --fail --silent --show-error -X DELETE "${registry_api}/manifests/${tag_digest}"
    printf 'Removed expired manifest: %s\n' "${expired_digests[${tag_digest}]}"
done

stage 'Local development build complete'
printf 'WebUI version: %s\nManagement Agent: %s\nHome Server Base: %s\nImmutable tag: %s:%s\nMoving tag: %s:dev\nFinal digest: %s\nRechunked layers: %s / 128\n' \
    "${webui_version}" "${management_result}" "${base_identity}" "${repository}" "${immutable_tag}" \
    "${repository}" "${digest}" "${layer_count}"
