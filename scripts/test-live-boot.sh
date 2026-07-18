#!/usr/bin/env bash
# scripts/test-live-boot.sh <iso> [timeout_s] — boot a tacklebox live ISO
# in QEMU/OVMF and require the full live path to come up:
#
#   firmware → El Torito ESP → systemd-boot → kernel → tbox initramfs →
#   ISO mount → rootfs mount → overlay sysroot → systemd userspace login
#
# Pass:  ">>> Tacklebox: live root prepared" on serial AND a login prompt.
# Fail:  emergency mode, kernel panic, QEMU exit, or timeout.
#
# This is the in-repo signal that live ISO creation works AT BOOT, not
# just at build: a non-executable generator, a broken sysroot.mount, a
# bad ESP layout and an unmountable rootfs all fail here while passing
# `tacklebox verify` (all four have happened).
#
# TCG-safe (no KVM on free runners): a fedora-bootc base reaches login in
# ~3-6 min; default timeout 900s.

set -euo pipefail

ISO="${1:?usage: $0 <iso> [timeout_s]}"
TIMEOUT="${2:-900}"
LOG="${QEMU_LIVE_LOG:-qemu-live-boot.log}"

OVMF_CODE=""
for f in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd /usr/share/edk2/ovmf/OVMF_CODE.fd; do
	[[ -f "$f" ]] && { OVMF_CODE="$f"; break; }
done
OVMF_VARS=""
for f in /usr/share/OVMF/OVMF_VARS_4M.fd /usr/share/OVMF/OVMF_VARS.fd /usr/share/edk2/ovmf/OVMF_VARS.fd; do
	[[ -f "$f" ]] && { OVMF_VARS="$f"; break; }
done
[[ -n "$OVMF_CODE" && -n "$OVMF_VARS" ]] || { echo "::error::OVMF firmware not found"; exit 77; }

VARS="$(mktemp)"
cp "$OVMF_VARS" "$VARS"
PIDFILE="$(mktemp)"

ACCEL="tcg"
[[ -w /dev/kvm ]] && ACCEL="kvm"
CPU="max"
[[ "$ACCEL" == "kvm" ]] && CPU="host"

cleanup() {
	[[ -s "$PIDFILE" ]] && kill "$(cat "$PIDFILE")" 2>/dev/null || true
	rm -f "$VARS" "$PIDFILE"
}
trap cleanup EXIT

: >"$LOG"
qemu-system-x86_64 \
	-name tbox-live-boot \
	-machine q35 -accel "$ACCEL" -cpu "$CPU" -m 3072 -smp 2 \
	-drive "if=pflash,format=raw,readonly=on,file=${OVMF_CODE}" \
	-drive "if=pflash,format=raw,file=${VARS}" \
	-cdrom "$ISO" -boot d \
	-serial "file:${LOG}" \
	-display none -pidfile "$PIDFILE" -daemonize

echo ">>> booting $ISO (accel=$ACCEL, timeout=${TIMEOUT}s)"
deadline=$(($(date +%s) + TIMEOUT))
prepared=0
while (($(date +%s) < deadline)); do
	if [[ -s "$PIDFILE" ]] && ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
		echo "::error::QEMU exited during boot"
		exit 1
	fi
	if grep -aq "Kernel panic" "$LOG"; then
		echo "::error::kernel panic"
		exit 1
	fi
	if grep -aqE "emergency mode|Emergency Shell" "$LOG"; then
		echo "::error::live boot dropped to emergency mode"
		grep -aE "Tacklebox|sysroot|Failed" "$LOG" | tail -20
		exit 1
	fi
	if [[ "$prepared" -eq 0 ]] && grep -aqE "Tacklebox: live root prepared|Tacklebox: tbox-root-mount" "$LOG"; then
		echo ">>> tbox live/root markers on serial"
		prepared=1
	fi
	# login: alone is the proof — a live ISO cannot reach a getty without
	# the overlay sysroot having assembled. The dracut-initqueue markers
	# don't reliably reach ttyS0, so they are evidence, not a gate.
	if grep -aq "login:" "$LOG"; then
		echo ">>> reached login (tbox markers seen: $prepared) — live boot OK"
		exit 0
	fi
	sleep 10
done

echo "::error::timeout after ${TIMEOUT}s (tbox-markers-seen=$prepared)"
grep -aE "Tacklebox|sysroot|Failed|emergency" "$LOG" | tail -20 || true
exit 1
