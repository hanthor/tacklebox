package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "os-release")
	content := `NAME="Fedora Linux"
VERSION="42 (Cloud Edition)"
ID=fedora
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	name, ver := parseOSRelease(path)
	if name != "Fedora Linux" {
		t.Errorf("got name %q, want Fedora Linux", name)
	}
	if ver != "42 (Cloud Edition)" {
		t.Errorf("got version %q, want 42 (Cloud Edition)", ver)
	}
}

func TestStatus_NoInstall(t *testing.T) {
	tmp := t.TempDir()
	// Should fail because tbox-install doesn't exist
	err := runStatus(nil, []string{tmp})
	if err == nil {
		t.Errorf("expected error for empty dir")
	}
}
