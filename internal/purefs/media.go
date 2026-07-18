package purefs

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

// EspFile is one file to place into the ESP image.
type EspFile struct {
	Path   string // absolute inside the ESP, e.g. /EFI/BOOT/BOOTX64.EFI
	Source func() (io.ReadCloser, error)
}

// WriteEsp authors a FAT32 ESP image at outPath containing the given
// files, replacing the mkfs.fat + mcopy pipeline. Size is derived from
// content (padded ~15% + 4 MiB for FAT overhead, floor 64 MiB so FAT32's
// minimum cluster count is always satisfied).
func WriteEsp(outPath string, files []EspFile) error {
	var total int64
	sizes := map[string]int64{}
	for _, f := range files {
		r, err := f.Source()
		if err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		n, err := io.Copy(io.Discard, r)
		r.Close()
		if err != nil {
			return err
		}
		sizes[f.Path] = n
		total += n
	}
	size := total + total/7 + 4<<20
	if size < 64<<20 {
		size = 64 << 20
	}
	size = (size + 511) &^ 511

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("esp create: %w", err)
	}
	defer out.Close()
	if err := out.Truncate(size); err != nil {
		return err
	}
	// reproducible=true: fixed volume serial + timestamps, so the same
	// content always yields the same ESP bytes (browser/CI parity).
	fs, err := fat32.Create(file.New(out, false), size, 0, 512, "ESP", true)
	if err != nil {
		return fmt.Errorf("esp mkfs: %w", err)
	}

	if err := populateEsp(fs, files); err != nil {
		return err
	}
	return out.Sync()
}

// populateEsp fills a freshly created FAT32 filesystem with the given
// files, creating parent directories as needed. Shared by the file-backed
// (WriteEsp) and in-memory (BuildEspBytes) paths.
func populateEsp(fs *fat32.FileSystem, files []EspFile) error {
	made := map[string]bool{}
	var mkdirAll func(p string) error
	mkdirAll = func(p string) error {
		if p == "/" || p == "" || made[p] {
			return nil
		}
		if err := mkdirAll(path.Dir(p)); err != nil {
			return err
		}
		made[p] = true
		return fs.Mkdir(p)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		if err := mkdirAll(path.Dir(f.Path)); err != nil {
			return fmt.Errorf("mkdir %s: %w", path.Dir(f.Path), err)
		}
		src, err := f.Source()
		if err != nil {
			return err
		}
		dst, err := fs.OpenFile(f.Path, os.O_CREATE|os.O_RDWR)
		if err != nil {
			src.Close()
			return fmt.Errorf("esp create %s: %w", f.Path, err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			return fmt.Errorf("esp write %s: %w", f.Path, err)
		}
		src.Close()
	}
	return nil
}

// IsoFile is one file to place into the ISO9660 tree.
type IsoFile struct {
	Path   string
	Source func() (io.ReadCloser, error)
}

// WriteIso authors the final UEFI-bootable ISO9660 at outPath, replacing
// xorriso: Rock Ridge tree + El Torito EFI entry pointing at espPath
// (which must also be present among files as /EFI/efi.img).
func WriteIso(outPath, label string, files []IsoFile, espPath string) error {
	// ISO workspace size: sum of content + slack.
	var total int64
	for _, f := range files {
		r, err := f.Source()
		if err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		n, err := io.Copy(io.Discard, r)
		r.Close()
		if err != nil {
			return err
		}
		total += n
	}
	size := total + total/10 + 8<<20

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return err
	}
	// Private workspace: go-diskfs stages the whole ISO tree here before
	// Finalize copies it into the image — never point it at a shared dir.
	ws, err := os.MkdirTemp(filepath.Dir(outPath), ".iso-ws-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(ws)
	b := file.New(f, false)
	fs, err := iso9660.Create(b, size, 0, 2048, ws)
	if err != nil {
		return fmt.Errorf("iso create: %w", err)
	}

	made := map[string]bool{}
	var mkdirAll func(p string) error
	mkdirAll = func(p string) error {
		if p == "/" || p == "" || made[p] {
			return nil
		}
		if err := mkdirAll(path.Dir(p)); err != nil {
			return err
		}
		made[p] = true
		return fs.Mkdir(p)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, in := range files {
		if err := mkdirAll(path.Dir(in.Path)); err != nil {
			return err
		}
		src, err := in.Source()
		if err != nil {
			return err
		}
		dst, err := fs.OpenFile(in.Path, os.O_CREATE|os.O_RDWR)
		if err != nil {
			src.Close()
			return fmt.Errorf("iso create %s: %w", in.Path, err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			return fmt.Errorf("iso write %s: %w", in.Path, err)
		}
		src.Close()
	}

	err = fs.Finalize(iso9660.FinalizeOptions{
		RockRidge:        true,
		VolumeIdentifier: label,
		ElTorito: &iso9660.ElTorito{
			BootCatalog: "/boot.catalog",
			Entries: []*iso9660.ElToritoEntry{{
				Platform:  iso9660.EFI,
				Emulation: iso9660.NoEmulation,
				BootFile:  espPath,
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("iso finalize: %w", err)
	}
	return nil
}

// FileSource adapts an on-disk file into a Source func.
func FileSource(p string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return os.Open(p) }
}

// StringSource serves literal content.
func StringSource(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}
