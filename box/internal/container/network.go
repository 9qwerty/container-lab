// network.go
package container

import (
	"box/internal/config"
	"box/internal/namespace"
	"fmt"
	"hash/fnv"
	"os/exec"
	"strconv"
	"strings"
)

type PortMapping = config.PortMapping

var must = namespace.Must

type NetConfig struct {
	VethHost string
	VethNS   string
	Bridge   string
	BridgeIP string // เช่น 10.200.5.1
	NSIP     string // เช่น 10.200.5.2
	Subnet   string // เช่น 10.200.5.0/24
	OutIf    string // interface ที่ออกเน็ตจริงบน host เช่น eth0
}

// deriveNetConfig
func DeriveNetConfig(name string) (*NetConfig, error) {
	h := fnv.New32a()
	h.Write([]byte(name))
	idx := (h.Sum32() % 250) + 2 // กัน .0 กับ .1

	outIf, err := detectOutInterface()
	if err != nil {
		return nil, err
	}

	return &NetConfig{
		VethHost: fmt.Sprintf("veth-%d", idx),
		VethNS:   fmt.Sprintf("ceth-%d-ns", idx),
		Bridge:   "box0",
		BridgeIP: fmt.Sprintf("10.200.%d.1", idx),
		NSIP:     fmt.Sprintf("10.200.%d.2", idx),
		Subnet:   fmt.Sprintf("10.200.%d.0/24", idx),
		OutIf:    outIf,
	}, nil
}

func detectOutInterface() (string, error) {
	out, err := runOut("ip", "route", "get", "8.8.8.8")
	if err != nil {
		return "", fmt.Errorf("detect out interface: %w", err)
	}
	// parse "8.8.8.8 via 192.168.1.1 dev eth0 ..." เอา token หลัง "dev"
	fields := splitFields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("could not parse output interface from: %s", out)
}

