package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tuna-os/tacklebox/internal/runner"
)

func SetupBootloader(espMount string) error {
	fmt.Printf(">>> Installing systemd-boot to %s\n", espMount)
	// We use --root so bootctl thinks the ESP is at the right place
	// but we might need to use --esp-path specifically
	err := runner.Run("sudo", "bootctl", "install", "--esp-path", espMount, "--no-variables")
	if err != nil {
		return fmt.Errorf("failed to install systemd-boot: %w", err)
	}
	return nil
}

func WriteBLSEntry(espMount string, id string, title string, kernelPath string, initrdPath string, options string) error {
	entryDir := filepath.Join(espMount, "loader", "entries")
	if err := os.MkdirAll(entryDir, 0755); err != nil {
		return err
	}

	content := fmt.Sprintf("title %s\nlinux %s\ninitrd %s\noptions %s\n",
		title, kernelPath, initrdPath, options)

	entryFile := filepath.Join(entryDir, id+".conf")
	fmt.Printf(">>> Writing BLS entry: %s\n", entryFile)
	return os.WriteFile(entryFile, []byte(content), 0644)
}
