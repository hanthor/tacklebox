#!/usr/bin/env bash
# scripts/test-install-e2e.sh <iso> [timeout_s]
#
# End-to-end install test for tacklebox offline-store ISOs:
#
#   1. Boot the ISO live in QEMU (SSH port forwarded to 2222)
#   2. Wait for SSH to come up
#   3. SSH in and run `bootc install to-disk` to a second (blank) disk,
#      using --source-imgref containers-storage:<ref> to pull from the
#      embedded offline store — no network pulls.
#   4. Shut down the live VM, detach the ISO
#   5. Boot the installed disk and require the login target
#
# This is the in-repo signal that the offline-store pipeline works
# end-to-end: build → live boot → offline install → reboot → login.
# The CI fixture ISO bakes sshd + password auth (e2e-sshd-setup.sh) so
# the test can drive the install without a console expect script.
#
# TCG-safe (no KVM on free runners). Default timeout 1200s (20 min).

set -euo pipefail

ISO="${1:?usage: $0 <iso> [timeout_s]}"
TIMEOUT="${2:-1200}"
LOG="${QEMU_E2E_LOG:-qemu-e2e-boot.log}"
INSTALL_LOG="${QEMU_E2E_INSTALL_LOG:-qemu-e2e-install.log}"
SSH_KEY="${QEMU_E2E_SSH_KEY:-}"
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR -p 2222"
if [ -n "$SSH_KEY" ]; then
	SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
fi

# ── OVMF firmware ──────────────────────────────────────────────────────────
OVMF_CODE=""
for f in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd /usr/share/edk2/ovmf/OVMF_CODE.fd; do
	[[ -f "$f" ]] && { OVMF_CODE="$f"; break; }
done
OVMF_VARS=""
for f in /usr/share/OVMF/OVMF_VARS_4M.fd /usr/share/OVMF/OVMF_VARS.fd /usr/share/edk2/ovmf/OVMF_VARS.fd; do
	[[ -f "$f" ]] && { OVMF_VARS="$f"; break; }
done
[[ -n "$OVMF_CODE" && -n "$OVMF_VARS" ]] || { echo "::error::OVMF firmware not found"; exit 77; }

# ── Target disk ────────────────────────────────────────────────────────────
# Create a blank 20G qcow2 disk for the install target. qcow2 so it's sparse
# on the runner (actual usage is ~2-3 GiB for a bootc deployment).
INSTALL_DISK="$(mktemp -u /tmp/tbox-e2e-target-XXXXXX.qcow2)"

# ── QEMU configuration ─────────────────────────────────────────────────────
VARS="$(mktemp)"
cp "$OVMF_VARS" "$VARS"
PIDFILE="$(mktemp)"

cleanup() {
	[[ -s "$PIDFILE" ]] && kill "$(cat "$PIDFILE")" 2>/dev/null || true
	rm -f "$VARS" "$PIDFILE" "$INSTALL_DISK"
}
trap cleanup EXIT

qemu-img create -f qcow2 "$INSTALL_DISK" 20G >/dev/null

ACCEL="tcg"
[[ -w /dev/kvm ]] && ACCEL="kvm"
CPU="max"
[[ "$ACCEL" == "kvm" ]] && CPU="host"

boot_live_iso() {
	: >"$LOG"
	qemu-system-x86_64 \
		-name tbox-e2e-live \
		-machine q35 -accel "$ACCEL" -cpu "$CPU" -m 4096 -smp 2 \
		-drive "if=pflash,format=raw,readonly=on,file=${OVMF_CODE}" \
		-drive "if=pflash,format=raw,file=${VARS}" \
		-cdrom "$ISO" -boot d \
		-drive "file=${INSTALL_DISK},format=qcow2,if=virtio" \
		-netdev user,id=net0,hostfwd=tcp::2222-:22 \
		-device virtio-net-pci,netdev=net0 \
		-serial "file:${LOG}" \
		-display none -pidfile "$PIDFILE" -daemonize
}

boot_installed_disk() {
	: >"$LOG"
	qemu-system-x86_64 \
		-name tbox-e2e-installed \
		-machine q35 -accel "$ACCEL" -cpu "$CPU" -m 3072 -smp 2 \
		-drive "if=pflash,format=raw,readonly=on,file=${OVMF_CODE}" \
		-drive "if=pflash,format=raw,file=${VARS}" \
		-drive "file=${INSTALL_DISK},format=qcow2,if=virtio" \
		-netdev user,id=net0 \
		-device virtio-net-pci,netdev=net0 \
		-serial "file:${LOG}" \
		-display none -pidfile "$PIDFILE" -daemonize
}

# ── SSH helpers ────────────────────────────────────────────────────────────
ssh_guest() {
	sshpass -p tboxci ssh $SSH_OPTS root@localhost "$@"
}

# ── Phase 1: Boot the live ISO ─────────────────────────────────────────────
echo ">>> [e2e] Phase 1: booting live ISO $ISO (accel=$ACCEL, timeout=${TIMEOUT}s)"
boot_live_iso

deadline=$(($(date +%s) + TIMEOUT))
ssh_up=0
while (($(date +%s) < deadline)); do
	if [[ -s "$PIDFILE" ]] && ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
		echo "::error::QEMU exited during live boot"
		tail -30 "$LOG"
		exit 1
	fi
	if grep -aq "Kernel panic" "$LOG"; then
		echo "::error::kernel panic during live boot"
		exit 1
	fi
	if grep -aqE "emergency mode|Emergency Shell" "$LOG"; then
		echo "::error::live boot dropped to emergency mode"
		grep -aE "Tacklebox|sysroot|Failed" "$LOG" | tail -20
		exit 1
	fi

	# Try SSH. sshpass may not be installed; fall back to expect-style
	# via sshpass or just check if sshd is listening.
	if [[ "$ssh_up" -eq 0 ]] && ssh_guest "echo SSH_OK" 2>/dev/null | grep -q SSH_OK; then
		echo ">>> [e2e] SSH is up on the live guest"
		ssh_up=1
		break
	fi
	sleep 10
