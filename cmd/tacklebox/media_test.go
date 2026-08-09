package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/recipe"
)

func TestBelongsToEnv(t *testing.T) {
	cases := []struct {
		entry, env string
		want       bool
	}{
		{"bluefin-live.conf", "bluefin", true},
		{"bluefin-persistent.conf", "bluefin", true},
		{"bluefin.conf", "bluefin", true},
		{"bluefin2-live.conf", "bluefin", false}, // prefix guard
		{"bazzite-live.conf", "bluefin", false},
		{"bluefinx.conf", "bluefin", false},
	}
	for _, c := range cases {
		if got := belongsToEnv(c.entry, c.env); got != c.want {
			t.Errorf("belongsToEnv(%q, %q) = %v, want %v", c.entry, c.env, got, c.want)
		}
	}
}

func TestRewriteSortKey(t *testing.T) {
	body := "title Bluefin (live)\nsort-key 0-tbox-bluefin-live\nlinux /EFI/bluefin/vmlinuz\noptions root=x\n"

	got, changed := rewriteSortKey(body, "00-tbox-")
	if !changed {
		t.Fatal("expected change promoting to 00-tbox-")
	}
	if !strings.Contains(got, "sort-key 00-tbox-bluefin-live\n") {
		t.Errorf("sort-key not promoted: %q", got)
	}
	// Idempotent: rewriting to the same prefix reports no change.
	if _, changed := rewriteSortKey(got, "00-tbox-"); changed {
		t.Error("rewriteSortKey reported a change when prefix already matched")
	}
	// Demote back.
	demoted, changed := rewriteSortKey(got, "0-tbox-")
	if !changed || !strings.Contains(demoted, "sort-key 0-tbox-bluefin-live\n") {
		t.Errorf("demote failed: changed=%v body=%q", changed, demoted)
	}
}

func TestRewriteSortKeyLeavesNonTboxKeys(t *testing.T) {
	body := "title Other\nsort-key bootc-1\noptions root=x\n"
	got, changed := rewriteSortKey(body, "00-tbox-")
	if changed {
		t.Error("non-tbox sort-key should be left untouched")
	}
	if got != body {
		t.Errorf("body mutated: %q", got)
	}
}

