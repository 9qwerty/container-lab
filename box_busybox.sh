#!/usr/bin/env bash

set -euo pipefail

NAME="busybox"
HOSTNAME="mycontainer"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --hostname)
            HOSTNAME="${2:?--hostname requires a value}"
            shift 2
            ;;
        *)
            NAME="$1"
            shift
            ;;
    esac
done

CHROOT_DIR="$HOME/chroot/$NAME"
BUSYBOX="$(command -v busybox)"

mkdir -p \
    "$CHROOT_DIR/bin" \
    "$CHROOT_DIR/proc" \
    "$CHROOT_DIR/dev" \
    "$CHROOT_DIR/sys" \
    "$CHROOT_DIR/run" \
    "$CHROOT_DIR/etc"

# Copy busybox to chroot if it doesn't exist
if [[ ! -f "$CHROOT_DIR/bin/busybox" ]]; then
    cp "$BUSYBOX" "$CHROOT_DIR/bin/busybox"
fi

# Create symlinks for basic commands
for cmd in sh ls cat mkdir; do
    [[ -e "$CHROOT_DIR/bin/$cmd" ]] || \
        ln -s busybox "$CHROOT_DIR/bin/$cmd"
done

# Create mount points for sys, run, and etc
# mount -t sysfs sysfs "$CHROOT_DIR/sys"
# mount -t proc proc "$CHROOT_DIR/proc"
# mount -t devtmpfs devtmpfs "$CHROOT_DIR/dev"
# mount -t tmpfs tmpfs "$CHROOT_DIR/run"
# mount -t tmpfs tmpfs "$CHROOT_DIR/etc"

sudo unshare \
    --mount \
    --pid \
    --fork \
    --uts \
    --ipc \
    --mount-proc="$CHROOT_DIR/proc" \
    chroot "$CHROOT_DIR" /bin/sh -c "
        hostname '$HOSTNAME'
        exec /bin/sh
    "
