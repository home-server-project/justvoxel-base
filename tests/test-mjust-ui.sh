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
[[ $(jv_variant_kind justvoxel-hws) == hws ]] || fail 'JustVoxel HWS variant kind is wrong'
[[ $(jv_variant_name justvoxel-hws) == HWS ]] || fail 'JustVoxel HWS display name is wrong'
jv_variant_is_hws justvoxel-hws || fail 'HWS predicate rejected JustVoxel HWS'
if jv_variant_is_hws justvoxel-vm; then fail 'HWS predicate accepted JustVoxel VM'; fi

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
network="${repo_root}/mjust/libexec/network"

for id in setup status players configure service whitelist backups migration storage update system validate logs advanced exit; do
    grep -Fq "${id})" "${menu}" || fail "menu preview/dispatch id missing: ${id}"
done

for forbidden in '/etc/containers/systemd/minecraft.container' 'minecraft-jv-' '/v1/admin/configuration' '.instances[0]' 'jv_select_registered_instance'; do
    if grep -Fq "${forbidden}" "${menu}"; then fail "menu must not use fallback authority or the direct-command selector: ${forbidden}"; fi
done
grep -Fq 'local -a client=(/usr/libexec/justvoxel/mjust/api-client)' "${menu}" || fail 'menu must use the shared API client'
grep -Fq 'if (( EUID != 0 )); then client=(sudo "${client[@]}"); fi' "${menu}" || fail 'unprivileged menu inventory must use sudo'
grep -Fq '"${client[@]}" GET /v1/minecraft/instances/runtime' "${menu}" || fail 'menu must request authoritative runtime inventory'
grep -A1 -Fx 'while true; do' "${menu}" | grep -Fxq '    refresh_inventory' || fail 'main loop must refresh inventory before showing the menu'
grep -Fq 'Management API / Minecraft inventory unavailable.' "${menu}" || fail 'unavailable inventory must have a concise diagnostic'

# Exercise the real inventory parsing, replacing only the transport and EUID
# so root and ordinary-user paths can be covered without appliance access.
(
    source <(sed -n '/^refresh_inventory() {/,/^}/p' "${menu}" |
        sed 's@/usr/libexec/justvoxel/mjust/api-client@menu_api_client@g; s/EUID/menu_test_euid/g')
    menu_api_client() {
        printf 'direct %s\n' "$*" >> "${visible}"
        printf '%s' "${inventory_response}"
        return "${inventory_rc}"
    }
    sudo() {
        printf 'sudo %s\n' "$*" >> "${visible}"
        [[ $1 == menu_api_client ]] || return 1
        shift
        menu_api_client "$@"
    }
    for menu_test_euid in 0 1000; do
        inventory_rc=0
        for inventory_response in \
            '{"instances":[]}' \
            '{"instances":[{"id":"jv-abc123"}]}' \
            '{"instances":[{"id":"jv-abc123"},{"id":"jv-def456"}]}'; do
            : > "${visible}"
            configured=unavailable
            refresh_inventory
            if [[ ${inventory_response} == '{"instances":[]}' ]]; then expected=no; else expected=yes; fi
            [[ ${configured} == "${expected}" ]] || fail "incorrect inventory state: ${inventory_response}"
            expected='direct GET /v1/minecraft/instances/runtime'
            if (( menu_test_euid != 0 )); then expected=$'sudo menu_api_client GET /v1/minecraft/instances/runtime\n'"${expected}"; fi
            [[ $(cat "${visible}") == "${expected}" ]] || fail 'inventory request used incorrect root/sudo transport'
        done
        # Include invalid later IDs, missing fields/types and multiple JSON documents.
        for inventory_response in \
            'not JSON' '{}' '[]' 'null' '{"instances":null}' '{"instances":{}}' \
            '{"instances":[{"id":"minecraft"}]}' \
            '{"instances":[{"id":"jv-abc123"},{"id":"bad"}]}' \
            '{"instances":[{}]}' '{"instances":[{"id":123}]}' \
            '{"instances":["jv-abc123"]}' \
            '{"instances":[]} {"instances":[]}'; do
            configured=yes
            refresh_inventory
            [[ ${configured} == unavailable ]] || fail "invalid inventory was treated as healthy or empty: ${inventory_response}"
        done
        inventory_rc=1
        inventory_response='{"instances":[]}'
        configured=no
        refresh_inventory
        [[ ${configured} == unavailable ]] || fail 'failed API request must not be treated as zero servers'
    done
)

