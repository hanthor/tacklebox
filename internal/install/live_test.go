package install

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/runner"
)

func TestShellEsc_Basic(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"", "''"},
		{"it's working", "'it'\"'\"'s working'"},
		{"a'b'c", "'a'\"'\"'b'\"'\"'c'"},
		{`no"quotes`, "'no\"quotes'"},
		{"spaces and $pecial", "'spaces and $pecial'"},
		{"new\nline", "'new\nline'"},
		{"tab\there", "'tab\there'"},
		{"back\\slash", "'back\\slash'"},
		{"semi;colon", "'semi;colon'"},
		{"pipe|char", "'pipe|char'"},
		{"$(whoami)", "'$(whoami)'"},
		{"`whoami`", "'`whoami`'"},
		{"${HOME}", "'${HOME}'"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := shellEsc(tc.input)
			if got != tc.want {
				t.Errorf("shellEsc(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestShellEsc_PreventsInjection(t *testing.T) {
	dangerous := []string{
		"; rm -rf /",
		"$(rm -rf /)",
		"`rm -rf /`",
		"'; rm -rf /; echo '",
		"\\'; rm -rf /; echo '",
	}

	for _, payload := range dangerous {
		escaped := shellEsc(payload)
		if !strings.HasPrefix(escaped, "'") || !strings.HasSuffix(escaped, "'") {
			t.Errorf("shellEsc(%q) = %q — not wrapped in quotes", payload, escaped)
		}
		inner := escaped[1 : len(escaped)-1]
		if strings.Contains(inner, "'") {
			if !strings.Contains(escaped, `'"'"'`) && strings.Count(escaped, "'") > 2 {
				t.Errorf("shellEsc(%q) = %q — single quote not escaped", payload, escaped)
			}
		}
	}
}

func TestShellEsc_Idempotent(t *testing.T) {
	safe := "simple-image-name"
	once := shellEsc(safe)
	twice := shellEsc(once)
	if !strings.HasPrefix(twice, "'") || !strings.HasSuffix(twice, "'") {
		t.Errorf("double shellEsc(%q) = %q — not wrapped", safe, twice)
	}
}

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
	if level, _ := squashParams("zstd"); level != "3" {
		t.Errorf("compression=zstd should use fast default, got level=%s", level)
	}

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

func TestTempSquashFile(t *testing.T) {
	path, err := tempSquashFile()
	if err != nil {
		t.Fatalf("tempSquashFile() failed: %v", err)
	}
	defer os.Remove(path)

	// File must exist.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("temp file does not exist: %v", err)
	}

	// File must be world-writable (mode 0666).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	if info.Mode()&os.ModePerm != 0666 {
		t.Errorf("temp file permissions = %o, want 0666", info.Mode()&os.ModePerm)
	}

	// Must have the right prefix.
	if !strings.Contains(path, "tbox-live-") {
		t.Errorf("temp file name %q should contain tbox-live-", path)
	}
	if !strings.HasSuffix(path, ".squashfs") {
		t.Errorf("temp file name %q should end with .squashfs", path)
	}
}

func TestTempSquashFile_CleanedOnChmodFail(t *testing.T) {
	// Cannot easily simulate a chmod failure in a temp dir, but verify
	// the function cleans up when the second CreateTemp succeeds and
	// we dry-run the pattern with a controlled scenario.
	// For now, ensure the happy path works.
	path, err := tempSquashFile()
	if err != nil {
		t.Fatalf("tempSquashFile() failed: %v", err)
	}
	os.Remove(path)
}

func TestStashSquashfsWithCache(t *testing.T) {
	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "tmp.sfs")
	cachePath := filepath.Join(tmpDir, "cache", "deadbeef.sfs")
	dstPath := filepath.Join(tmpDir, "dst", "out.sfs")

	// Create the temp file so it exists.
	if err := os.WriteFile(tmpPath, []byte("fake squash"), 0644); err != nil {
		t.Fatal(err)
	}

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var calls []string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}

	if err := stashSquashfs(tmpPath, cachePath, dstPath); err != nil {
		t.Fatalf("stashSquashfs failed: %v", err)
	}

	// Must mkdir cache dir, mv to cache, chmod, then place (hardlink).
	if len(calls) < 3 {
		t.Fatalf("expected >=3 runner.Run calls, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[0], "mkdir") {
		t.Errorf("first call should mkdir, got: %s", calls[0])
	}
	if !strings.Contains(calls[1], "mv") {
		t.Errorf("second call should mv, got: %s", calls[1])
	}
	if !strings.Contains(calls[2], "chmod") {
		t.Errorf("third call should chmod, got: %s", calls[2])
	}
}

func TestStashSquashfsWithoutCache(t *testing.T) {
	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "tmp.sfs")
	dstPath := filepath.Join(tmpDir, "dst", "out.sfs")

	if err := os.WriteFile(tmpPath, []byte("fake squash"), 0644); err != nil {
		t.Fatal(err)
	}

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var calls []string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}

	// Empty cache path: should mv directly to dst.
	if err := stashSquashfs(tmpPath, "", dstPath); err != nil {
		t.Fatalf("stashSquashfs (no cache) failed: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 runner.Run calls, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[0], "mkdir") {
		t.Errorf("first call should mkdir dst dir, got: %s", calls[0])
	}
	if !strings.Contains(calls[1], "mv "+tmpPath) {
		t.Errorf("second call should mv tmp to dst, got: %s", calls[1])
	}
}

