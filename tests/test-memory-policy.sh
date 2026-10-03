#!/usr/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/common.sh"
# shellcheck disable=SC1091
source "${repo_root}/mjust/libexec/player-guidance.sh"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

TEST_MEM_KIB=0
TEST_SWAP_KIB=$((64 * 1024 * 1024))

# Override only the /proc/meminfo lookup used by the recommendation helpers.
# Swap is intentionally huge and ignored: recommendations must use MemTotal only.
awk() {
    if [[ $# -eq 2 && $2 == /proc/meminfo ]]; then
        printf 'MemTotal: %s kB\nSwapTotal: %s kB\n' "${TEST_MEM_KIB}" "${TEST_SWAP_KIB}" | command awk "$1"
        return 0
    fi
    command awk "$@"
}

check_plan() {
    local gib="$1" expected="$2" actual
    TEST_MEM_KIB=$((gib * 1024 * 1024))
    actual="$(suggest_memory_values)"
    [[ ${actual} == "${expected}" ]] || fail "${gib} GiB physical RAM produced '${actual}', expected '${expected}'"
    [[ $(physical_ram_gib) == "${gib}" ]] || fail "physical RAM helper did not report ${gib} GiB"
}

check_plan 4  '2G 3G'
check_plan 6  '3G 4G'
check_plan 8  '4G 6G'
check_plan 12 '8G 10G'
check_plan 16 '8G 12G'
check_plan 32 '8G 12G'

# Kernel/firmware reservations must not demote nominal RAM sizing classes.
for mib in 7884 7936 8064; do
    TEST_MEM_KIB=$((mib * 1024))
    [[ $(suggest_memory_values) == '4G 6G' ]] || fail "8 GB-class ${mib} MiB recommendation did not preserve actual system headroom"
done
TEST_MEM_KIB=$((3840 * 1024))
[[ $(suggest_memory_values) == '2G 2816M' ]] || fail 'nominal 4 GB-class recommendation exceeded its memory budget'
TEST_MEM_KIB=$((5888 * 1024))
[[ $(suggest_memory_values) == '3G 4G' ]] || fail 'nominal 6 GB-class recommendation exceeded its memory budget'

# Reduced MemTotal must always retain the hard reserve, even below normal tiers.
for mib in 1536 2048 2560 3072 3584 3840 5632 7680 7884 15872 32256; do
    TEST_MEM_KIB=$((mib * 1024))
    read -r heap maximum <<< "$(suggest_memory_values)"
    heap_mib=$(memory_to_mib "${heap}")
    maximum_mib=$(memory_to_mib "${maximum}")
    (( heap_mib > 0 && heap_mib < maximum_mib )) || fail "${mib} MiB produced an unusable heap/container pair"
    (( mib - maximum_mib >= 1024 )) || fail "${mib} MiB recommendation violated the hard reserve"
done
TEST_MEM_KIB=$((1024 * 1024))
if suggest_memory_values >/dev/null; then
    fail 'recommendation must fail when no Minecraft allocation can preserve the hard reserve'
fi

# Prove that swap/zram capacity is not part of either recommendation input.
TEST_MEM_KIB=$((4 * 1024 * 1024))
for TEST_SWAP_KIB in 0 $((64 * 1024 * 1024)); do
    [[ $(suggest_memory_values) == '2G 3G' ]] || fail 'swap/zram influenced the 4 GiB Minecraft memory recommendation'
    [[ $(physical_ram_gib) == 4 ]] || fail 'swap/zram influenced physical RAM used by player guidance'
done

echo 'Minecraft physical-RAM recommendation tests passed.'
