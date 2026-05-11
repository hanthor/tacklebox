#!/bin/bash
# test-boot.sh <image> [timeout_sec]
#
# Boots a tacklebox image in QEMU and asserts that it reaches userspace.
# Requires qemu-system-x86_64 and OVMF.

set -euo pipefail

IMG="$1"
TIMEOUT="${2:-300}"
LOG="qemu-boot.log"
VARS="OVMF_VARS_test.fd"

if [ ! -f "$IMG" ]; then
    echo "Error: image $IMG not found"
    exit 1
fi

# Determine if it's an ISO or a block image
if [[ "$IMG" == *.iso ]]; then
    DRIVE_OPTS="file=$IMG,index=0,media=cdrom,format=raw"
else
    DRIVE_OPTS="file=$IMG,format=raw,if=virtio"
fi

# Prepare OVMF
OVMF_CODE=""
for p in "/usr/share/edk2/ovmf/OVMF_CODE.fd" "/usr/share/OVMF/OVMF_CODE.fd" "/usr/share/edk2/x64/OVMF_CODE.fd"; do
    if [ -f "$p" ]; then
        OVMF_CODE="$p"
        break
    fi
done

OVMF_VARS_SRC=""
for p in "/usr/share/edk2/ovmf/OVMF_VARS.fd" "/usr/share/OVMF/OVMF_VARS.fd" "/usr/share/edk2/x64/OVMF_VARS.fd"; do
    if [ -f "$p" ]; then
        OVMF_VARS_SRC="$p"
        break
    fi
done

if [ -z "$OVMF_CODE" ] || [ -z "$OVMF_VARS_SRC" ]; then
    echo "Error: OVMF_CODE.fd or OVMF_VARS.fd not found in standard paths"
    exit 1
fi

cp "$OVMF_VARS_SRC" "$VARS"

echo ">>> Booting $IMG (timeout ${TIMEOUT}s)..."
rm -f "$LOG"
touch "$LOG"

# Run QEMU with a timeout. 
# We use -display none -serial stdio and redirect to log.
# We use -accel kvm if available, else tcg.
ACCEL="tcg"
if [ -e /dev/kvm ] && [ -w /dev/kvm ]; then
    ACCEL="kvm"
fi

# shellcheck disable=SC2054
QEMU_CMD=(
    qemu-system-x86_64
    -m 4G
    -smp 2
    -accel "$ACCEL"
    -drive "if=pflash,format=raw,readonly=on,file=$OVMF_CODE"
    -drive "if=pflash,format=raw,file=$VARS"
    -drive "$DRIVE_OPTS"
    -netdev user,id=net0 -device virtio-net-pci,netdev=net0
    -serial "file:$LOG"
    -display none
)

# If not in KVM, we might need more time or a lighter CPU
if [ "$ACCEL" == "tcg" ]; then
    QEMU_CMD+=(-cpu max)
fi

# Start QEMU in background
"${QEMU_CMD[@]}" &
QEMU_PID=$!

# Function to kill QEMU on exit
cleanup() {
    kill "$QEMU_PID" 2>/dev/null || true
    rm -f "$VARS"
}
trap cleanup EXIT

# Wait for patterns in the log
START_TIME=$(date +%s)
SUCCESS=0

# Patterns to look for
PATTERNS=(
    "tbox-root.service: Deactivated successfully"
    "tbox-root.service: Succeeded"
    "Finished tbox-root.service"
    "ostree-prepare-root.service: Deactivated successfully"
    "Finished ostree-prepare-root.service"
    "Welcome to"
    "login:"
)

# Join patterns for grep
PATTERN_REGEX=$(printf "|%s" "${PATTERNS[@]}")
PATTERN_REGEX=${PATTERN_REGEX:1}

echo ">>> Monitoring $LOG for success patterns..."
while true; do
    CURRENT_TIME=$(date +%s)
    ELAPSED=$((CURRENT_TIME - START_TIME))
    
    if [ "$ELAPSED" -gt "$TIMEOUT" ]; then
        echo ">>> ERROR: Timeout reached after ${ELAPSED}s"
        echo ">>> Last 20 lines of $LOG:"
        tail -n 20 "$LOG"
        exit 1
    fi

    # Check for failure patterns (panic, etc.)
    if grep -qi "Kernel panic" "$LOG"; then
        echo ">>> ERROR: Kernel panic detected!"
        tail -n 20 "$LOG"
        exit 1
    fi

    # Check for success (we want to see at least 'Welcome to' or 'login:')
    if grep -qE "$PATTERN_REGEX" "$LOG"; then
        echo ">>> SUCCESS: Reached expected pattern after ${ELAPSED}s"
        SUCCESS=1
        break
    fi

    sleep 2
done

if [ "$SUCCESS" -eq 1 ]; then
    echo ">>> Final verification of critical services..."
    for p in "tbox-root.service" "ostree-prepare-root.service"; do
        if grep -q "$p" "$LOG"; then
            echo "  [OK] $p found in log"
        else
            echo "  [WARN] $p NOT found in log (may have missed it in stream)"
        fi
    done
    exit 0
fi
