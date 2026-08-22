// disk.go
package disk

import (
	"box/internal/config"
	"box/internal/namespace"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type OverlayConfig = config.OverlayConfig

type DiskConfig struct {
	ImgPath  string
	MountDir string
	LoopDev  string
}

const diskSize = "2G"

// setupDisk: create and mount disk image, return DiskConfig
func SetupDisk(workspace, name string) (*DiskConfig, error) {
	mountDir := filepath.Join(workspace, name)
	imgPath := filepath.Join(workspace, name+".img")

	uid, gid := namespace.HostIDs()
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir mount dir: %w", err)
	}
	if err := os.Chown(mountDir, uid, gid); err != nil {
		return nil, fmt.Errorf("chown mount dir: %w", err)
	}

	dc := &DiskConfig{ImgPath: imgPath, MountDir: mountDir}

	// เช็คว่า mount อยู่แล้วหรือยัง (idempotent เหมือน bash เดิม)
	if isMounted(mountDir) {
		fmt.Println("Disk already mounted at", mountDir, "skipping.")
		loopDev, err := findLoopDevByImg(imgPath)
		if err != nil {
			return nil, err
		}
		dc.LoopDev = loopDev
		return dc, nil
	}

	// สร้าง image file ถ้ายังไม่มี
	if _, err := os.Stat(imgPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image (%s) at %s ...\n", diskSize, imgPath)
		if err := truncateFile(imgPath, diskSize); err != nil {
			return nil, fmt.Errorf("truncate: %w", err)
		}
		if err := runCmd("mkfs.ext4", "-q", imgPath); err != nil {
			return nil, fmt.Errorf("mkfs.ext4: %w", err)
		}
		// chown rootfs to host user/group
		if err := os.Chown(imgPath, uid, gid); err != nil {
			return nil, fmt.Errorf("chown imgPath: %w", err)
		}
	} else {
		fmt.Println("Disk image already exists, reusing.")
	}

	// attach loop device
	loopDev, err := attachLoop(imgPath)
	if err != nil {
		return nil, fmt.Errorf("losetup: %w", err)
	}
	dc.LoopDev = loopDev
	fmt.Println("Attached loop device:", loopDev)

	// mount ผ่าน syscall ตรงๆ (ไม่ shell-out mount command)
	if err := syscall.Mount(loopDev, mountDir, "ext4", 0, ""); err != nil {
		return nil, fmt.Errorf("mount %s -> %s: %w", loopDev, mountDir, err)
	}
	fmt.Printf("Mounted %s -> %s (limit: %s)\n", loopDev, mountDir, diskSize)

	return dc, nil
}

// cleanupDisk: unmount and detach loop device, optionally remove image file
func CleanupDisk(dc *DiskConfig, removeAfter bool) {
	fmt.Println("Detaching disk ...")

	// umount -l (lazy unmount)
	syscall.Unmount(dc.MountDir, syscall.MNT_DETACH)

	if dc.LoopDev != "" {
		runCmd("losetup", "-d", dc.LoopDev)
	}

	if removeAfter {
		os.Remove(dc.ImgPath)
		os.RemoveAll(dc.MountDir)
	}
}

// ---------------------------------------------------------
// helpers
// ---------------------------------------------------------

func truncateFile(path, size string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	bytesSize, err := parseSize(size)
	if err != nil {
		return err
	}
	return f.Truncate(bytesSize)
}

// parseSize: support "2G", "2M", "2K" formats only
func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	var mult int64 = 1
	numPart := s

	switch {
	case strings.HasSuffix(s, "G"):
		mult = 1 << 30
		numPart = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult = 1 << 20
		numPart = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult = 1 << 10
		numPart = strings.TrimSuffix(s, "K")
	}

	var num int64
	if _, err := fmt.Sscanf(numPart, "%d", &num); err != nil {
		return 0, fmt.Errorf("invalid size: %s", s)
	}
	return num * mult, nil
}

func attachLoop(imgPath string) (string, error) {
	out, err := exec.Command("losetup", "-f", "--show", imgPath).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func findLoopDevByImg(imgPath string) (string, error) {
	out, err := exec.Command("losetup", "-j", imgPath).Output()
	if err != nil {
		return "", err
	}
	// output format: "/dev/loop0: []: (/path/to/img)"
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", fmt.Errorf("no loop device found for %s", imgPath)
	}
	parts := strings.SplitN(line, ":", 2)
	return parts[0], nil
}

func isMounted(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	absPath, _ := filepath.Abs(path)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == absPath {
			return true
		}
	}
	return false
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w (%s)", name, err, stderr.String())
	}
	return nil
}

func SetupOverlay(o OverlayConfig) error {
	uid, gid := namespace.HostIDs()
	if err := os.MkdirAll(o.UpperDir, 0755); err != nil {
		return err
	}

	if err := os.Chown(o.UpperDir, uid, gid); err != nil {
		return err
	}

	if err := os.MkdirAll(o.WorkDir, 0755); err != nil {
		return err
	}

	if err := os.Chown(o.WorkDir, uid, gid); err != nil {
		return err
	}

	if err := os.MkdirAll(o.MergedDir, 0755); err != nil {
		return err
	}

	if err := os.Chown(o.MergedDir, uid, gid); err != nil {
		return err
	}

	opts := fmt.Sprintf(
		"lowerdir=%s,upperdir=%s,workdir=%s",
		o.LowerDir,
		o.UpperDir,
		o.WorkDir,
	)

	return syscall.Mount(
		"overlay",
		o.MergedDir,
		"overlay",
		0,
		opts,
	)
}

func CleanupOverlay(o OverlayConfig) {
	syscall.Unmount(o.MergedDir, syscall.MNT_DETACH)
}
