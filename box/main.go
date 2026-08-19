// main.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
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

	/*
		if os.Args[1] == "child" {
			if len(os.Args) < 4 {
				fmt.Fprintln(os.Stderr, "child: missing rootfs/hostname args")
				os.Exit(1)
			}
			child(&Config{RootFS: os.Args[2], Hostname: os.Args[3]})
			return
		}
	*/

	if os.Args[1] == "child" {
		var cfg Config
		configData := os.Getenv("CDATA")
		if configData == "" {
			fmt.Fprintln(os.Stderr, "child: missing config data")
			os.Exit(1)
		}
		if err := json.Unmarshal([]byte(configData), &cfg); err != nil {
			fmt.Fprintln(os.Stderr, "child: invalid config:", err)
			os.Exit(1)
		}
		child(&cfg)
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
	cgroupDir := cfg.CGroupDir
	arch, errArch := detectArch()
	must(errArch)
	must(setupRootfs(arch, cfg))

	must(setupCgroupSkeleton(cgroupDir))

	configData, errConfigData := json.Marshal(cfg)
	must(errConfigData)

	cmd := exec.Command("/proc/self/exe", "child")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = append(os.Environ(), "CDATA="+string(configData))

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
	must(addToCgroup(pid, cgroupDir))

	memoryMax, errMemoryMax := os.ReadFile(filepath.Join(cgroupDir, "memory.max"))
	must(errMemoryMax)
	fmt.Printf("memory.max: %s", string(memoryMax))

	// รอ child ทำงานจบ (เช่น bash exit)
	err := cmd.Wait()
	cleanupCgroup(cgroupDir)
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

	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))

	switch cfg.DeviceMode {
	case DeviceModeBind:
		devTarget := filepath.Join(cfg.RootFS, "dev")
		must(os.MkdirAll(devTarget, 0755))
		must(syscall.Mount("/dev", devTarget, "", syscall.MS_BIND|syscall.MS_REC, ""))
	case DeviceModeMKNOD:
		must(os.MkdirAll(filepath.Join(cfg.RootFS, "dev"), 0755))
		must(setupDevNodes(filepath.Join(cfg.RootFS, "dev")))
	}

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
	must(syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620,gid=5"))
	must(syscall.Mount("tmpfs", "/dev/shm", "tmpfs", 0, ""))
	must(syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "mode=1777"))

	ptmx := "/dev/ptmx"
	if _, err := os.Lstat(ptmx); os.IsNotExist(err) {
		must(os.Symlink("pts/ptmx", ptmx))
	}

	time.Sleep(2 * time.Second)

	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm"}

	// -------------------------
	// apt update
	// -------------------------
	if cfg.InitApp == true {
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
	}

	// -------------------------
	// install essential packages
	// -------------------------
	if cfg.InitApp == true {
		fmt.Println("[child] apt update...")
		aptUpdate := exec.Command("/usr/bin/apt-get", "install", "-y", "iproute2", "iputils-ping", "net-tools", "curl", "htop")
		aptUpdate.Stdin = os.Stdin
		aptUpdate.Stdout = os.Stdout
		aptUpdate.Stderr = os.Stderr
		aptUpdate.Env = env

		if err := aptUpdate.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "[child] apt install failed:", err)
			return
		}
	}

	// -------------------------
	// install python3
	// -------------------------
	if cfg.InitApp == true {
		fmt.Println("[child] installing python3...")
		aptInstall := exec.Command(
			"/usr/bin/apt-get",
			"install",
			"-y",
			"python3",
			"python3-pip",
			"python3-venv",
		)
		aptInstall.Stdin = os.Stdin
		aptInstall.Stdout = os.Stdout
		aptInstall.Stderr = os.Stderr
		aptInstall.Env = env

		if err := aptInstall.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "[child] apt install python3 failed:", err)
			return
		}
	}

	// -------------------------
	// bash
	// -------------------------
	cmd := exec.Command("/bin/bash")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[child] bash exited with error:", err)
	}

	syscall.Unmount("/tmp", syscall.MNT_DETACH)
	syscall.Unmount("/dev/pts", syscall.MNT_DETACH)
	syscall.Unmount("/dev/shm", syscall.MNT_DETACH)
	if cfg.DeviceMode == DeviceModeBind {
		syscall.Unmount(filepath.Join(cfg.RootFS, "dev"), syscall.MNT_DETACH)
	}
}

func child(cfg *Config) {
	childMount(cfg)
}

// ---------------------------------------------------------
// cgroup v2 helpers
// ---------------------------------------------------------
func setupCgroupSkeleton(cgroupDir string) error {
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

func addToCgroupBackup(pid int, cgroupDir string) error {
	return os.WriteFile(cgroupDir+"/cgroup.procs", []byte(strconv.Itoa(pid)), 0644)
}

func addToCgroup(pid int, cgroupDir string) error {
	path := filepath.Join(cgroupDir, "cgroup.procs")

	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("add pid %d to cgroup: %w", pid, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read cgroup.procs: %w", err)
	}

	fmt.Printf("cgroup.procs: %s", data)

	return nil
}

func cleanupCgroup(cgroupDir string) {
	if err := os.Remove(cgroupDir); err != nil {
		fmt.Println("cleanup cgroup:", err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