func TestReassignDefaultBLS(t *testing.T) {
	esp := t.TempDir()
	entries := filepath.Join(esp, "loader", "entries")
	if err := os.MkdirAll(entries, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, sortKey string) {
		body := "title x\nsort-key " + sortKey + "\nlinux /a\ninitrd /b\noptions root=x\n"
		if err := os.WriteFile(filepath.Join(entries, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// bazzite is currently default; we promote bluefin.
	write("bazzite-live.conf", "00-tbox-bazzite-live")
	write("bluefin-live.conf", "0-tbox-bluefin-live")
	write("bluefin-persistent.conf", "0-tbox-bluefin-persistent")

	if err := reassignDefaultBLS(esp, "bluefin"); err != nil {
		t.Fatal(err)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(entries, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if !strings.Contains(read("bluefin-live.conf"), "sort-key 00-tbox-bluefin-live") {
		t.Error("bluefin-live not promoted to default")
	}
	if !strings.Contains(read("bluefin-persistent.conf"), "sort-key 00-tbox-bluefin-persistent") {
		t.Error("bluefin-persistent not promoted to default")
	}
	if !strings.Contains(read("bazzite-live.conf"), "sort-key 0-tbox-bazzite-live") {
		t.Error("bazzite not demoted from default")
	}
}

func TestCheckESPFit(t *testing.T) {
	dir := t.TempDir() // backed by a real filesystem with plenty of free space

	if err := checkESPFit(dir, 1); err != nil {
		t.Errorf("expected fit for 1 env on a normal tmpdir, got %v", err)
	}
	// Demand just more envs than the filesystem can hold (computed from real
	// free space so the requirement exceeds it without overflowing uint64).
	free, err := espFreeBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	tooMany := int(free/espHeadroomPerEnv) + 2
	if err := checkESPFit(dir, tooMany); err == nil {
		t.Errorf("expected ESP-fit failure requesting %d envs (free=%d MiB)", tooMany, free>>20)
	}
}

// --- validateMutationTarget ---

func TestValidateMutationTargetRejectsISO(t *testing.T) {
	err := validateMutationTarget("media.iso")
	if err == nil {
		t.Fatal("expected error for ISO target")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMutationTargetRejectsISOUppercase(t *testing.T) {
	err := validateMutationTarget("MEDIA.ISO")
	if err == nil {
		t.Fatal("expected error for uppercase .ISO target")
	}
}

func TestValidateMutationTargetAcceptsBlockDevice(t *testing.T) {
	if err := validateMutationTarget("/dev/sdb"); err != nil {
		t.Errorf("expected /dev/sdb to be accepted, got: %v", err)
	}
}

func TestValidateMutationTargetRejectsMissingFile(t *testing.T) {
	err := validateMutationTarget("/nonexistent/target.img")
	if err == nil {
		t.Fatal("expected error for nonexistent image file")
	}
}

func TestValidateMutationTargetAcceptsExistingFile(t *testing.T) {
	img := filepath.Join(t.TempDir(), "media.img")
	if err := os.WriteFile(img, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateMutationTarget(img); err != nil {
		t.Errorf("expected existing image to be accepted, got: %v", err)
	}
}

// --- confirmMutation ---

func TestConfirmMutationYesSkipsPrompt(t *testing.T) {
	newMockRunner(t)
	if err := confirmMutation("/dev/sdb", "summary", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfirmMutationRefusesNonInteractive(t *testing.T) {
	newMockRunner(t)
	err := confirmMutation("/dev/sdb", "summary", false)
	if err == nil {
		t.Fatal("expected error when stdin is not a terminal and --yes is unset")
	}
	if !strings.Contains(err.Error(), "without --yes") && !strings.Contains(err.Error(), "read confirmation") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- installedEnvs / readPersistedRecipe ---

func TestInstalledEnvs(t *testing.T) {
	storeMount := t.TempDir()
	os.MkdirAll(filepath.Join(storeMount, "tbox-install", "bluefin"), 0755)
	os.MkdirAll(filepath.Join(storeMount, "tbox-install", "bazzite"), 0755)
	os.WriteFile(filepath.Join(storeMount, "tbox-install", "not-an-env"), []byte("x"), 0644)

	got, err := installedEnvs(storeMount)
	if err != nil {
		t.Fatalf("installedEnvs: %v", err)
	}
	want := []string{"bazzite", "bluefin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInstalledEnvsMissingDir(t *testing.T) {
	storeMount := t.TempDir()
	_, err := installedEnvs(storeMount)
	if err == nil {
		t.Fatal("expected error when tbox-install is missing")
	}
}

func TestReadPersistedRecipe(t *testing.T) {
	storeMount := t.TempDir()

	// Two envs: only baz has a recipe.
	os.MkdirAll(filepath.Join(storeMount, "tbox-install", "foo"), 0755)
	recipeDir := filepath.Join(storeMount, "tbox-install", "baz", "etc", "tacklebox")
	os.MkdirAll(recipeDir, 0755)
	r := recipe.MediaRecipe{MediaName: "test-media", DefaultBoot: "baz"}
	data, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(recipeDir, "recipe.json"), data, 0644)

	got, err := readPersistedRecipe(storeMount)
	if err != nil {
		t.Fatalf("readPersistedRecipe: %v", err)
	}
	if got == nil {
		t.Fatal("expected a recipe, got nil")
	}
	if got.MediaName != "test-media" {
		t.Errorf("MediaName = %q, want test-media", got.MediaName)
	}
	if got.DefaultBoot != "baz" {
		t.Errorf("DefaultBoot = %q, want baz", got.DefaultBoot)
	}
}

func TestReadPersistedRecipeNoRecipe(t *testing.T) {
	storeMount := t.TempDir()
	os.MkdirAll(filepath.Join(storeMount, "tbox-install", "foo"), 0755)

	got, err := readPersistedRecipe(storeMount)
	if err != nil {
		t.Fatalf("readPersistedRecipe: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil when no env has a recipe, got %+v", got)
	}
}
