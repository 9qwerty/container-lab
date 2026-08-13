#!/usr/bin/env bash

set -euo pipefail

NAME="${1:-busybox}"

CHROOT_DIR="$HOME/chroot/$NAME"
BUSYBOX=$(which busybox)

mkdir -p "$CHROOT_DIR/bin"

if [[ ! -f "$CHROOT_DIR/bin/busybox" ]]; then
    cp "$BUSYBOX" "$CHROOT_DIR/bin/busybox"
fi

for cmd in sh ls cat mkdir; do
    [[ -e "$CHROOT_DIR/bin/$cmd" ]] || \
        ln -s busybox "$CHROOT_DIR/bin/$cmd"
done

# sudo chroot "$CHROOT_DIR" /bin/sh

sudo unshare --mount --pid --fork \
    --mount-proc="$CHROOT_DIR/proc" \
    chroot "$CHROOT_DIR" /bin/sh
