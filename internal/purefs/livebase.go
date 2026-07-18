package purefs

import (
	"fmt"
	"io"
	"strings"

	"github.com/tuna-os/tacklebox/internal/oci"
)

// EnsureLiveUser bakes a passwordless live user into the rootfs tree —
// tacklebox's distro-agnostic replacement for livesys-scripts (absent on
// EL10/openSUSE/GNOME OS images; dakota-iso creates the user manually for
// the same reason). Running at authoring time instead of boot time means
// the account exists in the squash itself: display-manager autologin
// (written by the recipe's live_customize desktop adapter) works on first
// boot with no boot-time service dependency.
//
// Pure tree surgery — the same code runs in CI and in the browser build.
func EnsureLiveUser(root *oci.Node, store oci.BlobStore, name string, uid int) error {
	if n := lookupEtc(root, "passwd"); n != nil {
		if content, err := readFile(store, n); err == nil && strings.Contains(content, "\n"+name+":") {
			return nil // already present (image ships its own live user)
		}
	}

	home := "/var/home/" + name
	if root.Lookup("var/home") == nil && root.Lookup("home") != nil {
		home = "/home/" + name
	}

	edits := []struct{ file, line string }{
		{"passwd", fmt.Sprintf("%s:x:%d:%d:Live User:%s:/bin/bash", name, uid, uid, home)},
		// Empty password field: passwordless login permitted (dakota's
		// `passwd --delete liveuser` equivalent).
		{"shadow", name + "::20000:0:99999:7:::"},
		{"group", fmt.Sprintf("%s:x:%d:", name, uid)},
		{"gshadow", name + ":!::"},
	}
	for _, e := range edits {
		n := lookupEtc(root, e.file)
		if n == nil {
			if e.file == "gshadow" {
				continue // optional on some distros
			}
			return fmt.Errorf("no /etc/%s in rootfs", e.file)
		}
		content, err := readFile(store, n)
		if err != nil {
			return fmt.Errorf("read /etc/%s: %w", e.file, err)
		}
		if !strings.HasSuffix(content, "\n") && content != "" {
			content += "\n"
		}
		content += e.line + "\n"
		ref, size, err := store.Put(strings.NewReader(content))
		if err != nil {
			return fmt.Errorf("store /etc/%s: %w", e.file, err)
		}
		n.Ref = ref
		n.Size = size
	}

	// Home directory (mode 0700, owned by the user), seeded from /etc/skel.
	parts := strings.Split(strings.TrimPrefix(home, "/"), "/")
	dir := root
	for _, p := range parts[:len(parts)-1] {
		c, ok := dir.Children[p]
		if !ok || c.Type != oci.TypeDir {
			c = &oci.Node{Type: oci.TypeDir, Mode: 0o755, Children: map[string]*oci.Node{}}
			dir.Children[p] = c
		}
		dir = c
	}
	userHome := &oci.Node{Type: oci.TypeDir, Mode: 0o700, UID: uid, GID: uid, Children: map[string]*oci.Node{}}
	dir.Children[parts[len(parts)-1]] = userHome
	if skel := root.Lookup("etc/skel"); skel != nil && skel.Type == oci.TypeDir {
		copySkel(skel, userHome, uid)
	}
	return nil
}

func lookupEtc(root *oci.Node, name string) *oci.Node {
	n := root.Lookup("etc/" + name)
	if n == nil || n.Type != oci.TypeFile {
		return nil
	}
	return n
}

func readFile(store oci.BlobStore, n *oci.Node) (string, error) {
	r, err := store.Open(n.Ref)
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	return string(b), err
}

func copySkel(skel, dst *oci.Node, uid int) {
	for name, c := range skel.Children {
		switch c.Type {
		case oci.TypeDir:
			nd := &oci.Node{Type: oci.TypeDir, Mode: c.Mode, UID: uid, GID: uid, Children: map[string]*oci.Node{}}
			dst.Children[name] = nd
			copySkel(c, nd, uid)
		case oci.TypeFile:
			dst.Children[name] = &oci.Node{Type: oci.TypeFile, Mode: c.Mode, UID: uid, GID: uid, Ref: c.Ref, Size: c.Size}
		case oci.TypeSymlink:
			dst.Children[name] = &oci.Node{Type: oci.TypeSymlink, Mode: c.Mode, UID: uid, GID: uid, Target: c.Target}
		}
	}
}
