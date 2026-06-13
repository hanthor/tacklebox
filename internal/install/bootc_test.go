package install

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/runner"
)

func TestParseBackend(t *testing.T) {
	cases := []struct {
		name string
		json string
		want Backend
	}{
		{"ostree label", `{"Labels":{"ostree.bootable":"true"}}`, BackendOstree},
		{"empty inspect falls back to composefs", `{"Labels":{}}`, BackendComposefs},
		{"composefs only", `{"Annotations":{"composefs.digest":"abc"}}`, BackendComposefs},
		{"ostree substring anywhere wins", `{"Comment":"based on ostree pipeline"}`, BackendOstree},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseBackend(tc.json); got != tc.want {
				t.Errorf("parseBackend(%q) = %q, want %q", tc.json, got, tc.want)
			}
		})
	}
}

// TestClearEnvDir_NoExist verifies that ClearEnvDir is a no-op when the
// directory doesn't exist.
func TestClearEnvDir_NoExist(t *testing.T) {
	if err := ClearEnvDir("/nonexistent/path/that/cannot/exist/xyz"); err != nil {
		t.Errorf("unexpected error for non-existent dir: %v", err)
	}
}

// TestClearEnvDir_NormalDir verifies ClearEnvDir removes a plain directory tree
// (no immutable bits — simulates the fresh-build case).
func TestClearEnvDir_NormalDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "env")
	if err := os.MkdirAll(filepath.Join(dir, "sub1", "sub2"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub1", "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ClearEnvDir(dir); err != nil {
		t.Fatalf("ClearEnvDir returned error: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("directory still exists after ClearEnvDir")
	}
}

// TestPullUser_TargetsUserStore verifies PullUser pulls into the invoking
// user's rootless store (via the SUDO_USER drop-back prefix), not root's
// store — the regression guard for ISO builds double-pulling images.
func TestPullUser_TargetsUserStore(t *testing.T) {
	t.Setenv("SUDO_USER", "alice")
	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var calls [][]string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		// First call is `… image exists …`; report "not present" so
		// PullUser proceeds to pull. The podman subcommand is buried after
		// the `sudo -u alice …` drop-back prefix, so match anywhere.
		if contains(args, "exists") {
			return errors.New("not present")
		}
		return nil
	}

	if err := PullUser("ghcr.io/x/y:latest"); err != nil {
		t.Fatalf("PullUser: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("want 2 calls (exists, pull), got %d: %#v", len(calls), calls)
	}
	// Both calls must drop back to alice's store via `sudo -u alice`.
	for _, c := range calls {
		if c[0] != "sudo" || c[1] != "-u" || c[2] != "alice" {
			t.Errorf("call did not target user store: %#v", c)
		}
	}
	if last := calls[1]; last[len(last)-1] != "ghcr.io/x/y:latest" || !contains(last, "pull") {
		t.Errorf("second call is not a pull of the image: %#v", last)
	}
}

func TestPullUser_RejectsMissingLocalhost(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error {
		return errors.New("not present") // image exists → false
	}
	err := PullUser("localhost/tbox-iso-alpha:latest")
	if err == nil || !strings.Contains(err.Error(), "not found in the invoking user's podman store") {
		t.Fatalf("want localhost-not-found error, got %v", err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
