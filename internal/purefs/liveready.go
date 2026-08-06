package purefs

import (
	"github.com/tuna-os/tacklebox/internal/oci"
)

// liveReadyUnit mirrors tunaOS's tunaos-live-ready.service
// (system_files/usr/lib/systemd/system/): print TUNAOS_LIVE_READY to the
// kernel console once the live environment is usable. The e2e harness
// (tuna-os/tunaos scripts/iso-e2e.sh) polls a QEMU serial log for that
// string before it will install or SSH. After= entries naming units an
// image does not ship (livesys.service on non-tunaOS bases) are ignored
// by systemd, so the same text is safe everywhere.
const liveReadyUnit = `[Unit]
Description=TunaOS live environment ready marker
After=livesys.service NetworkManager-wait-online.service graphical.target multi-user.target
Wants=NetworkManager-wait-online.service

[Service]
Type=oneshot
RemainAfterExit=yes
StandardOutput=journal+console
StandardError=journal+console
ExecStart=/bin/sh -c 'echo "TUNAOS_LIVE_READY uptime=$(awk "{print \\$1}" /proc/uptime)"'
ExecStart=/bin/sh -c 'sleep 2 && echo "TUNAOS_LIVE_READY uptime=$(awk "{print \\$1}" /proc/uptime)"'

[Install]
WantedBy=multi-user.target
`

// EnsureLiveReadyMarker bakes the readiness marker into any image the
// builder processes. tunaOS images ship the unit themselves; a reference
// image (aurora, bluefin) does not — so its live ISO boots fine and the
// harness still times out at 900 s waiting for a marker that cannot
// come (iso-builder run 31095386421: serial went quiet at ~95 s, a
// healthy KDE boot reaching its graphical target, then 13 idle minutes).
// Idempotent, and an image's own richer unit is left untouched.
func EnsureLiveReadyMarker(root *oci.Node, store oci.BlobStore) error {
	const unit = "usr/lib/systemd/system/tunaos-live-ready.service"
	if n := root.Lookup(unit); n != nil {
		return nil
	}
	if err := writeFileNode(root, store, unit, liveReadyUnit, 0o644); err != nil {
		return err
	}
	symlinkNode(root, "usr/lib/systemd/system/multi-user.target.wants/tunaos-live-ready.service",
		"../tunaos-live-ready.service")
	return nil
}
