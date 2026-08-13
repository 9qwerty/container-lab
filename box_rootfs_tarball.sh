#!/usr/bin/env bash

set -euo pipefail

NAME="${1:-rootfs}"

CHROOT_DIR="$HOME/chroot/$NAME"

ARCH="$(uname -m)"

case "$ARCH" in
    x86_64)
        ROOTFS_ARCH="amd64"
        ;;
    aarch64|arm64)
        ROOTFS_ARCH="arm64"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

ROOTFS_URL="https://partner-images.canonical.com/oci/jammy/current/ubuntu-jammy-oci-${ROOTFS_ARCH}-root.tar.gz"
ROOTFS_FILE="ubuntu-jammy-oci-${ROOTFS_ARCH}-root.tar.gz"

echo "Architecture : $ARCH"
echo "Rootfs       : $ROOTFS_ARCH"

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
else
    echo "Root filesystem already exists, skipping download/extract."
fi

# --------------------------------------------------
# Container setup
# --------------------------------------------------

cp /etc/resolv.conf "$CHROOT_DIR/etc/resolv.conf"

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
    echo "Cleaning up mounts..."
    sudo umount -l "$CHROOT_DIR/sys"      2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/pts"  2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev"      2>/dev/null || true
}
trap cleanup EXIT

# sudo chroot "$CHROOT_DIR" /bin/bash

sudo unshare --mount --pid --fork \
    --mount-proc="$CHROOT_DIR/proc" \
    chroot "$CHROOT_DIR" /bin/bash
