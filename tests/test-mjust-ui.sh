#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/common.sh"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/ui.sh"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/player-guidance.sh"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/update-policy.sh"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

visible="$(mktemp)"
tty_helper="$(mktemp)"
trap 'rm -f "${visible}" "${tty_helper}"' EXIT

result="$(choose 'Choose a value' 'one' 'two' 2>"${visible}" <<< '2')"
[[ ${result} == 2 ]] || fail "choose stdout was '${result}', expected exactly '2'"
grep -Fq 'Choose a value' "${visible}" || fail 'legacy choose prompt was not visible'

[[ $(validate_nonroot_id 1000; echo $?) == 0 ]] || fail 'valid non-root UID was rejected'
if validate_nonroot_id 0; then fail 'UID/GID 0 must be rejected'; fi
for value in 1 5 10 20 100 999999; do validate_positive_int "${value}" || fail "positive player limit rejected: ${value}"; done
for value in 0 -1 abc 10.5; do if validate_positive_int "${value}"; then fail "invalid player limit accepted: ${value}"; fi; done

[[ $(jv_variant_kind justvoxel-vm) == vm ]] || fail 'JustVoxel VM variant kind is wrong'
[[ $(jv_variant_name justvoxel-vm) == VM ]] || fail 'JustVoxel VM display name is wrong'
[[ $(jv_variant_kind justvoxel-hwe) == hwe ]] || fail 'JustVoxel HWE variant kind is wrong'
[[ $(jv_variant_name justvoxel-hwe) == HWE ]] || fail 'JustVoxel HWE display name is wrong'
[[ $(jv_variant_kind justvoxel-baremetal) == hwe ]] || fail 'legacy Bare Metal variant must map to HWE'
jv_variant_is_hwe justvoxel-hwe || fail 'HWE predicate rejected JustVoxel HWE'
if jv_variant_is_hwe justvoxel-vm; then fail 'HWE predicate accepted JustVoxel VM'; fi

[[ $(normalize_daily_backup_time '4:30') == '04:30' ]] || fail '4:30 should normalize to 04:30'
[[ $(daily_backup_schedule_from_time '4:30') == '*-*-* 04:30:00' ]] || fail 'daily schedule rendering is incorrect'

manifest_fixture='{"manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":{"os":"linux","architecture":"amd64"}},{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","platform":{"os":"linux","architecture":"arm64"}}]}'
[[ $(jv_manifest_platform_digest "${manifest_fixture}" linux amd64) == 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' ]] || fail 'amd64 platform digest selection failed'
[[ $(jv_game_version_state pinned 26.2 26.2) == current ]] || fail 'equal pinned game version should be current'
[[ $(jv_game_version_state pinned 26.2 26.3) == update_available ]] || fail 'newer stable pinned game version should be update_available'

TERM=dumb
export TERM
[[ $(jui_backend) == none ]] || fail 'TERM=dumb must disable the interactive selector backend'
unset TERM

command -v script >/dev/null 2>&1 || fail 'util-linux script command is required for pseudo-TTY UI regression coverage'
cat > "${tty_helper}" <<EOF
#!/usr/bin/bash
set -euo pipefail
source "${repo_root}/mjust/libexec/ui.sh"
TERM=xterm
export TERM
PATH=/nonexistent
export PATH
value="\$(jui_choose 'Nested selector' 'one' 'two')"
printf 'RESULT=%s\n' "\${value}"
EOF
chmod +x "${tty_helper}"
tty_output="$(printf '1\n' | script -qec "${tty_helper}" /dev/null 2>&1 || true)"
grep -Fq 'RESULT=one' <<< "${tty_output}" || fail "nested selector failed under a pseudo-TTY: ${tty_output}"

menu="${repo_root}/mjust/libexec/menu"
configure="${repo_root}/mjust/libexec/configure"
storage_ui="${repo_root}/mjust/libexec/storage-ui.sh"
logs="${repo_root}/mjust/libexec/logs"
whitelist="${repo_root}/mjust/libexec/whitelist"
justfile="${repo_root}/mjust/justfile"
mjust_bin="${repo_root}/mjust/bin/mjust"
storage_plan="${repo_root}/mjust/libexec/storage-plan"

for id in setup setup-advanced status players configure service whitelist backups migration storage update system validate logs advanced exit; do
    grep -Fq "${id})" "${menu}" || fail "menu preview/dispatch id missing: ${id}"
done

grep -Fq "jui_choose 'Safe configuration changes'" "${configure}" || fail 'Configure must use the interactive selector'
grep -Fq "jui_choose 'Container image policy'" "${configure}" || fail 'container image policy must use the interactive selector'
grep -Fq "jui_choose 'Minecraft version policy'" "${configure}" || fail 'Minecraft version policy must use the interactive selector'
grep -Fq "'Back'" "${configure}" || fail 'Configure nested menus must expose Back navigation'
grep -Fq 'Enable / disable Bedrock cross-play' "${configure}" || fail 'post-setup Bedrock toggle missing'

grep -Fq "'Show whitelist' 'Add player' 'Remove player' 'Back'" "${menu}" || fail 'friendly whitelist menu missing'
grep -Fq 'bedrock-enabled' "${whitelist}" || fail 'whitelist Bedrock capability probe missing'
grep -Fq 'Bedrock cross-play is disabled.' "${menu}" || fail 'friendly disabled-Bedrock message missing from the terminal menu'

grep -Fq "'Storage overview' 'Move Minecraft data' 'Back'" "${menu}" || fail 'friendly storage menu missing'
grep -Fq 'Storage devices / provisioning' "${menu}" || fail 'advanced storage provisioning entry missing'
grep -Fq '/backups' "${storage_ui}" || fail 'backup storage must default to a backups directory'

grep -Fq 'System status & updates' "${menu}" || fail 'combined system status/update menu missing'
if grep -Fq "'Operating system status' 'Check / download OS update'" "${menu}"; then
    fail 'duplicate OS status/update menu entries remain'
fi

grep -Fq "'Minecraft logs' 'Advanced / full system log' 'Back'" "${menu}" || fail 'simple/advanced logs submenu missing'
grep -Fq -- '-o cat' "${logs}" || fail 'simple logs must use message-only journal output'
grep -Fq -- '--advanced' "${logs}" || fail 'advanced logs mode missing'

grep -Fq 'Administrator password' "${menu}" || fail 'administrator password menu entry missing'
grep -Fq '/usr/bin/mjust password-reset' "${menu}" || fail 'administrator password menu dispatch missing'
grep -Fq 'password-reset:' "${justfile}" || fail 'top-level password-reset recipe missing'
grep -Fq 'mjust status --details' "${mjust_bin}" || fail 'detailed status discovery missing from mjust --list'
grep -Fq 'mjust web enable' "${mjust_bin}" || fail 'WebUI management discovery missing from mjust --list'
grep -Fq 'HWE backup choices:' "${storage_plan}" || fail 'HWE storage guidance missing'
if grep -Fq 'Bare Metal backup choices:' "${storage_plan}"; then fail 'obsolete Bare Metal storage wording remains'; fi

grep -Fq 'All mjust commands' "${menu}" || fail 'advanced all-commands entry missing'
grep -Fq '/usr/bin/mjust --list' "${menu}" || fail 'all-commands entry must use authoritative mjust --list output'

echo 'mjust UI regression tests passed.'
