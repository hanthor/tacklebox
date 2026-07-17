package oci

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Node is one entry in the reconstructed rootfs. The tree is pure
// metadata; regular-file bodies live in a BlobStore so a multi-GB root
// never has to fit in memory.
type Node struct {
	Type     NodeType
	Mode     int64
	UID, GID int
	Size     int64
	// Ref addresses the body in the BlobStore (files only).
	Ref string
	// Target is the symlink target or hardlink destination path.
	Target string
	// Devmajor/Devminor for device nodes.
	Devmajor, Devminor int64
	Xattrs             map[string][]byte
	Children           map[string]*Node // dirs only
}

type NodeType uint8

const (
	TypeDir NodeType = iota
	TypeFile
	TypeSymlink
	TypeHardlink
	TypeChar
	TypeBlock
	TypeFifo
)

// BlobStore holds file bodies. Implementations: MemStore (tests, WASM
// first cut), DirStore (native builds — content-addressed temp files).
type BlobStore interface {
	Put(r io.Reader) (ref string, size int64, err error)
	Open(ref string) (io.ReadCloser, error)
}

// Unpack streams every layer of m bottom-to-top and applies OCI overlay
// semantics — whiteouts (.wh.<name>), opaque markers (.wh..wh..opq),
// replacement — returning the image's final rootfs tree. No mounts, no
// privileges: ownership and modes live in the tree, not on any real
// filesystem.
func (c *Client) Unpack(ref Ref, m *Manifest, store BlobStore, progress func(layer int, total int)) (*Node, error) {
	root := &Node{Type: TypeDir, Mode: 0o755, Children: map[string]*Node{}}
	for i, layer := range m.Layers {
		if progress != nil {
			progress(i, len(m.Layers))
		}
		body, err := c.Blob(ref, layer)
		if err != nil {
			return nil, fmt.Errorf("layer %d: %w", i, err)
		}
		if err := applyLayer(root, body, layer.MediaType, store); err != nil {
			body.Close()
			return nil, fmt.Errorf("layer %d: %w", i, err)
		}
		if err := body.Close(); err != nil {
			return nil, fmt.Errorf("layer %d: %w", i, err)
		}
	}
	return root, nil
}

func applyLayer(root *Node, r io.Reader, mediaType string, store BlobStore) error {
	var tr *tar.Reader
	switch {
	case strings.Contains(mediaType, "zstd"):
		zr, err := zstd.NewReader(r)
		if err != nil {
			return err
		}
		defer zr.Close()
		tr = tar.NewReader(zr)
	case strings.Contains(mediaType, "gzip"):
		gz, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr = tar.NewReader(gz)
	default:
		tr = tar.NewReader(r)
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := applyEntry(root, hdr, tr, store); err != nil {
			return fmt.Errorf("%s: %w", hdr.Name, err)
		}
	}
}

