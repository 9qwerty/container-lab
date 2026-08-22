// cgroup.go
package container

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ---------------------------------------------------------
// cgroup v2 helpers
// ---------------------------------------------------------
func enableCpusetController() error {
	return os.WriteFile("/sys/fs/cgroup/cgroup.subtree_control", []byte("+cpuset"), 0644)
}

func SetupCgroupSkeleton(cgroupDir string) error {
	if err := enableCpusetController(); err != nil {
		fmt.Fprintln(os.Stderr, "[cgroup] warn: enable cpuset failed:", err)
	}

	if err := os.MkdirAll(cgroupDir, 0755); err != nil {
		return err
	}
	limits := map[string]string{
		"cpu.max":          "50000 100000",
		"cpuset.cpus":      "0",
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

func AddToCgroupBackup(pid int, cgroupDir string) error {
	return os.WriteFile(cgroupDir+"/cgroup.procs", []byte(strconv.Itoa(pid)), 0644)
}

func AddToCgroup(pid int, cgroupDir string) error {
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

func CleanupCgroup(cgroupDir string) {
	if err := os.Remove(cgroupDir); err != nil {
		fmt.Println("cleanup cgroup:", err)
	}
}

// container/cgroup_rootless.go
func RootlessCgroupBase() (string, error) {
	uid := os.Getuid()
	base := fmt.Sprintf(
		"/sys/fs/cgroup/user.slice/user-%d.slice/user@%d.service",
		uid, uid,
	)
	if _, err := os.Stat(filepath.Join(base, "cgroup.controllers")); err != nil {
		return "", fmt.Errorf("delegated cgroup not found at %s (is user@%d.service running? try: loginctl enable-linger %s): %w",
			base, uid, os.Getenv("USER"), err)
	}
	return base, nil
}

func SetupCgroupSkeletonRootless(name string) (string, error) {
	base, err := RootlessCgroupBase()
	if err != nil {
		return "", err
	}
	// ต้องเปิด controller ที่ระดับ parent ก่อนถึงจะสร้าง subgroup แล้วตั้ง limit ได้
	controllers := "+cpu +memory +pids"
	hasCpuset := CheckCpusetDelegated(base)
	if hasCpuset {
		controllers += " +cpuset"
	}
	if err := os.WriteFile(
		filepath.Join(base, "cgroup.subtree_control"),
		[]byte(controllers), 0644,
	); err != nil {
		return "", fmt.Errorf("enable delegated controllers: %w", err)
	}
	dir := filepath.Join(base, "gobox-"+name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	if hasCpuset {
		effCpus, err := os.ReadFile(filepath.Join(base, "cpuset.cpus.effective"))
		if err != nil {
			return "", fmt.Errorf("read effective cpus: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cpuset.cpus"), effCpus, 0644); err != nil {
			return "", fmt.Errorf("set cpuset.cpus: %w", err)
		}

		effMems, err := os.ReadFile(filepath.Join(base, "cpuset.mems.effective"))
		if err != nil {
			return "", fmt.Errorf("read effective mems: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cpuset.mems"), effMems, 0644); err != nil {
			return "", fmt.Errorf("set cpuset.mems: %w", err)
		}
	}

	limits := map[string]string{
		"cpu.max":          "50000 100000",
		"cpuset.cpus":      "0",
		"memory.max":       "512M",
		"memory.swap.max":  "0",
		"pids.max":         "128",
		"memory.oom.group": "1",
	}
	if !hasCpuset {
		delete(limits, "cpuset.cpus")
	}
	for file, val := range limits {
		if err := os.WriteFile(dir+"/"+file, []byte(val), 0644); err != nil {
			return "", fmt.Errorf("write %s: %w", file, err)
		}
	}

	return dir, nil
}

func mustLookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		panic(fmt.Sprintf("executable %q not found: %v", name, err))
	}
	return path
}

func EnsureInDelegatedCgroup() error {
	base, err := RootlessCgroupBase()
	if err != nil {
		return err
	}
	cur, _ := os.ReadFile("/proc/self/cgroup")
	if strings.Contains(string(cur), base[len("/sys/fs/cgroup"):]) {
		return nil // already inside delegated subtree
	}
	// re-exec ตัวเองผ่าน systemd-run เพื่อย้ายเข้า delegated subtree
	args := append([]string{"--user", "--scope", "--", os.Args[0]}, os.Args[1:]...)
	return syscall.Exec(mustLookPath("systemd-run"), append([]string{"systemd-run"}, args...), os.Environ())
}

func CheckCpusetDelegated(base string) bool {
	data, err := os.ReadFile(filepath.Join(base, "cgroup.controllers"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: cannot read cgroup.controllers: %v\n", err)
		return false
	}

	controllers := strings.Fields(string(data))
	for _, c := range controllers {
		if c == "cpuset" {
			return true
		}
	}

	// หา path ของ user.slice กับ user-<uid>.slice เพื่อบอกคำสั่งที่ต้องรัน
	uid := os.Getuid()
	userSlice := "/sys/fs/cgroup/user.slice/cgroup.subtree_control"
	userUidSlice := fmt.Sprintf("/sys/fs/cgroup/user.slice/user-%d.slice/cgroup.subtree_control", uid)

	fmt.Fprintln(os.Stderr, "warn: controller \"cpuset\" is not delegated to this user session.")
	fmt.Fprintln(os.Stderr, "      cpuset.cpus limits will be skipped. To enable it, run:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  sudo su -c 'echo \"+cpuset\" > %s'\n", userSlice)
	fmt.Fprintf(os.Stderr, "  sudo su -c 'echo \"+cpuset\" > %s'\n", userUidSlice)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "      (note: sudo echo ... > file will NOT work due to shell redirection running as your user; use sudo su -c or sudo tee instead)")

	return false
}

func CheckLinger() error {
	u, err := user.Current()
	if err != nil {
		return err
	}

	cmd := exec.Command("loginctl", "show-user", u.Username, "-p", "Linger")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check linger: %w", err)
	}

	// output: Linger=yes
	if strings.TrimSpace(string(out)) == "Linger=yes" {
		return nil
	}

	fmt.Printf(`
Linger is not enabled for user %s.

Please run:

    sudo loginctl enable-linger %s

Then run this program again.
`, u.Username, u.Username)

	return fmt.Errorf("systemd user linger is not enabled")
}

func SelfMigrateToDelegated() error {
	base, err := RootlessCgroupBase() // .../user@1000.service
	if err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(base, "cgroup.procs"),
		[]byte(strconv.Itoa(os.Getpid())),
		0644,
	)
}
