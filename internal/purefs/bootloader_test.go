package purefs

import (
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/oci"
)

func bootTree(t *testing.T, files ...string) *oci.Node {
	t.Helper()
	store := &oci.MemStore{}
	root := &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
	for _, f := range files {
		addFile(t, store, root, f, "PE\x00\x00", 0o644, 0, 0)
	}
	return root
}

func TestDetectBootChainSdBoot(t *testing.T) {
	root := bootTree(t, "usr/lib/systemd/boot/efi/systemd-bootx64.efi")
	bc, err := DetectBootChain(root)
	if err != nil {
		t.Fatal(err)
	}
	if bc.Kind != "sdboot" || bc.SdBoot != "usr/lib/systemd/boot/efi/systemd-bootx64.efi" {
		t.Fatalf("got %+v", bc)
	}
}

func TestDetectBootChainSdBootSignedOnly(t *testing.T) {
	// Debian ships only the .signed name — an sbat-signed PE that boots
	// unchanged, not a detached signature.
	root := bootTree(t, "usr/lib/systemd/boot/efi/systemd-bootx64.efi.signed")
	bc, err := DetectBootChain(root)
	if err != nil {
		t.Fatal(err)
	}
	if bc.Kind != "sdboot" || !strings.HasSuffix(bc.SdBoot, ".signed") {
		t.Fatalf("got %+v", bc)
	}
}

func TestDetectBootChainBootupdLegacyLayout(t *testing.T) {
	root := bootTree(t,
		"usr/lib/bootupd/updates/EFI/centos/shimx64.efi",
		"usr/lib/bootupd/updates/EFI/centos/grubx64.efi",
		"usr/lib/bootupd/updates/EFI/centos/mmx64.efi",
		"usr/lib/bootupd/updates/EFI/BOOT/BOOTX64.EFI",
	)
	bc, err := DetectBootChain(root)
	if err != nil {
		t.Fatal(err)
	}
	if bc.Kind != "grub2" || bc.Vendor != "centos" {
		t.Fatalf("got %+v", bc)
	}
	if bc.Shim != "usr/lib/bootupd/updates/EFI/centos/shimx64.efi" ||
		bc.Grub != "usr/lib/bootupd/updates/EFI/centos/grubx64.efi" ||
		bc.MokMgr != "usr/lib/bootupd/updates/EFI/centos/mmx64.efi" {
		t.Fatalf("got %+v", bc)
	}
}

func TestDetectBootChainOstreeBootLayout(t *testing.T) {
	root := bootTree(t,
		"usr/lib/ostree-boot/efi/EFI/fedora/shimx64.efi",
		"usr/lib/ostree-boot/efi/EFI/fedora/grubx64.efi",
	)
	bc, err := DetectBootChain(root)
	if err != nil {
		t.Fatal(err)
	}
	if bc.Kind != "grub2" || bc.Vendor != "fedora" || bc.MokMgr != "" {
		t.Fatalf("got %+v", bc)
	}
}

func TestDetectBootChainVersionedLayout(t *testing.T) {
	// bootupd's current Fedora layout: versioned shim and GRUB payloads
	// matched into a pair by vendor directory.
	root := bootTree(t,
		"usr/lib/bootupd/updates/EFI.json",
		"usr/lib/efi/grub2/2.12-4.fc42/EFI/fedora/grubx64.efi",
		"usr/lib/efi/shim/15.8-3/EFI/fedora/shimx64.efi",
		"usr/lib/efi/shim/15.8-3/EFI/fedora/mmx64.efi",
		"usr/lib/efi/shim/15.8-3/EFI/BOOT/BOOTX64.EFI",
	)
	bc, err := DetectBootChain(root)
	if err != nil {
		t.Fatal(err)
	}
	if bc.Kind != "grub2" || bc.Vendor != "fedora" {
		t.Fatalf("got %+v", bc)
	}
	if bc.Grub != "usr/lib/efi/grub2/2.12-4.fc42/EFI/fedora/grubx64.efi" ||
		bc.Shim != "usr/lib/efi/shim/15.8-3/EFI/fedora/shimx64.efi" ||
		bc.MokMgr != "usr/lib/efi/shim/15.8-3/EFI/fedora/mmx64.efi" {
		t.Fatalf("got %+v", bc)
	}
}

func TestDetectBootChainVersionedLayoutVendorMismatch(t *testing.T) {
	// A GRUB payload whose vendor has no matching shim is not a bootable
	// pair — must error, not return half a chain.
	root := bootTree(t,
		"usr/lib/efi/grub2/2.12/EFI/fedora/grubx64.efi",
		"usr/lib/efi/shim/15.8/EFI/centos/shimx64.efi",
	)
	if _, err := DetectBootChain(root); err == nil {
		t.Fatal("expected an error for a vendor-mismatched pair")
	}
}

func TestDetectBootChainPrefersSdBoot(t *testing.T) {
	// Both loaders present → sdboot, so adding GRUB detection cannot
	// change any previously-working build.
	root := bootTree(t,
		"usr/lib/systemd/boot/efi/systemd-bootx64.efi",
		"usr/lib/bootupd/updates/EFI/fedora/shimx64.efi",
		"usr/lib/bootupd/updates/EFI/fedora/grubx64.efi",
	)
	bc, err := DetectBootChain(root)
	if err != nil {
		t.Fatal(err)
	}
	if bc.Kind != "sdboot" {
		t.Fatalf("got %+v", bc)
	}
}

func TestDetectBootChainNeither(t *testing.T) {
	root := bootTree(t, "usr/lib/os-release")
	_, err := DetectBootChain(root)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"systemd-boot", "bootupd", "shim"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %s", err, want)
		}
	}
}

func TestLiveGrubCfg(t *testing.T) {
	cfg := LiveGrubCfg("TunaOS browser-live (live)",
		"/images/pxeboot/browser-live/vmlinuz",
		"/images/pxeboot/browser-live/initrd.img",
		"root=tbox:CDLABEL=TUNAOS console=ttyS0")
	for _, want := range []string{
		"menuentry 'TunaOS browser-live (live)'",
		"linux /images/pxeboot/browser-live/vmlinuz root=tbox:CDLABEL=TUNAOS console=ttyS0",
		"initrd /images/pxeboot/browser-live/initrd.img",
		"set timeout=3",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("grub.cfg missing %q:\n%s", want, cfg)
		}
	}
}
