//go:build integration

// Integration tests for the tacklebox CLI. Run with:
//
//   go test -tags=integration ./cmd/tacklebox/...
//
// These tests build the tacklebox binary and exec it against a temp
// directory; they need:
//
//   - sudo (for losetup/mount/mkfs)
//   - podman, xorriso, mtools, mksquashfs, dosfstools, gdisk, bootctl
//   - network access to pull fixture images
//   - ~30 GB of /tmp (the test recipes default to a 10 G loop image)
//
// They're slow (minutes per case) — gate them behind the build tag so
// `go test ./...` stays fast for inner-loop work.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildTacklebox compiles the binary under test once per package run.
// Subtests share it via the returned path.
func buildTacklebox(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tacklebox")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/tacklebox")
	cmd.Dir = filepath.Join("..", "..") // module root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

// TestBuild_Help is the cheapest possible sanity check that the
// integration suite is wired correctly.
func TestBuild_Help(t *testing.T) {
	bin := buildTacklebox(t)
	out, err := exec.Command(bin, "build", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("build --help failed: %v\n%s", err, out)
	}
	for _, want := range []string{"--iso", "--xz", "RECIPE"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("--help missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestVerify_Help exercises the verify subcommand's help text. Same
// spirit as TestBuild_Help — confirms the binary actually exposes the
// features the CI workflow depends on.
func TestVerify_Help(t *testing.T) {
	bin := buildTacklebox(t)
	out, err := exec.Command(bin, "verify", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("verify --help failed: %v\n%s", err, out)
	}
	for _, want := range []string{"PATH", "ISO9660", "block image"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("verify --help missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestUpdate_Help confirms the update subcommand is registered and
// exposes the expected flags. Fast — no I/O.
func TestUpdate_Help(t *testing.T) {
	bin := buildTacklebox(t)
	out, err := exec.Command(bin, "update", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("update --help failed: %v\n%s", err, out)
	}
	for _, want := range []string{"RECIPE", "TARGET", "--yes", "PERSIST", "HOST"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("update --help missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestUpdate_FixtureSmoke is the heavy E2E test for `tacklebox update`:
//
//  1. Builds a 10 G two-env loop image from fixtures/smoke-2env.json.
//  2. Runs `tacklebox update --yes fixtures/smoke-2env.json <image>`
//     (same recipe, effectively a full re-install).
//  3. Runs `tacklebox verify` to confirm all envs are distinct and
//     all BLS entries still resolve correctly.
//
// Skipped when required tools are absent or sudo -n doesn't work
// (same conditions as TestBuild_FixtureSmoke).
func TestUpdate_FixtureSmoke(t *testing.T) {
	for _, tool := range []string{"sudo", "podman", "xorriso", "mksquashfs", "mcopy", "mkfs.fat", "sgdisk", "bootctl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("required tool %q not found in PATH", tool)
		}
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skip("sudo -n unavailable; skipping update fixture smoke")
	}

	bin := buildTacklebox(t)
	work := t.TempDir()
	imgPath := filepath.Join(work, "tacklebox.img")

	moduleRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	recipe := filepath.Join(moduleRoot, "fixtures", "smoke-2env.json")
	if _, err := os.Stat(recipe); err != nil {
		t.Skipf("fixture recipe missing: %v", err)
	}

	// Step 1: build initial image.
	t.Log("building initial 2-env image…")
	build := exec.Command("sudo", "-E", bin, "build", recipe, "-b", work)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("initial build failed: %v", err)
	}
	if _, err := os.Stat(imgPath); err != nil {
		t.Fatalf("expected image %s not produced: %v", imgPath, err)
	}

	// Step 2: run update with the same recipe.
	// This re-installs both envs without reformatting the disk.
	updateWork := filepath.Join(work, "update-scratch")
	t.Log("running tacklebox update on the built image…")
	upd := exec.Command("sudo", "-E", bin, "update", "--yes", recipe, imgPath, "-b", updateWork)
	upd.Stdout = os.Stdout
	upd.Stderr = os.Stderr
	if err := upd.Run(); err != nil {
		t.Fatalf("tacklebox update failed: %v", err)
	}

	// Step 3: verify the updated image.
	t.Log("verifying updated image…")
	verify := exec.Command("sudo", "-E", bin, "verify", imgPath)
	verify.Stdout = os.Stdout
	verify.Stderr = os.Stderr
	if err := verify.Run(); err != nil {
		t.Fatalf("verify after update failed: %v", err)
	}
	t.Log("PASS: build → update → verify all succeeded")
}
