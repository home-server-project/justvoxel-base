#!/usr/bin/bash
set -euo pipefail

# Validate JustVoxel product identity, direct Home Server Base parent, and
# inherited AlmaLinux upstream provenance.
# shellcheck disable=SC1091
source /usr/lib/os-release
[[ "${ID:-}" == "justvoxel" ]]
[[ "${VERSION_ID%%.*}" == "10" ]]
[[ "${PLATFORM_ID:-}" == "platform:el10" ]]
[[ "${JUSTVOXEL_BASE_ID:-}" == "home-server-base" ]]
[[ "${JUSTVOXEL_BASE_PRETTY_NAME:-}" == "Home Server Base 10" ]]
[[ "${JUSTVOXEL_BASE_VERSION_ID%%.*}" == "10" ]]
[[ "${JUSTVOXEL_BASE_PLATFORM_ID:-}" == "platform:el10" ]]
[[ "${JUSTVOXEL_BASE_CPE_NAME:-}" == "cpe:/o:home-server-project:home-server-base:10" ]]
[[ "${JUSTVOXEL_BASE_PROFILE:-}" == "almalinux-10-minimal-plus" ]]
[[ "${JUSTVOXEL_BASE_CHANNEL:-}" == "stable" ]]
[[ "${HOME_SERVER_BASE_UPSTREAM_ID:-}" == "almalinux" ]]
[[ "${HOME_SERVER_BASE_UPSTREAM_VERSION_ID%%.*}" == "10" ]]
[[ "${HOME_SERVER_BASE_UPSTREAM_PLATFORM_ID:-}" == "platform:el10" ]]
[[ "${HOME_SERVER_BASE_UPSTREAM_CPE_NAME:-}" == cpe:/o:almalinux:* ]]

for cmd in \
    bootc podman skopeo nmcli nmtui nm-hsp resolvectl firewall-cmd sshd sudo just mjust fzf gum \
    tailscale netbird curl jq openssl tar gzip rsync ping dig traceroute nc tcpdump lsof \
    findmnt mountpoint flock timeout mkfs.xfs btrfs mount.nfs mount.cifs mount.ntfs-3g lsblk blkid wipefs parted partprobe udevadm \
    qemu-ga vmtoolsd iperf3 micro spf; do
    command -v "${cmd}" >/dev/null
done

for filesystem_module in \
    '*/kernel/fs/fat/fat.ko*' \
    '*/kernel/fs/fat/vfat.ko*' \
    '*/kernel/fs/exfat/exfat.ko*'; do
    if ! find /usr/lib/modules -type f -path "${filesystem_module}" -print -quit | grep -q .; then
        echo "ERROR: required filesystem kernel module is missing: ${filesystem_module}" >&2
        exit 1
    fi
done

rpm -q \
    NetworkManager NetworkManager-tui systemd-resolved firewalld openssh-server sudo \
    pam authselect authselect-libs libpwquality \
    podman skopeo just fzf gum container-selinux policycoreutils-python-utils selinux-policy-extra \
    util-linux xfsprogs btrfs-progs ntfs-3g parted iperf3 nfs-utils cifs-utils qemu-guest-agent open-vm-tools \
    hyperv-daemons gssproxy zram-generator micro superfile nm-hsp >/dev/null

test -f /etc/pam.d/justvoxel
grep -Fqx 'auth       include      system-auth' /etc/pam.d/justvoxel
grep -Fqx 'account    include      system-auth' /etc/pam.d/justvoxel
grep -Fqx 'password   include      system-auth' /etc/pam.d/justvoxel

test -f /etc/sudoers.d/justvoxel-pwfeedback
test "$(stat -c '%a %U %G' /etc/sudoers.d/justvoxel-pwfeedback)" = '440 root root'
grep -Fqx 'Defaults pwfeedback' /etc/sudoers.d/justvoxel-pwfeedback
visudo -cf /etc/sudoers.d/justvoxel-pwfeedback >/dev/null

test -x /usr/lib/systemd/system-generators/zram-generator
test -f /etc/systemd/zram-generator.conf
grep -Fqx '[zram0]' /etc/systemd/zram-generator.conf
if grep -Eq '^[[:space:]]*(zram-size|compression-algorithm|swap-priority|writeback-device)[[:space:]]*=' /etc/systemd/zram-generator.conf; then
    echo 'ERROR: JustVoxel zram policy should use zram-generator built-in sizing/compression defaults.' >&2
    exit 1
fi

