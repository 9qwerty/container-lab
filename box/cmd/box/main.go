// main.go
package main

import (
	"box/internal/app"
	"box/internal/cli"
	"box/internal/config"
	"box/internal/container"
	"box/internal/disk"
	"box/internal/namespace"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Config = config.Config

type CloneConfig struct {
	CLONE_NEWUSER bool
	CLONE_NEWUTS  bool
	CLONE_NEWPID  bool
	CLONE_NEWNS   bool
	CLONE_NEWIPC  bool
	CLONE_NEWNET  bool
}

var detectArch = namespace.DetectArch
var must = namespace.Must

func (c CloneConfig) CloneFlags() uintptr {
	var flags uintptr

	if c.CLONE_NEWUSER {
		flags |= syscall.CLONE_NEWUSER
	}
	if c.CLONE_NEWUTS {
		flags |= syscall.CLONE_NEWUTS
	}
	if c.CLONE_NEWPID {
		flags |= syscall.CLONE_NEWPID
	}
	if c.CLONE_NEWNS {
		flags |= syscall.CLONE_NEWNS
	}
	if c.CLONE_NEWIPC {
		flags |= syscall.CLONE_NEWIPC
	}
	if c.CLONE_NEWNET {
		flags |= syscall.CLONE_NEWNET
	}

	return flags
}

func main() {
	if len(os.Args) < 2 {
		cli.Help()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "reexec":
		var cfg Config
		configData := os.Getenv("CDATA")
		json.Unmarshal([]byte(configData), &cfg)

		sync := os.NewFile(3, "sync")
		buf := make([]byte, 1)
		if _, err := sync.Read(buf); err != nil {
			fmt.Fprintln(os.Stderr, "child: sync read:", err)
			os.Exit(1)
		}
		sync.Close()

		// ★ important: re-exec to get full capabilities back from uid=0 legacy rule
		exe, err := os.Executable()
		if err != nil {
			exe = "/proc/self/exe"
		}
		env := append(os.Environ(), "CDATA="+configData)
		must(syscall.Exec(exe, []string{exe, "child"}, env), "reexec child")
		return

	case "child":
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

	cfg, err := cli.ParseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	switch cfg.Command {
	case "list":
		must(cli.ListWorkspace(cfg), "list workspace")
	case "rm":
		must(cli.RemoveWorkspace(cfg.Name, cfg), "remove workspace")
	case "run":
		if cfg.IsRoot {
			fmt.Println("::Root::")
			runContainer(cfg)
		} else {
			fmt.Println("::Rootless::")
			runContainerRootless(cfg)
		}
	}
}

// ---------------------------------------------------------
// run: ทำงานบน host ปกติ - สร้าง namespace ใหม่ + cgroup แล้ว re-exec ตัวเอง
// ---------------------------------------------------------
func runContainer(cfg *Config) {
	cgroupDir := cfg.CGroupDir
	arch, errArch := detectArch()
	must(errArch, "detect arch")

	dc, errDisk := disk.SetupDisk(cfg.Workspace, cfg.Name)
	must(errDisk, "setup disk")

	must(app.SetupRootfs(arch, cfg), "setup rootfs")

	must(container.SetupCgroupSkeleton(cgroupDir), "setup cgroup skeleton")

	configData, errConfigData := json.Marshal(cfg)
	must(errConfigData, "marshal config")

	syncR, syncW, errPipe := os.Pipe()
	must(errPipe, "create sync pipe")

	nc, errNet := container.DeriveNetConfig(cfg.Name)
	must(errNet, "derive net config")

	cmd := exec.Command("/proc/self/exe", "child")
	cmd.ExtraFiles = []*os.File{syncR}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = append(os.Environ(), "CDATA="+string(configData))

	// ---------------------------------------
	// Namespaces
	// ---------------------------------------
	cloneConfig := CloneConfig{
		CLONE_NEWUSER: false,
		CLONE_NEWUTS:  true,
		CLONE_NEWPID:  true,
		CLONE_NEWNS:   true,
		CLONE_NEWIPC:  true,
		CLONE_NEWNET:  true,
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneConfig.CloneFlags(),
	}

	if cloneConfig.CLONE_NEWUSER {
		hUID, hGID := namespace.HostIDs()
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: hUID, Size: 1},
		}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: hGID, Size: 1},
		}
		cmd.SysProcAttr.GidMappingsEnableSetgroups = false
	}

	// ---------------------------------------
	// Start child
	// ---------------------------------------
	must(cmd.Start(), "start child")
	syncR.Close()

	pid := cmd.Process.Pid
	fmt.Println("child pid:", pid)

	// ---------------------------------------
	// CGroup
	// ---------------------------------------
	must(container.AddToCgroup(pid, cgroupDir), "add cgroup")

	// ---------------------------------------
	// Network
	// ---------------------------------------
	must(container.SetupNetwork(nc, pid), "setup network")
	must(container.ExposePorts(cfg.Ports, nc), "expose ports")

	// ---------------------------------------
	// Release child
	// ---------------------------------------
	if _, err := syncW.Write([]byte{1}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()

		must(err)
	}

	syncW.Close()

	// ---------------------------------------
	// Check memory limit
	// ---------------------------------------
	memoryMax, errMemoryMax := os.ReadFile(
		filepath.Join(cgroupDir, "memory.max"),
	)

	must(errMemoryMax, "read memory.max")

	fmt.Printf(
		"memory.max: %s",
		string(memoryMax),
	)

	// ---------------------------------------
	// Wait
	// ---------------------------------------
	err := cmd.Wait()
	container.CleanupPorts(cfg.Ports, nc)
	container.CleanupNetwork(nc)
	container.CleanupCgroup(cgroupDir)
	disk.CleanupDisk(dc, cfg.Cleanup)
	if err != nil {
		fmt.Fprintln(os.Stderr, "child exited:", err)
	}

	if cfg.Cleanup {
		fmt.Println("Cleaning up...")
		if rmErr := cli.RemoveWorkspace(cfg.Name, cfg); rmErr != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", rmErr)
		}
		fmt.Println("Cleaned up.")
	}
}

