package install

import (
	"testing"

	"github.com/tuna-os/tacklebox/internal/runner"
)

func TestDetectBackend_Ostree(t *testing.T) {
	oldOutputFn := runner.OutputFn
	defer func() { runner.OutputFn = oldOutputFn }()

	runner.OutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "skopeo" && len(args) >= 2 && args[0] == "inspect" {
			return []byte(`{"Labels":{"ostree.bootable":"true","ostree.commit":"abc123"}}`), nil
		}
		return nil, nil
	}

	backend, err := DetectBackend("quay.io/centos-bootc/centos-bootc:stream10")
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if backend != BackendOstree {
		t.Errorf("backend = %q, want %q", backend, BackendOstree)
	}
}

func TestDetectBackend_Composefs(t *testing.T) {
	oldOutputFn := runner.OutputFn
	defer func() { runner.OutputFn = oldOutputFn }()

	runner.OutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "skopeo" && len(args) >= 2 && args[0] == "inspect" {
			return []byte(`{"Name":"fedora-bootc","Labels":{}}`), nil
		}
		return nil, nil
	}

	backend, err := DetectBackend("quay.io/fedora/fedora-bootc:42")
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if backend != BackendComposefs {
		t.Errorf("backend = %q, want %q", backend, BackendComposefs)
	}
}

func TestDetectBackend_OstreeInConfig(t *testing.T) {
	oldOutputFn := runner.OutputFn
	defer func() { runner.OutputFn = oldOutputFn }()

	runner.OutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "skopeo" && len(args) >= 2 && args[0] == "inspect" {
			return []byte(`{"Config":{"Cmd":["/usr/libexec/ostree-prepare-root"]}}`), nil
		}
		return nil, nil
	}

	backend, err := DetectBackend("some-registry/some-image:latest")
	if err != nil {
		t.Fatalf("DetectBackend: %v", err)
	}
	if backend != BackendOstree {
		t.Errorf("backend = %q, want %q", backend, BackendOstree)
	}
}
