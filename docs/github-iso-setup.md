# How to set up a GitHub repo that builds an ISO with Tacklebox

This guide walks you through creating a GitHub repository that builds a
UEFI-bootable ISO from one or more bootc container images using Tacklebox.

---

## How ISO builds work

`tacklebox build recipe.json --iso output.iso` uses the **IsoTarget** path:

1. Each bootable environment is packed into a squashfs file
   (`LiveOS/<id>.rootfs.sfs`) using `podman image mount` + `mksquashfs`.
2. The systemd-boot EFI binary is extracted from the first image in your recipe
   (it must ship `/usr/lib/systemd/boot/efi/systemd-bootx64.efi`).
3. `xorriso` wraps everything into an ISO9660+El Torito image that boots on
   real hardware and QEMU.

At runtime the ISO boots via `dmsquash-live`. Each env's squashfs is loop-mounted
and an overlayfs on top gives you a writable (but ephemeral) root. **No disk is
written.** Persistent mode is not supported for ISO targets — use a block target
(USB drive) if you need persistence.

---

## Image requirement: the `95tbox-root` dracut module

> **This is the single most important prerequisite.**

Tacklebox does not patch the initramfs it finds inside your container image.
For a multi-env ISO (more than one `bootable_environment`) the initramfs
**must** include the `95tbox-root` dracut module so the kernel can pivot to the
correct squashfs at boot. Without it, boot stalls at `initrd-switch-root`.

You have two options:

### Option A — Use pre-built superiso-live images (easiest)

The `superiso` project publishes images that already include the module, e.g.:

```
ghcr.io/tuna-os/superiso-live-bluefin:latest
ghcr.io/tuna-os/superiso-live-bazzite:latest
ghcr.io/tuna-os/superiso-live-aurora:latest
```

Reference these directly in your recipe and you're done.

### Option B — Bake the module into your own image

Fork or derive from a bootc image and add a layer that installs the dracut
module and rebuilds the initramfs. The superiso `live/Containerfile.generic`
is the canonical example:

```dockerfile
FROM ghcr.io/ublue-os/bluefin:stable

# Copy the tacklebox dracut module into the image
COPY --from=ghcr.io/tuna-os/tacklebox:latest \
     /usr/lib/dracut/modules.d/95tbox-root \
     /usr/lib/dracut/modules.d/95tbox-root

# Rebuild the initramfs with the new module
RUN DRACUT_NO_XATTR=1 dracut -v --force --zstd --reproducible --no-hostonly \
        --add 'dmsquash-live tbox-root' \
        /usr/lib/modules/$(ls /usr/lib/modules)/initramfs.img \
        $(ls /usr/lib/modules)
```

Build and push this image (e.g. `ghcr.io/your-org/my-live-image:latest`) then
reference it in your recipe.

---

## Repository layout

A minimal repo looks like this:

```
my-iso-repo/
├── .github/
│   └── workflows/
│       └── build-iso.yml      # CI workflow (see below)
├── recipes/
│   └── my-iso.json            # your recipe
└── README.md
```

---

## Writing a recipe

ISO recipes are identical to block recipes except:

- Only `"modes": ["live"]` is meaningful (ISOs are always ephemeral).
- `size` is used for internal staging only; the final ISO is as large as it
  needs to be.
- `partitions` is ignored for ISO targets.

```json
{
  "media_name": "MY_ISO",
  "size": "20G",
  "shared_store": {
    "format": "ext4",
    "compression": "zstd"
  },
  "bootable_environments": [
    {
      "id": "bluefin",
      "image": "ghcr.io/tuna-os/superiso-live-bluefin:latest",
      "desktop": "gnome",
      "modes": ["live"]
    },
    {
      "id": "bazzite",
      "image": "ghcr.io/tuna-os/superiso-live-bazzite:latest",
      "desktop": "kde",
      "modes": ["live"]
    }
  ]
}
```

**Sizing rule of thumb:** each squashfs is roughly 5–8 GiB. A two-env ISO
needs ~16 GiB of free disk during the build; the output `.iso` will be
smaller (squashfs is already compressed).

---

## Building locally

```bash
# Install build dependencies (Fedora/rpm-ostree host)
sudo dnf install -y xorriso mtools squashfs-tools dosfstools \
                    systemd-boot podman

# Build tacklebox
git clone https://github.com/tuna-os/tacklebox
cd tacklebox
go build -o tacklebox ./cmd/tacklebox

# Build the ISO
sudo ./tacklebox build recipes/my-iso.json --iso /tmp/my-iso.iso
```

The output ISO is a hybrid image: it boots from a USB drive
(`sudo dd if=/tmp/my-iso.iso of=/dev/sdX bs=4M status=progress`) and from
a virtual CD-ROM in QEMU.

