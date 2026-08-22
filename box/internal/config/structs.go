package config

type DeviceMode int

const (
	DeviceModeBind DeviceMode = iota
	DeviceModeMKNOD
)

type OverlayConfig struct {
	LowerDir  string
	UpperDir  string
	WorkDir   string
	MergedDir string
}

type PortMapping struct {
	HostPort      int
	ContainerPort int
}

type Config struct {
	IsRoot     bool
	Workspace  string
	RootFS     string
	CGroupDir  string
	Command    string // "run" | "list" | "rm"
	Name       string
	Hostname   string
	DeviceMode DeviceMode
	Cleanup    bool // --remove/--rm
	Remove     bool // สำหรับ command "rm"
	Ports      []PortMapping
	InitApp    bool
	Overlay    OverlayConfig
}
