package purefs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func memInput(p, content string) IsoInput {
	return IsoInput{
		Path: p,
		Size: int64(len(content)),
		Source: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(content)), nil
		},
	}
}

func buildTestIso(t *testing.T) (string, map[string]string) {
	t.Helper()
	contents := map[string]string{
		"/EFI/efi.img":                         strings.Repeat("E", 5000),
		"/EFI/BOOT/BOOTX64.EFI":                strings.Repeat("B", 3000),
		"/images/pxeboot/env-a/vmlinuz":        strings.Repeat("K", 9000),
		"/images/pxeboot/env-a/initrd.img":     strings.Repeat("I", 7000),
		"/LiveOS/env-a.rootfs.sfs":             strings.Repeat("R", 12345),
		"/LiveOS/a-much-longer-file-name.data": "rockridge name survival",
	}
	var files []IsoInput
	for p, c := range contents {
		files = append(files, memInput(p, c))
	}
	out := filepath.Join(t.TempDir(), "test.iso")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteIso9660(f, "TBOXTEST", files, "/EFI/efi.img"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out, contents
}

func TestIso9660Layout(t *testing.T) {
	out, _ := buildTestIso(t)
	st, _ := os.Stat(out)
	if st.Size()%sectorSize != 0 {
		t.Fatalf("iso size %d not sector aligned", st.Size())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[16*sectorSize+1:16*sectorSize+6]) != "CD001" {
		t.Fatal("no PVD signature")
	}
	if string(data[17*sectorSize+7:17*sectorSize+30]) != "EL TORITO SPECIFICATION" {
		t.Fatal("no El Torito boot record")
	}
	if !bytes.Contains(data, []byte("a-much-longer-file-name.data")) {
		t.Fatal("Rock Ridge NM name not present")
	}
}

// TestIso9660ExternalTools validates with whatever host tooling exists:
// xorriso structural report and, when running as root, a kernel mount.
func TestIso9660ExternalTools(t *testing.T) {
	out, contents := buildTestIso(t)

	if _, err := exec.LookPath("xorriso"); err == nil {
		cmd := exec.Command("xorriso", "-indev", out, "-find", "/", "-type", "f")
		outp, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("xorriso rejected the image: %v\n%s", err, outp)
		}
		for p := range contents {
			if !strings.Contains(string(outp), "'"+p+"'") {
				t.Errorf("xorriso listing missing %s:\n%s", p, outp)
			}
		}
		report, _ := exec.Command("xorriso", "-indev", out, "-report_el_torito", "plain").CombinedOutput()
		if !strings.Contains(string(report), "UEFI") && !strings.Contains(string(report), "EFI") {
			t.Errorf("el torito report lacks EFI entry:\n%s", report)
		}
	} else {
		t.Log("xorriso not installed; structural cross-check skipped")
	}

	if os.Geteuid() == 0 {
		mnt := t.TempDir()
		if err := exec.Command("mount", "-o", "loop,ro", out, mnt).Run(); err != nil {
			t.Fatalf("kernel refused to mount: %v", err)
		}
		defer exec.Command("umount", mnt).Run()
		for p, want := range contents {
			got, err := os.ReadFile(filepath.Join(mnt, p))
			if err != nil {
				t.Fatalf("read %s from mounted iso: %v", p, err)
			}
			if string(got) != want {
				t.Errorf("%s: content mismatch (%d vs %d bytes)", p, len(got), len(want))
			}
		}
	} else {
		t.Log("not root; kernel mount check skipped (run via sudo for full validation)")
	}
	fmt.Println("iso:", out)
}
