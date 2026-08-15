#!/usr/bin/env bash

set -euo pipefail

source _variable.sh
source _generate_alias.sh
source _lib.sh

WORKSPACE="$HOME/chroot/rootfs"

common_cli "$@"

remove_workspace

echo "Name: $NAME"
echo "Creating chroot at $WORKSPACE/$NAME ..."

CHROOT_DIR="$WORKSPACE/$NAME"

arch_detect

ROOTFS_URL="https://partner-images.canonical.com/oci/jammy/current/ubuntu-jammy-oci-${ROOTFS_ARCH}-root.tar.gz"
ROOTFS_FILE="ubuntu-jammy-oci-${ROOTFS_ARCH}-root.tar.gz"

mkdir -p "$CHROOT_DIR"

if [[ ! -x "$CHROOT_DIR/bin/bash" ]]; then
    echo "Root filesystem not found."

    if [[ ! -f "$ROOTFS_FILE" ]]; then
        echo "Downloading rootfs..."
        curl -fLO "$ROOTFS_URL"
    else
        echo "Rootfs archive already downloaded."
    fi

    echo "Extracting rootfs..."
    tar xzf "$ROOTFS_FILE" -C "$CHROOT_DIR"
    cp -L /etc/resolv.conf "$CHROOT_DIR/etc/resolv.conf"
else
    echo "Root filesystem already exists, skipping download/extract."
fi

# --------------------------------------------------
# Container setup
# --------------------------------------------------

sudo mkdir -p \
    "$CHROOT_DIR/proc" \
    "$CHROOT_DIR/sys" \
    "$CHROOT_DIR/dev" \
    "$CHROOT_DIR/run" \
    "$CHROOT_DIR/tmp"

sudo chmod 1777 "$CHROOT_DIR/tmp"

# --- bind-mount /dev, /dev/pts, /sys from host — this is the missing piece ---
sudo mount --bind /dev "$CHROOT_DIR/dev"
sudo mount --bind /dev/pts "$CHROOT_DIR/dev/pts"
sudo mount --bind /sys "$CHROOT_DIR/sys"

cleanup() {
    echo "Cleaning up ..."
    echo "Removing existing chroot at $CHROOT_DIR ..."
    sudo rm -rf "$CHROOT_DIR"
}

cleanup_exit() {
    echo "Cleaning up mounts..."
    sudo umount -l "$CHROOT_DIR/sys"      2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/pts"  2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev"      2>/dev/null || true
    if [[ "$CLEANUP" -eq 1 ]]; then
        cleanup
    fi
}
trap cleanup_exit EXIT

sudo unshare \
    --mount \
    --pid \
    --fork \
    --uts \
    --ipc \
    --mount-proc="$CHROOT_DIR/proc" \
    chroot "$CHROOT_DIR" /bin/bash -c "
        hostname '$HOSTNAME'
        exec /bin/bash
    "