# Only the dedicated operation picker selects a registered immutable ID.
# Exercise real menu functions/dispatch with fixture transport and commands.
(
    fixture_dir="$(mktemp -d)"
    trap 'rm -rf -- "${fixture_dir}"' EXIT
    fixture_source() {
        sed 's@/usr/libexec/justvoxel/mjust/api-client@menu_api_client@g; s@/usr/bin/mjust@menu_mjust@g; s/EUID/menu_test_euid/g'
    }
    source <(sed -n '/^[a-z_]*() {/,/^}/p' "${menu}" | fixture_source)
    source <({
        echo 'fixture_dispatch() {'
        sed -n '/^    case "${action}" in/,/^    esac/p' "${menu}"
        echo '}'
    } | fixture_source)
    menu_api_client() {
        [[ $* == 'GET /v1/minecraft/instances' ]] || fail 'picker used incorrect inventory authority'
        echo "direct $*" >> "${fixture_dir}/api"
        printf '%s' "${inventory_response}"
        return "${inventory_rc}"
    }
    sudo() {
        if [[ $1 == menu_api_client ]]; then
            echo "sudo $*" >> "${fixture_dir}/api"
            shift
            menu_api_client "$@"
        else
            echo "sudo $*" >> "${fixture_dir}/commands"
        fi
    }
    menu_mjust() { echo "mjust $*" >> "${fixture_dir}/commands"; }
    jui_pause() { echo pause >> "${fixture_dir}/pauses"; }
    jui_input() { printf 'Alex'; }
    jui_choose() {
        printf '%s\n' "$@" >> "${fixture_dir}/selectors"
        local answer
        IFS= read -r answer < "${fixture_dir}/choices" || fail 'unexpected selector'
        sed -i '1d' "${fixture_dir}/choices"
        printf '%s' "${answer}"
        return "${selector_rc}"
    }
    jv_variant_is_hws() { return 0; }
    reset_fixture() {
        local file
        for file in api commands selectors choices pauses; do : > "${fixture_dir}/${file}"; done
        inventory_rc=0 selector_rc=0 configured=yes
    }
    queue() { printf '%s\n' "$@" > "${fixture_dir}/choices"; }
    assert_commands() {
        [[ $(cat "${fixture_dir}/commands") == "$1" ]] || fail "incorrect menu dispatch: $(cat "${fixture_dir}/commands")"
    }
    assert_pickers() {
        local count
        count="$(grep -Fxc 'Choose Minecraft server' "${fixture_dir}/selectors" || true)"
        [[ ${count} == "$1" ]] || fail "expected $1 picker(s), got ${count}"
        [[ ! -s ${fixture_dir}/choices ]] || fail 'menu returned before consuming expected choices'
    }
    many='{"instances":[{"id":"jv-abc123","display_name":"Family Survival"},{"id":"jv-def456","display_name":"Kids Creative"}]}'
    selected='Kids Creative (jv-def456)'
    for menu_test_euid in 0 1000; do
        reset_fixture
        inventory_response='{"instances":[]}'
        action=players
        fixture_dispatch
        assert_commands ''
        assert_pickers 0
        [[ ! -s ${fixture_dir}/selectors ]] || fail 'zero servers opened a selector'

        reset_fixture
        inventory_response='{"instances":[{"id":"jv-def456","display_name":"Kids Creative"}]}'
        picked_id=stale picked_name=stale
        choose_instance picked_id picked_name || fail 'single registered server was rejected'
        [[ ${picked_id} == jv-def456 && ${picked_name} == 'Kids Creative' ]] || fail 'single server identity/name lost'
        [[ ! -s ${fixture_dir}/selectors ]] || fail 'single server displayed a picker'
        expected='direct GET /v1/minecraft/instances'
        if (( menu_test_euid != 0 )); then expected=$'sudo menu_api_client GET /v1/minecraft/instances\n'"${expected}"; fi
        [[ $(cat "${fixture_dir}/api") == "${expected}" ]] || fail 'picker used incorrect root/sudo transport'
        reset_fixture
        action=players
        fixture_dispatch
        assert_commands 'mjust players jv-def456'
        assert_pickers 0


        reset_fixture
        inventory_response=${many}
        queue "${selected}"
        choose_instance picked_id picked_name || fail 'explicit server choice rejected'
        [[ ${picked_id} == jv-def456 && ${picked_name} == 'Kids Creative' ]] || fail 'choice did not map to exact registered ID/name'
        expected=$'Choose Minecraft server\nFamily Survival (jv-abc123)\nKids Creative (jv-def456)\nBack'
        [[ $(cat "${fixture_dir}/selectors") == "${expected}" ]] || fail 'server picker labels/order incorrect'
    done
    # Exact labels map to IDs even with duplicate names or reordered inventory.
    for inventory_response in \
        '{"instances":[{"id":"jv-def456","display_name":"Kids Creative"},{"id":"jv-abc123","display_name":"Family Survival"}]}' \
        '{"instances":[{"id":"jv-abc123","display_name":"Kids Creative"},{"id":"jv-def456","display_name":"Kids Creative"}]}'; do
        reset_fixture
        queue "${selected}"
        choose_instance picked_id picked_name || fail 'reordered/duplicate-name choice rejected'
        [[ ${picked_id} == jv-def456 ]] || fail 'picker guessed by position or display name'
    done
    for inventory_response in \
        'bad JSON' '{}' '[]' 'null' '{"instances":null}' '{"instances":{}}' \
        '{"instances":[null]}' '{"instances":["jv-abc123"]}' \
        '{"instances":[{"id":"bad","display_name":"Name"}]}' \
        '{"instances":[{"id":123,"display_name":"Name"}]}' \
        '{"instances":[{"id":"jv-abc123","display_name":""}]}' \
        '{"instances":[{"id":"jv-abc123","display_name":123}]}' \
        '{"instances":[{"id":"jv-abc123"}]}' \
        '{"instances":[{"id":"jv-abc123","display_name":"A"},{"id":"jv-abc123","display_name":"B"}]}' \
        '{"instances":[{"id":"jv-abc123","display_name":"A"},{}]}' \
        '{"instances":[]} {"instances":[]}'; do
        reset_fixture
        action=players
        fixture_dispatch
        assert_commands ''
        [[ ! -s ${fixture_dir}/selectors ]] || fail 'invalid inventory opened a selector'
    done
    reset_fixture
    inventory_response=${many} inventory_rc=1
    fixture_dispatch
    assert_commands ''
    [[ ! -s ${fixture_dir}/selectors ]] || fail 'API failure opened a selector'

    # Back, cancellation and unrecognized output never dispatch or keep stale IDs.
    for answer in Back 'Kids Creative' 'Forged (jv-def456)'; do
        reset_fixture
        queue "${answer}"
        picked_id=stale picked_name=stale
        if choose_instance picked_id picked_name; then fail 'unrecognized/cancelled selection succeeded'; fi
        [[ -z ${picked_id} && -z ${picked_name} ]] || fail 'cancelled picker retained stale identity'
    done
    for action in players configure service whitelist backups update logs; do
        reset_fixture
        queue Back
        fixture_dispatch
        assert_commands ''
        assert_pickers 1
    done
    reset_fixture
    selector_rc=1
    queue "${selected}"
    action=players
    fixture_dispatch
    assert_commands ''

    reset_fixture
    queue "${selected}" 'Start Minecraft' 'Stop Minecraft' 'Restart Minecraft' Back
    action=service
    fixture_dispatch
    assert_pickers 1
    [[ $(grep -Fc 'direct GET' "${fixture_dir}/api") == 1 ]] || fail 'service reloaded selection inside submenu'
    assert_commands $'mjust start jv-def456\nmjust stop jv-def456\nmjust restart jv-def456'
    grep -Fxq 'Minecraft service - Kids Creative' "${fixture_dir}/selectors" || fail 'service title lost friendly name'

    reset_fixture
    queue "${selected}" 'Show whitelist' 'Add player' 'Java Edition' 'Remove player' 'Java Edition' \
        'Add player' 'Bedrock / Xbox' 'Remove player' 'Bedrock / Xbox' Back
    action=whitelist
    fixture_dispatch
    assert_pickers 1
    [[ $(grep -Fc 'direct GET' "${fixture_dir}/api") == 1 ]] || fail 'whitelist reloaded selection inside submenu'
    assert_commands $'mjust whitelist-list jv-def456\nmjust whitelist-add-java Alex jv-def456\nmjust whitelist-remove-java Alex jv-def456\nsudo /usr/libexec/justvoxel/mjust/whitelist --instance jv-def456 bedrock-enabled\nmjust whitelist-add-bedrock Alex jv-def456\nsudo /usr/libexec/justvoxel/mjust/whitelist --instance jv-def456 bedrock-enabled\nmjust whitelist-remove-bedrock Alex jv-def456'
    grep -Fxq 'Whitelist - Kids Creative' "${fixture_dir}/selectors" || fail 'whitelist title lost friendly name'

    reset_fixture
    queue "${selected}" 'Create backup now' 'Restore Minecraft data' 'Restore full Minecraft data' \
        'Automatic backup settings' 'Backup storage location' Back
    action=backups
    fixture_dispatch
    assert_pickers 1
    [[ $(grep -Fc 'direct GET' "${fixture_dir}/api") == 1 ]] || fail 'backups reloaded selection inside submenu'
    assert_commands $'mjust backup jv-def456\nmjust restore jv-def456\nmjust restore-full jv-def456\nsudo /usr/libexec/justvoxel/mjust/configure --instance jv-def456 backup\nsudo /usr/libexec/justvoxel/mjust/storage-provision backup'
    grep -Fxq 'Backups - Kids Creative' "${fixture_dir}/selectors" || fail 'backup title lost friendly name'

    for action in players configure update logs; do
        reset_fixture
        queue "${selected}"
        fixture_dispatch
        assert_pickers 1
        case ${action} in
            configure) expected='sudo /usr/libexec/justvoxel/mjust/configure --instance jv-def456' ;;
            update) expected='mjust update-minecraft jv-def456' ;;
            *) expected="mjust ${action} jv-def456" ;;
        esac
        assert_commands "${expected}"
        if [[ ${action} == logs || ${action} == configure ]]; then
            [[ ! -s ${fixture_dir}/pauses ]] || fail "${action} added an unnecessary pause"
        else
            [[ $(cat "${fixture_dir}/pauses") == pause ]] || fail "${action} lost its pause"
        fi
    done

    reset_fixture
    queue 'Export server' "${selected}" 'Import server' 'Family Survival (jv-abc123)' 'Recover / finalize interrupted import' Back
    action=migration
    fixture_dispatch
    assert_pickers 2
    assert_commands $'sudo /usr/libexec/justvoxel/mjust/migration-export --instance jv-def456\nsudo /usr/libexec/justvoxel/mjust/migration-import --instance jv-abc123\nmjust migration-recover'
    [[ $(head -n1 "${fixture_dir}/selectors") == Migration ]] || fail 'Migration selected a server before an operation'
    reset_fixture
    inventory_rc=1
    queue 'Recover / finalize interrupted import' Back
    fixture_dispatch
    assert_pickers 0
    assert_commands 'mjust migration-recover'
    [[ ! -s ${fixture_dir}/api ]] || fail 'global migration recovery requested server inventory'
    for operation in 'Export server' 'Import server'; do
        reset_fixture
        queue "${operation}" Back Back
        fixture_dispatch
        assert_commands ''
        assert_pickers 1
    done

    # Exercise global main operations and their existing submenus with API down.
    reset_fixture
    inventory_rc=1
    for action in setup status validate; do fixture_dispatch; done
    action=system
    queue 'System resources' 'File browser' Network UPS 'System status & updates' 'Update system' Back Back
    fixture_dispatch
    action=storage
    queue 'Storage overview' 'Move Minecraft data' Back
    fixture_dispatch
    action=advanced
    for operation in 'Administrator password' 'Show login welcome' 'Hide login welcome' 'Enable login welcome' 'Start over / reset Minecraft'; do
        queue "${operation}"
        fixture_dispatch
    done
    queue 'Storage devices / provisioning' 'Dedicated disk / USB backup storage' 'Storage plan / devices' Back
    fixture_dispatch
    assert_pickers 0
    [[ ! -s ${fixture_dir}/api ]] || fail 'global operation requested picker inventory'
    assert_commands $'mjust setup\nmjust status\nmjust validate\nmjust resources\nmjust files\nmjust net\nmjust ups\nmjust os-status\nmjust os-update\nmjust os-status\nsudo /usr/libexec/justvoxel/mjust/storage-summary\nmjust storage-migrate\nmjust password-reset\nmjust welcome\nmjust welcome-off\nmjust welcome-on\nmjust start-over\nmjust storage-disk\nmjust storage-plan'
)

