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

    # Whether we can use initqueue at all comes down to one thing: can we
    # write into $hookdir. On the appended-overlay path (the browser
    # builder, and purebuild without --initrd) we ride a stock initrd that
    # was never built with us in it, so our queue directories were never
    # created — and on EL10 they cannot be created afterwards:
    #
    #   mkdir: cannot create directory '/lib/dracut/hooks/initqueue':
    #   Read-only file system                       (run 30628621563)
    #
    # $hookdir is /lib/dracut/hooks there, which is on the image's
    # read-only /usr. The existing fallback below tested the wrong thing:
    # it asks whether initqueue EXISTS. It does — /usr/sbin/initqueue —
    # and it then fails on a `mv` into a directory nothing can create:
    #
    #   mv: cannot move '/tmp/367-tbox-live-root.sh' to
    #       '/lib/dracut/hooks/initqueue/settled/tbox-live-root.sh':
    #       No such file or directory
    #
    # tbox-live-root never ran, and the boot fell through to the image's
    # own bootc root setup, dying on
    # `overlayfs: failed to resolve '/run/rootfsbase': -2`. That is the
    # whole of tacklebox#166: this path was unit-tested and had never been
    # booted. Three boots of yellowfin:niri failed here before the hook
    # was made to print $hookdir and the mkdir errno.
    #
    # Try to create them (a no-op on the built-in path, where dracut made
    # them at build time), then decide from what actually exists.
    hd=${hookdir:-/lib/dracut/hooks}
    for d in "" /settled /finished /online /timeout; do
        mkdir -p "$hd/initqueue$d" 2> /dev/null || true
    done
    mkdir -p "$hd/emergency" 2> /dev/null || true
    mkdir -p /etc/udev/rules.d 2> /dev/null || true

    # Assemble the live root on the first udev-settled initqueue pass
    # where the ISO device exists (USB/CD enumeration can take a while).
    # The initqueue "finished" condition must be the DONE MARKER our
    # script writes, not the device itself: dracut-initqueue exits the
    # moment a finished check passes, and waiting on the device would
    # let it exit before the settled queue (and thus our mounts) ever
    # ran on a device that was present from the start.
    #
    # The test is the DIRECTORY, not the command: see above.
    if [ -d "$hd/initqueue/settled" ] && command -v initqueue > /dev/null 2>&1; then
        initqueue --settled --unique /sbin/tbox-live-root
        # wait_for_dev writes into $hookdir too, so it is only meaningful
        # on this branch. On the fallback branch tbox-live-root has
        # already run synchronously and the marker is either there or the
        # boot is lost anyway.
        wait_for_dev -n /run/tacklebox-live-done
    elif [ -d "$hd/initqueue/settled" ] && [ -x /sbin/initqueue ]; then
        /sbin/initqueue --settled --unique /sbin/tbox-live-root
        wait_for_dev -n /run/tacklebox-live-done
    else
        # No usable queue directory (or no initqueue): drive
        # tbox-live-root from udev directly. The label symlink appearing
        # is the trigger; the script is idempotent and writes the done
        # marker itself.
        #
        # Nothing waits for the marker on this branch, deliberately. udev
        # is not running yet — systemd-udevd starts after
        # dracut-cmdline.service — so blocking here would deadlock until
        # timeout and the rule could never fire. The ordering that makes
        # this safe is in tbox-live-generator: sysroot.mount is
        # After=dracut-initqueue.service, which runs once udev has
        # settled, by which time the rule has fired. The synchronous call
        # below only covers a device that was already enumerated.
        {
            echo 'SUBSYSTEM=="block", ACTION=="add|change", ENV{ID_FS_LABEL}!="", RUN+="/sbin/tbox-live-root"'
            echo 'SUBSYSTEM=="block", KERNEL=="sr[0-9]*", ACTION=="add|change", RUN+="/sbin/tbox-live-root"'
        } > /etc/udev/rules.d/99-tbox-live.rules
        udevadm control --reload 2> /dev/null || true
        udevadm trigger --action=change --subsystem-match=block 2> /dev/null || true
        # Also try immediately in case the device is already present.
        /sbin/tbox-live-root 2> /dev/null || true
    fi
fi
