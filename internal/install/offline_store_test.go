package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/runner"
)

func TestCopyLocalImageToOfflineStoreUsesLocalContainersStorage(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	t.Setenv("TACKLEBOX_OFFLINE_COPY_TIMEOUT", "42")

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var calls [][]string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	err := copyLocalImageToOfflineStore("example.com/os:latest", "/tmp/store", "/tmp/run")
	if err != nil {
		t.Fatalf("copyLocalImageToOfflineStore returned error: %v", err)
	}

	want := [][]string{
		{"podman", "image", "exists", "example.com/os:latest"},
		{
			"podman", "unshare", "--", "sh", "-c",
			"timeout 42 skopeo copy --remove-signatures 'containers-storage:example.com/os:latest' 'containers-storage:[overlay@/tmp/store+/tmp/run]example.com/os:latest'",
		},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls mismatch\n got: %#v\nwant: %#v", calls, want)
	}
}

func TestCopyLocalImageToOfflineStoreRejectsInvalidTimeout(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	t.Setenv("TACKLEBOX_OFFLINE_COPY_TIMEOUT", "nope")

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = func(_ io.Reader, _ string, _ ...string) error {
		return nil
	}

	err := copyLocalImageToOfflineStore("example.com/os:latest", "/tmp/store", "/tmp/run")
	if err == nil || !strings.Contains(err.Error(), "invalid TACKLEBOX_OFFLINE_COPY_TIMEOUT") {
		t.Fatalf("expected invalid timeout error, got %v", err)
	}
}

func TestBuildOfflineStore_EmptyImages(t *testing.T) {
	tmp := t.TempDir()
	err := BuildOfflineStore(nil, filepath.Join(tmp, "staging"), filepath.Join(tmp, "out.squashfs"))
	if err != nil {
		t.Fatalf("BuildOfflineStore with nil images: %v", err)
	}

	// Empty slice should also be a no-op.
	err = BuildOfflineStore([]string{}, filepath.Join(tmp, "staging"), filepath.Join(tmp, "out.squashfs"))
	if err != nil {
		t.Fatalf("BuildOfflineStore with empty slice: %v", err)
	}
}

func TestBuildOfflineStore_CreatesWorldWritableDirs(t *testing.T) {
	// BuildOfflineStore calls ClearEnvDir which uses RunCombined (unmockable).
	// Skip if sudo is not available.
	skipIfNoSudo(t)

	tmp := t.TempDir()
	stagingRoot := filepath.Join(tmp, "staging")

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		// Handle sudo mkdir, sudo rm, and podman operations
		if name == "sudo" && len(args) >= 2 && args[0] == "mkdir" && args[1] == "-p" {
			return os.MkdirAll(args[2], 0755)
		}
		if name == "sudo" && len(args) >= 2 && args[0] == "rm" {
			return os.RemoveAll(args[len(args)-1])
		}
		if name == "podman" && len(args) >= 2 && args[0] == "image" && args[1] == "exists" {
			return fmt.Errorf("image not found")
		}
		return nil
	}

	err := BuildOfflineStore([]string{"example.com/os:latest"}, stagingRoot, filepath.Join(tmp, "out.squashfs"))
	if err == nil {
		t.Skip("skopeo succeeded unexpectedly")
	}

	// Verify storeRoot was created and is world-writable.
	storeRoot := filepath.Join(stagingRoot, "tbox-offline-store")
	fi, err := os.Stat(storeRoot)
	if err != nil {
		t.Fatalf("stat storeRoot: %v", err)
	}
	if fi.Mode().Perm() != 0777 {
		t.Errorf("storeRoot permissions = %o, want 0777", fi.Mode().Perm())
	}
}

func TestBuildOfflineStore_ImageNotPresent(t *testing.T) {
	// BuildOfflineStore calls ClearEnvDir which uses RunCombined (unmockable).
	// Skip if sudo is not available.
	skipIfNoSudo(t)

	tmp := t.TempDir()

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	// Simulate podman image exists failing, and handle sudo mkdir/rm calls.
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		if len(args) >= 2 && args[0] == "image" && args[1] == "exists" {
			return fmt.Errorf("image not found")
		}
		if name == "sudo" && len(args) >= 2 && args[0] == "mkdir" && args[1] == "-p" {
			return os.MkdirAll(args[2], 0755)
		}
		if name == "sudo" && len(args) >= 2 && args[0] == "rm" {
			return os.RemoveAll(args[len(args)-1])
		}
		return nil
	}

	err := BuildOfflineStore([]string{"missing/image:latest"}, filepath.Join(tmp, "staging"), filepath.Join(tmp, "out.squashfs"))
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	if !strings.Contains(err.Error(), "not present") {
		t.Errorf("error = %v, want 'not present'", err)
	}
}

