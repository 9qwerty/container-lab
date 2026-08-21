// app_initial.go
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func hostIDs() (uid, gid int) {
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

func chownRootfs(rootfsDir string, uid, gid int) error {
	return filepath.WalkDir(rootfsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}

func setupResolvConf(rootfsDir string) error {
	resolvPath := filepath.Join(rootfsDir, "etc", "resolv.conf")

	content := "nameserver 8.8.8.8\nnameserver 1.1.1.1\n"

	if err := os.MkdirAll(filepath.Dir(resolvPath), 0755); err != nil {
		return fmt.Errorf("mkdir /etc: %w", err)
	}

	return os.WriteFile(resolvPath, []byte(content), 0644)
}

func setupHostsFile(rootfsDir, hostname string) error {
	hostsPath := filepath.Join(rootfsDir, "etc", "hosts")
	line := fmt.Sprintf("127.0.0.1 %s\n", hostname)

	f, err := os.OpenFile(hostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(line)
	return err
}

func setupRootfs(arch string, cfg *Config) error {
	uid, gid := hostIDs()
	url := fmt.Sprintf(
		"https://partner-images.canonical.com/oci/jammy/current/ubuntu-jammy-oci-%s-root.tar.gz",
		arch,
	)
	archiveFile := fmt.Sprintf("ubuntu-jammy-oci-%s-root.tar.gz", arch)

	lowerDir := cfg.Overlay.LowerDir
	// ถ้ามี /bin/bash อยู่แล้วใน rootfs ข้ามทั้งหมด (idempotent เหมือน script เดิม)
	if _, err := os.Stat(filepath.Join(lowerDir, "bin", "bash")); err == nil {
		fmt.Println("Root filesystem already exists, skipping download/extract.")
		return nil
	}

	if err := downloadFile(url, archiveFile); err != nil {
		return fmt.Errorf("download : %w", err)
	}
	if err := chownRootfs(archiveFile, uid, gid); err != nil {
		return fmt.Errorf("chown rootfs : %w", err)
	}

	if err := extractTarGz(archiveFile, lowerDir); err != nil {
		return fmt.Errorf("extract : %w", err)
	}
	if err := chownRootfs(lowerDir, uid, gid); err != nil {
		return fmt.Errorf("chown rootfs : %w", err)
	}

	if err := setupResolvConf(lowerDir); err != nil {
		return fmt.Errorf("setup resolv.conf : %w", err)
	}
	if err := setupHostsFile(lowerDir, cfg.Hostname); err != nil {
		return fmt.Errorf("setup hosts : %w", err)
	}
	return nil
}

func appInitial(env []string) {
	// -------------------------
	// apt update
	// -------------------------
	fmt.Println("[child] apt update...")
	aptUpdate := exec.Command("/usr/bin/apt-get", "update")
	aptUpdate.Stdin = os.Stdin
	aptUpdate.Stdout = os.Stdout
	aptUpdate.Stderr = os.Stderr
	aptUpdate.Env = env

	if err := aptUpdate.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[child] apt update failed:", err)
		return
	}

	// -------------------------
	// install essential packages
	// -------------------------
	fmt.Println("[child] apt update...")
	essential := exec.Command("/usr/bin/apt-get", "install", "-y", "iproute2", "iputils-ping", "net-tools", "curl", "htop")
	essential.Stdin = os.Stdin
	essential.Stdout = os.Stdout
	essential.Stderr = os.Stderr
	essential.Env = env

	if err := essential.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[child] apt install failed:", err)
		return
	}

	// -------------------------
	// install python3
	// -------------------------
	fmt.Println("[child] installing python3...")
	python := exec.Command(
		"/usr/bin/apt-get",
		"install",
		"-y",
		"python3",
		"python3-pip",
		"python3-venv",
	)
	python.Stdin = os.Stdin
	python.Stdout = os.Stdout
	python.Stderr = os.Stderr
	python.Env = env

	if err := python.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[child] apt install python3 failed:", err)
		return
	}
}
