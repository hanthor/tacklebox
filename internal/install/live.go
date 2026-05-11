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
// Uses `podman unshare` instead of `sudo podman` so that:
//   - Images in the invoking user's store (e.g. localhost/ images built with
//     plain `podman build`) are accessible even when tacklebox is run via sudo.
//   - UID mappings for overlay layer dirs are correct inside the squashfs
//     (the same reason SuperISO's build-store-sqfs.sh uses podman unshare).
//
// Mount and squashfs happen inside one `podman unshare -- sh -c '...'` session
// because the path returned by `podman image mount` only exists within that
// user-namespace context.
//
// The squashfs is written to a user-writable temp file first, then
// sudo-moved into dstSquashfs (which may be in a root-owned staging tree).
func InstallLive(image, dstSquashfs string) error {
	mountSerialise.Lock()
	defer mountSerialise.Unlock()

	mksquashfsPath, err := exec.LookPath("mksquashfs")
	if err != nil {
		return fmt.Errorf("mksquashfs not found in PATH: %w", err)
	}

	level, block := "3", "131072"
	if os.Getenv("SUPERISO_COMPRESSION") == "release" {
		level, block = "15", "1048576"
	}

	// Write to a user-writable temp file; sudo-move to final dest afterwards.
	tmpF, err := os.CreateTemp("", "tbox-live-*.squashfs")
	if err != nil {
		return fmt.Errorf("create temp squashfs: %w", err)
	}
	tmpF.Close()
	tmpPath := tmpF.Name()
	defer os.Remove(tmpPath)

	// Single podman unshare session: mount → squashfs → unmount.
	// shellEscape is applied to every variable interpolated into the script.
	script := fmt.Sprintf(`set -eu
MOUNT=$(podman image mount %s)
trap 'podman image unmount %s' EXIT
%s "$MOUNT" %s \
  -noappend -comp zstd -Xcompression-level %s -b %s \
  -processors 4 \
  -e proc -e sys -e dev -e run -e tmp \
  -e var/lib/containers/storage`,
		shellEsc(image), shellEsc(image),
		mksquashfsPath, shellEsc(tmpPath),
		level, block)

	fmt.Printf(">>> [live] squashing %s -> %s (podman unshare)\n", image, dstSquashfs)
	if err := runner.Run("podman", "unshare", "--", "sh", "-c", script); err != nil {
		return fmt.Errorf("squashfs %s: %w", image, err)
	}

	if err := runner.Run("sudo", "mkdir", "-p", filepath.Dir(dstSquashfs)); err != nil {
		return err
	}
	if err := runner.Run("sudo", "mv", tmpPath, dstSquashfs); err != nil {
		return fmt.Errorf("move squashfs to %s: %w", dstSquashfs, err)
	}
	return nil
}

