package install

import (
	"os"

	"github.com/tuna-os/tacklebox/internal/runner"
)

// UserPodmanPrefix returns the command prefix for running podman as the
// original (non-root) user when tacklebox has been invoked via sudo.
//
// The problem it solves:
//   - 'sudo tacklebox build' runs as root (UID 0).
//   - Container images built with plain 'podman build' live in the invoking
//     user's store (~/.local/share/containers/storage), not root's store.
//   - 'podman unshare' requires a non-root user ("please use unshare with
//     rootless") — it cannot be called as root.
//
// When SUDO_USER is set, this returns a prefix that drops back to that user:
//
//	["sudo", "-u", "<SUDO_USER>", "-H", "--preserve-env=PATH", "podman"]
//
// If SUDO_USER is not set (running as root directly, or as the target user
// already) returns ["podman"] unchanged.
func UserPodmanPrefix() []string {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" || sudoUser == "root" {
		return []string{"podman"}
	}
	// -H   : set HOME to the target user's home so podman finds its config,
	//        XDG_RUNTIME_DIR, and container storage correctly.
	// --preserve-env=PATH : keep the caller's PATH so the user's podman
	//        binary (e.g. linuxbrew) is found rather than /usr/bin/podman.
	return []string{"sudo", "-u", sudoUser, "-H", "--preserve-env=PATH", "podman"}
}

// RunUnshare executes a shell script inside `podman unshare` as the
// original (non-root) user. This is required for:
//
//   - Accessing localhost/ images in the user's container store.
//   - Getting correct UID mappings when mksquashfs-ing overlay layer dirs.
//
// The script string is passed directly to `sh -c`; use shellEsc() from
// live.go to safely interpolate variable values into it.
func RunUnshare(script string) error {
	prefix := UserPodmanPrefix()
	// Build: <prefix> unshare -- sh -c <script>
	// e.g.  sudo -u james -H --preserve-env=PATH podman unshare -- sh -c '...'
	args := make([]string, 0, len(prefix)+4)
	args = append(args, prefix[1:]...)
	args = append(args, "unshare", "--", "sh", "-c", script)
	return runner.Run(prefix[0], args...)
}
