package install

import (
	"os"
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
