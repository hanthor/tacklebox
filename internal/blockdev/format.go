package blockdev

import (
	"fmt"
	"strings"

	"github.com/tuna-os/tacklebox/internal/runner"
)

type Partition struct {
	Number int
	Label  string
	Size   string
	Type   string
	FS     string
}

func FormatDisk(device string, partitions []Partition) error {
	fmt.Printf(">>> Wiping disk: %s\n", device)
	if err := runner.Run("sgdisk", "--zap-all", device); err != nil {
		return fmt.Errorf("failed to zap disk: %w", err)
	}

	for _, p := range partitions {
		fmt.Printf(">>> Creating partition %d: %s (%s)\n", p.Number, p.Label, p.Size)
		args := []string{
			fmt.Sprintf("--new=%d:0:%s", p.Number, p.Size),
			fmt.Sprintf("--change-name=%d:%s", p.Number, p.Label),
			fmt.Sprintf("--typecode=%d:%s", p.Number, p.Type),
			device,
		}
		if err := runner.Run("sgdisk", args...); err != nil {
			return fmt.Errorf("failed to create partition %d: %w", p.Number, err)
		}
	}

	runner.Run("udevadm", "settle")
	runner.Run("partprobe", device)

	return nil
}

func CreateFilesystems(device string, partitions []Partition) error {
	for _, p := range partitions {
		partDev := fmt.Sprintf("%sp%d", device, p.Number)
		if !strings.Contains(device, "nvme") && !strings.Contains(device, "loop") {
			partDev = fmt.Sprintf("%s%d", device, p.Number)
		}

		fmt.Printf(">>> Formatting %s as %s\n", partDev, p.FS)
		var err error
		switch p.FS {
		case "vfat":
			err = runner.Run("mkfs.vfat", "-I", "-n", p.Label, partDev)
		case "btrfs":
			err = runner.Run("mkfs.btrfs", "-f", "-L", p.Label, partDev)
		case "ext4":
			err = runner.Run("mkfs.ext4", "-F", "-L", p.Label, partDev)
		default:
			return fmt.Errorf("unsupported filesystem: %s", p.FS)
		}

		if err != nil {
			return fmt.Errorf("failed to format %s: %w", partDev, err)
		}
	}
	return nil
}
