#!/usr/bin/bash
set -euo pipefail

test "$(cat /usr/lib/justvoxel/variant)" = "justvoxel-vm"

# VM keeps network-share clients and guarded partition tooling because a second
# virtual disk or NFS/SMB backup target is part of the supported storage model.
rpm -q nfs-utils cifs-utils parted xfsprogs btrfs-progs util-linux >/dev/null

# Validate the JustVoxel VM variant delta rather than rejecting every package
# with hardware-related functionality. AlmaLinux minimal-plus already includes
# fwupd and microcode_ctl, and open-vm-tools pulls in pciutils. Those packages
# are therefore intentionally allowed in the VM image.
#
# The packages below are JustVoxel physical-hardware administration additions
# that should stay out of the VM variant.
for package in \
    nut nut-client smartmontools smartmontools-selinux nvme-cli \
    lm_sensors ethtool usbutils dmidecode fwupd-efi \
    NetworkManager-wifi amd-ucode-firmware atheros-firmware \
    brcmfmac-firmware iwlwifi-dvm-firmware iwlwifi-mvm-firmware realtek-firmware \
    mt7xxx-firmware hdparm powertop; do
    if rpm -q "${package}" >/dev/null 2>&1; then
        echo "ERROR: physical-hardware package present in VM image: ${package}" >&2
        exit 1
    fi
done
