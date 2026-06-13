package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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

// TestClearEnvDir_NormalDir verifies ClearEnvDir removes a plain directory tree.
// Requires sudo (for chattr + rm -rf). Skipped when sudo is not available.
func TestClearEnvDir_NormalDir(t *testing.T) {
	// ClearEnvDir uses RunCombined for the rm -rf step, which bypasses the
	// runner mock. Skip if sudo is not available in the test environment.
	skipIfNoSudo(t)

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

// skipIfNoSudo skips the test when sudo is not found in PATH.
func skipIfNoSudo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Skip("sudo not available in test environment")
	}
}