# Direct commands still use the unchanged noninteractive shared selector.
# Existing instance-controls/configuration suites remain untouched.
(
    source <(sed 's@/usr/libexec/justvoxel/mjust/api-client GET /v1/minecraft/instances/runtime@direct_inventory@g' \
        "${repo_root}/mjust/libexec/instance-selection.sh")
    direct_inventory() { echo '{"instances":[{"id":"jv-abc123"},{"id":"jv-def456"}]}'; }
    jui_choose() { fail 'direct selector became interactive'; }
    if jv_select_registered_instance >/dev/null 2>&1; then fail 'direct ambiguous selection succeeded'; fi
    [[ $(jv_select_registered_instance jv-def456) == jv-def456 ]] || fail 'direct explicit immutable ID rejected'
    if jv_select_registered_instance 'Kids Creative' >/dev/null 2>&1; then fail 'direct selector accepted a display name'; fi
)

# Capture both main-menu selectors without invoking any appliance commands.
(
    source <(sed -n '/^select_rich() {/,/^}/p; /^select_compact() {/,/^}/p; /^migration_menu() {/,/^}/p' "${menu}")
    choose_instance() { fail 'main inventory/menu must not select a server'; }
    jui_terminal_cols() { echo 100; }
    jui_terminal_lines() { echo 30; }
    jui_menu_profile() { echo standard; }
    jui_menu_gap_enabled() { return 1; }
    fzf() { cat > "${visible}"; printf 'exit\tExit\n'; }
    jui_choose() { printf '%s\n' "$@" > "${visible}"; printf 'Back\n'; }

    configured=no
    select_rich >/dev/null
    expected=$'setup\tFirst setup\nstatus\tStatus\nstorage\tStorage\nsystem\tSystem\nexit\tExit'
    [[ $(cat "${visible}") == "${expected}" ]] || fail 'unconfigured rich menu has incorrect entries or order'
    select_compact >/dev/null
    expected=$'JustVoxel\nFirst setup\nStatus\nStorage\nSystem\nExit'
    [[ $(cat "${visible}") == "${expected}" ]] || fail 'unconfigured compact menu has incorrect entries or order'

    configured=yes
    select_rich >/dev/null
    grep -Fxq $'migration\tMigration' "${visible}" || fail 'configured rich menu must retain Migration'
    expected=$'status\tStatus\nplayers\tPlayers\nconfigure\tConfigure Minecraft\nservice\tStart / Stop / Restart\nwhitelist\tWhitelist\nbackups\tBackups\nmigration\tMigration\nstorage\tStorage\nupdate\tUpdate Minecraft\nsystem\tSystem\nvalidate\tValidation\nlogs\tLogs\nadvanced\tAdvanced\nexit\tExit'
    [[ $(cat "${visible}") == "${expected}" ]] || fail 'configured rich menu has incorrect entries or order'
    select_compact >/dev/null
    grep -Fxq 'Migration' "${visible}" || fail 'configured compact menu must retain Migration'
    expected=$'JustVoxel\nStatus\nPlayers\nConfigure Minecraft\nStart / Stop / Restart\nWhitelist\nBackups\nMigration\nStorage\nUpdate Minecraft\nSystem\nValidation\nLogs\nAdvanced\nExit'
    [[ $(cat "${visible}") == "${expected}" ]] || fail 'configured compact menu has incorrect entries or order'

    configured=unavailable
    select_rich >/dev/null
    expected=$'status\tStatus\nsystem\tSystem\nexit\tExit'
    [[ $(cat "${visible}") == "${expected}" ]] || fail 'unavailable rich menu must expose only Status, System and Exit'
    if grep -Fq 'First setup' "${visible}"; then fail 'unavailable rich menu must not offer First setup'; fi
    select_compact >/dev/null
    expected=$'JustVoxel\nStatus\nSystem\nExit'
    [[ $(cat "${visible}") == "${expected}" ]] || fail 'unavailable compact menu must expose only Status, System and Exit'
    if grep -Fq 'First setup' "${visible}"; then fail 'unavailable compact menu must not offer First setup'; fi

    migration_menu
    expected=$'Migration\nExport server\nImport server\nRecover / finalize interrupted import\nBack'
    [[ $(cat "${visible}") == "${expected}" ]] || fail 'Migration submenu has incorrect entries or order'
)
if grep -Fq 'Import existing Minecraft server' "${menu}" || grep -Eq '^        import\)' "${menu}"; then
    fail 'dead pre-setup import menu plumbing remains'
