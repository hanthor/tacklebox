package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuna-os/tacklebox/internal/runner"
)

func SetupBootloader(espMount string) error {
	fmt.Printf(">>> Installing systemd-boot to %s\n", espMount)
	if err := runner.Run("sudo", "bootctl", "install", "--esp-path", espMount, "--no-variables"); err != nil {
		return fmt.Errorf("failed to install systemd-boot: %w", err)
	}

	// Write a default loader.conf so multi-entry menus are usable on serial
	// consoles and headless boots. Without this systemd-boot waits forever
	// on the menu when there are >1 entries, which makes both qemu testing
	// and unattended USB boots unpleasant.
	//
	// timeout=5 gives a real keyboard user a moment to choose; `default *`
	// auto-selects the highest-sort-key entry (matches user expectation
	// across distros).
	loaderConf := "timeout 5\ndefault *\nconsole-mode max\neditor no\n"
	loaderConfPath := filepath.Join(espMount, "loader", "loader.conf")
	if err := runner.Run("sudo", "mkdir", "-p", filepath.Dir(loaderConfPath)); err != nil {
		return err
	}
	// Write via `tee` so sudo owns the write — host user may not be able to
	// write directly to a root-mounted vfat.
	if err := runner.RunWithStdin(strings.NewReader(loaderConf), "sudo", "tee", loaderConfPath); err != nil {
		return fmt.Errorf("write loader.conf: %w", err)
	}
	return nil
}

func WriteBLSEntry(espMount string, id string, title string, kernelPath string, initrdPath string, options string) error {
	entryDir := filepath.Join(espMount, "loader", "entries")
	if err := os.MkdirAll(entryDir, 0755); err != nil {
		return err
	}

	// sort-key prefix `0-tbox-` ensures our entries outrank bootc's own
	// auto-generated entries (which use `bootc-*` sort keys) so the default
	// boot is one of the ones we actually configured.
	content := fmt.Sprintf("title %s\nsort-key 0-tbox-%s\nlinux %s\ninitrd %s\noptions %s\n",
		title, id, kernelPath, initrdPath, options)

	entryFile := filepath.Join(entryDir, id+".conf")
	fmt.Printf(">>> Writing BLS entry: %s\n", entryFile)
	return os.WriteFile(entryFile, []byte(content), 0644)
}
