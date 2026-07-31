#!/bin/sh
# Claim root=tbox:… for the tbox-live module. Accepted forms:
#
#   root=tbox:CDLABEL=<iso-label>   (canonical — written by tacklebox build)
#   root=tbox:LABEL=<label>
#   root=tbox:/dev/<path>
#
# Deliberately NOT root=live:… — if an image also ships dmsquash-live,
# both modules would try to claim the same root. The tbox: scheme keeps
# exactly one owner of the live boot path.
#
# This is a dracut cmdline hook: it is sourced, so no exit; getarg &
# friends come from dracut-lib.sh which the hook runner has sourced.
#
# shellcheck disable=SC2034  # rootok is consumed by dracut-cmdline.sh

[ -z "$root" ] && root=$(getarg root=)

# tboxroot tracks whether WE claimed this root. Testing rootok instead
# would misfire: cmdline hooks share shell state, and on block boots
# (root=LABEL=…) dracut's own parse-root hook has already set rootok=1
# before this hook runs — acting on it armed a wait_for_dev on a marker
# that never comes and hung dracut-initqueue until timeout.
tboxroot=""

case "$root" in
tbox:CDLABEL=* | tbox:LABEL=*)
    label=${root#tbox:}
    label=${label#*LABEL=}
    # udev escapes '/' and ' ' in /dev/disk/by-label names
    label=$(echo "$label" | sed 's,/,\\x2f,g;s, ,\\x20,g')
    root="tbox:/dev/disk/by-label/${label}"
    rootok=1
    tboxroot=1
    ;;
tbox:/dev/*)
    rootok=1
    tboxroot=1
    ;;
esac

if [ "$tboxroot" = "1" ]; then
    # The device path travels via a file, NOT argv: initqueue stores its
    # command as a shell line and re-parses it, so an unquoted \x20
    # (udev's space escape in by-label paths) collapses to x20 and the
    # path never matches (observed: label 'TunaOS Yellowfin', run
    # 29627295208). A file survives any characters.
    printf '%s' "${root#tbox:}" > /run/tbox-live-root.dev

    # A stock initramfs only contains the hook directories its own modules
    # asked for at build time. On the appended-overlay path (the browser
    # builder, and purebuild without --initrd) we ride an initrd that was
    # never built with us in it, and EL10's ships no initqueue tree at all.
    # Neither initqueue nor wait_for_dev creates $hookdir — they `mv` and
    # redirect straight into it — so every hook below silently failed to
    # install while dracut-cmdline still reported success:
    #
    #   mv: cannot move '/tmp/352-tbox-live-root.sh' to
    #       '/lib/dracut/hooks/initqueue/settled/tbox-live-root.sh':
    #       No such file or directory
    #
    # tbox-live-root then never ran, and the boot fell through to the
    # image's own bootc root setup, which died on
    # `overlayfs: failed to resolve '/run/rootfsbase': -2`. That is the
    # whole of tacklebox#166 — the overlay was unit-tested and had never
    # been booted. First observed on tacklebox run 30626431386
    # (yellowfin:niri, purebuild + pure-iso boot gate).
    #
    # Creating the directories is safe on the built-in path too: dracut
    # made them already, so this is a no-op there.
    for d in "" /settled /finished /online /timeout; do
        mkdir -p "${hookdir:-/lib/dracut/hooks}/initqueue$d"
    done
    mkdir -p "${hookdir:-/lib/dracut/hooks}/emergency" /etc/udev/rules.d

    # Assemble the live root on the first udev-settled initqueue pass
    # where the ISO device exists (USB/CD enumeration can take a while).
    # The initqueue "finished" condition must be the DONE MARKER our
    # script writes, not the device itself: dracut-initqueue exits the
    # moment a finished check passes, and waiting on the device would
    # let it exit before the settled queue (and thus our mounts) ever
    # ran on a device that was present from the start.
    # initqueue is only present when a dracut module requested it at BUILD
    # time (dracut_need_initqueue). A tbox-appended-overlay initramfs (the
    # browser builder path) rides a stock initrd that may not include it —
    # fall back to a udev rule that runs tbox-live-root when the labeled
    # device appears. Both paths converge on the /run done marker.
    if command -v initqueue > /dev/null 2>&1; then
        initqueue --settled --unique /sbin/tbox-live-root
    elif [ -x /sbin/initqueue ]; then
        /sbin/initqueue --settled --unique /sbin/tbox-live-root
    else
        # No initqueue: drive tbox-live-root from udev directly. The label
        # symlink appearing is the trigger; the script is idempotent and
        # writes the done marker itself.
        {
            echo 'SUBSYSTEM=="block", ACTION=="add|change", ENV{ID_FS_LABEL}!="", RUN+="/sbin/tbox-live-root"'
            echo 'SUBSYSTEM=="block", KERNEL=="sr[0-9]*", ACTION=="add|change", RUN+="/sbin/tbox-live-root"'
        } > /etc/udev/rules.d/99-tbox-live.rules
        udevadm control --reload 2> /dev/null || true
        udevadm trigger --action=change --subsystem-match=block 2> /dev/null || true
        # Also try immediately in case the device is already present.
        /sbin/tbox-live-root 2> /dev/null || true
    fi
    wait_for_dev -n /run/tacklebox-live-done
fi
