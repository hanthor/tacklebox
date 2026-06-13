package install

// Pure-function tests for live.go. The actual InstallLive +
// ExtractEFIBinary functions need podman/mksquashfs/sudo and are
// covered by the verify-smoke CI job (and locally by examples/iso-smoke.json),
// not here.

import (
	"strings"
	"testing"
)

func TestSquashParams(t *testing.T) {
	t.Setenv("SUPERISO_COMPRESSION", "")

	if level, block := squashParams(""); level != "3" || block != "131072" {
		t.Errorf("default: got level=%s block=%s, want 3/131072", level, block)
	}
	for _, c := range []string{"release", "max"} {
		if level, block := squashParams(c); level != "15" || block != "1048576" {
			t.Errorf("compression=%s: got level=%s block=%s, want 15/1048576", c, level, block)
		}
	}
	// Unknown values fall back to the fast default rather than erroring.
	if level, _ := squashParams("zstd"); level != "3" {
		t.Errorf("compression=zstd should use fast default, got level=%s", level)
	}

	// The SuperISO env var overrides the recipe (script compatibility).
	t.Setenv("SUPERISO_COMPRESSION", "release")
	if level, _ := squashParams(""); level != "15" {
		t.Errorf("SUPERISO_COMPRESSION=release should win, got level=%s", level)
	}
}

func TestSquashCacheName(t *testing.T) {
	a := squashCacheName([]string{"bluefin=sha1", "bazzite=sha2"}, "3", "131072")
	b := squashCacheName([]string{"bazzite=sha2", "bluefin=sha1"}, "3", "131072")
	if a != b {
		t.Error("cache name must be independent of env declaration order")
	}
	if a == squashCacheName([]string{"bluefin=sha1", "bazzite=sha2"}, "15", "1048576") {
		t.Error("compression settings must change the cache name")
	}
	if a == squashCacheName([]string{"bluefin=sha9", "bazzite=sha2"}, "3", "131072") {
		t.Error("an image ID change must change the cache name")
	}
}

func TestCombinedSquashScript(t *testing.T) {
	envs := []LiveEnv{
		{ID: "bluefin", Image: "ghcr.io/ublue-os/bluefin:stable"},
		{ID: "bazzite", Image: "ghcr.io/ublue-os/bazzite:stable"},
	}
	s := combinedSquashScript(envs, "/usr/bin/mksquashfs", "/tmp/out.sfs", "3", "131072")

	for _, want := range []string{
		"podman image mount 'ghcr.io/ublue-os/bluefin:stable'",
		"podman image mount 'ghcr.io/ublue-os/bazzite:stable'",
		`mkdir -p "$STAGE"/'bluefin'`,
		`mount --rbind "$M" "$STAGE"/'bazzite'`,
		"-e 'bluefin/proc'",
		"'bazzite/var/lib/containers/storage'",
		"-comp zstd -Xcompression-level 3 -b 131072",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
	// One mksquashfs invocation over the whole staging dir — that's where
	// the cross-env dedup happens.
	if got := strings.Count(s, "/usr/bin/mksquashfs"); got != 1 {
		t.Errorf("want exactly 1 mksquashfs invocation, got %d", got)
	}
	// Both images must be unmounted in the exit trap.
	if got := strings.Count(s, "podman image unmount"); got != 2 {
		t.Errorf("want 2 unmounts in trap, got %d", got)
	}
}
