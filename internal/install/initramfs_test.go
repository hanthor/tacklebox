package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitramfsCacheKey(t *testing.T) {
	a := initramfsCacheKey("sha256:aaa", IsoInitramfsModules)
	b := initramfsCacheKey("sha256:bbb", IsoInitramfsModules)
	c := initramfsCacheKey("sha256:aaa", BlockInitramfsModules)

	if len(a) != 16 {
		t.Errorf("key length = %d, want 16", len(a))
	}
	if a == b {
		t.Error("different image IDs must produce different keys")
	}
	if a == c {
		t.Error("different module sets must produce different keys (ISO vs block initramfs)")
	}
	if a != initramfsCacheKey("sha256:aaa", IsoInitramfsModules) {
		t.Error("key must be stable for the same inputs")
	}
}

func TestInitramfsScript(t *testing.T) {
	s := initramfsScript(IsoInitramfsModules)
	for _, want := range []string{
		`dracut --force --no-hostonly --reproducible --add "dmsquash-live tbox-root"`,
		"for m in dmsquash-live tbox-root; do",
		"/tbox-out/initramfs.img",
		"TBOX_INITRAMFS=",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
	// The Sprintf %%s escape must come out as a literal %s for printf.
	if !strings.Contains(s, `printf '%s\n'`) {
		t.Errorf("script printf format mangled:\n%s", s)
	}
}

func TestMaterializeDracutModule(t *testing.T) {
	dir, err := materializeDracutModule()
	if err != nil {
		t.Fatalf("materializeDracutModule: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	for _, f := range []string{"module-setup.sh", "tbox-root-mount.sh", "tbox-root.service"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("embedded module file %s not materialized: %v", f, err)
		}
	}
}
