package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tuna-os/tacklebox/internal/runner"
)

// InstallLive packs the rootfs of image into a single squashfs file at
// dstSquashfs, suitable for a live ISO consumed by dmsquash-live.
//
// The squashfs excludes /var/lib/containers/storage — that store is
// shipped separately on the ISO (see PLAN-merge.md §"Design: ISO output
// type"). Excluding it here keeps the per-env squashfs small enough to
// fit on a CI runner.
//
// Compression is zstd at level 3 with a 128 KiB block size — same fast
// preset SuperISO's build-live-env.sh uses for non-release builds. The
// `release` preset upstream is level 15 / 1 MiB blocks, ~3-5x slower
// for ~10% smaller output; not worth defaulting to.
func InstallLive(image, dstSquashfs string) error {
	mountSerialise.Lock()
	defer mountSerialise.Unlock()

	fmt.Printf(">>> [live] mounting %s\n", image)
	out, err := runner.Output("sudo", "podman", "image", "mount", image)
	if err != nil {
		return fmt.Errorf("podman image mount %s: %w", image, err)
	}
	mount := strings.TrimSpace(string(out))
	defer func() {
		// Best-effort unmount. podman ref-counts mounts so this is safe
		// even if another goroutine is also holding the image (won't
		// actually unmount until all refs drop).
		_ = runner.Run("sudo", "podman", "image", "unmount", image)
	}()

	if err := runner.Run("sudo", "mkdir", "-p", filepath.Dir(dstSquashfs)); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dstSquashfs), err)
	}

	// mksquashfs is often outside sudo's secure_path (e.g. linuxbrew).
	// Resolve from the user's PATH so `sudo /abs/path/mksquashfs ...`
	// works regardless of how sudoers is configured.
	mksquashfs, err := exec.LookPath("mksquashfs")
	if err != nil {
		return fmt.Errorf("mksquashfs not found in PATH: %w", err)
	}

	fmt.Printf(">>> [live] mksquashfs %s -> %s\n", mount, dstSquashfs)
	args := []string{mksquashfs, mount, dstSquashfs,
		"-noappend", "-comp", "zstd", "-Xcompression-level", "3", "-b", "131072",
		"-processors", "4",
		// Pseudo-mount points and runtime dirs that don't belong in the
		// squashfs (and would carry stale device nodes / sockets if
		// included).
		"-e", "proc", "-e", "sys", "-e", "dev", "-e", "run", "-e", "tmp",
		// /var/lib/containers/storage is the additional-image-store
		// shipped separately; it's the single biggest chunk of a live
		// container by ~6 GB on average.
		"-e", "var/lib/containers/storage",
	}
	if err := runner.Run("sudo", args...); err != nil {
		return fmt.Errorf("mksquashfs %s: %w", image, err)
	}
	return nil
}

// ExtractEFIBinary copies a systemd-boot EFI binary into destDir,
// returning the basename written ("BOOTX64.EFI" / "BOOTAA64.EFI").
//
// We try the live image first (some bootc images ship sd-boot under
// /usr/lib/systemd/boot/efi/, e.g. dakota), then fall back to the host
// — the host binary is what `bootctl install` uses, so it's
// guaranteed-present on any system that can run BlockTarget too.
//
// The image arg is currently only used for diagnostics on fallback
// failure; once we have a way to detect "image has sd-boot" cheaply
// (without paying a podman run on each build) we can prefer the image.
func ExtractEFIBinary(image, destDir string) (string, error) {
	if err := runner.Run("sudo", "mkdir", "-p", destDir); err != nil {
		return "", err
	}

	// Pick the right binary name for the host arch. We don't yet
	// cross-build, so the host arch IS the target arch.
	hostBins := []struct{ src, dst string }{
		{"/usr/lib/systemd/boot/efi/systemd-bootx64.efi", "BOOTX64.EFI"},
		{"/usr/lib/systemd/boot/efi/systemd-bootaa64.efi", "BOOTAA64.EFI"},
	}
	for _, b := range hostBins {
		if info, statErr := os.Stat(b.src); statErr == nil && !info.IsDir() {
			if err := runner.Run("sudo", "cp", b.src, filepath.Join(destDir, b.dst)); err != nil {
				return "", fmt.Errorf("copy host EFI binary %s: %w", b.src, err)
			}
			return b.dst, nil
		}
	}
	return "", fmt.Errorf("no systemd-boot EFI binary on host (and image %s wasn't probed); install systemd-boot-efi or systemd-boot-unsigned", image)
}

// mountSerialise prevents two goroutines from racing podman image mount
// for the same image (podman handles it but logs warnings; serialising
// keeps build output clean).
var mountSerialise sync.Mutex
