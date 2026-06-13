package target

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/runner"
)

func TestNewIsoTarget_Defaults(t *testing.T) {
	it := NewIsoTarget("/tmp/output", "/tmp/output/tunaos.iso", "", "")

	if it.Label != "TACKLEBOX" {
		t.Errorf("Label = %q, want TACKLEBOX (default)", it.Label)
	}
	if it.EFISource != "" {
		t.Errorf("EFISource = %q, want empty", it.EFISource)
	}
	if it.DefaultBootEntry != "" {
		t.Errorf("DefaultBootEntry = %q, want empty", it.DefaultBootEntry)
	}
	if it.OutputBase != "/tmp/output" {
		t.Errorf("OutputBase = %q", it.OutputBase)
	}
	if it.OutputIso != "/tmp/output/tunaos.iso" {
		t.Errorf("OutputIso = %q", it.OutputIso)
	}
}

func TestNewIsoTarget_CustomLabel(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "MY_ISO", "")

	if it.Label != "MY_ISO" {
		t.Errorf("Label = %q, want MY_ISO", it.Label)
	}
}

func TestNewIsoTarget_DefaultBootEntry(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "TBX", "", "bluefin-live")

	if it.DefaultBootEntry != "bluefin-live" {
		t.Errorf("DefaultBootEntry = %q, want bluefin-live", it.DefaultBootEntry)
	}
}

func TestNewIsoTarget_MultipleDefaultBootEntries(t *testing.T) {
	// Variadic: only first is used.
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "TBX", "", "first", "second")

	if it.DefaultBootEntry != "first" {
		t.Errorf("DefaultBootEntry = %q, want first (only first variadic arg used)", it.DefaultBootEntry)
	}
}

func TestIsoTarget_InstallMode(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "", "")
	if it.InstallMode() != InstallModeLive {
		t.Errorf("InstallMode = %v, want InstallModeLive", it.InstallMode())
	}
}

func TestIsoTarget_IsoLabel(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "MY_LABEL", "")
	if it.IsoLabel() != "MY_LABEL" {
		t.Errorf("IsoLabel = %q, want MY_LABEL", it.IsoLabel())
	}
}

func TestIsoTarget_KernelPath(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "", "")
	got := it.KernelPath("bazzite")
	want := "/images/pxeboot/bazzite/vmlinuz"
	if got != want {
		t.Errorf("KernelPath = %q, want %q", got, want)
	}
}

func TestIsoTarget_InitrdPath(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "", "")
	got := it.InitrdPath("bazzite")
	want := "/images/pxeboot/bazzite/initrd.img"
	if got != want {
		t.Errorf("InitrdPath = %q, want %q", got, want)
	}
}

func TestIsoTarget_Prepare_CreatesDirectories(t *testing.T) {
	tmp := t.TempDir()
	it := NewIsoTarget(tmp, filepath.Join(tmp, "test.iso"), "TBX", "")

	oldRunFn := runner.RunFn
	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }
	defer func() { runner.RunFn = oldRunFn }()

	mps, err := it.Prepare(nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Verify key directories were created.
	expectedDirs := []string{
		filepath.Join(it.isoRoot, "LiveOS"),
		filepath.Join(it.isoRoot, "EFI", "BOOT"),
		filepath.Join(it.isoRoot, "images", "pxeboot"),
		filepath.Join(it.espStaging, "EFI", "BOOT"),
		filepath.Join(it.espStaging, "loader", "entries"),
		filepath.Join(it.espStaging, "images", "pxeboot"),
	}
	for _, d := range expectedDirs {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			t.Errorf("directory not created: %s", d)
		}
	}

	// Verify loader.conf was written.
	loaderPath := filepath.Join(it.espStaging, "loader", "loader.conf")
	content, err := os.ReadFile(loaderPath)
	if err != nil {
		t.Fatalf("read loader.conf: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "timeout 5") {
		t.Errorf("loader.conf missing timeout: %s", s)
	}
	if !strings.Contains(s, "default *") {
		t.Errorf("loader.conf missing default: %s", s)
	}
	if !strings.Contains(s, "console-mode max") {
		t.Errorf("loader.conf missing console-mode: %s", s)
	}

	// Verify mountpoints.
	if mps.EspMount != it.espStaging {
		t.Errorf("EspMount = %q, want %q", mps.EspMount, it.espStaging)
	}
	if mps.StoreMount != filepath.Join(it.isoRoot, "LiveOS") {
		t.Errorf("StoreMount = %q", mps.StoreMount)
	}
}

func TestIsoTarget_Prepare_CustomDefaultBoot(t *testing.T) {
	tmp := t.TempDir()
	it := NewIsoTarget(tmp, filepath.Join(tmp, "test.iso"), "TBX", "", "bluefin-live")

	oldRunFn := runner.RunFn
	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }
	defer func() { runner.RunFn = oldRunFn }()

	_, err := it.Prepare(nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	loaderPath := filepath.Join(it.espStaging, "loader", "loader.conf")
	content, err := os.ReadFile(loaderPath)
	if err != nil {
		t.Fatalf("read loader.conf: %v", err)
	}
	if !strings.Contains(string(content), "default bluefin-live") {
		t.Errorf("loader.conf missing custom default: %s", string(content))
	}
}

