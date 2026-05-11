# Tacklebox task runner

output_base := "/var/mnt/data/tacklebox-build"
image_path := output_base / "tacklebox.img"
recipe_path := "examples/multi-test.json"

# Build the tacklebox binary
build:
    go build -o tacklebox ./cmd/tacklebox

# Run unit tests
test:
    go test ./...

# Provision a real disk (CAUTION: wipes the disk!)
provision-usb device="/dev/sda" recipe=recipe_path: build
    sudo ./tacklebox build {{recipe}} {{device}} -b {{output_base}}-usb

# Verify the generated image (partitions and boot entries)
verify-test image=image_path:
    #!/usr/bin/env bash
    set -euo pipefail
    echo ">>> Verifying image: {{image}}"
    LOOP_DEV=$(sudo losetup --find --show --partscan {{image}})
    trap 'sudo umount {{output_base}}/mount-esp 2>/dev/null || true; sudo losetup -d "$LOOP_DEV"' EXIT
    echo ">>> Mounted at $LOOP_DEV"
    sudo mount "${LOOP_DEV}p1" {{output_base}}/mount-esp
    ls -R {{output_base}}/mount-esp/loader/entries
    cat {{output_base}}/mount-esp/loader/entries/*.conf

# Boot the generated image in QEMU (nographic)
boot image=image_path:
    sudo qemu-system-x86_64 \
        -m 4G -accel kvm -cpu host \
        -drive if=pflash,format=raw,readonly=on,file=/usr/share/edk2/ovmf/OVMF_CODE.fd \
        -drive file={{image}},format=raw,if=virtio \
        -net none -display none -serial stdio

# Build and compress a multi-boot image for redistribution
build-xz recipe=recipe_path: build
    sudo ./tacklebox build {{recipe}} --xz -b {{output_base}}-dist

# Clean up build artifacts
clean:
    sudo rm -rf {{output_base}}*
    rm -f tacklebox
