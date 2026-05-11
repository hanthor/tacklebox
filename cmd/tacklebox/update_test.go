package main

import (
	"strings"
	"testing"
)

func TestResolveDeviceBlockDev(t *testing.T) {
	// A /dev/* path should be returned unchanged without any cleanup.
	cleanups := 0
	addCleanup := func(f func()) { cleanups++ }

	got, err := resolveDevice("/dev/sdb", addCleanup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/dev/sdb" {
		t.Errorf("got %q, want /dev/sdb", got)
	}
	if cleanups != 0 {
		t.Errorf("expected 0 cleanups for block device, got %d", cleanups)
	}
}

func TestResolveDeviceMissingImage(t *testing.T) {
	// A non-existent image file should produce an error before losetup runs.
	addCleanup := func(f func()) {}
	_, err := resolveDevice("/nonexistent/tacklebox.img", addCleanup)
	if err == nil {
		t.Fatal("expected error for missing image, got nil")
	}
	if !strings.Contains(err.Error(), "stat") && !strings.Contains(err.Error(), "attach") {
		t.Errorf("unexpected error message: %v", err)
	}
}
