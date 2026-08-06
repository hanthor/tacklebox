package purefs

import (
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/oci"
)

func TestEnsureLiveReadyMarker(t *testing.T) {
	store := &oci.MemStore{}
	root := &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
	if err := EnsureLiveReadyMarker(root, store); err != nil {
		t.Fatal(err)
	}
	body := readNode(t, store, root, "usr/lib/systemd/system/tunaos-live-ready.service")
	if !strings.Contains(body, "TUNAOS_LIVE_READY") || !strings.Contains(body, "journal+console") {
		t.Fatalf("unit body:\n%s", body)
	}
	mustSymlink(t, root,
		"usr/lib/systemd/system/multi-user.target.wants/tunaos-live-ready.service",
		"../tunaos-live-ready.service")
}

func TestEnsureLiveReadyMarkerKeepsImageOwnUnit(t *testing.T) {
	store := &oci.MemStore{}
	root := &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
	addFile(t, store, root, "usr/lib/systemd/system/tunaos-live-ready.service", "IMAGE-OWN\n", 0o644, 0, 0)
	if err := EnsureLiveReadyMarker(root, store); err != nil {
		t.Fatal(err)
	}
	if got := readNode(t, store, root, "usr/lib/systemd/system/tunaos-live-ready.service"); got != "IMAGE-OWN\n" {
		t.Fatalf("image's own unit was clobbered: %q", got)
	}
}
