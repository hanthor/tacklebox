#!/bin/bash
check() { return 0; }
depends() { echo "base rootfs-block"; return 0; }
install() {
    # MUST run after rootfs-block has mounted the partition at /sysroot,
    # but BEFORE ostree-prepare-root (which lives at pre-pivot priority 30
    # in the ostree dracut module). The previous registration at pre-mount
    # ran before /sysroot was populated, so the subdir check silently
    # failed and ostree-prepare-root then crashed at switch_root.
    inst_hook pre-pivot 10 "$moddir/tbox-root-mount.sh"
}
