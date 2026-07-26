package install

import (
	"os"
	"path/filepath"
	"testing"
)

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCustomizeCacheKeyStable(t *testing.T) {
	dir := t.TempDir()
	s := writeScript(t, dir, "a.sh", "echo hi\n")

	k1, err := customizeCacheKey("sha256:abc", []string{s})
	if err != nil {
		t.Fatal(err)
	}
	k2, err := customizeCacheKey("sha256:abc", []string{s})
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Fatalf("same inputs produced different keys: %s vs %s", k1, k2)
	}
	if len(k1) != 16 {
		t.Fatalf("key length = %d, want 16", len(k1))
	}
}

func TestCustomizeCacheKeyVariesByContentAndImage(t *testing.T) {
	dir := t.TempDir()
	s := writeScript(t, dir, "a.sh", "echo hi\n")

	base, err := customizeCacheKey("sha256:abc", []string{s})
	if err != nil {
		t.Fatal(err)
	}

	otherImage, err := customizeCacheKey("sha256:def", []string{s})
	if err != nil {
		t.Fatal(err)
	}
	if otherImage == base {
		t.Fatal("different image IDs produced the same key")
	}

	writeScript(t, dir, "a.sh", "echo changed\n")
	changed, err := customizeCacheKey("sha256:abc", []string{s})
	if err != nil {
		t.Fatal(err)
	}
	if changed == base {
		t.Fatal("changed script content produced the same key")
	}
}

func TestCustomizeCacheKeyVariesByScriptOrder(t *testing.T) {
	dir := t.TempDir()
	a := writeScript(t, dir, "a.sh", "echo a\n")
	b := writeScript(t, dir, "b.sh", "echo b\n")

	ab, err := customizeCacheKey("sha256:abc", []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	ba, err := customizeCacheKey("sha256:abc", []string{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if ab == ba {
		t.Fatal("script order should change the key (scripts run in order)")
	}
}

func TestCustomizeCacheKeyMissingScript(t *testing.T) {
	if _, err := customizeCacheKey("sha256:abc", []string{"/nonexistent/x.sh"}); err == nil {
		t.Fatal("expected error for missing script")
	}
}

func TestCustomizeLiveNoScriptsPassthrough(t *testing.T) {
	ref, err := CustomizeLive("ghcr.io/example/image:latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "ghcr.io/example/image:latest" {
		t.Fatalf("no-scripts case must return the original ref, got %s", ref)
	}
}

// Regression: the key hashed only the named scripts, but the whole directory
// is mounted and customize-live.sh sources desktop-<flavor>.sh out of it. So
// editing an adapter left the tag unchanged, the build reported
// "derived image cache hit", and shipped the previous live payload while
// looking successful — the ISO silently did not contain the change.
func TestCustomizeCacheKeyVariesBySourcedSibling(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "customize-live.sh")
	sibling := filepath.Join(dir, "desktop-cosmic.sh")
	if err := os.WriteFile(main, []byte("source ./desktop-cosmic.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("OnlyShowIn=COSMIC;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := customizeCacheKey("sha256:img", []string{main})
	if err != nil {
		t.Fatal(err)
	}

	// Edit ONLY the sibling — the named script is untouched.
	if err := os.WriteFile(sibling, []byte("# no OnlyShowIn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := customizeCacheKey("sha256:img", []string{main})
	if err != nil {
		t.Fatal(err)
	}

	if before == after {
		t.Fatalf("editing a sourced sibling must change the cache key; got %q both times", before)
	}
}
