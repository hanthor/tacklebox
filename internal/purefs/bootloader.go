package purefs

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/tuna-os/tacklebox/internal/oci"
)

// BootChain is the EFI boot path a live ISO can use, resolved from the
// image tree. Two independent code paths exist because bootc images ship
// one of two loaders and the loader is NOT a backend signal (wootc's
// deployer probe learned this the hard way — a composefs image may carry
// either; see tuna-os/wootc payload/deployer/deploy.sh, backend probe):
//
//   - Kind "sdboot": the image ships systemd-boot. The PE becomes
//     BOOTX64.EFI and BLS entries under /loader drive it. This is the
//     original tacklebox path (dakota, marlin, the TunaOS editions).
//   - Kind "grub2": no systemd-boot, but the image ships a signed
//     shim+GRUB pair in its bootupd payload (bluefin, aurora, bonito,
//     yellowfin — the traditional-ostree/uBlue shape). shim becomes
//     BOOTX64.EFI, loads grubx64.efi from the same directory, and a
//     grub.cfg menuentry replaces the BLS entry.
type BootChain struct {
	Kind string // "sdboot" | "grub2"

	// sdboot: tree path of the systemd-boot PE. Debian ships only the
	// .signed name (an sbat-signed PE, not a detached signature), so
	// either name boots unchanged as BOOTX64.EFI.
	SdBoot string

	// grub2: tree paths of the signed pair, plus the MOK manager when
	// the payload carries one (optional — QEMU/OVMF without Secure Boot
	// never invokes it).
	Shim   string
	Grub   string
	MokMgr string
	// Vendor is the EFI vendor directory the pair was found under
	// ("fedora", "centos", ...). grub.cfg is duplicated under this
	// directory because a signed GRUB's embedded prefix has been
	// observed to resolve either to its own directory or to the vendor
	// path, depending on the build (wootc writes its Phase-2 menu to
	// every candidate for the same reason).
	Vendor string
}

// sdBootCandidates in preference order — see the Debian note above.
var sdBootCandidates = []string{
	"usr/lib/systemd/boot/efi/systemd-bootx64.efi",
	"usr/lib/systemd/boot/efi/systemd-bootx64.efi.signed",
}

// DetectBootChain resolves which loader the live ISO will boot through.
//
// systemd-boot wins when both are present: it is the proven path for
// every image that built before this detection existed, so adding the
// GRUB path cannot change any previously-working build.
func DetectBootChain(root *oci.Node) (*BootChain, error) {
	for _, p := range sdBootCandidates {
		if rp, n := resolveFile(root, p); n != nil {
			return &BootChain{Kind: "sdboot", SdBoot: rp}, nil
		}
	}

	// bootupd's older layout (and ostree-boot): one vendor directory
	// holding the whole signed set.
	for _, base := range []string{
		"usr/lib/bootupd/updates/EFI",
		"usr/lib/ostree-boot/efi/EFI",
	} {
		d := root.Lookup(base)
		if d == nil || d.Type != oci.TypeDir {
			continue
		}
		for _, vendor := range sortedNames(d) {
			vd := d.Children[vendor]
			if vd == nil || vd.Type != oci.TypeDir {
				continue
			}
			shim, sn := resolveFile(root, base+"/"+vendor+"/shimx64.efi")
			grub, gn := resolveFile(root, base+"/"+vendor+"/grubx64.efi")
			if sn != nil && gn != nil {
				bc := &BootChain{Kind: "grub2", Shim: shim, Grub: grub, Vendor: vendor}
				bc.MokMgr, _ = resolveFile(root, base+"/"+vendor+"/mmx64.efi")
				return bc, nil
			}
		}
	}

	// bootupd's current Fedora layout: versioned shim and GRUB payloads
	// under /usr/lib/efi/{shim,grub2}/<version>/EFI/<vendor>/, matched
	// into a coherent pair by vendor directory (only EFI.json remains
	// under bootupd/updates). Vendor comes from the MATCHED location;
	// the stored paths are hardlink-resolved (see resolveFile).
	if grub := findFirst(root, "usr/lib/efi/grub2", "grubx64.efi", ""); grub != "" {
		vendor := path.Base(path.Dir(grub))
		if shim := findFirst(root, "usr/lib/efi/shim", "shimx64.efi", "EFI/"+vendor+"/shimx64.efi"); shim != "" {
			grubR, gn := resolveFile(root, grub)
			shimR, sn := resolveFile(root, shim)
			if gn != nil && sn != nil {
				bc := &BootChain{Kind: "grub2", Shim: shimR, Grub: grubR, Vendor: vendor}
				if mm := findFirst(root, "usr/lib/efi/shim", "mmx64.efi", "EFI/"+vendor+"/mmx64.efi"); mm != "" {
					bc.MokMgr, _ = resolveFile(root, mm)
				}
				return bc, nil
			}
		}
	}

	return nil, fmt.Errorf(
		"image ships no bootable EFI loader: no systemd-boot at usr/lib/systemd/boot/efi/systemd-bootx64.efi[.signed] " +
			"and no bootupd shim+GRUB pair under usr/lib/bootupd/updates/EFI, usr/lib/ostree-boot/efi/EFI or usr/lib/efi/{shim,grub2}")
}

