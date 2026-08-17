#!/usr/bin/env bash

set -euo pipefail

source _variable.sh
source _generate_alias.sh
source _lib.sh

WORKSPACE="$HOME/chroot/limit-disk"

common_cli "$@"

remove_workspace

echo "Name: $NAME"

CHROOT_DIR="$WORKSPACE/$NAME"

arch_detect

# --------------------------------------------------
# Disk quota (loop device)
# --------------------------------------------------
DISK_SIZE="2G"
DISK_IMG="$WORKSPACE/${NAME}.img"
LOOPDEV=""

setup_disk() {
    mkdir -p "$CHROOT_DIR"

    if mountpoint -q "$CHROOT_DIR"; then
        echo "Disk already mounted at $CHROOT_DIR, skipping."
        LOOPDEV=$(losetup -j "$DISK_IMG" | cut -d: -f1)
        sudo chown "$(id -u):$(id -g)" "$CHROOT_DIR"
        return
    fi

    if [[ ! -f "$DISK_IMG" ]]; then
        echo "Creating disk image ($DISK_SIZE) at $DISK_IMG ..."
        truncate -s "$DISK_SIZE" "$DISK_IMG"
        mkfs.ext4 -q "$DISK_IMG"
    else
        echo "Disk image already exists, reusing."
    fi

    LOOPDEV=$(sudo losetup -f --show "$DISK_IMG")
    echo "Attached loop device: $LOOPDEV"

    sudo mount "$LOOPDEV" "$CHROOT_DIR"
    sudo chown "$(id -u):$(id -g)" "$CHROOT_DIR"
    echo "Mounted $LOOPDEV -> $CHROOT_DIR (limit: $DISK_SIZE)"
}
setup_disk

cleanup_disk() {
    echo "Detaching disk ..."
    sudo umount -l "$CHROOT_DIR" 2>/dev/null || true
    if [[ -n "$LOOPDEV" ]]; then
        sudo losetup -d "$LOOPDEV" 2>/dev/null || true
    fi
    if [[ "$CLEANUP" -eq 1 ]]; then
        sudo rm -f "$DISK_IMG"
        sudo rm -rf "$CHROOT_DIR"
    fi
}

ROOTFS_URL="https://partner-images.canonical.com/oci/jammy/current/ubuntu-jammy-oci-${ROOTFS_ARCH}-root.tar.gz"
ROOTFS_FILE="ubuntu-jammy-oci-${ROOTFS_ARCH}-root.tar.gz"

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

    echo "$HOSTNAME" | sudo tee "$CHROOT_DIR/etc/hostname" >/dev/null

    sudo tee "$CHROOT_DIR/etc/resolv.conf" >/dev/null <<'EOF'
nameserver 8.8.8.8
nameserver 1.1.1.1
EOF
    echo "127.0.0.1 $HOSTNAME" | sudo tee -a "$CHROOT_DIR/etc/hosts" >/dev/null
else
    echo "Root filesystem already exists, skipping download/extract."
fi

# --------------------------------------------------
# Network setup
# --------------------------------------------------

OUT_IF=$(ip route get 8.8.8.8 | awk '{print $5; exit}')

NETNS="box-net-$NAME"
IDX=$(( ($(cksum <<< "$NAME" | cut -d' ' -f1) % 250) + 2 ))
VETH_HOST="veth-${IDX}"
VETH_NS="ceth-${IDX}-ns"
BRIDGE="box0"
SUBNET="10.200.${IDX}"
BR_IP="${SUBNET}.1"
NS_IP="${SUBNET}.2"

setup_ns() {
    echo "Setting up netns $NETNS ..."

    sudo ip link del "$VETH_HOST" 2>/dev/null || true

    # สร้าง netns ถ้ายังไม่มี (idempotent)
    if ! sudo ip netns list | grep -qw "$NETNS"; then
        sudo ip netns add "$NETNS"
    fi

    # สร้าง bridge บน host ถ้ายังไม่มี
    if ! ip link show "$BRIDGE" &>/dev/null; then
        sudo ip link add "$BRIDGE" type bridge
        # sudo ip addr add "${BR_IP}/24" dev "$BRIDGE"
        sudo ip link set "$BRIDGE" up
    fi

    # เติม IP ของ subnet ตัวเองเสมอ ไม่ว่า bridge จะมีอยู่แล้วหรือไม่
    sudo ip addr add "${BR_IP}/24" dev "$BRIDGE" 2>/dev/null || true

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
    # sudo iptables -t nat -A POSTROUTING -s "${SUBNET}.0/24" -o "$OUT_IF" -j MASQUERADE
    # sudo iptables -A FORWARD -i "$BRIDGE" -o "$OUT_IF" -j ACCEPT
    # sudo iptables -A FORWARD -i "$OUT_IF" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT

    sudo iptables -t nat -C POSTROUTING -s "${SUBNET}.0/24" -o "$OUT_IF" -j MASQUERADE 2>/dev/null \
      || sudo iptables -t nat -A POSTROUTING -s "${SUBNET}.0/24" -o "$OUT_IF" -j MASQUERADE
    sudo iptables -C FORWARD -i "$BRIDGE" -o "$OUT_IF" -j ACCEPT 2>/dev/null \
    || sudo iptables -A FORWARD -i "$BRIDGE" -o "$OUT_IF" -j ACCEPT
    sudo iptables -C FORWARD -i "$OUT_IF" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null \
    || sudo iptables -A FORWARD -i "$OUT_IF" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT
}
setup_ns

