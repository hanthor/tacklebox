package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tuna-os/tacklebox/internal/blockdev"
	"github.com/tuna-os/tacklebox/internal/install"
	"github.com/tuna-os/tacklebox/internal/recipe"
	"github.com/tuna-os/tacklebox/internal/runner"
)

var buildCmd = &cobra.Command{
	Use:   "build [recipe.json] [target]",
	Short: "Build a multi-boot image from a recipe",
	Long:  "Build a multi-boot image or provision a real disk (e.g. /dev/sda) from a recipe.",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		recipePath := args[0]
		data, err := os.ReadFile(recipePath)
		if err != nil {
			return fmt.Errorf("failed to read recipe: %w", err)
		}

		var r recipe.MediaRecipe
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("failed to parse recipe: %w", err)
		}

		outputBase, _ := cmd.Flags().GetString("output-base")
		if err := os.MkdirAll(outputBase, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}

		var targetDevice string
		var isBlockDevice bool

		if len(args) == 2 {
			targetDevice = args[1]
			if strings.HasPrefix(targetDevice, "/dev/") {
				isBlockDevice = true
				fmt.Printf(">>> Target is a block device: %s\n", targetDevice)
			}
		}

		fmt.Printf(">>> Building media: %s (%s)\n", r.MediaName, r.Size)
		fmt.Printf(">>> Output directory: %s\n", outputBase)

		var imagePath string
		var loopDev string

		if !isBlockDevice {
			// 1. Create raw image file if no target device provided
			imagePath = filepath.Join(outputBase, "tacklebox.img")
			fmt.Printf(">>> Creating raw image: %s\n", imagePath)
			if err := runner.Run("truncate", "-s", r.Size, imagePath); err != nil {
				return fmt.Errorf("failed to create sparse file: %w", err)
			}

			// 2. Setup loop device
			loopDevBytes, err := runner.Output("sudo", "losetup", "--find", "--show", "--partscan", imagePath)
			if err != nil {
				return fmt.Errorf("failed to setup loop device: %w", err)
			}
			loopDev = strings.TrimSpace(string(loopDevBytes))
			targetDevice = loopDev
			defer func() {
				fmt.Printf(">>> Detaching loop device: %s\n", loopDev)
				runner.Run("sudo", "losetup", "-d", loopDev)
			}()
		}

		// 3. Partition disk
		partitions := []blockdev.Partition{
			{Number: 1, Label: "TBOX_ESP", Size: "1G", Type: "ef00", FS: "vfat"},
			{Number: 2, Label: "TBOX_STORE", Size: "20G", Type: "8300", FS: r.SharedStore.Format},
			{Number: 3, Label: "TBOX_PERSIST", Size: "0", Type: "8300", FS: "ext4"},
		}
		if err := blockdev.FormatDisk(targetDevice, partitions); err != nil {
			return err
		}

		// 4. Create filesystems
		if err := blockdev.CreateFilesystems(targetDevice, partitions); err != nil {
			return err
		}

		// 5. Mount partitions
		espMount := filepath.Join(outputBase, "mount-esp")
		storeMount := filepath.Join(outputBase, "mount-store")
		os.MkdirAll(espMount, 0755)
		os.MkdirAll(storeMount, 0755)

		// Handle partition naming (/dev/sda1 vs /dev/nvme0n1p1 vs /dev/loop0p1)
		espDev := fmt.Sprintf("%sp1", targetDevice)
		storeDev := fmt.Sprintf("%sp2", targetDevice)
		if !strings.Contains(targetDevice, "nvme") && !strings.Contains(targetDevice, "loop") {
			espDev = fmt.Sprintf("%s1", targetDevice)
			storeDev = fmt.Sprintf("%s2", targetDevice)
		}

		fmt.Printf(">>> Mounting ESP to %s\n", espMount)
		if err := runner.Run("sudo", "mount", espDev, espMount); err != nil {
			return err
		}
		defer runner.Run("sudo", "umount", espMount)

		fmt.Printf(">>> Mounting STORE to %s\n", storeMount)
		if err := runner.Run("sudo", "mount", storeDev, storeMount); err != nil {
			return err
		}
		defer runner.Run("sudo", "umount", storeMount)

		// 6. Setup systemd-boot
		if err := install.SetupBootloader(espMount); err != nil {
			return err
		}

		// 7. Install Payloads and Configure Boot Entries
		for _, env := range r.BootableEnvironments {
			backend := install.Backend(env.Backend)
			if backend == "" {
				var err error
				backend, err = install.DetectBackend(env.Image)
				if err != nil {
					return err
				}
			}

			// Pull and install to shared store with unique stateroot
			envRoot := filepath.Join(storeMount, "tbox-install", env.ID)
			runner.Run("sudo", "rm", "-rf", envRoot)
			if err := runner.Run("sudo", "mkdir", "-p", envRoot); err != nil {
				return fmt.Errorf("failed to create env root: %w", err)
			}
			fmt.Printf(">>> [DEBUG] Starting installation for payload: %s\n", env.ID)
			if err := install.PullAndInstall(env.Image, envRoot, env.ID, backend); err != nil {
				fmt.Printf(">>> [ERROR] Installation failed for %s: %v\n", env.ID, err)
				return err
			}
			fmt.Printf(">>> [DEBUG] Installation complete for %s. Extracting boot files...\n", env.ID)

			// Extract boot files to ESP
			bootDir := filepath.Join(espMount, "EFI", env.ID)
			fmt.Printf(">>> Creating boot directory: %s\n", bootDir)
			if err := runner.Run("sudo", "mkdir", "-p", bootDir); err != nil {
				return fmt.Errorf("failed to create boot dir: %w", err)
			}
			kver, err := install.ExtractBootFiles(env.Image, bootDir)
			if err != nil {
				fmt.Printf(">>> [ERROR] Boot file extraction failed for %s: %v\n", env.ID, err)
				return err
			}
			fmt.Printf(">>> [DEBUG] Boot files extracted for %s (kernel=%s)\n", env.ID, kver)

			// Generate BLS Entries
			kernelRelPath := filepath.Join("/EFI", env.ID, "vmlinuz")
			initrdRelPath := filepath.Join("/EFI", env.ID, "initrd.img")

			for _, mode := range env.Modes {
				title := fmt.Sprintf("%s (%s)", env.ID, mode)
				id := fmt.Sprintf("%s-%s", env.ID, mode)
				
				options := fmt.Sprintf("root=LABEL=TBOX_STORE rw console=ttyS0 tacklebox.root=tbox-install/%s", env.ID)
				if mode == recipe.ModeLive {
					options += " rd.live.overlay=tmpfs"
				} else {
					options += " tacklebox.persist=LABEL=TBOX_PERSIST"
				}

				if backend == install.BackendOstree {
					options += fmt.Sprintf(" ostree=/ostree/boot.1/%s/current/0", env.ID)
				} else {
					options += " rootflags=subvol=containers/storage/overlay/default/diff"
				}

				if err := install.WriteBLSEntry(espMount, id, title, kernelRelPath, initrdRelPath, options); err != nil {
					return err
				}
			}
			fmt.Printf(">>> Finished environment: %s (kernel=%s)\n", env.ID, kver)
		}


		if !isBlockDevice {
			fmt.Printf(">>> Tacklebox build complete: %s\n", imagePath)

			xz, _ := cmd.Flags().GetBool("xz")
			if xz {
				fmt.Printf(">>> Compressing image to %s.xz...\n", imagePath)
				if err := runner.Run("xz", "-T0", "-k", imagePath); err != nil {
					return fmt.Errorf("failed to compress image: %w", err)
				}
				fmt.Printf(">>> Compression complete: %s.xz\n", imagePath)
			}
		} else {
			fmt.Printf(">>> Tacklebox provisioning complete: %s\n", targetDevice)
		}
		return nil
	},
}

func init() {
	buildCmd.Flags().Bool("xz", false, "Compress the final image with xz")
	rootCmd.AddCommand(buildCmd)
}
