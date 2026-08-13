#!/usr/bin/env bash

set -euo pipefail

NAME="${1:-jammy}"

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

sudo mkdir -p "$CHROOT_DIR/dev/pts" "$CHROOT_DIR/dev/shm"

# --- ตั้ง device nodes ขั้นต่ำสำหรับ apt/gpg/bash ---
sudo mknod -m 666 "$CHROOT_DIR/dev/null"    c 1 3
sudo mknod -m 666 "$CHROOT_DIR/dev/zero"    c 1 5
sudo mknod -m 666 "$CHROOT_DIR/dev/full"    c 1 7
sudo mknod -m 666 "$CHROOT_DIR/dev/random"  c 1 8
sudo mknod -m 666 "$CHROOT_DIR/dev/urandom" c 1 9
sudo mknod -m 666 "$CHROOT_DIR/dev/tty"     c 5 0
sudo chown root:root "$CHROOT_DIR"/dev/{null,zero,full,random,urandom,tty}

# --- pty/shm แยก namespace ของตัวเอง ไม่ผูกกับ host ---
sudo mount -t devpts devpts "$CHROOT_DIR/dev/pts" -o newinstance,ptmxmode=0666
ln -sf pts/ptmx "$CHROOT_DIR/dev/ptmx"
sudo mount -t tmpfs tmpfs "$CHROOT_DIR/dev/shm"

# /sys ยัง bind ได้ตามปกติ (อ่านอย่างเดียวเป็นส่วนใหญ่ ความเสี่ยงต่ำกว่า /dev มาก)
sudo mount --bind /sys "$CHROOT_DIR/sys"
sudo mount -o remount,ro,bind "$CHROOT_DIR/sys"

cleanup() {
    sudo umount -l "$CHROOT_DIR/sys"      2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/shm"  2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/pts"  2>/dev/null || true
    sudo rm -f "$CHROOT_DIR"/dev/{null,zero,full,random,urandom,tty}
}
trap cleanup EXIT

sudo unshare --mount --pid --fork \
    --mount-proc="$CHROOT_DIR/proc" \
    chroot "$CHROOT_DIR" /bin/bash
