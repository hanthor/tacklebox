// purebuild assembles a live TunaOS ISO entirely in Go — no podman, no
// mksquashfs, no xorriso, no sudo. It is the native proving harness for
// the pure-Go core (tunaOS ADR 0002); the WASM browser builder wires the
// same packages behind a UI.
//
// Layout and kernel cmdline mirror internal/target.IsoTarget exactly, so
// the ISO boots through the same tbox-live dracut path as production
// media. The initramfs must contain the tbox modules: pass --initrd with
// a rebuilt initramfs (dracut --add "tbox-live tbox-root" inside the
// image), or the boot will stop in the stock initrd.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuna-os/tacklebox/internal/oci"
	"github.com/tuna-os/tacklebox/internal/purefs"
)

func main() {
	var (
		image    = flag.String("image", "", "image as <repo>:<tag>, e.g. tuna-os/sailfin:kde")
		registry = flag.String("registry", "https://ghcr.io", "registry base URL (or CORS shim)")
		out      = flag.String("out", "tunaos-pure.iso", "output ISO path")
		label    = flag.String("label", "TUNAOS", "ISO volume label (CDLABEL)")
		initrd   = flag.String("initrd", "", "path to a tbox-enabled initramfs (overrides the image's stock one)")
		workdir  = flag.String("workdir", ".purebuild", "scratch directory")
	)
	flag.Parse()
	if *image == "" || !strings.Contains(*image, ":") {
		log.Fatal("--image <repo>:<tag> is required")
	}
	repo := (*image)[:strings.LastIndex(*image, ":")]
	tag := (*image)[strings.LastIndex(*image, ":")+1:]
	envID := filepath.Base(repo) + "-" + tag

	if err := os.MkdirAll(*workdir, 0o755); err != nil {
		log.Fatal(err)
	}

	c := oci.NewClient(*registry)
	ref := oci.Ref{Repo: repo, Tag: tag}
	log.Printf(">>> resolving %s", *image)
	m, err := c.ResolveManifest(ref, "amd64")
	if err != nil {
		log.Fatal(err)
	}
	store := &oci.DirStore{Dir: filepath.Join(*workdir, "blobs")}
	log.Printf(">>> unpacking %d layers", len(m.Layers))
	root, err := c.Unpack(ref, m, store, func(i, n int) {
		fmt.Printf("\r    layer %d/%d", i+1, n)
	})
	fmt.Println()
	if err != nil {
		log.Fatal(err)
	}

	// Kernel + stock initramfs + systemd-boot out of the image tree.
	modDir := root.Lookup("usr/lib/modules")
	if modDir == nil {
		log.Fatal("no /usr/lib/modules in image")
	}
	var kver string
	for name := range modDir.Children {
		if modDir.Lookup(name+"/vmlinuz") != nil {
			kver = name
			break
		}
	}
	if kver == "" {
		log.Fatal("no kernel found under /usr/lib/modules")
	}
	log.Printf(">>> kernel %s", kver)

	blob := func(p string) func() (io.ReadCloser, error) {
		n := root.Lookup(p)
		if n == nil || n.Type != oci.TypeFile {
			log.Fatalf("missing in image: %s", p)
		}
		return func() (io.ReadCloser, error) { return store.Open(n.Ref) }
	}
	initrdSource := blob("usr/lib/modules/" + kver + "/initramfs.img")
	if *initrd != "" {
		initrdSource = purefs.FileSource(*initrd)
		log.Printf(">>> using tbox initramfs %s", *initrd)
	} else {
		log.Printf("!!! stock initramfs — live boot will stop without tbox modules")
	}
	sdBoot := blob("usr/lib/systemd/boot/efi/systemd-bootx64.efi")

	// Live rootfs (EROFS; tbox-live mounts with -t auto).
	sfsName := envID + ".rootfs.sfs"
	sfsPath := filepath.Join(*workdir, sfsName)
	log.Printf(">>> authoring EROFS live root %s", sfsName)
	sf, err := os.Create(sfsPath)
	if err != nil {
		log.Fatal(err)
	}
	if err := purefs.WriteErofs(root, store, sf, 0); err != nil {
		log.Fatal(err)
	}
	if err := sf.Close(); err != nil {
		log.Fatal(err)
	}

	// BLS entry — same template as cmd/tacklebox's liveKernelCmdline.
	kargs := fmt.Sprintf(
		"root=tbox:CDLABEL=%s tacklebox.live.squashimg=%s"+
			" tacklebox.live.overlay.size=8192 enforcing=0"+
			" tacklebox.env=%s console=ttyS0,115200n8",
		*label, sfsName, envID,
	)
	entry := fmt.Sprintf("title TunaOS %s (live)\nlinux /images/pxeboot/%s/vmlinuz\ninitrd /images/pxeboot/%s/initrd.img\noptions %s\n",
		envID, envID, envID, kargs)
	loaderConf := "timeout 3\n"

	kernelPath := "/images/pxeboot/" + envID + "/vmlinuz"
	initrdPath := "/images/pxeboot/" + envID + "/initrd.img"

	log.Printf(">>> authoring ESP")
	espPath := filepath.Join(*workdir, "efi.img")
	if err := purefs.WriteEsp(espPath, []purefs.EspFile{
		{Path: "/EFI/BOOT/BOOTX64.EFI", Source: sdBoot},
		{Path: "/loader/loader.conf", Source: purefs.StringSource(loaderConf)},
		{Path: "/loader/entries/" + envID + ".conf", Source: purefs.StringSource(entry)},
		{Path: kernelPath, Source: blob("usr/lib/modules/" + kver + "/vmlinuz")},
		{Path: initrdPath, Source: initrdSource},
	}); err != nil {
		log.Fatal(err)
	}

	log.Printf(">>> authoring ISO")
	if err := purefs.WriteIso(*out, *label, []purefs.IsoFile{
		{Path: "/EFI/efi.img", Source: purefs.FileSource(espPath)},
		{Path: "/EFI/BOOT/BOOTX64.EFI", Source: sdBoot},
		{Path: kernelPath, Source: blob("usr/lib/modules/" + kver + "/vmlinuz")},
		{Path: initrdPath, Source: initrdSource},
		{Path: "/LiveOS/" + sfsName, Source: purefs.FileSource(sfsPath)},
	}, "/EFI/efi.img"); err != nil {
		log.Fatal(err)
	}
	st, _ := os.Stat(*out)
	log.Printf(">>> done: %s (%.1f GB)", *out, float64(st.Size())/1e9)
}
