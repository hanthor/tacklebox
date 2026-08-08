#!/usr/bin/env bash
# CI-only dev fixture: enables SSH password auth on the live image so the
# install E2E test can SSH into the QEMU guest and drive `bootc install
# to-disk` from the embedded offline store.
#
# Runs inside the customize container (root, CAP_SYS_ADMIN, network).
# Deliberately NOT production quality — this is a CI harness only.
#
# Also provisions the superiso-store.mount unit + storage.conf drop-in
# so the offline store squashfs (embedded in the ISO at
# /LiveOS/store.squashfs.img) gets loop-mounted at
# /var/lib/superiso-store at boot and is visible to bootc-installer as
# an additionalimagestores entry.

set -euo pipefail

# ── OpenSSH server ──────────────────────────────────────────────────────────
if command -v dnf >/dev/null 2>&1; then
	dnf install -y openssh-server 2>&1 | tail -5
elif command -v apt-get >/dev/null 2>&1; then
	apt-get update -qq && apt-get install -y -qq openssh-server 2>&1 | tail -5
fi

# Root password for CI SSH access. The QEMU userland network forwards
# port 2222 → guest:22, so this is never exposed beyond the host runner.
echo 'root:tboxci' | chpasswd

# Allow root login with password (CI only).
if [ -f /etc/ssh/sshd_config ]; then
	sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
	sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
fi
if [ -d /etc/ssh/sshd_config.d ]; then
	printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' \
		> /etc/ssh/sshd_config.d/99-tbox-ci.conf
fi

# Enable sshd so it starts on boot without manual intervention.
systemctl enable sshd.service 2>/dev/null || systemctl enable ssh.service 2>/dev/null || true

# SELinux: relabel sshd_config.d drop-in if selinux is present.
command -v restorecon >/dev/null 2>&1 && restorecon -R /etc/ssh 2>/dev/null || true

# ── superiso-store.mount ────────────────────────────────────────────────────
# The ISO embeds the offline containers-storage store at
# /LiveOS/store.squashfs.img. At boot, tbox-live mounts the ISO at
# /run/initramfs/live. This mount unit loop-mounts the squashfs so
# bootc-installer can resolve images without network access.
#
# Unit name is the systemd-escaped path for /var/lib/superiso-store.
UNIT_DIR="/etc/systemd/system"
UNIT_NAME='var-lib-superiso\x2dstore.mount'
mkdir -p "$UNIT_DIR" "$UNIT_DIR/local-fs.target.wants"

cat > "$UNIT_DIR/$UNIT_NAME" <<'UNITEOF'
[Unit]
Description=Tacklebox offline image store (ISO squashfs)
DefaultDependencies=no
After=systemd-remount-fs.service local-fs-pre.target
Before=local-fs.target
ConditionPathExists=/run/initramfs/live/LiveOS/store.squashfs.img

[Mount]
What=/run/initramfs/live/LiveOS/store.squashfs.img
Where=/var/lib/superiso-store
Type=squashfs
Options=loop,ro,nodev

[Install]
WantedBy=local-fs.target
UNITEOF

ln -sf "../$UNIT_NAME" "$UNIT_DIR/local-fs.target.wants/$UNIT_NAME" 2>/dev/null || true

# ── storage.conf drop-in ────────────────────────────────────────────────────
# Tell containers-storage (and bootc-installer) about the offline store.
DROPIN_DIR="/etc/containers/storage.conf.d"
mkdir -p "$DROPIN_DIR"

cat > "$DROPIN_DIR/99-tbox-store.conf" <<'DROPEOF'
# Written by tacklebox CI E2E fixture at image-customize time.
# Makes the offline squashfs store (mounted at /var/lib/superiso-store by
# the superiso-store.mount unit) visible to bootc-installer as a read-only
# additional image store.  Images in this store can be installed with
# 'bootc install --source-imgref containers-storage:<ref>'.
#
# driver = "overlay" is explicit: additionalimagestores silently ignores
# stores whose driver doesn't match the primary graphroot (tuna-os/tacklebox#93).
[storage]
driver = "overlay"

[storage.options]
additionalimagestores = ["/var/lib/superiso-store"]
DROPEOF

echo "e2e-sshd-setup: done"
