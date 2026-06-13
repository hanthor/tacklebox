//go:build integration

// Integration test for the in-place media mutation machinery shared by
// `tacklebox add` and `tacklebox remove`. It builds a synthetic "already
// built" media on a real loop device (vfat ESP + ext4 STORE) and drives the
// remove path end-to-end: mount → enumerate → removeEnv → default
// re-promotion → recipe roster sync.
//
// It deliberately stops short of `add`'s bootc install (that needs real
// container images + bootc — covered by the build/iso CI jobs). What it does
// cover is everything add/remove do to the on-disk layout: the sudo + mount +
// file manipulation that unit tests can't reach.
//
// Run with:
//
//	go test -tags=integration -run=TestMutate_RemoveSmoke ./cmd/tacklebox/...
//
// Requires: sudo (passwordless), sgdisk, mkfs.vfat, mkfs.ext4, losetup, mount.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/blockdev"
	"github.com/tuna-os/tacklebox/internal/recipe"
)

func TestMutate_RemoveSmoke(t *testing.T) {
	for _, tool := range []string{"sudo", "sgdisk", "mkfs.vfat", "mkfs.ext4", "losetup", "mount"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("required tool %q not found", tool)
		}
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skip("sudo -n unavailable; skipping media-mutation smoke")
	}

	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "media.img")
	outputBase := filepath.Join(tmp, "out")
	if err := os.MkdirAll(outputBase, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Create a 256 MiB image and attach it (partscan so p1/p2 appear).
	mustRun(t, "truncate", "-s", "256M", imgPath)
	loop := strings.TrimSpace(mustOut(t, "sudo", "losetup", "--find", "--show", "--partscan", imgPath))
	t.Logf("loop device: %s", loop)
	t.Cleanup(func() { exec.Command("sudo", "losetup", "-d", loop).Run() }) //nolint:errcheck

	// 2. Two partitions: p1 ESP (vfat), p2 STORE (ext4).
	mustRun(t, "sudo", "sgdisk",
		"--new=1:0:+64M", "--typecode=1:EF00", "--change-name=1:TBOX_ESP",
		"--new=2:0:0", "--typecode=2:8300", "--change-name=2:TBOX_STORE", loop)
	exec.Command("sudo", "partprobe", loop).Run() //nolint:errcheck
	exec.Command("udevadm", "settle").Run()       //nolint:errcheck

	espDev := blockdev.PartitionPath(loop, 1)
	storeDev := blockdev.PartitionPath(loop, 2)
	mustRun(t, "sudo", "mkfs.vfat", "-n", "TBOX_ESP", espDev)
	mustRun(t, "sudo", "mkfs.ext4", "-q", "-L", "TBOX_STORE", storeDev)

	// 3. Populate a synthetic 2-env media (alpha = default, beta).
	seedMedia(t, tmp, espDev, storeDev)

	// 4. Drive the real mutation helpers against the loop device.
	var cleanups []func()
	addCleanup := func(f func()) { cleanups = append(cleanups, f) }
	defer func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}()

	storeMount, espMount, err := mountBlockMedia(loop, outputBase, addCleanup)
	if err != nil {
		t.Fatalf("mountBlockMedia: %v", err)
	}

	// Enumeration + persisted recipe.
	envs, err := installedEnvs(storeMount)
	if err != nil {
		t.Fatalf("installedEnvs: %v", err)
	}
	if got := strings.Join(envs, ","); got != "alpha,beta" {
		t.Fatalf("installedEnvs = %q, want alpha,beta", got)
	}
	base, err := readPersistedRecipe(storeMount)
	if err != nil || base == nil {
		t.Fatalf("readPersistedRecipe: r=%v err=%v", base, err)
	}
	if base.DefaultBoot != "alpha" {
		t.Fatalf("default_boot = %q, want alpha", base.DefaultBoot)
	}

	// 5. Remove the default env.
	if err := removeEnv(storeMount, espMount, "alpha"); err != nil {
		t.Fatalf("removeEnv(alpha): %v", err)
	}
	assertGone(t, filepath.Join(storeMount, "tbox-install", "alpha"))
	assertGone(t, filepath.Join(espMount, "EFI", "alpha"))
	assertGone(t, filepath.Join(espMount, "loader", "entries", "alpha-live.conf"))
	assertExists(t, filepath.Join(storeMount, "tbox-install", "beta"))
	assertExists(t, filepath.Join(espMount, "loader", "entries", "beta-live.conf"))

	// 6. alpha was default → promote beta.
	if err := reassignDefaultBLS(espMount, "beta"); err != nil {
		t.Fatalf("reassignDefaultBLS: %v", err)
	}
	body := mustReadFile(t, filepath.Join(espMount, "loader", "entries", "beta-live.conf"))
	if !strings.Contains(body, "sort-key 00-tbox-beta-live") {
		t.Errorf("beta not promoted to default; entry:\n%s", body)
	}

	// 7. Sync the roster down to the surviving env.
	merged := *base
	merged.DefaultBoot = "beta"
	merged.BootableEnvironments = filterEnvs(base.BootableEnvironments, map[string]bool{"alpha": true})
	if err := syncPersistedRecipe(storeMount, merged); err != nil {
		t.Fatalf("syncPersistedRecipe: %v", err)
	}
	after, err := readPersistedRecipe(storeMount)
	if err != nil || after == nil {
		t.Fatalf("readPersistedRecipe (after): r=%v err=%v", after, err)
	}
	if len(after.BootableEnvironments) != 1 || after.BootableEnvironments[0].ID != "beta" {
		t.Errorf("roster after sync = %+v, want only beta", after.BootableEnvironments)
	}
	if after.DefaultBoot != "beta" {
		t.Errorf("default_boot after sync = %q, want beta", after.DefaultBoot)
	}
	t.Log("PASS: remove dropped alpha, promoted beta, synced roster")
}

