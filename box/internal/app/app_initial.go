// app_initial.go
package app

import (
	"box/internal/archive"
	"box/internal/config"
	"box/internal/namespace"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

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

func SetupRootfs(arch string, cfg *config.Config) error {
	uid, gid := namespace.HostIDs()
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

	if err := archive.ExtractTarGz(archiveFile, lowerDir); err != nil {
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

func AppInitial(env []string) {
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

func downloadFile(url, destPath string) error {
	// ถ้ามีไฟล์อยู่แล้ว ข้ามการโหลด (เหมือน script bash เดิมที่เช็ค -f)
	if _, err := os.Stat(destPath); err == nil {
		fmt.Println("Rootfs archive already downloaded, skipping.")
		return nil
	}

	fmt.Println("Downloading rootfs from", url, "...")

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	fmt.Printf("Downloaded %d bytes -> %s\n", written, destPath)
	return nil
}

func CheckSubuidTools() {
	tools := []string{"newuidmap", "newgidmap"}

	for _, tool := range tools {
		path, err := exec.LookPath(tool)
		if err == nil {
			fmt.Printf("Found %s at %s\n", tool, path)
			continue
		}
		fmt.Printf("ERROR: %s not found\n\n", tool)
		fmt.Println("Install it with:")

		fmt.Println("  Debian / Ubuntu:")
		fmt.Println("    sudo apt install uidmap")

		fmt.Println("  RHEL / CentOS / Rocky / AlmaLinux:")
		fmt.Println("    sudo dnf install shadow-utils")

		fmt.Println("  Arch Linux:")
		fmt.Println("    sudo pacman -S shadow")

		os.Exit(1)

	}
}

// apt -o APT::Sandbox::User=root update
func SetupAptRootless() error {
	const path = "/etc/apt/apt.conf.d/99-rootless"
	const content = `APT::Sandbox::User "root";`

	if _, err := os.Stat("/etc/apt"); os.IsNotExist(err) {
		return nil
	}

	if err := os.MkdirAll("/etc/apt/apt.conf.d", 0755); err != nil {
		return fmt.Errorf("create apt config dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
