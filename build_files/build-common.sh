#!/usr/bin/bash
set -ouex pipefail

source /ctx/build_files/packages.env
: "${JUSTVOXEL_COMMON_PACKAGES:?JUSTVOXEL_COMMON_PACKAGES must be set}"
: "${TAILSCALE_PACKAGE:?TAILSCALE_PACKAGE must be set}"
: "${NETBIRD_PACKAGE:?NETBIRD_PACKAGE must be set}"

cp -avf /ctx/system_files/. /
install -m0440 /ctx/system_files/etc/sudoers.d/justvoxel-pwfeedback /etc/sudoers.d/justvoxel-pwfeedback

if ! dnf repolist --enabled | grep -Eiq '(^|[[:space:]])crb([[:space:]]|$)'; then
    echo "ERROR: AlmaLinux CRB repository is not enabled."
    exit 1
fi

dnf install -y epel-release curl
read -r -a common_packages <<< "${JUSTVOXEL_COMMON_PACKAGES}"
dnf install -y "${common_packages[@]}"

curl -fsSL \
    https://pkgs.tailscale.com/stable/rhel/10/tailscale.repo \
    -o /etc/yum.repos.d/tailscale.repo
sed -ri 's/^enabled=1/enabled=0/' /etc/yum.repos.d/tailscale.repo || true
dnf --enablerepo=tailscale-stable install -y "${TAILSCALE_PACKAGE}"
systemctl disable tailscaled.service 2>/dev/null || true

cat > /etc/yum.repos.d/netbird.repo <<'REPO'
[netbird]
name=NetBird
baseurl=https://pkgs.netbird.io/yum/
enabled=0
gpgcheck=1
gpgkey=https://pkgs.netbird.io/yum/repodata/repomd.xml.key
repo_gpgcheck=1
REPO

dnf --setopt=tsflags=noscripts --enablerepo=netbird install -y "${NETBIRD_PACKAGE}"
systemctl disable netbird.service 2>/dev/null || true

systemctl enable NetworkManager.service 2>/dev/null || true
systemctl enable systemd-resolved.service
systemctl enable firewalld.service 2>/dev/null || true
systemctl enable sshd.service 2>/dev/null || true
systemctl enable justvoxel-web-bootstrap.service

install -d -m0755 /usr/share/doc/justvoxel
cp -avf /ctx/docs/. /usr/share/doc/justvoxel/

install -d -m0755 /usr/share/justvoxel/templates
cp -avf /ctx/templates/. /usr/share/justvoxel/templates/

install -d -m0755 /usr/share/justvoxel/mjust
install -m0644 /ctx/mjust/justfile /usr/share/justvoxel/mjust/justfile
install -m0755 /ctx/mjust/bin/mjust /usr/bin/mjust

install -d -m0755 /usr/libexec/justvoxel
install -m0755 /ctx/runtime/minecraft-backup /usr/libexec/justvoxel/minecraft-backup
install -m0755 /ctx/runtime/justvoxel-motd /usr/libexec/justvoxel/motd
install -m0755 /ctx/runtime/justvoxel-console-issue /usr/libexec/justvoxel/console-issue-refresh
install -d -m0755 /usr/libexec/justvoxel/mjust
install -m0755 /ctx/mjust/libexec/* /usr/libexec/justvoxel/mjust/

if [[ ! -x /ctx/build_artifacts/management/management-agent ]]; then
    echo 'ERROR: verified JustVoxel management agent build artifact is missing.' >&2
    exit 1
fi
if [[ ! -x /ctx/build_artifacts/webui/justvoxel-webui || ! -r /ctx/build_artifacts/webui/webui-release.json ]]; then
    echo 'ERROR: verified JustVoxel WebUI build artifacts are missing.' >&2
    exit 1
fi
install -m0755 /ctx/build_artifacts/management/management-agent /usr/libexec/justvoxel/management-agent
install -m0755 /ctx/build_artifacts/webui/justvoxel-webui /usr/libexec/justvoxel/justvoxel-webui
install -d -m0755 /usr/lib/justvoxel
install -m0644 /ctx/build_artifacts/webui/webui-release.json /usr/lib/justvoxel/webui-release.json
jq -e '.management_api == "v1" and (.version | type == "string" and length > 0) and (.source_commit | test("^[0-9a-f]{40}$")) and (.build_date | type == "string" and length > 0) and (.go_version | type == "string" and length > 0) and (has("artifact_sha256") | not) and (has("release_tag") | not)' /usr/lib/justvoxel/webui-release.json >/dev/null
webui_version="$(jq -r '.version' /usr/lib/justvoxel/webui-release.json)"
webui_source="$(jq -r '.source_commit' /usr/lib/justvoxel/webui-release.json)"
webui_api="$(jq -r '.management_api' /usr/lib/justvoxel/webui-release.json)"
webui_version_output="$(/usr/libexec/justvoxel/justvoxel-webui -version)"
grep -Fqx "JustVoxel WebUI ${webui_version}" <<<"${webui_version_output}"
grep -Fqx "source=${webui_source}" <<<"${webui_version_output}"
grep -Fqx "management-api=${webui_api}" <<<"${webui_version_output}"
/usr/libexec/justvoxel/management-agent version | grep -Fq 'JustVoxel Management API v1'

install -d -m0755 /usr/libexec/justvoxel/health
install -m0755 /ctx/build_files/validate/common.sh /usr/libexec/justvoxel/health/common
install -m0755 /ctx/build_files/validate/vm.sh /usr/libexec/justvoxel/health/vm

for cmd in \
    bootc podman skopeo nmcli nmtui resolvectl firewall-cmd sshd sudo just mjust \
    tailscale netbird curl jq findmnt mountpoint flock mkfs.xfs mount.nfs mount.cifs \
    lsblk blkid wipefs parted partprobe udevadm qemu-ga vmtoolsd iperf3 python3 btop; do
    command -v "${cmd}"
done

bash -n /usr/libexec/justvoxel/minecraft-backup
bash -n /usr/libexec/justvoxel/motd
bash -n /usr/libexec/justvoxel/console-issue-refresh
bash -n /etc/profile.d/90-justvoxel-motd.sh
for script in /usr/bin/mjust /usr/libexec/justvoxel/mjust/*; do
    [[ -f "${script}" ]] || continue
    bash -n "${script}"
done
just --justfile /usr/share/justvoxel/mjust/justfile --list >/dev/null
semodule -l >/dev/null