test -f /etc/rpm-ostreed.conf
grep -Fqx '[Daemon]' /etc/rpm-ostreed.conf
grep -Eq '^[[:space:]]*LockLayering[[:space:]]*=[[:space:]]*true[[:space:]]*$' /etc/rpm-ostreed.conf

semodule -l >/dev/null
micro --version >/dev/null
spf --version >/dev/null
if /usr/bin/nm-hsp --invalid-option >/tmp/nm-hsp-invalid.txt 2>&1; then
    echo 'ERROR: nm-hsp accepted an invalid option.' >&2
    exit 1
fi
grep -Fq 'usage: nm-hsp [--snapshot]' /tmp/nm-hsp-invalid.txt
rm -f /tmp/nm-hsp-invalid.txt

test "$(systemctl is-enabled NetworkManager.service)" = "enabled"
test "$(systemctl is-enabled systemd-resolved.service)" = "enabled"
test "$(systemctl is-enabled firewalld.service)" = "enabled"
test "$(systemctl is-enabled sshd.service)" = "enabled"
test "$(systemctl is-enabled justvoxel-web-bootstrap.service)" = "enabled"
test "$(systemctl is-enabled justvoxel-minecraft-shutdown-guard.service)" = "enabled"
[[ "$(systemctl is-enabled justvoxel-webui.service 2>/dev/null || true)" != "enabled" ]]
[[ "$(systemctl is-enabled justvoxel-management.service 2>/dev/null || true)" != "enabled" ]]
[[ "$(systemctl is-enabled tailscaled.service 2>/dev/null || true)" != "enabled" ]]
[[ "$(systemctl is-enabled netbird.service 2>/dev/null || true)" != "enabled" ]]

for forbidden in cockpit-system cockpit-files cockpit-podman cockpit-storaged cockpit-machines libvirt-daemon-kvm qemu-kvm virt-install; do
    if rpm -q "${forbidden}" >/dev/null 2>&1; then
        echo "ERROR: unrelated package present in JustVoxel core: ${forbidden}" >&2
        exit 1
    fi
done

test -f /etc/NetworkManager/conf.d/90-systemd-resolved.conf
grep -Fqx 'dns=systemd-resolved' /etc/NetworkManager/conf.d/90-systemd-resolved.conf
test -f /usr/lib/tmpfiles.d/justvoxel-resolved.conf
grep -Fq '/run/systemd/resolve/stub-resolv.conf' /usr/lib/tmpfiles.d/justvoxel-resolved.conf

test -f /usr/lib/tmpfiles.d/justvoxel-gssproxy.conf
grep -Fqx 'd /var/lib/gssproxy/clients 0700 root root -' /usr/lib/tmpfiles.d/justvoxel-gssproxy.conf
grep -Fqx 'd /var/lib/gssproxy/rcache  0700 root root -' /usr/lib/tmpfiles.d/justvoxel-gssproxy.conf
grep -Fqx 'z /var/lib/gssproxy/clients 0700 root root -' /usr/lib/tmpfiles.d/justvoxel-gssproxy.conf
grep -Fqx 'z /var/lib/gssproxy/rcache  0700 root root -' /usr/lib/tmpfiles.d/justvoxel-gssproxy.conf

test -f /etc/profile.d/zz-justvoxel-prompt.sh
grep -Fq '38;5;82' /etc/profile.d/zz-justvoxel-prompt.sh

test -L /etc/issue
test "$(readlink /etc/issue)" = '/run/justvoxel/issue'
test -f /usr/lib/systemd/system/getty@.service.d/10-justvoxel-issue.conf
grep -Fqx 'After=NetworkManager.service justvoxel-webui.service' /usr/lib/systemd/system/getty@.service.d/10-justvoxel-issue.conf
grep -Fqx 'ExecStartPre=/usr/libexec/justvoxel/console-issue-refresh' /usr/lib/systemd/system/getty@.service.d/10-justvoxel-issue.conf
test -x /usr/libexec/justvoxel/console-issue-refresh
bash -n /usr/libexec/justvoxel/console-issue-refresh

# Validate the renderer directly. Image-build containers do not run the WebUI
# service and may not have a usable network, so pre-login validation checks only
# fields that are always meaningful in that environment.
issue_test="$(mktemp)"
trap 'rm -f "${issue_test}"' EXIT
/usr/libexec/justvoxel/motd --issue > "${issue_test}"
grep -Fq 'JUSTVOXEL' "${issue_test}"
grep -Fq 'Variant:' "${issue_test}"
grep -Fq 'Minecraft:' "${issue_test}"
grep -Fq 'Web interface:' "${issue_test}"
grep -Fq 'Network:' "${issue_test}"
grep -Fq '\e[38;5;45m' "${issue_test}"
if grep -Fq 'Common commands' "${issue_test}"; then
    echo 'ERROR: pre-login console banner must stop before Common commands.' >&2
    exit 1
