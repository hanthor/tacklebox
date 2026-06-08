package install

import (
	"testing"
)

func TestParseBackend_Ostree(t *testing.T) {
	// simulate the output of `skopeo inspect` for an ostree-based image
	inspectJSON := `{
  "Name": "quay.io/centos-bootc/centos-bootc",
  "Digest": "sha256:abc123",
  "RepoTags": ["stream10"],
  "Labels": {
    "ostree.bootable": "true",
    "ostree.commit": "abc123"
  }
}`

	got := parseBackend(inspectJSON)
	if got != BackendOstree {
		t.Errorf("parseBackend(ostree json) = %q, want %q", got, BackendOstree)
	}
}

func TestParseBackend_Composefs(t *testing.T) {
	// composefs images don't have the "ostree" string in their inspect output
	inspectJSON := `{
  "Name": "quay.io/fedora/fedora-bootc",
  "Digest": "sha256:def456",
  "RepoTags": ["42"],
  "Labels": {}
}`

	got := parseBackend(inspectJSON)
	if got != BackendComposefs {
		t.Errorf("parseBackend(composefs json) = %q, want %q", got, BackendComposefs)
	}
}

func TestParseBackend_OstreeInConfig(t *testing.T) {
	// "ostree" appears outside of Labels too.
	inspectJSON := `{
  "Name": "some-image",
  "Config": {
    "Env": ["PATH=/usr/bin"],
    "Cmd": ["/usr/libexec/ostree-prepare-root"]
  }
}`

	got := parseBackend(inspectJSON)
	if got != BackendOstree {
		t.Errorf("parseBackend(with ostree ref) = %q, want %q", got, BackendOstree)
	}
}
