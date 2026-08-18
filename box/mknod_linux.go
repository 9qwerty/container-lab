package main

import "golang.org/x/sys/unix"

func mkdevFromTar(major, minor int64) uint64 {
	return unix.Mkdev(uint32(major), uint32(minor))
}

func mknod(path string, mode uint32, dev int, typeflag byte) error {
	var m uint32 = mode
	switch typeflag {
	case '3': // char device (tar.TypeChar)
		m |= unix.S_IFCHR
	case '4': // block device (tar.TypeBlock)
		m |= unix.S_IFBLK
	}
	return unix.Mknod(path, m, dev)
}
