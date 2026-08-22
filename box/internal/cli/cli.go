// cli.go
package cli

import (
	"box/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config = config.Config
type DeviceMode = config.DeviceMode
type OverlayConfig = config.OverlayConfig
type PortMapping = config.PortMapping

const (
	DeviceModeBind  = config.DeviceModeBind
	DeviceModeMKNOD = config.DeviceModeMKNOD
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Help() {
	fmt.Println(`Usage: gobox <command> [options]

Commands:
  run [options]           Run a command in the chroot (see run options below)
  list, ls                List chroots in workspace
  rm <name>               Remove a chroot by name
  -h, --help              Show this help message

Options for 'run':
  --name <name>           Set the name (default: auto-generated)
  --hostname <hostname>   Set the hostname (default: same as name)
  --remove, --rm          Remove the chroot after exiting
  --port, -p <mapping>    Expose port, format HOST:CONTAINER or PORT
  --mount-mode <mode>     Set the mount mode (default: bind)
  --init                  Initialize the chroot with an application
  --apt                   Use apt as the package manager

Examples:
  gobox run
  gobox run --name mybox --hostname mybox --rm
  gobox list
  gobox rm mybox`)
}

func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(home, "gobox")
}

// ---------------------------------------------------------
// parseCLI: เทียบเท่า common_cli() ใน bash
// ---------------------------------------------------------
func ParseCLI(args []string) (*Config, error) {
	if len(args) == 0 {
		Help()
		os.Exit(0)
	}

	cfg := &Config{}
	goboxWorkspace := getenv("GOBOX_WORKSPACE", "")
	if goboxWorkspace != "" {
		cfg.Workspace = goboxWorkspace
	} else {
		cfg.Workspace = getHomeDir()
	}
	// default Config
	cfg.DeviceMode = DeviceModeBind
	cfg.Cleanup = false
	cfg.Remove = false
	cfg.CGroupDir = "/sys/fs/cgroup/gobox"
	cfg.InitApp = false
	cfg.Apt = false
	if os.Getuid() == 0 {
		cfg.IsRoot = true
	} else {
		cfg.IsRoot = false
		// sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
	}

	switch args[0] {
	case "-h", "--help":
		Help()
		os.Exit(0)

	case "list", "ls":
		cfg.Command = "list"
		return cfg, nil

	case "run":
		cfg.Command = "run"
		cfg.Name = generateUniqueAlias(cfg.Workspace) // ค่า default ก่อน parse option ทับ
		i := 1
		for i < len(args) {
			switch args[i] {
			case "--name":
				val, next, err := requireValue(args, i, "--name")
				if err != nil {
					return nil, err
				}
				cfg.Name = val
				i = next

			case "--hostname":
				val, next, err := requireValue(args, i, "--hostname")
				if err != nil {
					return nil, err
				}
				cfg.Hostname = val
				i = next

			case "--remove", "--rm":
				cfg.Cleanup = true
				i++

			case "--port", "-p":
				val, next, err := requireValue(args, i, "--port")
				if err != nil {
					return nil, err
				}
				pm, err := parsePortMapping(val)
				if err != nil {
					return nil, err
				}
				// กันซ้ำ host port ภายใน argument เดียวกัน (เทียบกับ loop เช็คซ้ำใน bash)
				for _, existing := range cfg.Ports {
					if existing.HostPort == pm.HostPort {
						return nil, fmt.Errorf("duplicate --port %d specified twice", pm.HostPort)
					}
				}
				cfg.Ports = append(cfg.Ports, pm)
				i = next

			case "--device-mode":
				val, next, err := requireValue(args, i, "--device-mode")
				if err != nil {
					return nil, err
				}
				mode, err := parseDeviceMode(val)
				if err != nil {
					return nil, err
				}
				cfg.DeviceMode = mode
				i = next
			case "--init":
				cfg.InitApp = true
				i++
			case "--apt":
				cfg.Apt = true
				i++

			default:
				Help()
				return nil, fmt.Errorf("unknown option for run: %s", args[i])
			}
		}
		goboxRootFS := getenv("GOBOX_ROOTFS", "")
		if goboxRootFS != "" {
			cfg.RootFS = goboxRootFS
		} else {
			cfg.RootFS = filepath.Join(cfg.Workspace, cfg.Name)
		}
		cfg.CGroupDir = filepath.Join("/sys/fs/cgroup", cfg.Name)
		cfg.Overlay = OverlayConfig{
			LowerDir:  filepath.Join(goboxWorkspace, "images/ubuntu/rootfs"),
			UpperDir:  filepath.Join(cfg.RootFS, "upper"),
			WorkDir:   filepath.Join(cfg.RootFS, "work"),
			MergedDir: filepath.Join(cfg.RootFS, "merged"),
		}

	case "rm":
		cfg.Command = "rm"
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			cfg.Name = args[1]
		} else {
			cfg.Command = "list" // เหมือน bash: rm เฉยๆ ไม่มีชื่อ -> list แล้วจบ
			return cfg, nil
		}
		cfg.Remove = true

	default:
		Help()
		return nil, fmt.Errorf("unknown option: %s", args[0])
	}

	if cfg.Command == "run" && cfg.Hostname == "" {
		cfg.Hostname = cfg.Name
	}

	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	return cfg, nil
}