// seedMedia mounts the two partitions and writes a minimal but realistic
// tacklebox layout for envs alpha (default) and beta, then unmounts.
func seedMedia(t *testing.T, tmp, espDev, storeDev string) {
	t.Helper()
	espMnt := filepath.Join(tmp, "seed-esp")
	storeMnt := filepath.Join(tmp, "seed-store")
	mustRun(t, "mkdir", "-p", espMnt, storeMnt)
	mustRun(t, "sudo", "mount", espDev, espMnt)
	mustRun(t, "sudo", "mount", storeDev, storeMnt)
	defer func() {
		exec.Command("sudo", "umount", storeMnt).Run() //nolint:errcheck
		exec.Command("sudo", "umount", espMnt).Run()   //nolint:errcheck
	}()

	r := recipe.MediaRecipe{
		MediaName:   "smoke",
		Size:        "256M",
		DefaultBoot: "alpha",
		BootableEnvironments: []recipe.BootableEnvironment{
			{ID: "alpha", Image: "localhost/alpha:latest", Modes: []recipe.BootMode{recipe.ModeLive}},
			{ID: "beta", Image: "localhost/beta:latest", Modes: []recipe.BootMode{recipe.ModeLive}},
		},
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed recipe: %v", err)
	}
	recipeJSON := string(data)

	for _, env := range []string{"alpha", "beta"} {
		// STORE: tbox-install/<env>/etc/tacklebox/recipe.json + a dummy file.
		etc := filepath.Join(storeMnt, "tbox-install", env, "etc", "tacklebox")
		mustRun(t, "sudo", "mkdir", "-p", etc)
		sudoWrite(t, filepath.Join(etc, "recipe.json"), recipeJSON)
		sudoWrite(t, filepath.Join(storeMnt, "tbox-install", env, "marker"), env)

		// ESP: EFI/<env>/vmlinuz + loader/entries/<env>-live.conf.
		mustRun(t, "sudo", "mkdir", "-p", filepath.Join(espMnt, "EFI", env))
		sudoWrite(t, filepath.Join(espMnt, "EFI", env, "vmlinuz"), "kernel-"+env)
	}
	mustRun(t, "sudo", "mkdir", "-p", filepath.Join(espMnt, "loader", "entries"))
	sudoWrite(t, filepath.Join(espMnt, "loader", "entries", "alpha-live.conf"),
		"title alpha (live)\nsort-key 00-tbox-alpha-live\nlinux /EFI/alpha/vmlinuz\ninitrd /EFI/alpha/initrd.img\noptions root=x\n")
	sudoWrite(t, filepath.Join(espMnt, "loader", "entries", "beta-live.conf"),
		"title beta (live)\nsort-key 0-tbox-beta-live\nlinux /EFI/beta/vmlinuz\ninitrd /EFI/beta/initrd.img\noptions root=x\n")
}

// --- small helpers (named to avoid colliding with package-level funcs) ---

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func mustOut(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// sudoWrite writes content to a root-owned path via `sudo tee`.
func sudoWrite(t *testing.T, path, content string) {
	t.Helper()
	cmd := exec.Command("sudo", "tee", path)
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sudo tee %s: %v\n%s", path, err, out)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		// Fall back to sudo cat for root-owned files on the mount.
		out, serr := exec.Command("sudo", "cat", path).CombinedOutput()
		if serr != nil {
			t.Fatalf("read %s: %v / sudo cat: %v", path, err, serr)
		}
		return string(out)
	}
	return string(b)
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected %s to be removed, but it still exists", path)
	}
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}