fi
grep -Fq "'Import server') migration_instance import ;;" "${menu}" || fail 'Migration import dispatch must remain available'
grep -Fq '  mjust import' "${menu}" || fail 'direct import command must remain advertised'

grep -Fq 'jui_menu_profile()' "${repo_root}/mjust/libexec/ui.sh" || fail 'shared adaptive menu profile helper missing'
grep -Fq 'jui_menu_gap_enabled()' "${repo_root}/mjust/libexec/ui.sh" || fail 'shared adaptive menu gap helper missing'
if grep -Fq 'main_menu_profile()' "${menu}" || grep -Fq 'main_menu_gap_enabled()' "${menu}"; then
    fail 'main menu still duplicates adaptive menu sizing helpers'
fi
grep -Fq -- '--height=100%' "${menu}" || fail 'main menu must use the full available terminal height'
grep -Fq -- '--info=hidden' "${menu}" || fail 'main menu should hide the fzf result counter'
grep -Fq -- '--gap=1' "${menu}" || fail 'tall terminals must support visual gaps between menu entries'
grep -Fq "right,35%,wrap,border-left" "${menu}" || fail 'wide main-menu preview geometry missing'
grep -Fq "right,40%,wrap,border-left" "${menu}" || fail 'standard main-menu preview geometry missing'
grep -Fq 'down,${preview_height},wrap,border-top' "${menu}" || fail 'narrow main-menu description placement missing'
if grep -Fq -- '--height=90%' "${menu}" || grep -Fq 'right,55%' "${menu}"; then
    fail 'legacy constrained main-menu geometry remains'