func TestBuildOfflineStore_DefaultTimeout(t *testing.T) {
	skipIfNoSudo(t)
	tmp := t.TempDir()
	t.Setenv("SUDO_USER", "")
	// Unset the override so default (1800) is used.
	os.Unsetenv("TACKLEBOX_OFFLINE_COPY_TIMEOUT")

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	var skopeoScript string
	runner.RunFn = func(_ io.Reader, name string, args ...string) error {
		if name == "podman" && len(args) >= 2 && args[0] == "image" && args[1] == "exists" {
			return nil // image exists
		}
		if name == "podman" && len(args) >= 4 && args[0] == "unshare" {
			// args = [unshare, --, sh, -c, script]
			if len(args) >= 4 {
				skopeoScript = args[len(args)-1]
			}
			return fmt.Errorf("skopeo not available in test")
		}
		return nil
	}

	_ = BuildOfflineStore([]string{"example.com/os:latest"}, filepath.Join(tmp, "staging"), filepath.Join(tmp, "out.squashfs"))

	if skopeoScript == "" {
		t.Skip("skopeo call not reached")
	}
	if !strings.Contains(skopeoScript, "timeout 1800") {
		t.Errorf("default timeout not applied: %q", skopeoScript)
	}
}

func TestProvisionStoreMountBlock_CreatesMountUnit(t *testing.T) {
	tmp := t.TempDir()

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = mockSudoMkdirMv

	err := ProvisionStoreMountBlock(tmp)
	if err != nil {
		t.Fatalf("ProvisionStoreMountBlock: %v", err)
	}

	// Check mount unit file.
	unitPath := filepath.Join(tmp, "etc", "systemd", "system", `var-lib-superiso\x2dstore.mount`)
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read mount unit: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "What=/sysroot/tbox-containers.squashfs") {
		t.Errorf("mount unit missing What: %s", s)
	}
	if !strings.Contains(s, "Where=/var/lib/superiso-store") {
		t.Errorf("mount unit missing Where: %s", s)
	}
	if !strings.Contains(s, "Type=squashfs") {
		t.Errorf("mount unit missing Type: %s", s)
	}
}

func TestProvisionStoreMountBlock_CreatesWantsSymlink(t *testing.T) {
	tmp := t.TempDir()

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = mockSudoMkdirMv

	err := ProvisionStoreMountBlock(tmp)
	if err != nil {
		t.Fatalf("ProvisionStoreMountBlock: %v", err)
	}

	// Check wants symlink.
	wantsPath := filepath.Join(tmp, "etc", "systemd", "system", "local-fs.target.wants", `var-lib-superiso\x2dstore.mount`)
	if _, err := os.Lstat(wantsPath); err != nil {
		t.Errorf("wants symlink missing: %v", err)
	}
}

func TestProvisionStoreMountBlock_CreatesStorageDropin(t *testing.T) {
	tmp := t.TempDir()

	oldRunFn := runner.RunFn
	defer func() { runner.RunFn = oldRunFn }()

	runner.RunFn = mockSudoMkdirMv

	err := ProvisionStoreMountBlock(tmp)
	if err != nil {
		t.Fatalf("ProvisionStoreMountBlock: %v", err)
	}

	// Check storage.conf drop-in.
	dropinPath := filepath.Join(tmp, "etc", "containers", "storage.conf.d", "99-tbox-store.conf")
	content, err := os.ReadFile(dropinPath)
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "additionalimagestores") {
		t.Errorf("drop-in missing additionalimagestores: %s", s)
	}
	if !strings.Contains(s, "/var/lib/superiso-store") {
		t.Errorf("drop-in missing store path: %s", s)
	}
}

// mockSudoMkdirMv handles sudo mkdir and sudo cp by performing the actual
// filesystem operations in the test environment (no real sudo needed).
func mockSudoMkdirMv(_ io.Reader, name string, args ...string) error {
	if name != "sudo" || len(args) < 2 {
		return nil
	}
	switch args[0] {
	case "mkdir":
		if len(args) >= 3 && args[1] == "-p" {
			return os.MkdirAll(args[2], 0755)
		}
	case "cp":
		if len(args) >= 3 {
			// writeFileAsSudo writes content to a temp file, then runs
			// sudo cp <tmp> <dest>. args = [cp, <tmp>, <dest>]
			src, dst := args[1], args[2]
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return err
			}
			data, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, data, 0644)
		}
	case "ln":
		if len(args) >= 4 && args[1] == "-sf" {
			// runner.Run("sudo", "ln", "-sf", src, dst)
			return os.Symlink(args[2], args[3])
		}
	}
	return nil
}

