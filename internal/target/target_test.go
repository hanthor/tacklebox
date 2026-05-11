package target

import (
	"testing"

	"github.com/tuna-os/tacklebox/internal/blockdev"
)

func TestBlockTarget_StaticPaths(t *testing.T) {
	bt := NewBlockTarget("/var/build", "", "10G", []blockdev.Partition{
		{Number: 1, Label: "TBOX_ESP", Size: "+1G", Type: "ef00", FS: "vfat"},
	})
	if got := bt.InstallMode(); got != InstallModeBootc {
		t.Errorf("InstallMode() = %q, want %q", got, InstallModeBootc)
	}
	if got, want := bt.KernelPath("bazzite"), "/EFI/bazzite/vmlinuz"; got != want {
		t.Errorf("KernelPath = %q, want %q", got, want)
	}
	if got, want := bt.InitrdPath("bazzite"), "/EFI/bazzite/initrd.img"; got != want {
		t.Errorf("InitrdPath = %q, want %q", got, want)
	}
}

func TestIsoTarget_StaticPaths(t *testing.T) {
	it := NewIsoTarget("/var/build", "/out.iso", "TBX_ISO", "ghcr.io/x/y:latest")
	if got := it.InstallMode(); got != InstallModeLive {
		t.Errorf("InstallMode() = %q, want %q", got, InstallModeLive)
	}
	if got, want := it.IsoLabel(), "TBX_ISO"; got != want {
		t.Errorf("IsoLabel() = %q, want %q", got, want)
	}
	if got, want := it.KernelPath("bazzite"), "/images/pxeboot/bazzite/vmlinuz"; got != want {
		t.Errorf("KernelPath = %q, want %q", got, want)
	}
	if got, want := it.InitrdPath("bazzite"), "/images/pxeboot/bazzite/initrd.img"; got != want {
		t.Errorf("InitrdPath = %q, want %q", got, want)
	}
}

func TestIsoTarget_DefaultLabel(t *testing.T) {
	it := NewIsoTarget("/var/build", "/out.iso", "" /* empty label triggers default */, "img")
	if got, want := it.IsoLabel(), "TACKLEBOX"; got != want {
		t.Errorf("default label = %q, want %q", got, want)
	}
}

func TestBlockTarget_CleanupIdempotent(t *testing.T) {
	bt := NewBlockTarget("/var/build", "", "10G", nil)
	bt.Cleanup()
	bt.Cleanup() // must not panic / re-run
}

func TestIsoTarget_CleanupIdempotent(t *testing.T) {
	it := NewIsoTarget("/var/build", "/out.iso", "X", "img")
	it.Cleanup()
	it.Cleanup()
}