func splitClean(name string) []string {
	p := path.Clean("/" + name)
	if p == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

// dirOf walks (creating) to the parent of parts, returning it plus the
// final name component.
func dirOf(root *Node, parts []string) (*Node, string) {
	n := root
	for _, p := range parts[:len(parts)-1] {
		c, ok := n.Children[p]
		if !ok || c.Type != TypeDir {
			c = &Node{Type: TypeDir, Mode: 0o755, Children: map[string]*Node{}}
			n.Children[p] = c
		}
		n = c
	}
	return n, parts[len(parts)-1]
}

func applyEntry(root *Node, hdr *tar.Header, body io.Reader, store BlobStore) error {
	parts := splitClean(hdr.Name)
	if parts == nil {
		if hdr.Typeflag == tar.TypeDir {
			root.Mode = hdr.Mode
			root.UID, root.GID = hdr.Uid, hdr.Gid
		}
		return nil
	}
	parent, name := dirOf(root, parts)

	if name == ".wh..wh..opq" {
		parent.Children = map[string]*Node{}
		return nil
	}
	if strings.HasPrefix(name, ".wh.") {
		delete(parent.Children, strings.TrimPrefix(name, ".wh."))
		return nil
	}

	node := &Node{Mode: hdr.Mode, UID: hdr.Uid, GID: hdr.Gid}
	if len(hdr.PAXRecords) > 0 {
		for k, v := range hdr.PAXRecords {
			if s, ok := strings.CutPrefix(k, "SCHILY.xattr."); ok {
				if node.Xattrs == nil {
					node.Xattrs = map[string][]byte{}
				}
				node.Xattrs[s] = []byte(v)
			}
		}
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		if existing, ok := parent.Children[name]; ok && existing.Type == TypeDir {
			existing.Mode, existing.UID, existing.GID = hdr.Mode, hdr.Uid, hdr.Gid
			if node.Xattrs != nil {
				existing.Xattrs = node.Xattrs
			}
			return nil
		}
		node.Type = TypeDir
		node.Children = map[string]*Node{}
	case tar.TypeReg:
		ref, size, err := store.Put(body)
		if err != nil {
			return err
		}
		node.Type = TypeFile
		node.Ref = ref
		node.Size = size
	case tar.TypeSymlink:
		node.Type = TypeSymlink
		node.Target = hdr.Linkname
	case tar.TypeLink:
		node.Type = TypeHardlink
		node.Target = path.Clean("/" + hdr.Linkname)[1:]
	case tar.TypeChar:
		node.Type = TypeChar
		node.Devmajor, node.Devminor = hdr.Devmajor, hdr.Devminor
	case tar.TypeBlock:
		node.Type = TypeBlock
		node.Devmajor, node.Devminor = hdr.Devmajor, hdr.Devminor
	case tar.TypeFifo:
		node.Type = TypeFifo
	default:
		return nil // pax headers etc. — already consumed by archive/tar
	}
	parent.Children[name] = node
	return nil
}

// Lookup resolves a slash path inside the tree (no link following).
func (n *Node) Lookup(p string) *Node {
	cur := n
	for _, part := range splitClean(p) {
		if cur == nil || cur.Type != TypeDir {
			return nil
		}
		cur = cur.Children[part]
	}
	return cur
}

// Walk visits every node depth-first in sorted order — deterministic
// iteration is what makes two builds of the same image byte-identical.
func (n *Node) Walk(fn func(path string, node *Node) error) error {
	var rec func(prefix string, d *Node) error
	rec = func(prefix string, d *Node) error {
		names := make([]string, 0, len(d.Children))
		for name := range d.Children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := d.Children[name]
			p := path.Join(prefix, name)
			if err := fn(p, c); err != nil {
				return err
			}
			if c.Type == TypeDir {
				if err := rec(p, c); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return rec("", n)
}

// ── Stores ────────────────────────────────────────────────────────────────

// MemStore keeps bodies in memory. Fine for tests and small trees.
type MemStore struct {
	blobs [][]byte
}

func (s *MemStore) Put(r io.Reader) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	s.blobs = append(s.blobs, b)
	return fmt.Sprintf("m%d", len(s.blobs)-1), int64(len(b)), nil
}

func (s *MemStore) Open(ref string) (io.ReadCloser, error) {
	var i int
	if _, err := fmt.Sscanf(ref, "m%d", &i); err != nil || i < 0 || i >= len(s.blobs) {
		return nil, fmt.Errorf("bad ref %q", ref)
	}
	return io.NopCloser(strings.NewReader(string(s.blobs[i]))), nil
}

// DirStore spills bodies to numbered files under dir — the native builds'
// store. Content lands with the invoking user's ownership; image ownership
// stays in the tree, applied by the squashfs writer, never by chown.
type DirStore struct {
	Dir string
	n   int
}

func (s *DirStore) Put(r io.Reader) (string, int64, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", 0, err
	}
	s.n++
	name := filepath.Join(s.Dir, fmt.Sprintf("b%08d", s.n))
	f, err := os.Create(name)
	if err != nil {
		return "", 0, err
	}
	size, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	return name, size, nil
}

func (s *DirStore) Open(ref string) (io.ReadCloser, error) {
	return os.Open(ref)
}