cleanup_ns() {
    echo "Cleaning up namespace $NETNS ..."
    sudo ip link del "$VETH_HOST" 2>/dev/null || true
    sudo ip netns del "$NETNS" 2>/dev/null || true
    sudo iptables -t nat -D POSTROUTING -s "${SUBNET}.0/24" -o "$OUT_IF" -j MASQUERADE 2>/dev/null || true
    sudo ip addr del "${BR_IP}/24" dev "$BRIDGE" 2>/dev/null || true
    if ! sudo ip netns list | grep -q '^box-net-'; then
        sudo iptables -D FORWARD -i "$BRIDGE" -o "$OUT_IF" -j ACCEPT 2>/dev/null || true
        sudo iptables -D FORWARD -i "$OUT_IF" -o "$BRIDGE" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true

        sudo ip link del "$BRIDGE" 2>/dev/null || true
    fi
}

# --------------------------------------------------
# cgroup v2 setup
# --------------------------------------------------
CGROUP_DIR="/sys/fs/cgroup/box-${NAME}"

setup_cgroup() {
    echo "Setting up cgroup at $CGROUP_DIR ..."

    # เปิด controller ที่ root ก่อน (ต้องทำครั้งเดียวต่อ boot ก็พอ แต่ idempotent ใส่ไว้ไม่เสียหาย)
    for c in cpu memory io pids; do
        if ! grep -qw "$c" /sys/fs/cgroup/cgroup.subtree_control; then
            echo "+$c" | sudo tee /sys/fs/cgroup/cgroup.subtree_control >/dev/null
        fi
    done

    sudo mkdir -p "$CGROUP_DIR"

    # CPU: 50000/100000 (period/quota แบบ CFS bandwidth)
    echo "50000 100000" | sudo tee "$CGROUP_DIR/cpu.max" >/dev/null

    echo "0" | sudo tee "$CGROUP_DIR/cpuset.cpus" >/dev/null

    # RAM: hard limit 512M, ถ้าเกิน OOM-kill เฉพาะ cgroup นี้
    echo "512M" | sudo tee "$CGROUP_DIR/memory.max" >/dev/null
    # (ถ้าอยากกัน swap thrash ด้วย ใส่ memory.swap.max ด้วย)
    echo "0" | sudo tee "$CGROUP_DIR/memory.swap.max" >/dev/null

    # จำกัดจำนวน process/thread กัน fork bomb
    echo "128" | sudo tee "$CGROUP_DIR/pids.max" >/dev/null

    echo 1 | sudo tee "$CGROUP_DIR/memory.oom.group" >/dev/null
}
setup_cgroup

cleanup_cgroup() {
    sudo rmdir "$CGROUP_DIR" 2>/dev/null || true
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

if dpkg -s lxcfs >/dev/null 2>&1; then
    echo "lxcfs is already installed, skipping installation ..."
else
    echo "lxcfs is not installed, installing ..."
    sudo apt update
    sudo apt install -y lxcfs
fi

# lxcfs
sudo mkdir -p "$CHROOT_DIR/var/lib/lxcfs"
sudo mount --bind /var/lib/lxcfs "$CHROOT_DIR/var/lib/lxcfs"
sudo mount -o remount,ro,bind "$CHROOT_DIR/var/lib/lxcfs"

cleanup_exit() {
    sudo umount -l "$CHROOT_DIR/sys"      2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/shm"  2>/dev/null || true
    sudo umount -l "$CHROOT_DIR/dev/pts"  2>/dev/null || true
    sudo rm -f "$CHROOT_DIR"/dev/{null,zero,full,random,urandom,tty}

    sudo umount -l "$CHROOT_DIR/var/lib/lxcfs"  2>/dev/null || true

    cleanup_ns
    cleanup_cgroup
    cleanup_disk
}
trap cleanup_exit EXIT

run_container() {
    echo "Running container ..."
    sudo sh -c '
        echo $$ > "'"$CGROUP_DIR/cgroup.procs"'"
        exec ip netns exec "'"$NETNS"'" \
            unshare \
            --mount --pid --fork --uts --ipc \
            --mount-proc="'"$CHROOT_DIR/proc"'" \
            chroot "'"$CHROOT_DIR"'" /bin/bash -c "
                hostname -F /etc/hostname
                for f in meminfo cpuinfo stat diskstats swaps uptime loadavg; do
                    if [ -e /var/lib/lxcfs/proc/\$f ]; then
                        mount --bind /var/lib/lxcfs/proc/\$f /proc/\$f
                    fi
                done
                apt update
                apt-get install -y iproute2 iputils-ping net-tools curl htop
                apt-get install -y python3 python3-pip python3-venv
                exec /bin/bash
            "
    '
}

run_container
