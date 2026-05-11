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
	fmt.Printf(">>> [v3] Pulling and installing image: %s (stateroot=%s, backend=%s)\n", image, stateroot, backend)

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
		return fmt.Errorf("bootc install failed: %w", err)
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
			return "", fmt.Errorf("failed to inspect image: %w", err)
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

func ExtractBootFiles(image string, destDir string) (string, error) {
	fmt.Printf(">>> [DEBUG] Extracting boot files from %s to %s\n", image, destDir)

	mountBytes, err := runner.Output("sudo", "podman", "image", "mount", image)
	if err != nil {
		return "", fmt.Errorf("failed to mount image: %w", err)
	}
	mountPath := strings.TrimSpace(string(mountBytes))
	fmt.Printf(">>> [DEBUG] Image mounted at: %s\n", mountPath)
	defer func() {
		fmt.Printf(">>> [DEBUG] Unmounting image: %s\n", image)
		runner.Run("sudo", "podman", "image", "unmount", image)
	}()

	// Find kernel version
	modulesDir := filepath.Join(mountPath, "usr/lib/modules")
	fmt.Printf(">>> [DEBUG] Searching for kernels in: %s\n", modulesDir)
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return "", fmt.Errorf("failed to read modules dir: %w", err)
	}

	var kver string
	for _, entry := range entries {
		if entry.IsDir() {
			fmt.Printf(">>> [DEBUG] Found directory: %s. Checking for modules.dep...\n", entry.Name())
			// Look for modules.dep to confirm it's a real kernel dir
			if _, err := os.Stat(filepath.Join(modulesDir, entry.Name(), "modules.dep")); err == nil {
				kver = entry.Name()
				fmt.Printf(">>> [DEBUG] Selected kernel version: %s\n", kver)
				break
			}
		}
	}

	if kver == "" {
		return "", fmt.Errorf("could not find kernel version in image")
	}

	if err := runner.Run("sudo", "mkdir", "-p", destDir); err != nil {
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
