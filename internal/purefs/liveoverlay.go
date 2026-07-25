package purefs

import (
	"fmt"
	"strings"

	"github.com/tuna-os/tacklebox/internal/oci"
)

// LiveOverlayRepo is where the per-variant live customize delta is published.
// Its tags are "<variant>-<flavor>", e.g. yellowfin-kde.
const LiveOverlayRepo = "tuna-os/live-overlay"

// LiveOverlayRef maps a base image reference to its live-overlay counterpart.
//
// Only tuna-os images have one: the overlay is the *output* of that recipe's
// live_customize step. Returns ok=false for anything else, and for refs
// without both a repo path and a tag, since the tag is built from the two.
//
//	tuna-os/yellowfin:kde -> tuna-os/live-overlay:yellowfin-kde
func LiveOverlayRef(image string) (oci.Ref, bool) {
	if !strings.HasPrefix(image, "tuna-os/") {
		return oci.Ref{}, false
	}
	parts := strings.SplitN(strings.TrimPrefix(image, "tuna-os/"), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return oci.Ref{}, false
	}
	return oci.Ref{Repo: LiveOverlayRepo, Tag: parts[0] + "-" + parts[1]}, true
}

// GraftLiveOverlay applies the published live-overlay delta for image onto an
// already-unpacked root (tunaOS#673).
//
// tuna-os/<variant>:<flavor> images have a customize delta published at
// tuna-os/live-overlay:<variant>-<flavor> carrying the live payload CI media
// ship — installer flatpak, autostart, polkit rules. Grafting it is what makes
// a natively-built ISO byte-comparable with the browser-built one; without it
// the two paths diverge silently, which is how 27 broken overlays went
// unnoticed.
//
// Layers already present in baseManifest are skipped, so only the delta is
// fetched. Absence of an overlay is NOT an error — most variants don't have
// one yet, and treating it as a hard dependency would deadlock every new
// variant (the overlay is produced by the very build that would then require
// it). Callers get applied=false and should fall through to the plain
// baseline; the returned error is reserved for an overlay that exists but
// could not be applied.
//
// progress, if non-nil, is called as layers land.
func GraftLiveOverlay(
	root *oci.Node,
	store oci.BlobStore,
	c *oci.Client,
	image string,
	baseManifest *oci.Manifest,
	progress func(layer, total int),
) (applied bool, err error) {
	if c == nil || root == nil || baseManifest == nil {
		return false, nil
	}
	ref, ok := LiveOverlayRef(image)
	if !ok {
		return false, nil
	}
	m, err := c.ResolveManifest(ref, "amd64")
	if err != nil {
		// No overlay published for this variant — the common case.
		return false, nil
	}
	skip := make(map[string]bool, len(baseManifest.Layers))
	for _, l := range baseManifest.Layers {
		skip[l.Digest] = true
	}
	if err := c.UnpackOnto(root, ref, m, store, skip, progress); err != nil {
		return false, fmt.Errorf("apply live overlay %s:%s: %w", ref.Repo, ref.Tag, err)
	}
	return true, nil
}
