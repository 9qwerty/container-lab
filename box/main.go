// main
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

var (
	cgroupDir = getenv("BOX_CGROUP", "/sys/fs/cgroup/gobox")
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) < 2 {
		help()
		os.Exit(0)
	}

	if os.Args[1] == "child" {
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "child: missing rootfs/hostname args")
			os.Exit(1)
		}
		child(&Config{RootFS: os.Args[2], Hostname: os.Args[3]})
		return
	}

	cfg, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	switch cfg.Command {
	case "list":
		must(listWorkspace(cfg))
	case "rm":
		must(removeWorkspace(cfg.Name, cfg))
	case "run":
		runContainer(cfg) // ฟังก์ชัน runContainer() เดิมที่คุยกันก่อนหน้า รับ cfg เข้าไปแทน global var
	}
}

// ---------------------------------------------------------
// run: ทำงานบน host ปกติ - สร้าง namespace ใหม่ + cgroup แล้ว re-exec ตัวเอง
// ---------------------------------------------------------
func runContainer(cfg *Config) {
	arch, errArch := detectArch()
	must(errArch)
	must(setupRootfs(arch, cfg))

	must(setupCgroupSkeleton())

	cmd := exec.Command("/proc/self/exe", "child", cfg.RootFS, cfg.Hostname)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWIPC,
		// ไม่ใส่ CLONE_NEWNET / CLONE_NEWUSER ตอนนี้ - เอาแค่เข้า container ได้ก่อน
	}

	must(cmd.Start())

	pid := cmd.Process.Pid
	fmt.Println("child pid:", pid)

	// ต้อง add pid เข้า cgroup "หลัง" Start() เพราะเพิ่งมี pid ตอนนี้
	must(addToCgroup(pid))

	// รอ child ทำงานจบ (เช่น bash exit)
	err := cmd.Wait()
	cleanupCgroup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "child exited:", err)
	}

	if cfg.Cleanup {
		fmt.Println("Cleaning up...")
		if rmErr := removeWorkspace(cfg.Name, cfg); rmErr != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", rmErr)
		}
		fmt.Println("Cleaned up.")
	}
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
	url := fmt.Sprintf(
		"https://partner-images.canonical.com/oci/jammy/current/ubuntu-jammy-oci-%s-root.tar.gz",
		arch,
	)
	archiveFile := fmt.Sprintf("ubuntu-jammy-oci-%s-root.tar.gz", arch)

	// ถ้ามี /bin/bash อยู่แล้วใน rootfs ข้ามทั้งหมด (idempotent เหมือน script เดิม)
	if _, err := os.Stat(filepath.Join(cfg.RootFS, "bin", "bash")); err == nil {
		fmt.Println("Root filesystem already exists, skipping download/extract.")
		return nil
	}

	if err := downloadFile(url, archiveFile); err != nil {
		return fmt.Errorf("download : %w", err)
	}
	if err := extractTarGz(archiveFile, cfg.RootFS); err != nil {
		return fmt.Errorf("extract : %w", err)
	}
	if err := setupResolvConf(cfg.RootFS); err != nil {
		return fmt.Errorf("setup resolv.conf : %w", err)
	}
	if err := setupHostsFile(cfg.RootFS, cfg.Hostname); err != nil {
		return fmt.Errorf("setup hosts : %w", err)
	}
	return nil
}

// ---------------------------------------------------------
// child: รันอยู่ข้างในแล้ว หลังจาก clone ด้วย namespace flags ข้างบน
// ---------------------------------------------------------
func childMount(cfg *Config) {
	fmt.Printf("[child] pid=%d entering container...\n", os.Getpid())

	must(syscall.Sethostname([]byte(cfg.Hostname)))

	// devTarget := filepath.Join(cfg.RootFS, "dev")
	// must(os.MkdirAll(devTarget, 0755))
	// must(syscall.Mount("/dev", devTarget, "", syscall.MS_BIND|syscall.MS_REC, ""))

	// chroot เข้า rootfs
	must(syscall.Chroot(cfg.RootFS))
	must(os.Chdir("/"))

	// mount proc ใหม่ (จำเป็นเพราะอยู่ใน PID namespace ใหม่ที่ยังไม่มี /proc เป็นของตัวเอง)
	must(syscall.Mount("proc", "/proc", "proc", 0, ""))
	defer syscall.Unmount("/proc", 0)

	// mount /dev/pts, /dev/shm แบบขั้นต่ำ (ถ้ายังไม่ทำใน rootfs)
	os.MkdirAll("/dev/pts", 0755)
	os.MkdirAll("/dev/shm", 0755)
	os.MkdirAll("/tmp", 01777)
	syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666")
	syscall.Mount("tmpfs", "/dev/shm", "tmpfs", 0, "")
	syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "mode=1777")

	cmd := exec.Command("/bin/bash")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm"}

	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[child] bash exited with error:", err)
	}

	syscall.Unmount("/tmp", 0)
	syscall.Unmount("/dev/pts", 0)
	syscall.Unmount("/dev/shm", 0)
	// syscall.Unmount("/dev", syscall.MNT_DETACH)
}

func childMKNOD(cfg *Config) error {
	return nil
}

func child(cfg *Config) {
	childMount(cfg)
	// childMKNOD(cfg)
}

// ---------------------------------------------------------
// cgroup v2 helpers
// ---------------------------------------------------------
func setupCgroupSkeleton() error {
	if err := os.MkdirAll(cgroupDir, 0755); err != nil {
		return err
	}
	limits := map[string]string{
		"cpu.max":          "50000 100000",
		"memory.max":       "512M",
		"memory.swap.max":  "0",
		"pids.max":         "128",
		"memory.oom.group": "1",
	}
	for file, val := range limits {
		if err := os.WriteFile(cgroupDir+"/"+file, []byte(val), 0644); err != nil {
			return fmt.Errorf("write %s: %w", file, err)
		}
	}
	return nil
}

func addToCgroup(pid int) error {
	return os.WriteFile(cgroupDir+"/cgroup.procs", []byte(strconv.Itoa(pid)), 0644)
}

func cleanupCgroup() {
	os.Remove(cgroupDir) // rmdir เฉยๆ ถ้ายังมี process อยู่จะ fail ซึ่งโอเค ปล่อยผ่าน
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
