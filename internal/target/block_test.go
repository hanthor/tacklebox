package target

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/blockdev"
	"github.com/tuna-os/tacklebox/internal/runner"
)

// needsSgdisk skips the test if sgdisk is not in PATH.
func needsSgdisk(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sgdisk"); err != nil {
		t.Skip("sgdisk not available in test environment")
	}
}

func TestNewBlockTarget_LoopImage(t *testing.T) {
	parts := []blockdev.Partition{
		{Size: "+1G", FS: "vfat", Label: "ESP"},
		{Size: "+27G", FS: "ext4", Label: "TBOX_STORE"},
		{Size: "0", FS: "ext4", Label: "TBOX_PERSIST"},
	}
	bt := NewBlockTarget("/tmp/output", "", "30G", parts)

	if bt.DeviceArg != "" {
		t.Errorf("DeviceArg = %q, want empty for loop image", bt.DeviceArg)
	}
	if bt.OutputBase != "/tmp/output" {
		t.Errorf("OutputBase = %q, want /tmp/output", bt.OutputBase)
	}
	if bt.SizeSpec != "30G" {
		t.Errorf("SizeSpec = %q, want 30G", bt.SizeSpec)
	}
	if len(bt.Partitions) != 3 {
		t.Fatalf("Partitions len = %d, want 3", len(bt.Partitions))
	}
	if bt.ImagePath != "" {
		t.Errorf("ImagePath should be empty before Prepare: %q", bt.ImagePath)
	}
}

func TestNewBlockTarget_RealDevice(t *testing.T) {
	bt := NewBlockTarget("/tmp/output", "/dev/sdb", "", nil)

	if bt.DeviceArg != "/dev/sdb" {
		t.Errorf("DeviceArg = %q, want /dev/sdb", bt.DeviceArg)
	}
	if bt.SizeSpec != "" {
		t.Errorf("SizeSpec = %q, want empty for real device", bt.SizeSpec)
	}
}

func TestBlockTarget_InstallMode(t *testing.T) {
	bt := NewBlockTarget("/tmp", "", "30G", nil)
	if bt.InstallMode() != InstallModeBootc {
		t.Errorf("InstallMode = %v, want InstallModeBootc", bt.InstallMode())
	}
}

func TestBlockTarget_KernelPath(t *testing.T) {
	bt := NewBlockTarget("/tmp", "", "30G", nil)
	got := bt.KernelPath("bluefin")
	want := "/EFI/bluefin/vmlinuz"
	if got != want {
		t.Errorf("KernelPath = %q, want %q", got, want)
	}
}

func TestBlockTarget_InitrdPath(t *testing.T) {
	bt := NewBlockTarget("/tmp", "", "30G", nil)
	got := bt.InitrdPath("aurora")
	want := "/EFI/aurora/initrd.img"
	if got != want {
		t.Errorf("InitrdPath = %q, want %q", got, want)
	}
}

func TestBlockTarget_Cleanup_Idempotent(t *testing.T) {
	bt := NewBlockTarget("/tmp", "", "30G", nil)
	calls := 0
	bt.addCleanup(func() { calls++ })

	bt.Cleanup()
	if calls != 1 {
		t.Errorf("first Cleanup: want 1 call, got %d", calls)
	}

	// Second call must be a no-op.
	bt.Cleanup()
	if calls != 1 {
		t.Errorf("second Cleanup should be no-op, got %d calls", calls)
	}
}

func TestBlockTarget_Cleanup_LIFO(t *testing.T) {
	bt := NewBlockTarget("/tmp", "", "30G", nil)
	var order []int
	bt.addCleanup(func() { order = append(order, 1) })
	bt.addCleanup(func() { order = append(order, 2) })
	bt.addCleanup(func() { order = append(order, 3) })

	bt.Cleanup()
	if len(order) != 3 {
		t.Fatalf("want 3 calls, got %d: %v", len(order), order)
	}
	// LIFO: last added runs first
	if order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Errorf("not LIFO: %v", order)
	}
}

func TestBlockTarget_Finalize_LoopImage(t *testing.T) {
	bt := NewBlockTarget("/tmp/output", "", "30G", nil)
	bt.ImagePath = "/tmp/output/tacklebox.img"

	// Finalize calls Cleanup internally, so we must have at least
	// one cleanup registered to avoid nil deref. (It's ok to have
	// an empty func.)
	bt.addCleanup(func() {})

	savedRunFn := runner.RunFn
	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }
	defer func() { runner.RunFn = savedRunFn }()

	artifact, err := bt.Finalize(nil)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if artifact != bt.ImagePath {
		t.Errorf("artifact = %q, want %q", artifact, bt.ImagePath)
	}
	// Cleanup must be disarmed after Finalize.
	if !bt.disarmed {
		t.Error("Cleanup not disarmed after Finalize")
	}
}

func TestBlockTarget_Finalize_RealDevice(t *testing.T) {
	bt := NewBlockTarget("/tmp/output", "/dev/sdb", "", nil)
	bt.addCleanup(func() {})

	savedRunFn := runner.RunFn
	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }
	defer func() { runner.RunFn = savedRunFn }()

	artifact, err := bt.Finalize(nil)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if artifact != "/dev/sdb" {
		t.Errorf("artifact = %q, want /dev/sdb", artifact)
	}
}

