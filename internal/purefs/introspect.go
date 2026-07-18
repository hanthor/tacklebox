package purefs

import "github.com/tuna-os/tacklebox/internal/oci"

// DetectDesktop mirrors the live baseline's desktop detection against an
// unpacked image tree: the browser builder uses it to auto-configure the
// live setup (autologin adapter, installer app, flatpak defaults) from
// an arbitrary bootable image, before any authoring happens.
func DetectDesktop(root *oci.Node) string {
	has := func(p string) bool { return root.Lookup(p) != nil }
	switch {
	case has("usr/share/wayland-sessions/plasma.desktop"),
		has("usr/share/wayland-sessions/plasmawayland.desktop"):
		return "kde"
	case has("usr/share/wayland-sessions/niri.desktop"):
		return "niri"
	case has("usr/share/wayland-sessions/cosmic.desktop"):
		return "cosmic"
	}
	for _, dir := range []string{"usr/share/xsessions", "usr/share/wayland-sessions"} {
		if d := root.Lookup(dir); d != nil {
			for name := range d.Children {
				if len(name) >= 4 && name[:4] == "xfce" {
					return "xfce"
				}
			}
		}
	}
	if has("usr/share/wayland-sessions") || has("usr/share/xsessions") {
		return "gnome"
	}
	return "none"
}

// ImageFacts is what the builder GUI shows after introspection.
type ImageFacts struct {
	Desktop   string `json:"desktop"`
	KernelVer string `json:"kernelVer"`
	HasSdBoot bool   `json:"hasSdBoot"`
	FileCount int    `json:"fileCount"`
}

func Introspect(root *oci.Node) ImageFacts {
	facts := ImageFacts{Desktop: DetectDesktop(root)}
	if mods := root.Lookup("usr/lib/modules"); mods != nil {
		for name := range mods.Children {
			if mods.Lookup(name+"/vmlinuz") != nil {
				facts.KernelVer = name
				break
			}
		}
	}
	facts.HasSdBoot = root.Lookup("usr/lib/systemd/boot/efi/systemd-bootx64.efi") != nil
	root.Walk(func(_ string, n *oci.Node) error {
		if n.Type == oci.TypeFile {
			facts.FileCount++
		}
		return nil
	})
	return facts
}
