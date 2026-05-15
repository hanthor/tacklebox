package install

import (
	"os"

	"github.com/tuna-os/tacklebox/internal/runner"
)

// UserCommandPrefix returns the command prefix for running a command as the
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
//	["sudo", "-u", "<SUDO_USER>", "-H", "--preserve-env=PATH", "<command>"]
//
// If SUDO_USER is not set (running as root directly, or as the target user
// already) returns ["podman"] unchanged.
func UserCommandPrefix(command string) []string {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" || sudoUser == "root" {
		return []string{command}
	}
	// -H   : set HOME to the target user's home so podman finds its config,
	//        XDG_RUNTIME_DIR, and container storage correctly.
	// --preserve-env keeps caller-provided runtime/storage roots when building
	// large media in constrained environments (e.g. /var/home nearly full).
	// PATH is required so the user's podman binary (e.g. linuxbrew) is found.
	// TMPDIR/XDG_* and CONTAINERS_STORAGE_CONF are optional but critical when
	// callers intentionally redirect scratch/storage into /var/tmp.
	return []string{
		"sudo",
		"-u", sudoUser,
		"-H",
		"--preserve-env=PATH,TMPDIR,XDG_RUNTIME_DIR,XDG_DATA_HOME,CONTAINERS_STORAGE_CONF",
		command,
	}
}

// UserPodmanPrefix returns the command prefix for running podman as the
// original user.
func UserPodmanPrefix() []string { return UserCommandPrefix("podman") }

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
