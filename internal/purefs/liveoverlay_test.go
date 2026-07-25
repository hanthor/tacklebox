package purefs

import "testing"

func TestLiveOverlayRef(t *testing.T) {
	cases := []struct {
		image   string
		wantTag string
		wantOK  bool
	}{
		{"tuna-os/yellowfin:kde", "yellowfin-kde", true},
		{"tuna-os/sailfin:gnome", "sailfin-gnome", true},
		{"tuna-os/albacore:cosmic", "albacore-cosmic", true},
		// Only tuna-os images have a published overlay — it is the output of
		// that recipe's live_customize step.
		{"ghcr.io/projectbluefin/dakota:latest", "", false},
		{"quay.io/almalinuxorg/almalinux-bootc:10-kitten", "", false},
		{"docker.io/library/debian:trixie", "", false},
		// The tag is built from repo+flavor, so both halves must be present.
		{"tuna-os/yellowfin", "", false},
		{"tuna-os/yellowfin:", "", false},
		{"tuna-os/:kde", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		ref, ok := LiveOverlayRef(c.image)
		if ok != c.wantOK {
			t.Errorf("LiveOverlayRef(%q) ok = %v, want %v", c.image, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if ref.Repo != LiveOverlayRepo {
			t.Errorf("LiveOverlayRef(%q) repo = %q, want %q", c.image, ref.Repo, LiveOverlayRepo)
		}
		if ref.Tag != c.wantTag {
			t.Errorf("LiveOverlayRef(%q) tag = %q, want %q", c.image, ref.Tag, c.wantTag)
		}
	}
}

// A nil client (the --rootfs-tar path, which ingests an already-customized
// tree) must be a clean no-op rather than a panic or an error.
func TestGraftLiveOverlayNilClientIsNoop(t *testing.T) {
	root, store := newImageTree(t, nil, nil)
	applied, err := GraftLiveOverlay(root, store, nil, "tuna-os/yellowfin:kde", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Error("applied = true with no client")
	}
}
