package purefs

import (
	"bytes"
	"debug/pe"
	"fmt"
)

// UKISections is what the live path needs out of a Unified Kernel Image:
// the kernel (an EFI-stub bzImage), the initrd, and — informationally —
// the baked-in cmdline. A UKI cannot be booted verbatim on live media
// (its cmdline mounts the verity root by GPT partition UUID, and an
// ISO9660 has no GPT partitions), so the sections are extracted and
// driven through the normal loader + BLS path with tbox live kargs
// instead (tacklebox#172).
type UKISections struct {
	Linux   []byte
	Initrd  []byte
	Cmdline string
}

// ExtractUKI parses a UKI (a PE binary assembled by ukify/objcopy) and
// returns its .linux and .initrd sections. Pure Go (debug/pe), so it
// runs identically in the browser engine.
func ExtractUKI(b []byte) (*UKISections, error) {
	f, err := pe.NewFile(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("not a PE binary (UKI expected): %w", err)
	}
	defer f.Close()

	section := func(name string) ([]byte, error) {
		s := f.Section(name)
		if s == nil {
			return nil, nil
		}
		data, err := s.Data()
		if err != nil {
			return nil, fmt.Errorf("read %s section: %w", name, err)
		}
		// SizeOfRawData is padded to the PE file alignment; the real
		// payload length is VirtualSize whenever it is smaller. objcopy
		// section additions sometimes leave VirtualSize zero — keep the
		// raw data in that case.
		if vs := s.VirtualSize; vs > 0 && int(vs) < len(data) {
			data = data[:vs]
		}
		return data, nil
	}

	linux, err := section(".linux")
	if err != nil {
		return nil, err
	}
	initrd, err := section(".initrd")
	if err != nil {
		return nil, err
	}
	if linux == nil || initrd == nil {
		return nil, fmt.Errorf("PE binary is not a UKI: missing %s section (has: %s)",
			map[bool]string{true: ".linux", false: ".initrd"}[linux == nil], sectionNames(f))
	}
	cmdline, err := section(".cmdline")
	if err != nil {
		return nil, err
	}
	return &UKISections{
		Linux:   linux,
		Initrd:  initrd,
		Cmdline: string(bytes.TrimRight(cmdline, "\x00\n ")),
	}, nil
}

func sectionNames(f *pe.File) string {
	var names []string
	for _, s := range f.Sections {
		names = append(names, s.Name)
	}
	return fmt.Sprintf("%v", names)
}
