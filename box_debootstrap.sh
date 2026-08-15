#!/usr/bin/env bash

set -euo pipefail

source _variable.sh
source _generate_alias.sh
source _lib.sh

WORKSPACE="$HOME/chroot/debootstrap"
INITIALIZE=0

common_cli "$@"

remove_workspace

echo "Name: $NAME"
echo "Creating chroot at $WORKSPACE/$NAME ..."

# Check if debootstrap is installed, install if not
if ! command -v debootstrap >/dev/null 2>&1; then
    echo "debootstrap not found, installing..."
    sudo apt-get update
    sudo apt-get install -y debootstrap
fi

CHROOT_DIR="$WORKSPACE/$NAME"

cleanup() {
    echo "Cleaning up ..."
    echo "Removing existing chroot at $CHROOT_DIR ..."
    sudo rm -rf "$CHROOT_DIR"
}

if [[ "$CLEANUP" -eq 1 ]]; then
    trap cleanup EXIT
fi

arch_detect

if [[ -x "$CHROOT_DIR/bin/bash" ]]; then
    echo "Rootfs already exists at $CHROOT_DIR, skipping debootstrap."
else
    sudo debootstrap --arch="$ROOTFS_ARCH" bookworm "$CHROOT_DIR" http://deb.debian.org/debian/
    INITIALIZE=1
fi

if [[ "$INITIALIZE" -eq 1 ]]; then
    echo "Initializing chroot..."
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

    echo "Done. Chroot ready at: $CHROOT_DIR"
fi

sudo unshare \
    --mount \
    --pid \
    --fork \
    --uts \
    --ipc \
    --mount-proc="$CHROOT_DIR/proc" \
    chroot "$CHROOT_DIR" /bin/bash -c "
        hostname -F /etc/hostname
        exec /bin/bash
    "
