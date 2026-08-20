// cgroup.go
package main

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

func setupCgroupSkeleton(cgroupDir string) error {
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