// ---------------------------------------------------------
// child: รันอยู่ข้างในแล้ว หลังจาก clone ด้วย namespace flags ข้างบน
// ---------------------------------------------------------
func childMount(cfg *Config) {
	fmt.Printf("[child] pid=%d entering container...\n", os.Getpid())

	must(syscall.Sethostname([]byte(cfg.Hostname)), "sethostname")

	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""), "mount / as private")

	overlay := cfg.Overlay
	must(disk.SetupOverlay(overlay), "setup overlay")
	defer disk.CleanupOverlay(overlay)
	out, err := exec.Command(
		"findmnt",
		"-T",
		overlay.MergedDir,
	).CombinedOutput()

	fmt.Printf(
		"findmnt:\n%s\nerr=%v\n",
		out,
		err,
	)

	switch cfg.DeviceMode {
	case config.DeviceModeBind:
		devTarget := filepath.Join(overlay.MergedDir, "dev")
		fmt.Printf("[child] uid=%d euid=%d gid=%d target=%s\n",
			os.Getuid(), os.Geteuid(), os.Getgid(), devTarget)
		must(os.MkdirAll(devTarget, 0755))
		must(syscall.Mount("/dev", devTarget, "", syscall.MS_BIND|syscall.MS_REC, ""), "mount /dev as bind")
	case config.DeviceModeMKNOD:
		must(os.MkdirAll(filepath.Join(overlay.MergedDir, "dev"), 0755))
		must(container.SetupDevNodes(filepath.Join(overlay.MergedDir, "dev")), "setupDevNodes")
	}

	lxcfsHandles := container.SetupLxcfs()

	// chroot เข้า rootfs
	must(syscall.Chroot(overlay.MergedDir), "chroot")
	must(os.Chdir("/"), "chdir")

	// mount proc ใหม่ (จำเป็นเพราะอยู่ใน PID namespace ใหม่ที่ยังไม่มี /proc เป็นของตัวเอง)
	must(syscall.Mount("proc", "/proc", "proc", 0, ""), "mount proc")
	defer syscall.Unmount("/proc", 0)

	container.MountLxcfs(lxcfsHandles)

	// mount /dev/pts, /dev/shm แบบขั้นต่ำ (ถ้ายังไม่ทำใน rootfs)
	os.MkdirAll("/dev/pts", 0755)
	os.MkdirAll("/dev/shm", 0755)
	os.MkdirAll("/tmp", 01777)
	must(syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620,gid=0"), "mount devpts")
	must(syscall.Mount("tmpfs", "/dev/shm", "tmpfs", 0, ""), "mount tmpfs")
	must(syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "mode=1777"), "mount tmpfs")

	ptmx := "/dev/ptmx"
	if _, err := os.Lstat(ptmx); os.IsNotExist(err) {
		must(os.Symlink("pts/ptmx", ptmx), "symlink pts/ptmx")
	}

	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm"}

	if cfg.InitApp == true {
		app.AppInitial(env)
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

	container.UnmountLxcfs()
	syscall.Unmount("/tmp", syscall.MNT_DETACH)
	syscall.Unmount("/dev/pts", syscall.MNT_DETACH)
	syscall.Unmount("/dev/shm", syscall.MNT_DETACH)
	if cfg.DeviceMode == config.DeviceModeBind {
		syscall.Unmount(filepath.Join(overlay.MergedDir, "dev"), syscall.MNT_DETACH)
	}
}