fi
grep -Fq 'Overall JustVoxel and Minecraft health.' "${menu}" || fail 'concise Status description missing'
grep -Fq 'Manage OS status, resources, files, networking and power.' "${menu}" || fail 'concise System description missing'
grep -Fq 'jui_is_interactive && command -v fzf' "${menu}" || fail 'interactive main menu must keep adaptive fzf descriptions available'
grep -Fq 'jui_menu_backend()' "${repo_root}/mjust/libexec/ui.sh" || fail 'shared menu backend selector missing'
grep -Fq "elif command -v fzf" "${repo_root}/mjust/libexec/ui.sh" || fail 'fzf menu backend missing'
grep -Fq -- '--height=100%' "${repo_root}/mjust/libexec/ui.sh" || fail 'submenus must use full terminal height'
grep -Fq -- '--info=hidden' "${repo_root}/mjust/libexec/ui.sh" || fail 'submenus should hide the fzf result counter'
grep -Fq -- '--gap=1' "${repo_root}/mjust/libexec/ui.sh" || fail 'submenus must support adaptive visual gaps'
grep -Fq 'sudo /usr/libexec/justvoxel/mjust/configure --instance "${instance_id}" backup || true' "${menu}" || fail 'backup schedule submenu must return directly without run_and_pause'
grep -Fq "'Backup storage location') sudo /usr/libexec/justvoxel/mjust/storage-provision backup || true ;;" "${menu}" || fail 'backup storage submenu must return directly without run_and_pause'

