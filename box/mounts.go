package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func setupDevNodes(rootDev string) error {
	nodes := []struct {
		name         string
		major, minor int64
		mode         uint32
	}{
		{"null", 1, 3, 0666},
		{"zero", 1, 5, 0666},
		{"full", 1, 7, 0666},
		{"random", 1, 8, 0666},
		{"urandom", 1, 9, 0666},
		{"tty", 5, 0, 0666},
	}
	for _, n := range nodes {
		path := filepath.Join(rootDev, n.name)
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("[dev] %s already exists, skipping\n", n.name)
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", n.name, err)
		}
		dev := int(mkdevFromTar(n.major, n.minor))
		if err := unix.Mknod(path, unix.S_IFCHR|n.mode, dev); err != nil {
			return fmt.Errorf("mknod %s: %w", n.name, err)
		}
		if err := os.Chmod(path, os.FileMode(n.mode)); err != nil {
			return fmt.Errorf("chmod %s: %w", n.name, err)
		}
		fmt.Printf("[dev] created %s\n", n.name)
	}
	return nil
}
