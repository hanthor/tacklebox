package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tuna-os/tacklebox/internal/runner"
)

type Backend string

const (
	BackendOstree    Backend = "ostree"
	BackendComposefs Backend = "composefs"
)

// Pull warms the local containers-storage so the install step doesn't pay
// per-image network/transport time. Safe to invoke concurrently for distinct
// images: podman pull takes a per-image lock but parallel pulls of different
// references serialize internally and still overlap network I/O.
//
// Locally-built images (e.g. `localhost/foo`) are skipped if already present,
// because `podman pull localhost/foo` would try to contact a registry literally
// named "localhost" and fail.
func Pull(image string) error {
	if err := runner.Run("podman", "image", "exists", image); err == nil {
		fmt.Printf(">>> Skip pull (already present): %s\n", image)
		return nil
	}
	fmt.Printf(">>> Pulling %s\n", image)
	if err := runner.Run("podman", "pull", image); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

func PullAndInstall(image string, targetDir string, stateroot string, backend Backend) error {
	fmt.Printf(">>> Installing image: %s (stateroot=%s, backend=%s)\n", image, stateroot, backend)

	podmanArgs := []string{
		"run", "--rm", "--privileged",
		"--pid=host",
		"-v", "/dev:/dev",
		"-v", targetDir + ":/target",
		"--mount", "type=bind,src=/var/lib/containers,dst=/var/lib/containers",
		"--security-opt", "label=disable",
	}

	if backend == BackendComposefs {
		// Create a dummy ESP for bootc to write to (must be OUTSIDE of targetDir)
		dummyEsp := filepath.Join(filepath.Dir(targetDir), "dummy-esp-"+stateroot)
		runner.Run("sudo", "rm", "-rf", dummyEsp)
		if err := runner.Run("sudo", "mkdir", "-p", dummyEsp); err != nil {
			return err
		}
		// Bind mount the dummy ESP to /boot/efi in the target root
		podmanArgs = append(podmanArgs, "-v", dummyEsp+":/target/boot/efi")
	}

	podmanArgs = append(podmanArgs, image)
	podmanArgs = append(podmanArgs, "bootc", "install", "to-filesystem", "--skip-finalize")

	if backend == BackendOstree {
		podmanArgs = append(podmanArgs, "--bootloader", "none")
		podmanArgs = append(podmanArgs, "--stateroot", stateroot)
	} else if backend == BackendComposefs {
		// Composefs requires a bootloader to be set to generate the digest
		podmanArgs = append(podmanArgs, "--bootloader", "systemd")
		podmanArgs = append(podmanArgs, "--composefs-backend")
		podmanArgs = append(podmanArgs, "--allow-missing-verity")
	}

	podmanArgs = append(podmanArgs, "/target")

	if err := runner.Run("podman", podmanArgs...); err != nil {
		return fmt.Errorf("bootc install %s -> %s: %w", image, stateroot, err)
	}

	fmt.Printf(">>> Successfully installed %s to %s\n", image, targetDir)
	return nil
}

func DetectBackend(image string) (Backend, error) {
	// If image is local, check containers-storage first
	if strings.HasPrefix(image, "localhost/") || !strings.Contains(image, "/") {
		out, err := runner.Output("skopeo", "inspect", "containers-storage:"+image)
		if err == nil {
			return parseBackend(string(out)), nil
		}
	}

	out, err := runner.Output("skopeo", "inspect", "docker://"+image)
	if err != nil {
		// Fallback to local check
		out, err = runner.Output("skopeo", "inspect", "containers-storage:"+image)
		if err != nil {
			return "", fmt.Errorf("failed to inspect image %s: %w", image, err)
		}
	}

	return parseBackend(string(out)), nil
}

func parseBackend(inspectJson string) Backend {
	if strings.Contains(inspectJson, "ostree") {
		return BackendOstree
	}
	return BackendComposefs
}

// extractStaging holds the host-side cache of (vmlinuz, initrd) artifacts
// keyed by image reference. Recipes that reference the same image from
// multiple bootable_environments only pay the podman run cost once.
type stagedFiles struct {
	dir  string // host-side staging directory holding vmlinuz + initrd.img
	kver string
}

var (
	extractCacheMu sync.Mutex
	extractCache   = map[string]stagedFiles{}
)

// stagingRoot is overridable by callers (the build command sets this from
// --output-base) so the staging dir lives next to the rest of the build
// artifacts instead of in /tmp.
var stagingRoot = "/tmp"

// SetStagingRoot redirects the per-image extract staging area. Must be called
// before the first ExtractBootFiles in a build.
func SetStagingRoot(p string) { stagingRoot = p }

// ExtractBootFiles copies vmlinuz + initramfs out of an image into destDir and
// returns the kernel version. It uses `podman run --rm` with cp inside the
// container instead of `podman image mount`, which avoids:
//   - holding a privileged overlay mount for the duration of the extract
//   - a separate ReadDir + N stat() syscalls from the host
//   - an unmount step that can fail if the image is referenced elsewhere
//
// Per-image results are cached so a multi-env recipe sharing an image pays
// the cost once.
// extractScript runs inside the container and is the same regardless of
// whether the dest is a staging dir or the ESP itself.
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

// fetchToStaging populates the cache entry for image: runs podman once,
// writes both files to a host-side staging dir under stagingRoot, returns
// the cached struct. Concurrent callers for the same image serialize on
// extractCacheMu and only one of them does the actual podman run.
func fetchToStaging(image string) (stagedFiles, error) {
	extractCacheMu.Lock()
	if s, ok := extractCache[image]; ok {
		extractCacheMu.Unlock()
		return s, nil
	}
	extractCacheMu.Unlock()

	// Sanitise image ref for use as a directory name. Replace anything that's
	// not [A-Za-z0-9._-] with '_'. Good enough for collision-free naming in
	// practice (image refs are themselves unique).
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '_' || r == '-':
			return r
		}
		return '_'
	}, image)
	dir := filepath.Join(stagingRoot, "tbox-extract", safe)

	if err := runner.Run("sudo", "mkdir", "-p", dir); err != nil {
		return stagedFiles{}, err
	}
	// Make the staging dir writable so the container (running as root via
	// sudo podman, but with label=disable) can write into it on hosts where
	// the bind-mount target sits on a filesystem that does carry SELinux.
	if err := runner.Run("sudo", "chmod", "0777", dir); err != nil {
		return stagedFiles{}, err
	}

	if runner.Verbose {
		fmt.Printf(">>> Extracting boot files from %s into staging %s\n", image, dir)
	}
	out, err := runner.Output("sudo", "podman", "run", "--rm",
		"--security-opt", "label=disable",
		"-v", dir+":/dest",
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

	s := stagedFiles{dir: dir, kver: kver}
	extractCacheMu.Lock()
	extractCache[image] = s
	extractCacheMu.Unlock()
	return s, nil
}

// CleanupStaging removes all host-side extract staging directories. Best-
// effort: errors are logged but not propagated.
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
// cache into destDir. The first call for a given image runs podman once;
// subsequent calls are just `cp` operations from the staging dir.
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