fi
if grep -Eq 'Tailscale:|NetBird:' "${issue_test}"; then
    echo 'ERROR: pre-login console banner must not expose overlay-network details.' >&2
    exit 1
fi
rm -f "${issue_test}"
trap - EXIT

/usr/libexec/justvoxel/console-issue-refresh
test -r /run/justvoxel/issue
rm -rf /run/justvoxel
test -f /etc/profile.d/90-justvoxel-motd.sh
bash -n /etc/profile.d/90-justvoxel-motd.sh
test -x /usr/libexec/justvoxel/motd
bash -n /usr/libexec/justvoxel/motd
grep -Fq 'Minecraft Server Appliance' /usr/libexec/justvoxel/motd
grep -Fq "justvoxel-hws|hws) variant='HWS'" /usr/libexec/justvoxel/motd
grep -Fq "network_state='Not connected - use mjust net'" /usr/libexec/justvoxel/motd
grep -Fq "network_state='Ethernet connected'" /usr/libexec/justvoxel/motd
grep -Fq "network_state='Ethernet connected, obtaining address...'" /usr/libexec/justvoxel/motd
grep -Fq "network_state='Wi-Fi connected'" /usr/libexec/justvoxel/motd
grep -Fq "wifi_value='Not connected'" /usr/libexec/justvoxel/motd
grep -Fq "line 'Network:'" /usr/libexec/justvoxel/motd
grep -Fq "line 'Wi-Fi:'" /usr/libexec/justvoxel/motd
grep -Fq "line 'Tailscale:'" /usr/libexec/justvoxel/motd
grep -Fq "line 'NetBird:'" /usr/libexec/justvoxel/motd
grep -Fq 'IPv4:' /usr/libexec/justvoxel/motd
grep -Fq 'Web interface:' /usr/libexec/justvoxel/motd
grep -Fq "line 'First setup:'" /usr/libexec/justvoxel/motd
if grep -Fq "line 'Validate system:'" /usr/libexec/justvoxel/motd; then
    echo 'ERROR: login guidance must not show the removed Validate system command.' >&2
    exit 1
fi
if grep -Fq 'setup-advanced' /usr/libexec/justvoxel/motd; then
    echo 'ERROR: login guidance must not expose the removed Advanced Setup path.' >&2
    exit 1
fi

test -f /usr/lib/justvoxel/variant
test -f /usr/lib/tmpfiles.d/justvoxel.conf
test -f /usr/lib/sysusers.d/justvoxel-rpc.conf
grep -Fqx 'u rpc 32 "Rpcbind Daemon" /var/lib/rpcbind -' /usr/lib/sysusers.d/justvoxel-rpc.conf

test -f /usr/lib/sysusers.d/justvoxel-web.conf
grep -Fq 'u justvoxel-web ' /usr/lib/sysusers.d/justvoxel-web.conf
test -f /usr/lib/tmpfiles.d/justvoxel-web.conf
test -f /usr/lib/systemd/system/justvoxel-minecraft-shutdown-guard.service
grep -Fqx 'After=minecraft.service' /usr/lib/systemd/system/justvoxel-minecraft-shutdown-guard.service
grep -Fqx 'ExecStop=/usr/libexec/justvoxel/mjust/host-shutdown-guard' /usr/lib/systemd/system/justvoxel-minecraft-shutdown-guard.service
test -f /usr/lib/systemd/system/justvoxel-management.service
test -f /usr/lib/systemd/system/justvoxel-webui.service
test -f /usr/lib/systemd/system/justvoxel-web-bootstrap.service
test -f /usr/lib/firewalld/services/justvoxel-web.xml
grep -Fqx 'User=justvoxel-web' /usr/lib/systemd/system/justvoxel-webui.service
grep -Fqx 'User=root' /usr/lib/systemd/system/justvoxel-management.service
grep -Fqx 'NoNewPrivileges=yes' /usr/lib/systemd/system/justvoxel-webui.service
sudo -u justvoxel-web test ! -r /etc/shadow
grep -Fq -- '--listen 0.0.0.0:8099' /usr/lib/systemd/system/justvoxel-webui.service
grep -Fq 'port="8099"' /usr/lib/firewalld/services/justvoxel-web.xml
if grep -Fq 'LoadCredential=' /usr/lib/systemd/system/justvoxel-webui.service; then
    echo 'ERROR: JustVoxel WebUI must not require self-signed TLS credentials for default local access.' >&2
    exit 1
