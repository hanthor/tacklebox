// Package tacklebox holds repo-level assets embedded into the binary so
// that internal packages can use them without the source checkout being
// present at runtime (installed binaries, CI runners, containers).
package tacklebox

import "embed"

// DracutTboxRoot is the 95tbox-root dracut module source. PrepareInitramfs
// (internal/install) materializes it to a temp dir and bind-mounts it into
// the dracut rebuild container at /usr/lib/dracut/modules.d/95tbox-root.
//
//go:embed src/dracut/95tbox-root
var DracutTboxRoot embed.FS