if grep -Fq 'setup-advanced' "${menu}" || grep -Fq 'Advanced setup' "${menu}" || grep -Fq 'setup-advanced' "${justfile}"; then
    fail 'legacy Advanced Setup must not be exposed through the mJust menu or recipes'
fi

grep -Fq "jui_choose 'Configure Minecraft'" "${configure}" || fail 'Configure must use the flattened interactive selector'
grep -Fq "'Show current configuration'         'Maximum players'         'Change memory limits'" "${configure}" || fail 'Configure flattened menu entries missing'
if grep -Fq "'Other safe Minecraft settings'" "${menu}"; then fail 'legacy Configure intermediate submenu remains'; fi
grep -Fq "'Back'" "${configure}" || fail 'Configure nested menus must expose Back navigation'
grep -Fq 'Enable / disable Bedrock cross-play' "${configure}" || fail 'post-setup Bedrock toggle missing'

grep -Fq "'Show whitelist' 'Add player' 'Remove player' 'Back'" "${menu}" || fail 'friendly whitelist menu missing'
grep -Fq 'bedrock-enabled' "${whitelist}" || fail 'whitelist Bedrock capability probe missing'
grep -Fq 'Bedrock cross-play is disabled.' "${menu}" || fail 'friendly disabled-Bedrock message missing from the terminal menu'