func child(cfg *Config) {
	sync := os.NewFile(3, "sync")
	defer sync.Close()

	buf := make([]byte, 1)
	if _, err := sync.Read(buf); err != nil {
		fmt.Fprintln(os.Stderr, "sync read:", err)
		os.Exit(1)
	}

	if cfg.IsRoot {
		childMount(cfg)
	} else {
		childMountRootless(cfg)
	}
}

func runContainerRootless(cfg *Config) {
	enabledArmor, errArmor := isAppArmorRestrictUnprivilegedUsernsEnabled()
	if errArmor != nil {
		fmt.Println("Error:", errArmor)
		return
	}
	if enabledArmor {
		fmt.Println("AppArmor restrict_unprivileged_userns is enabled")
		fmt.Println("sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0")
		os.Exit(0)
	} else {
		fmt.Println("AppArmor restrict_unprivileged_userns is disabled")
	}

	arch, errArch := detectArch()
	must(errArch, "detect arch")
	must(app.SetupRootfs(arch, cfg), "setup rootfs")

	configData, errConfigData := json.Marshal(cfg)
	must(errConfigData, "marshal config")

	syncR, syncW, errPipe := os.Pipe()
	must(errPipe, "create sync pipe")

	cmd := exec.Command("/proc/self/exe", "reexec")
	cmd.ExtraFiles = []*os.File{syncR}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = append(os.Environ(), "CDATA="+string(configData))

	// ---------------------------------------
	// Namespaces
	// ---------------------------------------
	cloneConfig := CloneConfig{
		CLONE_NEWUSER: true,
		CLONE_NEWUTS:  true,
		CLONE_NEWPID:  true,
		CLONE_NEWNS:   true,
		CLONE_NEWIPC:  true,
		CLONE_NEWNET:  false,
	}

	hUID, hGID := namespace.HostIDs()

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneConfig.CloneFlags(),
		// UidMappings: []syscall.SysProcIDMap{
		// 	{ContainerID: 0, HostID: hUID, Size: 1},
		// },
		// GidMappings: []syscall.SysProcIDMap{
		// 	{ContainerID: 0, HostID: hGID, Size: 1},
		// },
		// GidMappingsEnableSetgroups: false,
	}

	fmt.Println("host uid:", hUID)
	fmt.Println("host gid:", hGID)

	fmt.Printf("uid mappings: %+v\n", cmd.SysProcAttr.UidMappings)
	fmt.Printf("gid mappings: %+v\n", cmd.SysProcAttr.GidMappings)
	fmt.Printf("clone flags: %x\n", cmd.SysProcAttr.Cloneflags)

	// ---------------------------------------
	// Start child
	// ---------------------------------------
	must(cmd.Start(), "start child")
	syncR.Close()

	pid := cmd.Process.Pid
	fmt.Println("child pid:", pid)

	app.CheckSubuidTools()

	must(exec.Command("newuidmap", strconv.Itoa(pid),
		"0", strconv.Itoa(hUID), "1",
		"1", "100000", "65536",
	).Run(), "newuidmap")

	must(exec.Command("newgidmap", strconv.Itoa(pid),
		"0", strconv.Itoa(hGID), "1",
		"1", "100000", "65536",
	).Run(), "newgidmap")

	// ---------------------------------------
	// Release child
	// ---------------------------------------
	if _, err := syncW.Write([]byte{1}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()

		must(err)
	}

	syncW.Close()

	// ---------------------------------------
	// Wait
	// ---------------------------------------
	err := cmd.Wait()
	if err != nil {
		fmt.Fprintln(os.Stderr, "child exited:", err)
	}

	if cfg.Cleanup {
		fmt.Println("Cleaning up...")
		if rmErr := cli.RemoveWorkspace(cfg.Name, cfg); rmErr != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", rmErr)
		}
		fmt.Println("Cleaned up.")
	}
}

