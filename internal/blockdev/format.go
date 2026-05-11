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

// PartitionPath returns the kernel device path for partition n on device.
// /dev/sda + 1 -> /dev/sda1
// /dev/nvme0n1 + 1 -> /dev/nvme0n1p1
// /dev/loop0 + 1 -> /dev/loop0p1
// /dev/mmcblk0 + 1 -> /dev/mmcblk0p1
func PartitionPath(device string, n int) string {
	base := device
	// Strip any trailing slash just in case.
	base = strings.TrimRight(base, "/")
	// Devices whose names end in a digit need a "p" separator. This covers
	// nvme0n1, loop0, mmcblk0, md0, etc., without per-prefix special-casing.
	if len(base) > 0 && base[len(base)-1] >= '0' && base[len(base)-1] <= '9' {
		return fmt.Sprintf("%sp%d", base, n)
	}
	return fmt.Sprintf("%s%d", base, n)
}

// rereadTableMsg is the (harmless) sgdisk warning we tolerate: the in-kernel
// table is in use and can't be re-read mid-script. partprobe/udevadm settle
// afterwards picks up the new layout.
const rereadTableMsg = "could not be re-read"

func sgdiskTolerant(args ...string) error {
	out, err := runner.RunCombined("sgdisk", args...)
	if err == nil {
		return nil
	}
	if strings.Contains(string(out), rereadTableMsg) {
		fmt.Printf(">>> sgdisk: kernel table reread deferred (will partprobe): %v\n", err)
		return nil
	}
	return fmt.Errorf("sgdisk %s failed: %w\noutput: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
}

func FormatDisk(device string, partitions []Partition) error {
	fmt.Printf(">>> Wiping disk: %s\n", device)
	if err := sgdiskTolerant("--zap-all", device); err != nil {
		return err
	}

	for _, p := range partitions {
		fmt.Printf(">>> Creating partition %d: %s (%s)\n", p.Number, p.Label, p.Size)
		args := []string{
			fmt.Sprintf("--new=%d:0:%s", p.Number, p.Size),
			fmt.Sprintf("--change-name=%d:%s", p.Number, p.Label),
			fmt.Sprintf("--typecode=%d:%s", p.Number, p.Type),
			device,
		}
		if err := sgdiskTolerant(args...); err != nil {
			return err
		}
	}

	// Best-effort settle. partprobe may fail on loop devices that have already
	// picked up the new table via --partscan; that's fine, udevadm catches it.
	runner.Run("udevadm", "settle")
	runner.Run("partprobe", device)
	runner.Run("udevadm", "settle")

	return nil
}

func CreateFilesystems(device string, partitions []Partition) error {
	for _, p := range partitions {
		partDev := PartitionPath(device, p.Number)

		fmt.Printf(">>> Formatting %s as %s\n", partDev, p.FS)
		var err error
		switch p.FS {
		case "vfat":
			err = runner.Run("mkfs.vfat", "-I", "-n", p.Label, partDev)
		case "btrfs":
			err = runner.Run("mkfs.btrfs", "-f", "-L", p.Label, partDev)
		case "ext4":
			// -O verity was historically set here but the persist partition
			// is read-write, so verity is inapplicable and just costs setup.
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