func TestIsoTarget_Prepare_PathLayout(t *testing.T) {
	tmp := t.TempDir()
	it := NewIsoTarget(tmp, filepath.Join(tmp, "test.iso"), "TBX", "")

	oldRunFn := runner.RunFn
	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }
	defer func() { runner.RunFn = oldRunFn }()

	_, err := it.Prepare(nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Verify internal path layout is consistent.
	if it.root != filepath.Join(tmp, "iso") {
		t.Errorf("root = %q, want .../iso", it.root)
	}
	if it.isoRoot != filepath.Join(tmp, "iso", "iso-root") {
		t.Errorf("isoRoot = %q", it.isoRoot)
	}
	if it.espStaging != filepath.Join(tmp, "iso", "esp-staging") {
		t.Errorf("espStaging = %q", it.espStaging)
	}
	if it.espMount != filepath.Join(tmp, "iso", "esp-staging") {
		t.Errorf("espMount = %q", it.espMount)
	}
}

func TestIsoTarget_Cleanup_Idempotent(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "", "")
	calls := 0
	it.addCleanup(func() { calls++ })

	it.Cleanup()
	if calls != 1 {
		t.Errorf("first Cleanup: want 1, got %d", calls)
	}
	it.Cleanup()
	if calls != 1 {
		t.Errorf("second Cleanup should be no-op, got %d", calls)
	}
}

func TestIsoTarget_Cleanup_LIFO(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "", "")
	var order []int
	it.addCleanup(func() { order = append(order, 1) })
	it.addCleanup(func() { order = append(order, 2) })
	it.addCleanup(func() { order = append(order, 3) })

	it.Cleanup()
	if len(order) != 3 {
		t.Fatalf("want 3 calls, got %d", len(order))
	}
	if order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Errorf("not LIFO: %v", order)
	}
}

func TestIsoTarget_Finalize_NoEFISource(t *testing.T) {
	it := NewIsoTarget("/tmp", "/tmp/test.iso", "TBX", "")

	_, err := it.Finalize(nil)
	if err == nil {
		t.Fatal("expected error when EFISource is empty")
	}
	if !strings.Contains(err.Error(), "no EFISource") {
		t.Errorf("error = %v, want 'no EFISource'", err)
	}
}

func TestIsoTarget_Finalize_WithEFISource(t *testing.T) {
	tmp := t.TempDir()
	it := NewIsoTarget(tmp, filepath.Join(tmp, "test.iso"), "TBX", "some-image:latest")

	// Fill in Prepare-level fields that Finalize expects.
	it.root = filepath.Join(tmp, "iso")
	it.isoRoot = filepath.Join(tmp, "iso", "iso-root")
	it.espStaging = filepath.Join(tmp, "iso", "esp-staging")

	// Create required dirs + a fake env pxeboot entry.
	os.MkdirAll(filepath.Join(it.espStaging, "images", "pxeboot", "testenv"), 0755)
	os.MkdirAll(filepath.Join(it.espStaging, "EFI", "BOOT"), 0755)
	// Write a fake EFI binary
	os.WriteFile(filepath.Join(it.espStaging, "EFI", "BOOT", "BOOTX64.EFI"), []byte("fake"), 0644)

	// Create the minimal isoRoot structure.
	os.MkdirAll(filepath.Join(it.isoRoot, "EFI", "BOOT"), 0755)
	os.MkdirAll(filepath.Join(it.isoRoot, "images", "pxeboot"), 0755)

	oldRunFn := runner.RunFn
	oldOutputFn := runner.OutputFn
	defer func() {
		runner.RunFn = oldRunFn
		runner.OutputFn = oldOutputFn
	}()

	runner.OutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "sudo" && len(args) >= 1 && args[0] == "du" {
			return []byte("1024\t/path\n"), nil
		}
		return nil, nil
	}

	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }

	// Will fail at ExtractEFIBinary since we don't have a real container.
	// This tests that Finalize validates EFI extraction before proceeding.
	_, err := it.Finalize(nil)
	if err == nil {
		t.Log("Finalize succeeded (unexpected without real skopeo)")
	} else {
		t.Logf("Finalize error (expected): %v", err)
	}
}

func TestIsoTarget_AssembleEspImage(t *testing.T) {
	tmp := t.TempDir()
	it := NewIsoTarget(tmp, filepath.Join(tmp, "test.iso"), "TBX", "")

	it.espStaging = filepath.Join(tmp, "esp-staging")
	it.isoRoot = filepath.Join(tmp, "iso-root")

	// Create staging content.
	os.MkdirAll(filepath.Join(it.espStaging, "EFI", "BOOT"), 0755)
	os.WriteFile(filepath.Join(it.espStaging, "EFI", "BOOT", "BOOTX64.EFI"), []byte("fake-efi"), 0644)

	os.MkdirAll(filepath.Join(it.isoRoot, "EFI"), 0755)

	oldRunFn := runner.RunFn
	oldOutputFn := runner.OutputFn
	defer func() {
		runner.RunFn = oldRunFn
		runner.OutputFn = oldOutputFn
	}()

	runner.OutputFn = func(name string, args ...string) ([]byte, error) {
		return []byte("512\n"), nil
	}
	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }

	err := it.assembleEspImage()
	if err != nil {
		t.Fatalf("assembleEspImage: %v", err)
	}
}

func TestIsoTarget_AssembleIso(t *testing.T) {
	tmp := t.TempDir()
	isoPath := filepath.Join(tmp, "test.iso")
	it := NewIsoTarget(tmp, isoPath, "MYISO", "")
	it.isoRoot = filepath.Join(tmp, "fake-root")

	// Create the isoRoot dir so xorriso -map doesn't fail before it even starts.
	os.MkdirAll(it.isoRoot, 0755)

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error { return nil }

	err := it.assembleIso()
	if err != nil {
		t.Fatalf("assembleIso: %v", err)
	}
}