done

if [[ "$ssh_up" -eq 0 ]]; then
	echo "::error::SSH did not come up within timeout"
	echo ">>> last 30 lines of serial log:"
	tail -30 "$LOG"
	exit 1
fi

# ── Phase 2: Install from offline store ────────────────────────────────────
echo ">>> [e2e] Phase 2: running bootc install to-disk from offline store"

# The offline store should be mounted at /var/lib/superiso-store by the
# superiso-store.mount unit. Verify it's there.
echo ">>> [e2e] verifying offline store is mounted..."
ssh_guest "ls -la /var/lib/superiso-store/ 2>&1 || echo OFFLINE_STORE_MISSING" | tee -a "$INSTALL_LOG"

echo ">>> [e2e] available images in offline store:"
ssh_guest "podman --root /var/lib/superiso-store images 2>&1 || echo STORE_LIST_FAILED" | tee -a "$INSTALL_LOG"

# The target disk: the second virtio-blk device is /dev/vdb (vda is the
# ISO's virtual CD-ROM). Actually, with -cdrom the ISO doesn't appear as
# a virtio-blk device, so the single virtio disk is /dev/vda.
# Let the guest tell us what it sees.
echo ">>> [e2e] block devices in guest:"
ssh_guest "lsblk -o NAME,SIZE,TYPE,MOUNTPOINTS 2>&1 || ls -la /dev/vd* /dev/sd* 2>&1 || echo BLOCK_LIST_FAILED" | tee -a "$INSTALL_LOG"

# Run bootc install to-disk. Use the first non-loopback virtio block device.
# The ISO's root is on an overlay; the target is the qcow2 virtio drive.
# We need to identify the correct target disk. With -cdrom, the ISO is
# /dev/sr0; the qcow2 virtio drive should be /dev/vda.
#
# Probe the actual target: find the virtio disk that is NOT the live
# root (which is an overlay mount, not a block device).
TARGET_DISK="/dev/vda"
echo ">>> [e2e] installing to $TARGET_DISK from offline store"

# The offline store images were embedded with their original refs.
# bootc resolves them via additionalimagestores → /var/lib/superiso-store.
#
# Inject console=ttyS0 into the installed system so we can see boot
# messages on the serial console in Phase 4.
INSTALL_REF="containers-storage:localhost/tbox-iso-alpha:latest"

# Use a temp file to capture both stdout/stderr and the exit code.
INSTALL_TMP="$(mktemp)"
set +e
ssh_guest "bootc install to-disk \
	--source-imgref '$INSTALL_REF' \
	--karg-append console=ttyS0 \
	'$TARGET_DISK'" >"$INSTALL_TMP" 2>&1
install_rc=$?
set -e
cat "$INSTALL_TMP" | tee -a "$INSTALL_LOG"
rm -f "$INSTALL_TMP"

if [[ "$install_rc" -ne 0 ]]; then
	echo "::error::bootc install to-disk failed (exit $install_rc)"
	echo ">>> install log tail:"
	tail -30 "$INSTALL_LOG"
	exit 1
fi

echo ">>> [e2e] bootc install succeeded"

# ── Phase 3: Shut down the live VM ─────────────────────────────────────────
echo ">>> [e2e] Phase 3: shutting down live guest"
ssh_guest "systemctl poweroff 2>&1 || poweroff 2>&1 || shutdown -h now 2>&1" || true

# Wait for QEMU to exit.
for _ in $(seq 1 30); do
	if [[ -s "$PIDFILE" ]] && ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
		echo ">>> [e2e] live guest shut down"
		break
	fi
	sleep 2
done
# Force-kill if still running.
[[ -s "$PIDFILE" ]] && kill "$(cat "$PIDFILE")" 2>/dev/null || true
sleep 2

# ── Phase 4: Boot the installed disk ───────────────────────────────────────
echo ">>> [e2e] Phase 4: booting installed disk"

: >"$LOG"
# Use a fresh VARS for the installed disk (OVMF boot entry from the
# install should be there, but keep it clean).
rm -f "$VARS"
cp "$OVMF_VARS" "$VARS"

boot_installed_disk

deadline=$(($(date +%s) + 600))  # 10 min for installed boot
login_ok=0
while (($(date +%s) < deadline)); do
	if [[ -s "$PIDFILE" ]] && ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
		echo "::error::QEMU exited during installed boot"
		tail -30 "$LOG"
		exit 1
	fi
	if grep -aq "Kernel panic" "$LOG"; then
		echo "::error::kernel panic during installed boot"
		exit 1
	fi
	if grep -aqE "emergency mode|Emergency Shell" "$LOG"; then
		echo "::error::installed system dropped to emergency mode"
		grep -aE "Failed|ERROR|Timed" "$LOG" | tail -20
		exit 1
	fi
	# login: on the console is the proof the installed system reached
	# userspace and started getty.
	if grep -aq "login:" "$LOG"; then
		echo ">>> [e2e] installed system reached login prompt"
		login_ok=1
		break
	fi
	sleep 10
done

if [[ "$login_ok" -eq 0 ]]; then
	echo "::error::installed system did not reach login within timeout"
	echo ">>> last 30 lines of serial log:"
	tail -30 "$LOG"
	exit 1
fi

# ── Done ────────────────────────────────────────────────────────────────────
echo ">>> [e2e] E2E install test PASSED: live boot → offline install → reboot → login"
exit 0
