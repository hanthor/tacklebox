//go:build integration

// Integration test for blockdev.UnmountDevice.
//
// Run with:
//
//	go test -tags=integration -run=TestUnmountDevice_LoopSmoke ./internal/blockdev/...
//
// Requires: sudo (passwordless), sgdisk, mkfs.vfat, mount.
package blockdev

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnmountDevice_LoopSmoke is an end-to-end test for the USB pre-flight
// unmount path:
//
//  1. Creates a 20 MiB sparse file and attaches it as a loop device.
//  2. Writes a single GPT partition and formats it vfat.
//  3. Mounts the partition via `sudo mount`.
//  4. Calls UnmountDevice on the loop device.
//  5. Asserts the partition mount is gone from /proc/mounts.
func TestUnmountDevice_LoopSmoke(t *testing.T) {
	// Pre-flight: check tools we need.
	for _, tool := range []string{"sudo", "sgdisk", "mkfs.vfat", "losetup", "mount"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("required tool %q not found", tool)
		}
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skip("sudo -n unavailable; skipping loop-device smoke")
	}

	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "preflight-test.img")
	mountDir := filepath.Join(tmp, "mnt")
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Create a 20 MiB sparse image.
	t.Log("creating 20 MiB image")
	if err := run("truncate", "-s", "20M", imgPath); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// 2. Attach as a loop device.
	out, err := output("sudo", "losetup", "--find", "--show", "--partscan", imgPath)
	if err != nil {
		t.Fatalf("losetup: %v", err)
	}
	loopDev := strings.TrimSpace(out)
	t.Logf("loop device: %s", loopDev)
	t.Cleanup(func() {
		exec.Command("sudo", "losetup", "-d", loopDev).Run() //nolint:errcheck
	})

	// 3. Create a single partition.
	if err := run("sudo", "sgdisk", "--new=1:0:0", "--typecode=1:0700", loopDev); err != nil {
		t.Fatalf("sgdisk: %v", err)
	}
	exec.Command("sudo", "partprobe", loopDev).Run() //nolint:errcheck
	exec.Command("udevadm", "settle").Run()          //nolint:errcheck

	partDev := PartitionPath(loopDev, 1)
	t.Logf("partition device: %s", partDev)

	// 4. Format as vfat.
	if err := run("sudo", "mkfs.vfat", "-n", "TBXTEST", partDev); err != nil {
		t.Fatalf("mkfs.vfat: %v", err)
	}

	// 5. Mount the partition.
	if err := run("sudo", "mount", partDev, mountDir); err != nil {
		t.Fatalf("mount: %v", err)
	}
	t.Cleanup(func() {
		exec.Command("sudo", "umount", "-l", mountDir).Run() //nolint:errcheck
	})

	// Confirm it's actually mounted before calling UnmountDevice.
	if !isMounted(partDev) {
		t.Fatalf("%s not visible in /proc/mounts after mount — test pre-condition failed", partDev)
	}
	t.Logf("confirmed %s is mounted at %s", partDev, mountDir)

	// 6. Call UnmountDevice — this is the function under test.
	if err := UnmountDevice(loopDev); err != nil {
		t.Fatalf("UnmountDevice(%s): %v", loopDev, err)
	}

	// 7. Partition must no longer be mounted.
	if isMounted(partDev) {
		t.Errorf("%s still mounted after UnmountDevice — pre-flight unmount did not work", partDev)
	} else {
		t.Logf("PASS: %s unmounted successfully", partDev)
	}
}

// isMounted returns true if source appears in /proc/mounts.
func isMounted(source string) bool {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 1 && fields[0] == source {
			return true
		}
	}
	return false
}

// run is a thin exec wrapper used only in integration tests.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

// output is a thin exec wrapper that returns stdout.
func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}