func childMountRootless(cfg *Config) {
	fmt.Printf("[child] pid=%d entering container...\n", os.Getpid())

	fmt.Printf("[child] uid: %d , gid: %d\n", os.Getuid(), os.Getgid())

	data, _ := os.ReadFile("/proc/self/uid_map")
	fmt.Printf("[child] uid_map:\n%s", data)

	data, _ = os.ReadFile("/proc/self/gid_map")
	fmt.Printf("[child] gid_map:\n%s", data)

	data, _ = os.ReadFile("/proc/self/status")

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Cap") {
			fmt.Println("[child]", line)
		}
	}

	if err := syscall.Sethostname([]byte(cfg.Hostname)); err != nil {
		fmt.Println("[child] sethostname:", err)
	} else {
		fmt.Println("[child] sethostname: OK")
	}

	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""), "mount / as private")

	overlay := cfg.Overlay
	must(disk.SetupOverlay(overlay), "setup overlay")
	defer disk.CleanupOverlay(overlay)
	out, err := exec.Command(
		"findmnt",
		"-T",
		overlay.MergedDir,
	).CombinedOutput()

	fmt.Printf(
		"findmnt:\n%s\nerr=%v\n",
		out,
		err,
	)

	switch cfg.DeviceMode {
	case config.DeviceModeBind:
		devTarget := filepath.Join(overlay.MergedDir, "dev")
		fmt.Printf("[child] uid=%d euid=%d gid=%d target=%s\n",
			os.Getuid(), os.Geteuid(), os.Getgid(), devTarget)
		must(os.MkdirAll(devTarget, 0755))
		must(syscall.Mount("/dev", devTarget, "", syscall.MS_BIND|syscall.MS_REC, ""), "mount /dev as bind")
	case config.DeviceModeMKNOD:
		must(os.MkdirAll(filepath.Join(overlay.MergedDir, "dev"), 0755))
		must(container.SetupDevNodes(filepath.Join(overlay.MergedDir, "dev")), "setupDevNodes")
	}

	lxcfsHandles := container.SetupLxcfs()

	// chroot เข้า rootfs
	must(syscall.Chroot(overlay.MergedDir), "chroot")
	must(os.Chdir("/"), "chdir")

	if cfg.Apt {
		must(app.SetupAptRootless(), "setup apt rootless")
	}

	must(syscall.Mount("proc", "/proc", "proc", 0, ""), "mount proc")
	defer syscall.Unmount("/proc", 0)

	container.MountLxcfs(lxcfsHandles)

	// mount /dev/pts, /dev/shm แบบขั้นต่ำ (ถ้ายังไม่ทำใน rootfs)
	os.MkdirAll("/dev/pts", 0755)
	os.MkdirAll("/dev/shm", 0755)
	os.MkdirAll("/tmp", 01777)
	must(syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620,gid=0"), "mount devpts")
	must(syscall.Mount("tmpfs", "/dev/shm", "tmpfs", 0, ""), "mount tmpfs")
	must(syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "mode=1777"), "mount tmpfs")

	ptmx := "/dev/ptmx"
	if _, err := os.Lstat(ptmx); os.IsNotExist(err) {
		must(os.Symlink("pts/ptmx", ptmx), "symlink pts/ptmx")
	}

	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm"}

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

	container.UnmountLxcfs()
	syscall.Unmount("/tmp", syscall.MNT_DETACH)
	syscall.Unmount("/dev/pts", syscall.MNT_DETACH)
	syscall.Unmount("/dev/shm", syscall.MNT_DETACH)
	if cfg.DeviceMode == config.DeviceModeBind {
		syscall.Unmount(filepath.Join(overlay.MergedDir, "dev"), syscall.MNT_DETACH)
	}
}

func isAppArmorRestrictUnprivilegedUsernsEnabled() (bool, error) {
	data, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	if err != nil {
		return false, err
	}

	value := strings.TrimSpace(string(data))

	switch value {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected value: %q", value)
	}
}
