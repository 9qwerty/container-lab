#!/usr/bin/env bash

set -euo pipefail

source _variable.sh
source _generate_alias.sh
source _lib.sh

WORKSPACE="$HOME/chroot/net"

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

    sudo tee "$CHROOT_DIR/etc/resolv.conf" >/dev/null <<'EOF'
nameserver 8.8.8.8
nameserver 1.1.1.1
EOF
    echo "127.0.1.1 $HOSTNAME" | sudo tee -a "$CHROOT_DIR/etc/hosts"
else
    echo "Root filesystem already exists, skipping download/extract."
fi

# --------------------------------------------------
# Network setup
# --------------------------------------------------

OUT_IF=$(ip route get 8.8.8.8 | awk '{print $5; exit}')

NETNS="box-net"

setup_ns() {
    echo "Setting up netns $NETNS ..."
    IDX=1
    VETH_HOST="veth-${IDX}"
    VETH_NS="ceth-${IDX}-ns"
    BRIDGE="box0"
    SUBNET="10.200.${IDX}"
    BR_IP="${SUBNET}.1"
    NS_IP="${SUBNET}.2"

    sudo ip link del "$VETH_HOST" 2>/dev/null || true

    # สร้าง netns ถ้ายังไม่มี (idempotent)
    if ! sudo ip netns list | grep -qw "$NETNS"; then
        sudo ip netns add "$NETNS"
    fi

    # สร้าง bridge บน host ถ้ายังไม่มี
    if ! ip link show "$BRIDGE" &>/dev/null; then
        sudo ip link add "$BRIDGE" type bridge
        sudo ip addr add "${BR_IP}/24" dev "$BRIDGE"
        sudo ip link set "$BRIDGE" up
    fi

    # สร้าง veth pair
    sudo ip link add "$VETH_HOST" type veth peer name "$VETH_NS"
    sudo ip link set "$VETH_HOST" master "$BRIDGE"
    sudo ip link set "$VETH_HOST" up

    # ย้ายปลาย ceth เข้า netns โดยตรง ไม่ต้องผ่าน PID
    sudo ip link set "$VETH_NS" netns "$NETNS"

    # ตั้ง IP/route ฝั่งใน netns (ตรงนี้ script เดิมไม่ได้ทำ ลองเช็คดูว่าตั้งใจไหม)
    sudo ip netns exec "$NETNS" ip addr add "${NS_IP}/24" dev "$VETH_NS"
    sudo ip netns exec "$NETNS" ip link set "$VETH_NS" up
    sudo ip netns exec "$NETNS" ip link set lo up
    sudo ip netns exec "$NETNS" ip route add default via "$BR_IP"

    sudo sysctl -w net.ipv4.ip_forward=1
    sudo iptables -t nat -A POSTROUTING -s "${SUBNET}.0/24" -o "$OUT_IF" -j MASQUERADE
    sudo iptables -A FORWARD -i "$BRIDGE" -o "$OUT_IF" -j ACCEPT
    sudo iptables -A FORWARD -i "$OUT_IF" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT
}
setup_ns

cleanup_ns() {
    echo "Cleaning up namespace PID $NETNS ..."
    sudo ip link del "$VETH_HOST" 2>/dev/null || true
    sudo ip netns del "$NETNS" 2>/dev/null || true
    sudo iptables -t nat -D POSTROUTING -s "${SUBNET}.0/24" -o "$OUT_IF" -j MASQUERADE 2>/dev/null || true
    sudo iptables -D FORWARD -i "$BRIDGE" -o "$OUT_IF" -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -i "$OUT_IF" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true
}

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
    echo "Cleaning up ..."
    echo "Removing existing chroot at $CHROOT_DIR ..."
    sudo rm -rf "$CHROOT_DIR"
}

cleanup_exit() {
    sudo umount -l "$CHROOT_DIR/sys"      2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/shm"  2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/pts"  2>/dev/null || true
    sudo rm -f "$CHROOT_DIR"/dev/{null,zero,full,random,urandom,tty}

    if [[ "$CLEANUP" -eq 1 ]]; then
        cleanup
    fi
    cleanup_ns
}
trap cleanup_exit EXIT

sudo ip netns exec "$NETNS" \
    unshare \
    --mount --pid --fork --uts --ipc \
    --mount-proc="$CHROOT_DIR/proc" \
    chroot "$CHROOT_DIR" /bin/bash -c "
        # mount -t proc proc /proc
        hostname '$HOSTNAME'
        apt update
        apt-get install -y iproute2 iputils-ping net-tools curl
        exec /bin/bash
    "