grep -Fq "'Storage overview' 'Move Minecraft data' 'Back'" "${menu}" || fail 'friendly storage menu missing'
grep -Fq 'Storage devices / provisioning' "${menu}" || fail 'advanced storage provisioning entry missing'
grep -Fq '/backups' "${storage_ui}" || fail 'backup storage must default to a backups directory'

grep -Fq 'System status & updates' "${menu}" || fail 'combined system status/update menu missing'
grep -Fq "'File browser' 'Network'" "${menu}" || fail 'Network must follow File browser in the System menu'
grep -Fq "'Network') /usr/bin/mjust net || true ;;" "${menu}" || fail 'System Network menu dispatch missing'
grep -Fq "'UPS') /usr/bin/mjust ups || true ;;" "${menu}" || fail 'System UPS menu dispatch missing'
grep -Fq 'ups:' "${justfile}" || fail 'mjust ups recipe missing'

# Exercise the menu functions with a captured selector so variant-specific
# entries and their order are checked without launching appliance commands.
source <(sed -n '/^show_noninteractive_help() {/,/^}/p; /^system_menu() {/,/^}/p' "${menu}")
jv_variant_is_hws() { [[ ${test_variant} == hws ]]; }
jui_choose() { printf '%s\n' "$@" > "${visible}"; printf 'Back\n'; }
for test_variant in hws vm; do
    system_menu
    if [[ ${test_variant} == hws ]]; then
        expected=$'System\nSystem status & updates\nSystem resources\nFile browser\nNetwork\nUPS\nReboot JustVoxel\nPower off JustVoxel\nFirmware / UEFI setup\nBack'
    else
        expected=$'System\nSystem status & updates\nSystem resources\nFile browser\nNetwork\nReboot JustVoxel\nPower off JustVoxel\nBack'
    fi
    [[ $(cat "${visible}") == "${expected}" ]] || fail "${test_variant} System menu has incorrect entries or order"
    help="$(show_noninteractive_help)"
    if [[ ${test_variant} == hws ]]; then
        grep -Fq '  mjust ups' <<< "${help}" || fail 'HWS help must advertise mjust ups'
    elif grep -Fq 'mjust ups' <<< "${help}"; then
        fail 'VM help must not advertise mjust ups'
    fi
