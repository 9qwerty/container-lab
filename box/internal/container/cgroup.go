// cgroup.go
package container

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	if err := os.WriteFile(
		filepath.Join(base, "cgroup.subtree_control"),
		[]byte("+cpu +memory +pids"), 0644,
	); err != nil {
		return "", fmt.Errorf("enable delegated controllers: %w", err)
	}
	dir := filepath.Join(base, "gobox-"+name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}
