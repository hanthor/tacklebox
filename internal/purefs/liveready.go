package purefs

import (
	"fmt"
	"strings"

	"github.com/tuna-os/tacklebox/internal/oci"
)

// DefaultLiveMarker is the neutral readiness string the baked unit prints.
// tacklebox is a generic bootc ISO maker, so nothing distro-branded goes
// into images it builds; harnesses that poll a serial log for readiness
// (e.g. tuna-os/tunaos scripts/iso-e2e.sh) accept this alongside their
// own distro marker, and callers can override it per build.
const DefaultLiveMarker = "TBOX_LIVE_READY"

// liveReadyUnit prints the marker to the kernel console once the live
// environment is usable. After= entries naming units an image does not
// ship (livesys.service outside tunaOS) are ignored by systemd, so the
// same text is safe on every base.
const liveReadyUnit = `[Unit]
Description=Tacklebox live environment ready marker
After=livesys.service NetworkManager-wait-online.service graphical.target multi-user.target
Wants=NetworkManager-wait-online.service

[Service]
Type=oneshot
RemainAfterExit=yes
StandardOutput=journal+console
StandardError=journal+console
ExecStart=/bin/sh -c 'echo "%s uptime=$(awk "{print \\$1}" /proc/uptime)"'
ExecStart=/bin/sh -c 'sleep 2 && echo "%s uptime=$(awk "{print \\$1}" /proc/uptime)"'

[Install]
WantedBy=multi-user.target
`

// EnsureLiveReadyMarker bakes a readiness marker into any image the
// builder processes. Without one, a perfectly booted live ISO of an
// image that ships no readiness unit of its own times a polling harness
// out (iso-builder run 31095386421: aurora's serial went quiet at ~95 s
// — a healthy KDE boot reaching its graphical target — then 13 idle
// minutes). marker "" means DefaultLiveMarker; an image that already
// carries its own readiness unit (tunaOS ships
// tunaos-live-ready.service) is left untouched.
func EnsureLiveReadyMarker(root *oci.Node, store oci.BlobStore, marker string) error {
	const unit = "usr/lib/systemd/system/tbox-live-ready.service"
	for _, own := range []string{unit, "usr/lib/systemd/system/tunaos-live-ready.service"} {
		if n := root.Lookup(own); n != nil {
			return nil
		}
	}
	if marker == "" {
		marker = DefaultLiveMarker
	}
	if strings.ContainsAny(marker, "'\"\n") {
		return fmt.Errorf("live marker %q must not contain quotes or newlines", marker)
	}
	body := fmt.Sprintf(strings.ReplaceAll(liveReadyUnit, "%s", "%[1]s"), marker)
	if err := writeFileNode(root, store, unit, body, 0o644); err != nil {
		return err
	}
	symlinkNode(root, "usr/lib/systemd/system/multi-user.target.wants/tbox-live-ready.service",
		"../tbox-live-ready.service")
	return nil
}