---

## GitHub Actions workflow

Save this as `.github/workflows/build-iso.yml`:

```yaml
name: Build ISO

on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:
  # Build a fresh ISO every week even without commits
  schedule:
    - cron: '0 4 * * 1'

env:
  RECIPE: recipes/my-iso.json
  ISO_NAME: my-iso.iso

jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 60
    permissions:
      contents: write      # needed to upload a release asset
      packages: read       # needed to pull ghcr.io images

    steps:
      - uses: actions/checkout@v4
        with:
          submodules: recursive

      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      # Free up ~30 GB on the runner's root filesystem
      - name: Free disk space
        uses: jlumbroso/free-disk-space@main
        with:
          tool-cache: false
          android: true
          dotnet: true
          haskell: true
          large-packages: false
          docker-images: true
          swap-storage: false

      - name: Install build dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y --no-install-recommends \
            xorriso mtools squashfs-tools dosfstools \
            systemd-boot systemd-boot-efi gdisk podman

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build tacklebox
        run: |
          git clone --depth=1 https://github.com/tuna-os/tacklebox tacklebox-src
          cd tacklebox-src
          go build -o ../tacklebox ./cmd/tacklebox

      - name: Pre-pull images
        run: |
          # Pull all images from the recipe in parallel
          jq -r '.bootable_environments[].image' "$RECIPE" | \
            xargs -P4 -I{} sudo podman pull {}

      - name: Build ISO
        run: |
          sudo mkdir -p /mnt/tbx
          sudo ./tacklebox build "$RECIPE" \
            --iso "/mnt/tbx/$ISO_NAME" \
            -b /mnt/tbx

      - name: Verify ISO
        run: sudo ./tacklebox verify "/mnt/tbx/$ISO_NAME"

      - name: Upload ISO artifact
        uses: actions/upload-artifact@v4
        with:
          name: ${{ env.ISO_NAME }}
          path: /mnt/tbx/${{ env.ISO_NAME }}
          retention-days: 14

      # Optional: create a GitHub Release on tags
      - name: Create release
        if: startsWith(github.ref, 'refs/tags/')
        uses: softprops/action-gh-release@v2
        with:
          files: /mnt/tbx/${{ env.ISO_NAME }}
```

### What each stage does

| Stage | What happens |
|---|---|
| Free disk space | Recovers ~30 GiB needed for squashfs builds on free runners |
| Install build deps | `xorriso` (ISO assembly), `mtools` (FAT manipulation), `squashfs-tools`, `dosfstools`, `systemd-boot` |
| Log in to GHCR | Allows pulling private or rate-limited container images |
| Build tacklebox | Compiles the binary from source; pin to a tag for reproducibility |
| Pre-pull images | Parallel pull so build step doesn't time out on network I/O |
| Build ISO | Runs `tacklebox build --iso`; output lands in `/mnt` (more space) |
| Verify ISO | Sanity-checks BLS entries and squashfs distinctness |
| Upload artifact | ISO is available for 14 days from the Actions run |
| Create release | Attaches the ISO to a GitHub Release when you push a tag |

---

## Pinning the tacklebox version

For reproducible builds, pin tacklebox to a specific commit or tag:

```yaml
- name: Build tacklebox
  run: |
    git clone --depth=1 --branch v0.3.0 \
      https://github.com/tuna-os/tacklebox tacklebox-src
    cd tacklebox-src && go build -o ../tacklebox ./cmd/tacklebox
```

Alternatively, include tacklebox as a **git submodule**:

```bash
git submodule add https://github.com/tuna-os/tacklebox tacklebox
```

Then in the workflow:

```yaml
- uses: actions/checkout@v4
  with:
    submodules: recursive

- name: Build tacklebox
  run: |
    cd tacklebox && go build -o ../tacklebox ./cmd/tacklebox
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Boot stalls at `initrd-switch-root` | `95tbox-root` module missing from initramfs | Use superiso-live images or bake the module in (Option B above) |
| `tacklebox verify` fails: "same squashfs hash" | Two envs resolved to the identical container image | Use distinct image refs or check your registry tags |
| `xorriso` not found | Missing dep | `sudo apt-get install xorriso` |
| Build runs out of disk | squashfs staging fills `/` | Move output to `/mnt` with `-b /mnt/tbx`, or increase free disk |
| Runner timeout | Large images, slow pull | Pre-pull with `podman pull` before build step; or increase `timeout-minutes` |
| `systemd-bootx64.efi` not found in image | Image doesn't ship `systemd-boot` | Ensure your base image includes the `systemd-boot` package |