// ExtractEFIBinary copies a systemd-boot EFI binary into destDir,
// returning the basename written ("BOOTX64.EFI" / "BOOTAA64.EFI").
func ExtractEFIBinary(image, destDir string) (string, error) {
	if err := runner.Run("sudo", "mkdir", "-p", destDir); err != nil {
		return "", err
	}
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

// shellEsc single-quotes a string for safe interpolation into a shell script.
// Embedded single quotes are escaped with the '"'"' idiom.
func shellEsc(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// extractScript runs inside a container and copies vmlinuz + initramfs.img
// out of /usr/lib/modules/<kver>/ to /dest/.
const extractScript = `set -eu
kver=""
for d in /usr/lib/modules/*/; do
  if [ -f "$d/modules.dep" ]; then
    kver=$(basename "$d")
    break
  fi
done
if [ -z "$kver" ]; then
  echo "no kernel found under /usr/lib/modules" >&2
  exit 1
fi
cp "/usr/lib/modules/$kver/vmlinuz" /dest/vmlinuz
cp "/usr/lib/modules/$kver/initramfs.img" /dest/initrd.img
printf 'KVER=%s\n' "$kver"
`

// extractCacheMu + extractCache: per-image staging cache so a multi-env
// recipe sharing an image only pays the podman run cost once.
type stagedFiles struct {
	dir  string
	kver string
}

var (
	extractCacheMu sync.Mutex
	extractCache   = map[string]stagedFiles{}
)

var stagingRoot = "/tmp"

func SetStagingRoot(p string) { stagingRoot = p }

// fetchToStaging extracts vmlinuz + initramfs from image into a host
// staging directory using rootless podman (no sudo).
//
// Running without sudo means the user's own container store is used, so
// localhost/ images built with plain `podman build` are found without
// needing to be transferred to root's store. The extracted files are
// written to a user-writable temp dir and then sudo-moved into the final
// staging path (which may be root-owned).
func fetchToStaging(image string) (stagedFiles, error) {
	extractCacheMu.Lock()
	if s, ok := extractCache[image]; ok {
		extractCacheMu.Unlock()
		return s, nil
	}
	extractCacheMu.Unlock()

	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '_' || r == '-':
			return r
		}
		return '_'
	}, image)
	finalDir := filepath.Join(stagingRoot, "tbox-extract", safe)

	// Use a user-writable temp dir for the podman run so rootless podman
	// can write the output files without needing elevated permissions.
	tmpDir, err := os.MkdirTemp("", "tbox-extract-*")
	if err != nil {
		return stagedFiles{}, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if runner.Verbose {
		fmt.Printf(">>> Extracting boot files from %s\n", image)
	}

	// Rootless podman run — accesses the user's store directly.
	out, err := runner.Output("podman", "run", "--rm",
		"--security-opt", "label=disable",
		"-v", tmpDir+":/dest",
		"--entrypoint", "/bin/sh",
		image, "-c", extractScript)
	if err != nil {
		return stagedFiles{}, fmt.Errorf("extract boot files for %s: %w", image, err)
	}

	var kver string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "KVER=") {
			kver = strings.TrimPrefix(line, "KVER=")
			break
		}
	}
	if kver == "" {
		return stagedFiles{}, fmt.Errorf("extract %s: no KVER line in output: %s", image, string(out))
	}

	// sudo-move extracted files into the (possibly root-owned) final staging dir.
	if err := runner.Run("sudo", "mkdir", "-p", finalDir); err != nil {
		return stagedFiles{}, err
	}
	for _, f := range []string{"vmlinuz", "initrd.img"} {
		src := filepath.Join(tmpDir, f)
		dst := filepath.Join(finalDir, f)
		if err := runner.Run("sudo", "mv", src, dst); err != nil {
			return stagedFiles{}, fmt.Errorf("mv %s -> %s: %w", src, dst, err)
		}
	}

	s := stagedFiles{dir: finalDir, kver: kver}
	extractCacheMu.Lock()
	extractCache[image] = s
	extractCacheMu.Unlock()
	return s, nil
}

func CleanupStaging() {
	extractCacheMu.Lock()
	defer extractCacheMu.Unlock()
	for _, s := range extractCache {
		_ = runner.Run("sudo", "rm", "-rf", s.dir)
	}
	root := filepath.Join(stagingRoot, "tbox-extract")
	if fi, err := os.Stat(root); err == nil && fi.IsDir() {
		_ = runner.Run("sudo", "rmdir", root)
	}
	extractCache = map[string]stagedFiles{}
}

// ExtractBootFiles copies vmlinuz + initramfs from the per-image staging
// cache into destDir.
func ExtractBootFiles(image string, destDir string) (string, error) {
	if err := runner.Run("sudo", "mkdir", "-p", destDir); err != nil {
		return "", err
	}
	s, err := fetchToStaging(image)
	if err != nil {
		return "", err
	}
	if err := runner.Run("sudo", "cp", filepath.Join(s.dir, "vmlinuz"), filepath.Join(destDir, "vmlinuz")); err != nil {
		return "", fmt.Errorf("copy vmlinuz from staging: %w", err)
	}
	if err := runner.Run("sudo", "cp", filepath.Join(s.dir, "initrd.img"), filepath.Join(destDir, "initrd.img")); err != nil {
		return "", fmt.Errorf("copy initrd from staging: %w", err)
	}
	return s.kver, nil
}

// mountSerialise prevents concurrent podman image mount calls for the same
// image from racing (podman handles it but logs warnings).
var mountSerialise sync.Mutex