// LiveGrubCfg renders the grub.cfg for the grub2 chain — the same single
// live entry the BLS template expresses for systemd-boot. `linux` (not
// linuxefi) is correct on every bootupd-shipped GRUB: the RH patchset
// these signed builds carry makes `linux` do the EFI handover.
func LiveGrubCfg(title, kernelPath, initrdPath, kargs string) string {
	return fmt.Sprintf(
		"set default=0\nset timeout=3\n\nmenuentry '%s' {\n    linux %s %s\n    initrd %s\n}\n",
		title, kernelPath, kargs, initrdPath)
}

// resolveFile follows hardlinks to the regular file that carries the
// bytes, returning the resolved path and node ("" / nil when p does not
// lead to one). rpm ships the shim payload as a hardlink farm —
// shimx64.efi, shim.efi and EFI/BOOT/BOOTX64.EFI are ONE inode on
// Fedora — and tar represents every occurrence after the first as a
// link entry, which Unpack stores as TypeHardlink with a root-relative
// Target (the same convention the EROFS writer resolves). Matching only
// TypeFile made the whole chain read as absent on aurora:stable, whose
// versioned shim probe found nothing but link entries
// (iso-builder run 31069841619).
func resolveFile(root *oci.Node, p string) (string, *oci.Node) {
	for hops := 0; hops < 8; hops++ {
		n := root.Lookup(p)
		if n == nil {
			return "", nil
		}
		switch n.Type {
		case oci.TypeFile:
			return p, n
		case oci.TypeHardlink:
			p = n.Target
		default:
			return "", nil
		}
	}
	return "", nil
}

func sortedNames(d *oci.Node) []string {
	names := make([]string, 0, len(d.Children))
	for name := range d.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// findFirst returns the tree path of the first file (or hardlink that
// resolves to one) named `name` under `base` (sorted depth-first, so
// deterministic). A non-empty `suffix` additionally requires the path to
// end with it — that is how the versioned shim payload is matched to the
// vendor directory the GRUB payload named. The MATCHED path is returned
// (it carries the vendor location); callers resolve it before staging.
func findFirst(root *oci.Node, base, name, suffix string) string {
	d := root.Lookup(base)
	if d == nil || d.Type != oci.TypeDir {
		return ""
	}
	var found string
	_ = d.Walk(func(p string, n *oci.Node) error {
		if found != "" {
			return nil
		}
		if (n.Type == oci.TypeFile || n.Type == oci.TypeHardlink) && path.Base(p) == name {
			full := base + "/" + p
			if suffix != "" && !strings.HasSuffix(full, suffix) {
				return nil
			}
			if _, fn := resolveFile(root, full); fn != nil {
				found = full
			}
		}
		return nil
	})
	return found
}
