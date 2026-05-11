# 🧰 Tacklebox

**Tacklebox** is a high-performance orchestrator for `bootc` that provisions multi-tenant, updatable, and deduplicated bootable media (USB drives, SD cards, or raw disk images).

Born from the `superiso` project, Tacklebox evolves the concept from static ISOs to dynamic, writable GPT disks with a unified bootloader.

## ✨ Key Features

*   **🚀 Multi-Boot Dictatorship:** Automatically installs and manages `systemd-boot` on a unified ESP, resolving conflicts between Ostree and Composefs backends.
*   **🧠 Intelligent Deduplication:** Leverages a shared `containers/storage` and `ostree` repo across all bootable environments on a single disk.
*   **🔄 Integrated Update Lifecycle:** Update any OS on the drive in-place with `tacklebox update`. It safely rotates BLS entries and extracts new kernels/initrd files.
*   **💾 Modal Booting:** Supports both **Live (ephemeral)** and **Persistent** boot entries for the same OS image via smart kernel argument manipulation.
*   **📂 Shared Persistence:** Smart OverlayFS mounts allow sharing files in `/home/liveuser` across all OSes while isolating desktop-specific configurations (KDE vs GNOME).
*   **📦 Distribution Ready:** Built-in support for creating sparse `.img.xz` files for easy sharing.
*   **🛡️ Integrity First:** Automatically enables `fs-verity` on partitions to support modern container backends like Composefs.

## 🏗️ Architecture

### `tbox-root` Dracut Module
Tacklebox ships with a custom dracut module that handles the heavy lifting of multi-tenancy. At boot time, it:
1.  Locates the target OS subdirectory on the `TBOX_STORE` partition.
2.  Bind-mounts it to `/sysroot`.
3.  Sets up the persistent home overlay if requested.

### Composefs Support
Tacklebox automatically handles the unique requirements of the Composefs backend, including:
*   Enabling `fs-verity` during partition formatting.
*   Managing the required bootloader metadata that `bootc` expects.
*   Generating specialized BLS entries with `rootflags=subvol=...` mapping.

## 🛠 Usage

### Build a Multi-Boot Image
```bash
sudo tacklebox build recipe.json --xz
```

### Provision a Physical USB Drive
```bash
sudo tacklebox build recipe.json /dev/sda
```

## 📋 Recipe Schema

Tacklebox is driven by simple JSON recipes:

```json
{
  "media_name": "Tuna-Toolkit",
  "size": "30G",
  "shared_store": {
    "format": "ext4"
  },
  "bootable_environments": [
    {
      "id": "bluefin",
      "image": "ghcr.io/ublue-os/bluefin:stable",
      "modes": ["live", "persistent"]
    },
    {
      "id": "dakota",
      "image": "ghcr.io/projectbluefin/dakota:stable",
      "backend": "composefs",
      "modes": ["live"]
    }
  ]
}
```

## 🏗 Requirements

*   Go 1.22+
*   `podman` & `bootc`
*   `sgdisk` (gdisk)
*   `mkfs.vfat`, `mkfs.ext4` (with verity support)
*   `xz` (for compressed outputs)

## 👩‍💻 Development

Tacklebox uses `just` for common development tasks:

```bash
# Build the binary
just build

# Provision a test USB drive
just provision-usb device=/dev/sda recipe=examples/multi-test.json

# Build a compressed distribution image
just build-xz
```

---
*Part of the [Tuna OS](https://github.com/tuna-os) ecosystem.*
