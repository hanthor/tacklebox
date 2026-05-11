package install

import (
	"fmt"
	"path/filepath"
	"strings"

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

// ExtractBootFiles copies vmlinuz + initramfs out of an image into destDir and
// returns the kernel version. It uses `podman run --rm` with cp inside the
// container instead of `podman image mount`, which avoids:
//   - holding a privileged overlay mount for the duration of the extract
//   - a separate ReadDir + N stat() syscalls from the host
//   - an unmount step that can fail if the image is referenced elsewhere
//
// Per-image results are cached so a multi-env recipe sharing an image pays
// the cost once.
func ExtractBootFiles(image string, destDir string) (string, error) {
	if runner.Verbose {
		fmt.Printf(">>> Extracting boot files from %s to %s\n", image, destDir)
	}

	if err := runner.Run("sudo", "mkdir", "-p", destDir); err != nil {
		return "", err
	}

	// Single privileged podman invocation: discover kver, copy both files.
	// The shell inside the container has full visibility into /usr/lib/modules
	// without any host-side overlay mount.
	//
	// We print the kernel version on the last line of stdout so the parent
	// can recover it without parsing the cp output.
	script := `set -eu
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
	// label=disable: the ESP is vfat and can't carry SELinux xattrs, so :Z
	// would no-op and the container would still be denied. The bootc install
	// codepath disables labels for the same reason.
	out, err := runner.Output("sudo", "podman", "run", "--rm",
		"--security-opt", "label=disable",
		"-v", destDir+":/dest",
		"--entrypoint", "/bin/sh",
		image, "-c", script)
	if err != nil {
		return "", fmt.Errorf("extract boot files for %s: %w", image, err)
	}

	// Recover kver from the marker line.
	var kver string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "KVER=") {
			kver = strings.TrimPrefix(line, "KVER=")
			break
		}
	}
	if kver == "" {
		return "", fmt.Errorf("extract %s: no KVER line in output: %s", image, string(out))
	}
	return kver, nil
}