done

ups="${repo_root}/mjust/libexec/ups"
source <(sed -n '/^ups_format_status() {/,/^}/p; /^main() {/,/^}/p' "${ups}")
ups_request() { [[ $1 == GET && $2 == /v1/ups ]] || return 1; printf '%s\n' '{"available":false,"source":{},"message":"UPS hardware is unavailable."}'; }
jui_has_tty() { return 0; }
jui_choose() { : > "${visible}"; printf 'Back\n'; }
rm -f "${visible}"
ups_output="$(main)" || fail 'unavailable UPS status must return cleanly'
grep -Fq 'UPS hardware is unavailable.' <<< "${ups_output}" || fail 'unavailable UPS message was not shown'
[[ ! -e ${visible} ]] || fail 'unavailable UPS entered the interactive configuration menu'

grep -Fq 'net:' "${justfile}" || fail 'mjust net recipe missing'
grep -Fq 'Friendly network manager from Home Server Project' "${network}" || fail 'nm-hsp friendly description missing'
grep -Fq 'Normal Ethernet, Wi-Fi and easy network troubleshooting.' "${network}" || fail 'nm-hsp troubleshooting description missing'
grep -Fq 'Classic NetworkManager interface' "${network}" || fail 'nmtui description missing'
grep -Fq 'Advanced/classic Linux networking configuration.' "${network}" || fail 'nmtui advanced description missing'
grep -Fq 'sudo /usr/bin/nm-hsp' "${network}" || fail 'network menu must launch nm-hsp'
grep -Fq 'sudo /usr/bin/nmtui' "${network}" || fail 'network menu must launch nmtui'
if grep -Eq '^(net-hsp|nmtui):' "${justfile}"; then fail 'separate mjust net-hsp/nmtui commands must not exist'; fi
if grep -Fq "'Operating system status' 'Check / download OS update'" "${menu}"; then
    fail 'duplicate OS status/update menu entries remain'
fi

grep -Fq "logs) instance_command logs ;;" "${menu}" || fail 'Logs menu does not open the API-backed Minecraft log view directly'
grep -Fq '"${api_client}" GET "/v1/minecraft/instances/${instance_id}/logs?limit=100&format=cat"' "${logs}" || fail 'Minecraft logs must use the instance-addressed message-only Management API view'
grep -Fq 'instance_id="$(jv_select_registered_instance "${instance_id}")" || exit 1' "${logs}" || fail 'Minecraft logs must select an immutable registered instance'
if grep -Fq -- '--advanced' "${logs}" || grep -Fq 'journalctl ' "${logs}" || grep -Fq 'Advanced / full system log' "${menu}"; then
    fail 'legacy advanced/full-system logs remain exposed through mJust'
fi

grep -Fq 'Administrator password' "${menu}" || fail 'administrator password menu entry missing'
grep -Fq '/usr/bin/mjust password-reset' "${menu}" || fail 'administrator password menu dispatch missing'
grep -Fq 'password-reset:' "${justfile}" || fail 'top-level password-reset recipe missing'
grep -Fq 'mjust status --details' "${mjust_bin}" || fail 'detailed status discovery missing from mjust --list'
grep -Fq 'mjust web enable' "${mjust_bin}" || fail 'WebUI management discovery missing from mjust --list'
grep -Fq 'HWS backup choices:' "${storage_plan}" || fail 'HWS storage guidance missing'

grep -Fq 'All mjust commands' "${menu}" || fail 'advanced all-commands entry missing'
grep -Fq '/usr/bin/mjust --list' "${menu}" || fail 'all-commands entry must use authoritative mjust --list output'

grep -Fq 'mjust/update-minecraft --instance "${instance_id}"' "${configure}" || fail 'Configure must delegate selected Version updates'

echo 'mjust UI regression tests passed.'
