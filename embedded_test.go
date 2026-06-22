package tacklebox

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestDracutTboxRootEmbedded(t *testing.T) {
	// Verify the embedded FS is not nil
	if DracutTboxRoot == nil {
		t.Fatal("DracutTboxRoot embed.FS is nil")
	}

	// Verify expected files exist in the embedded FS
	expectedFiles := []string{
		"src/dracut/95tbox-root/module-setup.sh",
		"src/dracut/95tbox-root/tbox-root-mount.sh",
		"src/dracut/95tbox-root/tbox-root.service",
	}

	for _, f := range expectedFiles {
		data, err := fs.ReadFile(DracutTboxRoot, f)
		if err != nil {
			t.Errorf("expected embedded file %s: %v", f, err)
		}
		if len(data) == 0 {
			t.Errorf("embedded file %s is empty", f)
		}
	}
}

func TestDracutTboxRootFSIsValid(t *testing.T) {
	// Run fstest.TestFS to validate the embed.FS implementation
	err := fstest.TestFS(DracutTboxRoot,
		"src/dracut/95tbox-root/module-setup.sh",
		"src/dracut/95tbox-root/tbox-root-mount.sh",
		"src/dracut/95tbox-root/tbox-root.service",
	)
	if err != nil {
		t.Errorf("embedded FS validation failed: %v", err)
	}
}
