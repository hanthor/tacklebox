package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
