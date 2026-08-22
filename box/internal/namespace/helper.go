package namespace

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
)

func DetectArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

func HostIDs() (uid, gid int) {
	uid, gid = os.Getuid(), os.Getgid()
	if v := os.Getenv("SUDO_UID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			uid = n
		}
	}
	if v := os.Getenv("SUDO_GID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			gid = n
		}
	}
	return
}

func Must(err error, name ...string) {
	if err == nil {
		return
	}
	if len(name) > 0 {
		panic(fmt.Sprintf("%s: %v", name[0], err))
	}
	panic(err)
}
