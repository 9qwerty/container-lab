#!/usr/bin/env bash

set -euo pipefail

NAME="box-nvm"
HOSTNAME="nvm"
NEW=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --hostname)
            HOSTNAME="${2:?--hostname requires a value}"
            shift 2
            ;;
        --new)
            NEW=1
            shift
            ;;
        *)
            NAME="$1"
            shift
            ;;
    esac
done

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
echo "Hostname     : $HOSTNAME"

if [[ "$NEW" -eq 1 ]]; then
    echo "Removing existing chroot at $CHROOT_DIR ..."
    sudo umount -l "$CHROOT_DIR/proc"    2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/sys"     2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/pts" 2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev"     2>/dev/null || true
    sudo rm -rf "$CHROOT_DIR"
fi

sudo mkdir -p "$CHROOT_DIR"

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
sudo mount -t proc proc "$CHROOT_DIR/proc"

cleanup() {
    echo "Cleaning up mounts..."
    sudo umount -l "$CHROOT_DIR/proc"     2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/sys"      2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/pts"  2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev"      2>/dev/null || true
}
trap cleanup EXIT

if [[ ! -x "$CHROOT_DIR/bin/bash" ]]; then
    echo "Root filesystem not found."

    if [[ ! -f "$ROOTFS_FILE" ]]; then
        echo "Downloading rootfs..."
        curl -fLO "$ROOTFS_URL"
    else
        echo "Rootfs archive already downloaded."
    fi

    echo "Extracting rootfs..."
    sudo tar xzf "$ROOTFS_FILE" -C "$CHROOT_DIR"

    sudo cp -L /etc/resolv.conf "$CHROOT_DIR/etc/resolv.conf"

    echo "$HOSTNAME" | sudo tee "$CHROOT_DIR/etc/hostname" >/dev/null

    sudo tee "$CHROOT_DIR/etc/hosts" >/dev/null <<EOF
127.0.0.1   localhost
127.0.1.1   $HOSTNAME

::1         localhost ip6-localhost ip6-loopback
ff02::1     ip6-allnodes
ff02::2     ip6-allrouters
EOF

    echo "Installing curl, git, build tools, and nvm inside chroot..."
    sudo chroot "$CHROOT_DIR" /bin/bash -c "
        apt update
        apt install -y curl ca-certificates git build-essential
        curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.6/install.sh | bash
    "
else
    echo "Root filesystem already exists, skipping download/extract."
fi

sudo unshare \
    --mount \
    --pid \
    --fork \
    --uts \
    --ipc \
    chroot "$CHROOT_DIR" /bin/bash -c "
        hostname -F /etc/hostname
        exec /bin/bash
    "