fi
test -x /usr/libexec/justvoxel/management-agent
test -x /usr/libexec/justvoxel/justvoxel-webui
ldd /usr/libexec/justvoxel/management-agent | grep -Fq 'libpam.so'
ldd /usr/libexec/justvoxel/management-agent | grep -Fq 'libpwquality.so'
test -r /usr/lib/justvoxel/webui-release.json
jq -e '.management_api == "v1" and (.version | type == "string" and length > 0) and (.source_commit | test("^[0-9a-f]{40}$")) and (.build_date | type == "string" and length > 0) and (.go_version | type == "string" and length > 0) and (has("artifact_sha256") | not) and (has("release_tag") | not)' /usr/lib/justvoxel/webui-release.json >/dev/null
webui_version="$(jq -r '.version' /usr/lib/justvoxel/webui-release.json)"
webui_source="$(jq -r '.source_commit' /usr/lib/justvoxel/webui-release.json)"
webui_api="$(jq -r '.management_api' /usr/lib/justvoxel/webui-release.json)"
webui_version_output="$(/usr/libexec/justvoxel/justvoxel-webui -version)"
grep -Fqx "JustVoxel WebUI ${webui_version}" <<<"${webui_version_output}"
grep -Fqx "source=${webui_source}" <<<"${webui_version_output}"
grep -Fqx "management-api=${webui_api}" <<<"${webui_version_output}"
/usr/libexec/justvoxel/management-agent version | grep -Fq 'JustVoxel Management API v1'

test -x /usr/libexec/justvoxel/minecraft-backup
bash -n /usr/libexec/justvoxel/minecraft-backup
for template in \
    /usr/share/justvoxel/templates/quadlets/minecraft.container.in \
    /usr/share/justvoxel/templates/config/minecraft.env.in \
    /usr/share/justvoxel/templates/config/minecraft-backup.env.in \
    /usr/share/justvoxel/templates/systemd/minecraft-backup.service.in \
    /usr/share/justvoxel/templates/systemd/minecraft-backup.timer.in; do
    test -f "${template}"
done

test -x /usr/bin/mjust
test -f /usr/share/justvoxel/mjust/justfile
test -f /usr/libexec/justvoxel/mjust/storage-common.sh
test -x /usr/libexec/justvoxel/mjust/welcome
test -x /usr/libexec/justvoxel/mjust/status
test -x /usr/libexec/justvoxel/mjust/files
test -x /usr/libexec/justvoxel/mjust/network
test -x /usr/libexec/justvoxel/mjust/storage-summary
test -x /usr/libexec/justvoxel/mjust/web
test -x /usr/libexec/justvoxel/mjust/web-status-json
test -x /usr/libexec/justvoxel/mjust/ui.sh
test -x /usr/libexec/justvoxel/mjust/host-shutdown-guard
for script in /usr/libexec/justvoxel/mjust/*; do
    [[ -f "${script}" ]] || continue
    bash -n "${script}"
done
mjust_list="$(/usr/bin/mjust --list)"
if grep -Fq 'setup-advanced' <<<"${mjust_list}"; then
    echo 'ERROR: mjust command list must not expose the removed Advanced Setup path.' >&2
    exit 1
fi
grep -Fq 'status' <<<"${mjust_list}"
grep -Fq 'status --details' <<<"${mjust_list}"
grep -Fq 'files' <<<"${mjust_list}"
grep -Fq 'net' <<<"${mjust_list}"
grep -Fq 'web' <<<"${mjust_list}"
grep -Fq 'web enable' <<<"${mjust_list}"
grep -Fq 'password-reset' <<<"${mjust_list}"
grep -Fq 'welcome' <<<"${mjust_list}"
grep -Fq 'welcome-off' <<<"${mjust_list}"
grep -Fq 'welcome-on' <<<"${mjust_list}"

# The bootc image ships only immutable source templates and management logic.
# Active, administrator-owned runtime files are created later by `mjust setup`.
test ! -e /etc/containers/systemd/minecraft.container
test ! -e /etc/justvoxel/minecraft.env
test ! -e /etc/justvoxel/justvoxel.conf
