package purefs

import (
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/oci"
)

func TestEnsureLiveReadyMarker(t *testing.T) {
	store := &oci.MemStore{}
	root := &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
	if err := EnsureLiveReadyMarker(root, store, ""); err != nil {
		t.Fatal(err)
	}
	body := readNode(t, store, root, "usr/lib/systemd/system/tbox-live-ready.service")
	if !strings.Contains(body, "TBOX_LIVE_READY") || !strings.Contains(body, "journal+console") {
		t.Fatalf("unit body:\n%s", body)
	}
	if strings.Contains(body, "TUNAOS") {
		t.Fatalf("generic unit must not carry distro branding:\n%s", body)
	}
	mustSymlink(t, root,
		"usr/lib/systemd/system/multi-user.target.wants/tbox-live-ready.service",
		"../tbox-live-ready.service")
}

func TestEnsureLiveReadyMarkerCustom(t *testing.T) {
	store := &oci.MemStore{}
	root := &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
	if err := EnsureLiveReadyMarker(root, store, "ACME_LIVE_OK"); err != nil {
		t.Fatal(err)
	}
	body := readNode(t, store, root, "usr/lib/systemd/system/tbox-live-ready.service")
	if !strings.Contains(body, "ACME_LIVE_OK") || strings.Contains(body, "TBOX_LIVE_READY") {
		t.Fatalf("unit body:\n%s", body)
	}
	fresh := &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
	if err := EnsureLiveReadyMarker(fresh, store, "bad'marker"); err == nil {
		t.Fatal("quoted marker must be rejected")
	}
}

func TestEnsureLiveReadyMarkerKeepsImageOwnUnit(t *testing.T) {
	store := &oci.MemStore{}
	root := &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
	// tunaOS ships its own richer unit — the builder must not add a second.
	addFile(t, store, root, "usr/lib/systemd/system/tunaos-live-ready.service", "IMAGE-OWN\n", 0o644, 0, 0)
	if err := EnsureLiveReadyMarker(root, store, ""); err != nil {
		t.Fatal(err)
	}
	if root.Lookup("usr/lib/systemd/system/tbox-live-ready.service") != nil {
		t.Fatal("generic unit added alongside the image's own readiness unit")
	}
}
