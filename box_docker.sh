#!/usr/bin/env bash

set -euo pipefail

NAME="${1:-docker}"

CHROOT_DIR="$HOME/chroot/$NAME"

mkdir -p "$CHROOT_DIR"

if [[ ! -x "$CHROOT_DIR/bin/bash" ]]; then
    echo "Creating Ubuntu 22.04 root filesystem..."

    CONTAINER_ID="$(docker create ubuntu:22.04)"

    docker export "$CONTAINER_ID" | \
        tar -x -C "$CHROOT_DIR"

    docker rm "$CONTAINER_ID" >/dev/null
else
    echo "Root filesystem already exists, skipping export."
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
