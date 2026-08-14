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