// requireValue: ดึงค่าถัดจาก flag เช่น --name mybox -> "mybox"
// เทียบกับ "${2:?--name requires a value}" ใน bash
func requireValue(args []string, i int, flagName string) (value string, nextIndex int, err error) {
	if i+1 >= len(args) {
		return "", 0, fmt.Errorf("%s requires a value", flagName)
	}
	return args[i+1], i + 2, nil
}

// ---------------------------------------------------------
// list_workspace
// ---------------------------------------------------------
func ListWorkspace(cfg *Config) error {
	fmt.Println("Listing chroots in", cfg.Workspace, "...")

	entries, err := os.ReadDir(cfg.Workspace)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "No chroots found.")
			return nil
		}
		return err
	}

	var folders []string
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
		}
	}

	if len(folders) == 0 {
		fmt.Fprintln(os.Stderr, "No chroots found.")
		return nil
	}

	fmt.Println("Chroots found:")
	for _, f := range folders {
		fmt.Println("  " + f)
	}
	return nil
}

// ---------------------------------------------------------
// remove_workspace (ใช้ตอน command == "rm")
// ---------------------------------------------------------
func RemoveWorkspace(name string, cfg *Config) error {
	chrootDir := filepath.Join(cfg.Workspace, name)

	if _, err := os.Stat(chrootDir); os.IsNotExist(err) {
		fmt.Printf("no such chroot: %s\n", name)
		return nil
	}

	fmt.Println("Removing chroot at", chrootDir, "...")
	if err := os.RemoveAll(chrootDir); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	fmt.Println("Removed.")
	return nil
}

// ---------------------------------------------------------
// port mapping parse + validate (เทียบ parse_port_mapping)
// ---------------------------------------------------------
func parsePortMapping(mapping string) (PortMapping, error) {
	var hostStr, containerStr string

	if strings.Contains(mapping, ":") {
		parts := strings.SplitN(mapping, ":", 2)
		hostStr, containerStr = parts[0], parts[1]
	} else {
		hostStr, containerStr = mapping, mapping
	}

	hostPort, err := strconv.Atoi(hostStr)
	if err != nil || hostPort < 1 || hostPort > 65535 {
		return PortMapping{}, fmt.Errorf("invalid host port: %s", hostStr)
	}

	containerPort, err := strconv.Atoi(containerStr)
	if err != nil || containerPort < 1 || containerPort > 65535 {
		return PortMapping{}, fmt.Errorf("invalid container port: %s", containerStr)
	}

	return PortMapping{HostPort: hostPort, ContainerPort: containerPort}, nil
}

func parseDeviceMode(mode string) (DeviceMode, error) {
	switch mode {
	case "bind":
		return DeviceModeBind, nil
	case "mknod":
		return DeviceModeMKNOD, nil
	default:
		return 0, fmt.Errorf("invalid device mode: %s", mode)
	}
}
