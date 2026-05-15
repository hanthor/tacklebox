package install

import (
	"io"
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
