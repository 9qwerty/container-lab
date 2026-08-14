#!/usr/bin/env bash

set -euo pipefail

source _variable.sh
source _generate_alias.sh
source _lib.sh

WORKSPACE="$HOME/chroot/busybox"

common_cli "$@"

remove_workspace

echo "Name: $NAME"
echo "Creating chroot at $WORKSPACE/$NAME ..."

CHROOT_DIR="$WORKSPACE/$NAME"

cleanup() {
    echo "Cleaning up ..."
    echo "Removing existing chroot at $CHROOT_DIR ..."
    sudo rm -rf "$CHROOT_DIR"
}

if [[ "$CLEANUP" -eq 1 ]]; then
    trap cleanup EXIT
fi

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
