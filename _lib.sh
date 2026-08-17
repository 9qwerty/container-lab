#!/usr/bin/env bash

help() {
    echo "Usage: $0 <command> [options]"
    echo
    echo "Commands:"
    echo "  run [options]           Run a command in the chroot (see run options below)"
    echo "  list, ls                List chroots in $WORKSPACE"
    echo "  rm <name>               Remove a chroot by name"
    echo "  -h, --help              Show this help message"
    echo
    echo "Options for 'run':"
    echo "  --name <name>           Set the name (default: auto-generated)"
    echo "  --hostname <hostname>   Set the hostname (default: same as name)"
    echo "  --remove, --rm          Remove the chroot after exiting"
    echo
    echo "Examples:"
    echo "  $0 run"
    echo "  $0 run --name mybox --hostname mybox --rm"
    echo "  $0 list"
    echo "  $0 rm mybox"
}

list_workspace() {
    echo "Listing chroots in $WORKSPACE ..."
    local folders=()
    while IFS= read -r -d '' dir; do
        folders+=("$(basename "$dir")")
    done < <(find "$WORKSPACE" -mindepth 1 -maxdepth 1 -type d -print0)

    if [[ ${#folders[@]} -eq 0 ]]; then
        echo "No chroots found." >&2
        exit 0
    fi

    echo "Chroots found:"
    for folder in "${folders[@]}"; do
        echo "  $folder"
    done
}

common_cli() {
    if [[ $# -eq 0 ]]; then
        echo "Error: no options provided" >&2
        help
        exit 0
    fi

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -h|--help)
                help
                exit 0
                ;;
            list|ls)
                list_workspace
                exit 0
                ;;
            run)
                NAME="$(generate_unique_alias "$WORKSPACE")"
                shift
                while [[ $# -gt 0 ]]; do
                    case "$1" in
                        --name)
                            NAME="${2:?--name requires a value}"
                            shift 2
                            ;;
                        --hostname)
                            HOSTNAME="${2:?--hostname requires a value}"
                            shift 2
                            ;;
                        --remove|--rm)
                            CLEANUP=1
                            shift
                            ;;
                        --port|-p)
                            new_mapping="${2:?--port requires a value}"
                            shift 2

                            # validate format ก่อน (ใช้ logic เดียวกับ parse_port_mapping)
                            parse_port_mapping "$new_mapping"

                            # (a) กันซ้ำกันเองภายใน argument เดียวกัน
                            for existing in "${PORTS[@]}"; do
                                existing_host="${existing%%:*}"
                                if [[ "$existing_host" == "$HOST_PORT" ]]; then
                                    echo "Error: duplicate --port $HOST_PORT specified twice" >&2
                                    exit 1
                                fi
                            done
                            PORTS+=("$new_mapping")
                            ;;
                        *)
                            echo "Unknown option for run: $1" >&2
                            help
                            exit 1
                            ;;
                    esac
                done
                ;;
            rm)
                if [[ -n "${2:-}" && "${2:0:1}" != "-" ]]; then
                    NAME="$2"
                    shift 2
                else
                    list_workspace
                    exit 0
                fi
                REMOVE=1
                ;;
            *)
                echo "Unknown option: $1" >&2
                help
                exit 1
                ;;
        esac
    done

    if [[ -z "$NAME" ]]; then
        exit 1
    fi

    if [[ -z "$HOSTNAME" ]]; then
        HOSTNAME="$NAME"
    fi
}

remove_workspace() {
    if [[ "$REMOVE" -eq 1 ]]; then
        CHROOT_DIR_LOCAL="$WORKSPACE/$NAME"
        if [[ ! -d "$CHROOT_DIR_LOCAL" ]]; then
            echo "No such chroot: $NAME" >&2
            exit 1
        fi
        echo "Removing chroot at $CHROOT_DIR_LOCAL ..."
        sudo rm -rf "$CHROOT_DIR_LOCAL"
        echo "Removed."
        exit 0
    fi
}

arch_detect() {
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

    echo "Architecture : $ARCH"
    echo "Rootfs       : $ROOTFS_ARCH"
}

parse_port_mapping() {
    local mapping="$1"
    if [[ "$mapping" == *:* ]]; then
        HOST_PORT="${mapping%%:*}"
        CONTAINER_PORT="${mapping#*:}"
    else
        # ถ้าใส่ port เดียว ให้ host = container
        HOST_PORT="$mapping"
        CONTAINER_PORT="$mapping"
    fi

    # validate เป็นตัวเลขและอยู่ใน range
    if ! [[ "$HOST_PORT" =~ ^[0-9]+$ ]] || (( HOST_PORT < 1 || HOST_PORT > 65535 )); then
        echo "Invalid host port: $HOST_PORT" >&2
        exit 1
    fi
    if ! [[ "$CONTAINER_PORT" =~ ^[0-9]+$ ]] || (( CONTAINER_PORT < 1 || CONTAINER_PORT > 65535 )); then
        echo "Invalid container port: $CONTAINER_PORT" >&2
        exit 1
    fi
}

# ตรวจว่า host port นี้มีคนจับจองอยู่แล้วหรือไม่ (จาก 3 แหล่ง)
validate_port_available() {
    local host_port="$1"

    # (1) เช็คว่ามี iptables PREROUTING DNAT rule ผูก dport นี้อยู่แล้วหรือยัง
    #     ครอบคลุม container อื่นที่ยัง "รันอยู่" ตอนนี้ (rule ถูกลบตอน cleanup_ns)
    local existing_dnat
    existing_dnat=$(sudo iptables -t nat -S PREROUTING 2>/dev/null \
        | grep -- "--dport ${host_port} " \
        | grep -- '-j DNAT' || true)

    if [[ -n "$existing_dnat" ]]; then
        if echo "$existing_dnat" | grep -q -- "--to-destination ${NS_IP}:"; then
            echo "Port ${host_port} already forwarded to this container, skipping (idempotent)."
            return 0
        fi
        echo "Error: host port ${host_port} is already forwarded by another running container:" >&2
        echo "  $existing_dnat" >&2
        return 1
    fi

    # (2) เช็คว่า process บน host เองผูก port นี้อยู่แล้วหรือไม่ (เช่น nginx, apache บน host จริง)
    #     ss -H = no header, -t = tcp, -l = listening, -n = numeric
    if sudo ss -Htln "sport = :${host_port}" 2>/dev/null | grep -q ":${host_port}"; then
        echo "Error: host port ${host_port} is already in use by a process on the host:" >&2
        sudo ss -tlnp "sport = :${host_port}" 2>/dev/null >&2 || true
        return 1
    fi

    # (3) กัน well-known port ที่มักชนกับ service ระบบโดยไม่ตั้งใจ (แค่ warn ไม่ block)
    if (( host_port < 1024 )); then
        echo "Warning: host port ${host_port} is a privileged port (<1024)." >&2
    fi

    return 0
}