func splitFields(s string) []string {
	var fields []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' {
			if cur != "" {
				fields = append(fields, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		fields = append(fields, cur)
	}
	return fields
}

// setupNetwork: สร้าง veth pair, bridge, ยัดปลายหนึ่งเข้า netns ของ pid
func SetupNetwork(nc *NetConfig, pid int) error {
	// ลบเผื่อค้างจาก run ก่อนหน้า
	run("ip", "link", "del", nc.VethHost)

	// สร้าง bridge บน host ถ้ายังไม่มี
	if !interfaceExists(nc.Bridge) {
		must(run("ip", "link", "add", nc.Bridge, "type", "bridge"))
		must(run("ip", "link", "set", nc.Bridge, "up"))
	}
	// เติม subnet ip ให้ bridge (เผื่อยังไม่มี)
	run("ip", "addr", "add", nc.BridgeIP+"/24", "dev", nc.Bridge) // ignore error ถ้ามีอยู่แล้ว

	// สร้าง veth pair
	if err := run("ip", "link", "add", nc.VethHost, "type", "veth", "peer", "name", nc.VethNS); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}
	must(run("ip", "link", "set", nc.VethHost, "master", nc.Bridge))
	must(run("ip", "link", "set", nc.VethHost, "up"))

	// ย้ายปลาย veth เข้า netns ของ child process โดยตรงผ่าน pid
	if err := run("ip", "link", "set", nc.VethNS, "netns", strconv.Itoa(pid)); err != nil {
		return fmt.Errorf("move veth into netns: %w", err)
	}

	// ตั้งค่าฝั่งใน netns ผ่าน "ip netns exec" หรือ nsenter ด้วย pid ตรงๆ
	nsExec := func(args ...string) error {
		full := append([]string{"-t", strconv.Itoa(pid), "-n"}, args...)
		return run("nsenter", full...)
	}
	must(nsExec("ip", "addr", "add", nc.NSIP+"/24", "dev", nc.VethNS))
	must(nsExec("ip", "link", "set", nc.VethNS, "up"))
	must(nsExec("ip", "link", "set", "lo", "up"))
	must(nsExec("ip", "route", "add", "default", "via", nc.BridgeIP))

	// enable forwarding + NAT บน host
	run("sysctl", "-w", "net.ipv4.ip_forward=1")

	// MASQUERADE ให้ container ออกเน็ตได้ (idempotent: ลบก่อนแล้วค่อยเพิ่ม กันซ้ำ)
	run("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", nc.Subnet, "-o", nc.OutIf, "-j", "MASQUERADE")
	must(run("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", nc.Subnet, "-o", nc.OutIf, "-j", "MASQUERADE"))

	run("iptables", "-D", "FORWARD", "-i", nc.Bridge, "-o", nc.OutIf, "-j", "ACCEPT")
	must(run("iptables", "-A", "FORWARD", "-i", nc.Bridge, "-o", nc.OutIf, "-j", "ACCEPT"))

	run("iptables", "-D", "FORWARD", "-i", nc.OutIf, "-o", nc.Bridge, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	must(run("iptables", "-A", "FORWARD", "-i", nc.OutIf, "-o", nc.Bridge, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"))

	return nil
}

func CleanupNetwork(nc *NetConfig) {
	run("ip", "link", "del", nc.VethHost)
	run("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", nc.Subnet, "-o", nc.OutIf, "-j", "MASQUERADE")
	run("ip", "addr", "del", nc.BridgeIP+"/24", "dev", nc.Bridge)
	// ลบ bridge เฉพาะตอนไม่มี container อื่นใช้อยู่ - เช็คแบบง่ายด้วยจำนวน veth ที่เกาะ bridge
}

func interfaceExists(name string) bool {
	return run("ip", "link", "show", name) == nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func runOut(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return string(out), err
}

// validatePortAvailable: เทียบเท่า validate_port_available() ใน bash เดิม
func validatePortAvailable(hostPort int, nsIP string) error {
	// (1) เช็ค iptables PREROUTING DNAT rule ที่ผูก dport นี้อยู่แล้ว
	out, _ := runOut("iptables", "-t", "nat", "-S", "PREROUTING")
	dportStr := fmt.Sprintf("--dport %d ", hostPort)

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, dportStr) && strings.Contains(line, "-j DNAT") {
			if strings.Contains(line, "--to-destination "+nsIP+":") {
				fmt.Printf("Port %d already forwarded to this container, skipping (idempotent).\n", hostPort)
				return nil
			}
			return fmt.Errorf("host port %d already forwarded by another running container:\n  %s", hostPort, line)
		}
	}

	// (2) เช็คว่า host process ผูก port นี้อยู่แล้วหรือไม่ (ss -tln)
	ssOut, _ := runOut("ss", "-Htln")
	suffix := fmt.Sprintf(":%d", hostPort)
	for _, line := range strings.Split(ssOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && strings.HasSuffix(fields[3], suffix) {
			return fmt.Errorf("host port %d already in use by a process on the host", hostPort)
		}
	}

	// (3) warn ถ้าเป็น privileged port
	if hostPort < 1024 {
		fmt.Printf("Warning: host port %d is a privileged port (<1024).\n", hostPort)
	}

	return nil
}

// exposePorts: setup DNAT rules ให้ทุก port mapping
// เทียบเท่า loop "for mapping in ${PORTS[@]}" ใน setup_ns() เดิม
func ExposePorts(ports []PortMapping, nc *NetConfig) error {

	run("sysctl", "-w", "net.ipv4.conf.all.route_localnet=1")
	run("sysctl", "-w", "net.ipv4.conf.lo.route_localnet=1")
	confBridge := fmt.Sprintf("net.ipv4.conf.%s.route_localnet=1", nc.Bridge)
	run("sysctl", "-w", confBridge)

	for _, pm := range ports {
		if err := validatePortAvailable(pm.HostPort, nc.NSIP); err != nil {
			return fmt.Errorf("port conflict: %w", err)
		}

		fmt.Printf("Exposing port: %d -> %d\n", pm.HostPort, pm.ContainerPort)
		dest := fmt.Sprintf("%s:%d", nc.NSIP, pm.ContainerPort)
		hostPortStr := strconv.Itoa(pm.HostPort)

		// External traffic -> container (PREROUTING, table nat)
		run("iptables", "-t", "nat", "-D", "PREROUTING",
			"-p", "tcp", "-i", nc.OutIf, "--dport", hostPortStr,
			"-j", "DNAT", "--to-destination", dest)
		if err := run("iptables", "-t", "nat", "-A", "PREROUTING",
			"-p", "tcp", "-i", nc.OutIf, "--dport", hostPortStr,
			"-j", "DNAT", "--to-destination", dest); err != nil {
			return fmt.Errorf("PREROUTING rule for port %d: %w", pm.HostPort, err)
		}

		// Internal traffic (localhost) -> container (OUTPUT, table nat)
		run("iptables", "-t", "nat", "-D", "OUTPUT",
			"-p", "tcp", "-d", "127.0.0.1", "--dport", hostPortStr,
			"-j", "DNAT", "--to-destination", dest)
		if err := run("iptables", "-t", "nat", "-A", "OUTPUT",
			"-p", "tcp", "-d", "127.0.0.1", "--dport", hostPortStr,
			"-j", "DNAT", "--to-destination", dest); err != nil {
			return fmt.Errorf("OUTPUT rule for port %d: %w", pm.HostPort, err)
		}

		// Forward traffic -> container (FORWARD, table filter)
		containerPortStr := strconv.Itoa(pm.ContainerPort)
		run("iptables", "-D", "FORWARD",
			"-p", "tcp", "-d", nc.NSIP, "--dport", containerPortStr, "-j", "ACCEPT")
		if err := run("iptables", "-A", "FORWARD",
			"-p", "tcp", "-d", nc.NSIP, "--dport", containerPortStr, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("FORWARD rule for port %d: %w", pm.HostPort, err)
		}
	}

	// Internal traffic -> external (POSTROUTING, table nat)
	run("iptables", "-t", "nat", "-D", "POSTROUTING",
		"-o", nc.Bridge,
		"-m", "addrtype", "--src-type", "LOCAL",
		"-m", "addrtype", "--dst-type", "UNICAST",
		"-j", "MASQUERADE")
	if err := run("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-o", nc.Bridge,
		"-m", "addrtype", "--src-type", "LOCAL",
		"-m", "addrtype", "--dst-type", "UNICAST",
		"-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("POSTROUTING rule for %s: %w", nc.Bridge, err)
	}

	return nil
}

// cleanupPorts: ลบ DNAT rules ทั้งหมด (เรียกตอน container exit)
func CleanupPorts(ports []PortMapping, nc *NetConfig) {
	for _, pm := range ports {
		dest := fmt.Sprintf("%s:%d", nc.NSIP, pm.ContainerPort)
		hostPortStr := strconv.Itoa(pm.HostPort)
		containerPortStr := strconv.Itoa(pm.ContainerPort)

		run("iptables", "-t", "nat", "-D", "PREROUTING",
			"-p", "tcp", "-i", nc.OutIf, "--dport", hostPortStr,
			"-j", "DNAT", "--to-destination", dest)
		run("iptables", "-t", "nat", "-D", "OUTPUT",
			"-p", "tcp", "-d", "127.0.0.1", "--dport", hostPortStr,
			"-j", "DNAT", "--to-destination", dest)
		run("iptables", "-D", "FORWARD",
			"-p", "tcp", "-d", nc.NSIP, "--dport", containerPortStr, "-j", "ACCEPT")
	}
	run("iptables", "-t", "nat", "-D", "POSTROUTING",
		"-o", nc.Bridge,
		"-m", "addrtype", "--src-type", "LOCAL",
		"-m", "addrtype", "--dst-type", "UNICAST",
		"-j", "MASQUERADE")
}
