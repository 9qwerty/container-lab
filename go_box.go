// main.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

var (
	rootfsDir = getenv("BOX_ROOTFS", "")
	cgroupDir = getenv("BOX_CGROUP", "/sys/fs/cgroup/box-mycontainer")
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if rootfsDir == "" {
		fmt.Println("set BOX_ROOTFS env var to point to your rootfs dir")
		os.Exit(1)
	}
	if len(os.Args) < 2 {
		fmt.Println("usage: mycontainer run|child")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		run()
	case "child":
		child()
	default:
		fmt.Println("unknown command:", os.Args[1])
		os.Exit(1)
	}
}

// ---------------------------------------------------------
// run: ทำงานบน host ปกติ - สร้าง namespace ใหม่ + cgroup แล้ว re-exec ตัวเอง
// ---------------------------------------------------------
func run() {
	must(setupCgroupSkeleton())

	cmd := exec.Command("/proc/self/exe", "child")
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
}

// ---------------------------------------------------------
// child: รันอยู่ข้างในแล้ว หลังจาก clone ด้วย namespace flags ข้างบน
// ---------------------------------------------------------
func child() {
	fmt.Printf("[child] pid=%d entering container...\n", os.Getpid())

	must(syscall.Sethostname([]byte("mycontainer")))

	// chroot เข้า rootfs
	must(syscall.Chroot(rootfsDir))
	must(os.Chdir("/"))

	// mount proc ใหม่ (จำเป็นเพราะอยู่ใน PID namespace ใหม่ที่ยังไม่มี /proc เป็นของตัวเอง)
	must(syscall.Mount("proc", "/proc", "proc", 0, ""))
	defer syscall.Unmount("/proc", 0)

	// mount /dev/pts, /dev/shm แบบขั้นต่ำ (ถ้ายังไม่ทำใน rootfs)
	os.MkdirAll("/dev/pts", 0755)
	os.MkdirAll("/dev/shm", 0755)
	syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666")
	syscall.Mount("tmpfs", "/dev/shm", "tmpfs", 0, "")

	cmd := exec.Command("/bin/bash")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm"}

	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[child] bash exited with error:", err)
	}

	syscall.Unmount("/dev/pts", 0)
	syscall.Unmount("/dev/shm", 0)
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