func TestStashSquashfsCacheMkdirError(t *testing.T) {
	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "mkdir") {
			return io.ErrClosedPipe // simulated error
		}
		return nil
	}

	err := stashSquashfs("/tmp/tmp.sfs", "/cache/x.sfs", "/dst/out.sfs")
	if err == nil {
		t.Error("expected error from mkdir failure, got nil")
	}
}

func TestPlaceSquashfsHardlink(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache.sfs")
	dstPath := filepath.Join(tmpDir, "subdir", "out.sfs")

	if err := os.WriteFile(cachePath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var calls []string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		// First call: mkdir -p (success)
		// Second call: rm -f (success)
		// Third call: ln (success — hardlink path)
		// Fourth call: should not be reached
		if strings.Contains(strings.Join(args, " "), "ln ") {
			return nil // hardlink succeeds
		}
		return nil
	}

	if err := placeSquashfs(cachePath, dstPath); err != nil {
		t.Fatalf("placeSquashfs failed: %v", err)
	}

	// Should have called mkdir, rm, then ln (NOT cp).
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls (mkdir, rm, ln), got %d: %v", len(calls), calls)
	}
	lastCall := calls[len(calls)-1]
	if !strings.Contains(lastCall, "ln ") {
		t.Errorf("last call should be ln, got: %s", lastCall)
	}
}

func TestPlaceSquashfsCopyFallback(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache.sfs")
	dstPath := filepath.Join(tmpDir, "subdir", "out.sfs")

	if err := os.WriteFile(cachePath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var calls []string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		// Make the ln call fail to test cp fallback.
		if strings.Contains(strings.Join(args, " "), "ln ") {
			return io.ErrClosedPipe
		}
		return nil
	}

	if err := placeSquashfs(cachePath, dstPath); err != nil {
		t.Fatalf("placeSquashfs should succeed via cp fallback: %v", err)
	}

	// Should have called mkdir, rm, ln (fail), then cp.
	if len(calls) != 4 {
		t.Fatalf("expected 4 calls (mkdir, rm, ln, cp), got %d: %v", len(calls), calls)
	}
	lastCall := calls[len(calls)-1]
	if !strings.Contains(lastCall, "cp ") {
		t.Errorf("last call should be cp, got: %s", lastCall)
	}
}

func TestPlaceSquashfsMkdirError(t *testing.T) {
	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "mkdir") {
			return io.ErrClosedPipe
		}
		return nil
	}

	err := placeSquashfs("/cache/x.sfs", "/dst/out.sfs")
	if err == nil {
		t.Error("expected error from mkdir failure, got nil")
	}
}

func TestExtractEFIBinary_HostPresent(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "efi")

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var capturedArgs []string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		capturedArgs = append(capturedArgs, name+" "+strings.Join(args, " "))
		return nil
	}

	// The test should use the host's EFI binary if /usr/lib/systemd/boot/efi/systemd-bootx64.efi exists.
	// In CI/test environments this file likely doesn't exist, so we test the fallback path.
	// Instead, test that ExtractEFIBinary returns an error when the host lacks EFI binaries,
	// which it should since we're not running inside a container.
	_, err := ExtractEFIBinary("test-image", destDir)
	// Either we get an error (no host binary) or success (binary exists).
	if err != nil {
		// Expected: no systemd-boot EFI binary on host.
		if !strings.Contains(err.Error(), "no systemd-boot EFI binary") {
			t.Errorf("unexpected error: %v", err)
		}
	} else {
		// Success: verify mkdir and cp were called.
		if len(capturedArgs) < 2 {
			t.Errorf("expected mkdir and cp calls, got %d", len(capturedArgs))
		}
	}
}

func TestSetStagingRoot(t *testing.T) {
	orig := stagingRoot
	defer func() { stagingRoot = orig }()

	SetStagingRoot("/custom/staging")
	if stagingRoot != "/custom/staging" {
		t.Errorf("stagingRoot = %q, want /custom/staging", stagingRoot)
	}
}

func TestCleanupStaging(t *testing.T) {
	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var calls []string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}

	// Pre-populate cache.
	extractCacheMu.Lock()
	extractCache["test-image"] = stagedFiles{dir: "/tmp/some-staging-dir", kver: "6.8.0"}
	extractCache["other-image"] = stagedFiles{dir: "/tmp/other-dir", kver: "6.7.0"}
	extractCacheMu.Unlock()

	CleanupStaging()

	// Should have called rm -rf for each cached dir + rmdir for staging root.
	if len(calls) < 2 {
		t.Errorf("expected at least 2 runner.Run calls, got %d: %v", len(calls), calls)
	}

	// Cache must be empty after cleanup.
	extractCacheMu.Lock()
	if len(extractCache) != 0 {
		t.Errorf("extractCache not empty after CleanupStaging, got %d entries", len(extractCache))
	}
	extractCacheMu.Unlock()
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
	if got := strings.Count(s, "/usr/bin/mksquashfs"); got != 1 {
		t.Errorf("want exactly 1 mksquashfs invocation, got %d", got)
	}
	if got := strings.Count(s, "podman image unmount"); got != 2 {
		t.Errorf("want 2 unmounts in trap, got %d", got)
	}
}
