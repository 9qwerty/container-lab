package protocol

type Request struct {
	Action string `json:"action"`

	Name   string `json:"name,omitempty"`
	Rootfs string `json:"rootfs,omitempty"`
	Memory string `json:"memory,omitempty"`
	CPU    int    `json:"cpu,omitempty"`
}
