// Package oci is the pure-Go, root-free replacement for tacklebox's podman
// shell-outs (ADR: tunaOS docs/adr/0002-browser-iso-builder.md). It pulls
// images straight from an OCI registry and reconstructs their final rootfs
// as an in-memory tree with overlay semantics, so downstream consumers
// (squashfs writer, offline store) never need a mounted filesystem, a
// container runtime, or sudo. The same code compiles to native (CI) and
// WASM (the browser ISO builder), which is what keeps the two in lockstep.
package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client speaks the small read-only slice of the distribution API the
// builder needs: token, manifest (index + platform), config, blobs.
type Client struct {
	// Base is the registry root, e.g. "https://ghcr.io" or a CORS shim.
	Base string
	// HTTP lets tests and the WASM build substitute a transport.
	HTTP *http.Client
	// SkipBodies drops file bodies of matching paths during Unpack —
	// boot-irrelevant junk (tmp/, caches) never reaches the blob store.
	SkipBodies func(path string) bool

	token string
}

func NewClient(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: http.DefaultClient}
}

// Ref names an image inside one registry, e.g. Repo "tuna-os/yellowfin",
// Tag "kde" (or a digest).
type Ref struct {
	Repo string
	Tag  string
}

type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	} `json:"platform,omitempty"`
}

type Manifest struct {
	MediaType string       `json:"mediaType"`
	Manifests []Descriptor `json:"manifests,omitempty"` // index form
	Config    Descriptor   `json:"config"`
	Layers    []Descriptor `json:"layers,omitempty"`
}

const acceptManifest = "application/vnd.oci.image.index.v1+json, " +
	"application/vnd.oci.image.manifest.v1+json, " +
	"application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.docker.distribution.manifest.v2+json"

func (c *Client) authorize(repo string) error {
	if c.token != "" {
		return nil
	}
	url := fmt.Sprintf("%s/token?scope=repository:%s:pull", c.Base, repo)
	resp, err := c.HTTP.Get(url)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token: HTTP %d", resp.StatusCode)
	}
	var t struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return fmt.Errorf("token decode: %w", err)
	}
	c.token = t.Token
	return nil
}

func (c *Client) get(repo, path, accept string) (*http.Response, error) {
	if err := c.authorize(repo); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v2/%s/%s", c.Base, repo, path), nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return resp, nil
}

// ResolveManifest fetches ref's manifest, following one level of index
// indirection to the requested architecture (bare arch, no variant —
// matching the CI build matrix's published_tag layout).
func (c *Client) ResolveManifest(ref Ref, arch string) (*Manifest, error) {
	resp, err := c.get(ref.Repo, "manifests/"+ref.Tag, acceptManifest)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest decode: %w", err)
	}
	if len(m.Manifests) == 0 {
		return &m, nil
	}
	var pick *Descriptor
	for i := range m.Manifests {
		d := &m.Manifests[i]
		if d.Platform != nil && d.Platform.Architecture == arch && d.Platform.Variant == "" {
			pick = d
			break
		}
	}
	if pick == nil {
		pick = &m.Manifests[0]
	}
	resp2, err := c.get(ref.Repo, "manifests/"+pick.Digest, acceptManifest)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	var pm Manifest
	if err := json.NewDecoder(resp2.Body).Decode(&pm); err != nil {
		return nil, fmt.Errorf("platform manifest decode: %w", err)
	}
	return &pm, nil
}

// Blob streams a blob while verifying its digest: the returned reader
// yields the blob's bytes and its Close reports a digest mismatch as an
// error, so callers that consume to EOF get integrity for free.
func (c *Client) Blob(ref Ref, d Descriptor) (io.ReadCloser, error) {
	resp, err := c.get(ref.Repo, "blobs/"+d.Digest, "")
	if err != nil {
		return nil, err
	}
	return &verifyingReader{body: resp.Body, want: d.Digest, hash: sha256.New()}, nil
}

type verifyingReader struct {
	body io.ReadCloser
	want string
	hash interface {
		io.Writer
		Sum([]byte) []byte
	}
	eof bool
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.body.Read(p)
	if n > 0 {
		v.hash.Write(p[:n])
	}
	if err == io.EOF {
		v.eof = true
	}
	return n, err
}

func (v *verifyingReader) Close() error {
	defer v.body.Close()
	if !v.eof {
		// Caller abandoned the stream; nothing to assert.
		return nil
	}
	got := "sha256:" + hex.EncodeToString(v.hash.Sum(nil))
	if got != v.want {
		return fmt.Errorf("digest mismatch: got %s want %s", got, v.want)
	}
	return nil
}
