// lxcfs.go: setup virtualized /proc files from lxcfs on host
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var lxcfsProcFiles = []string{"meminfo", "cpuinfo", "diskstats", "stat", "swaps", "uptime"}

type lxcfsHandle struct {
	fd     *os.File
	target string
}

func isLxcfsAvailable() bool {
	_, err := os.Stat("/var/lib/lxcfs/proc/meminfo")
	return err == nil
}

func setupLxcfs() []lxcfsHandle {
	var handles []lxcfsHandle

	if !isLxcfsAvailable() {
		fmt.Fprintln(os.Stderr, "[lxcfs] not available on host, skipping virtualized /proc (run apt install lxcfs to install)")
		return handles
	}

	for _, f := range lxcfsProcFiles {
		src := filepath.Join("/var/lib/lxcfs/proc", f)
		fd, err := os.Open(src)
		if err != nil {
			// lxcfs อาจไม่ได้ติดตั้ง/ไม่ได้รัน - ข้ามไปเฉย ๆ ไม่ panic
			fmt.Fprintf(os.Stderr, "[lxcfs] skip %s: %v\n", f, err)
			continue
		}
		handles = append(handles, lxcfsHandle{fd: fd, target: f})
	}

	return handles
}

func mountLxcfs(handles []lxcfsHandle) {
	for _, h := range handles {
		src := fmt.Sprintf("/proc/self/fd/%d", h.fd.Fd())
		dst := filepath.Join("/proc", h.target)

		if err := syscall.Mount(src, dst, "", syscall.MS_BIND, ""); err != nil {
			fmt.Fprintf(os.Stderr, "[lxcfs] bind mount %s failed: %v\n", h.target, err)
		}
		h.fd.Close()
	}
}

func unmountLxcfs() {
	for _, f := range lxcfsProcFiles {
		syscall.Unmount(filepath.Join("/proc", f), syscall.MNT_DETACH)
	}
}