func TestBlockTarget_Prepare_LoopImage(t *testing.T) {
	needsSgdisk(t)
	tmp := t.TempDir()
	parts := []blockdev.Partition{
		{Size: "+1G", FS: "vfat", Label: "ESP"},
		{Size: "+27G", FS: "ext4", Label: "TBOX_STORE"},
		{Size: "0", FS: "ext4", Label: "TBOX_PERSIST"},
	}
	bt := NewBlockTarget(tmp, "", "2G", parts)

	noopTrack := func(name string, fn func() error) error { return fn() }

	oldRunFn := runner.RunFn
	oldOutput := runner.Output
	defer func() {
		runner.RunFn = oldRunFn
		runner.Output = oldOutput
	}()

	// Simulate successful truncate
	runner.Output = func(name string, args ...string) ([]byte, error) {
		// losetup: return a fake loop device path
		if name == "sudo" && len(args) >= 2 && args[1] == "losetup" {
			return []byte("/dev/loop99\n"), nil
		}
		return nil, nil
	}

	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		if name == "truncate" {
			// Create the sparse file
			f, err := os.Create(bt.ImagePath)
			if err != nil {
				return err
			}
			f.Close()
		}
		if name == "sudo" && len(args) >= 1 {
			// Allow all sudo ops
		}
		// Allow udevadm settle, mount, etc.
		return nil
	}

	mps, err := bt.Prepare(noopTrack)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if bt.ImagePath != filepath.Join(tmp, "tacklebox.img") {
		t.Errorf("ImagePath = %q, want %q", bt.ImagePath, filepath.Join(tmp, "tacklebox.img"))
	}
	if bt.loopDev != "/dev/loop99" {
		t.Errorf("loopDev = %q, want /dev/loop99", bt.loopDev)
	}
	if bt.targetDev != "/dev/loop99" {
		t.Errorf("targetDev = %q, want /dev/loop99", bt.targetDev)
	}

	if mps.EspMount == "" {
		t.Error("EspMount empty")
	}
	if mps.StoreMount == "" {
		t.Error("StoreMount empty")
	}
	if !strings.Contains(mps.EspMount, "mount-esp") {
		t.Errorf("EspMount = %q, want path containing mount-esp", mps.EspMount)
	}
	if !strings.Contains(mps.StoreMount, "mount-store") {
		t.Errorf("StoreMount = %q, want path containing mount-store", mps.StoreMount)
	}
}

func TestBlockTarget_Prepare_RealDevice(t *testing.T) {
	needsSgdisk(t)
	tmp := t.TempDir()
	parts := []blockdev.Partition{
		{Size: "+1G", FS: "vfat", Label: "ESP"},
		{Size: "+27G", FS: "ext4", Label: "TBOX_STORE"},
		{Size: "0", FS: "ext4", Label: "TBOX_PERSIST"},
	}
	bt := NewBlockTarget(tmp, "/dev/sdb", "", parts)

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }

	// no-op tracker to avoid nil pointer dereference
	noopTrack := func(name string, fn func() error) error { return fn() }

	_, err := bt.Prepare(noopTrack)
	if err != nil {
		t.Fatalf("Prepare real device: %v", err)
	}

	if bt.targetDev != "/dev/sdb" {
		t.Errorf("targetDev = %q, want /dev/sdb", bt.targetDev)
	}
	if bt.ImagePath != "" {
		t.Errorf("ImagePath = %q, want empty for real device", bt.ImagePath)
	}
	if bt.loopDev != "" {
		t.Errorf("loopDev = %q, want empty for real device", bt.loopDev)
	}
}

func TestBlockTarget_Prepare_LosupFailure(t *testing.T) {
	// sgdiskTolerant uses RunCombined which is not mockable.
	needsSgdisk(t)

	tmp := t.TempDir()
	parts := []blockdev.Partition{{Size: "+1G", FS: "vfat"}}
	bt := NewBlockTarget(tmp, "", "2G", parts)

	oldRunFn := runner.RunFn
	oldOutput := runner.Output
	defer func() {
		runner.RunFn = oldRunFn
		runner.Output = oldOutput
	}()

	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }
	runner.Output = func(name string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "losetup" {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, nil
	}

	noopTrack := func(name string, fn func() error) error { return fn() }
	_, err := bt.Prepare(noopTrack)
	if err == nil {
		t.Fatal("expected error from losetup failure")
	}
	if !strings.Contains(err.Error(), "setup loop device") {
		t.Errorf("error = %v, want 'setup loop device'", err)
	}
}

func TestFreeBytes(t *testing.T) {
	tmp := t.TempDir()
	n, err := FreeBytes(tmp)
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if n == 0 {
		t.Error("FreeBytes returned 0 on tmp dir")
	}
}

func TestFreeBytes_BadPath(t *testing.T) {
	_, err := FreeBytes("/nonexistent/path/xyzzy")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}
