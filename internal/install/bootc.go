package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuna-os/tacklebox/internal/runner"
)

type Backend string

const (
	BackendOstree    Backend = "ostree"
	BackendComposefs Backend = "composefs"
)

func PullAndInstall(image string, targetDir string, stateroot string, backend Backend) error {
	fmt.Printf(">>> Pulling and installing image: %s (stateroot=%s, backend=%s)\n", image, stateroot, backend)

	podmanArgs := []string{
		"run", "--rm", "--privileged",
		"--pid=host",
		"-v", "/dev:/dev",
		"-v", targetDir + ":/target",
		"--mount", "type=bind,src=/var/lib/containers,dst=/var/lib/containers",
		"--security-opt", "label=disable",
		image,
		"bootc", "install", "to-filesystem",
		"--skip-finalize",
		"--bootloader", "none",
		"/target",
	}

	if err := runner.Run("podman", podmanArgs...); err != nil {
		return fmt.Errorf("bootc install failed: %w", err)
	}

	fmt.Printf(">>> Successfully installed %s to %s\n", image, targetDir)
	return nil
}

func DetectBackend(image string) (Backend, error) {
	out, err := runner.Output("skopeo", "inspect", "docker://"+image)
	if err != nil {
		// Fallback to local check
		out, err = runner.Output("skopeo", "inspect", "containers-storage:"+image)
		if err != nil {
			return "", fmt.Errorf("failed to inspect image: %w", err)
		}
	}

	s := string(out)
	if strings.Contains(s, "ostree") {
		return BackendOstree, nil
	}
	return BackendComposefs, nil
}

func ExtractBootFiles(image string, destDir string) (string, error) {
	fmt.Printf(">>> Extracting boot files from %s to %s\n", image, destDir)
	
	mountBytes, err := runner.Output("sudo", "podman", "image", "mount", image)
	if err != nil {
		return "", fmt.Errorf("failed to mount image: %w", err)
	}
	mountPath := strings.TrimSpace(string(mountBytes))
	defer runner.Run("sudo", "podman", "image", "unmount", image)

	// Find kernel version
	modulesDir := filepath.Join(mountPath, "usr/lib/modules")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return "", fmt.Errorf("failed to read modules dir: %w", err)
	}

	var kver string
	for _, entry := range entries {
		if entry.IsDir() {
			// Look for modules.dep to confirm it's a real kernel dir
			if _, err := os.Stat(filepath.Join(modulesDir, entry.Name(), "modules.dep")); err == nil {
				kver = entry.Name()
				break
			}
		}
	}

	if kver == "" {
		return "", fmt.Errorf("could not find kernel version in image")
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	vmlinuzSrc := filepath.Join(modulesDir, kver, "vmlinuz")
	initrdSrc := filepath.Join(modulesDir, kver, "initramfs.img")

	if err := runner.Run("sudo", "cp", vmlinuzSrc, filepath.Join(destDir, "vmlinuz")); err != nil {
		return "", err
	}
	if err := runner.Run("sudo", "cp", initrdSrc, filepath.Join(destDir, "initrd.img")); err != nil {
		return "", err
	}

	return kver, nil
}
