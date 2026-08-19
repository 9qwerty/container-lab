// network.go
package main

import (
	"fmt"
	"hash/fnv"
	"os/exec"
	"strconv"
)

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
func deriveNetConfig(name string) (*NetConfig, error) {
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
func setupNetwork(nc *NetConfig, pid int) error {
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

func cleanupNetwork(nc *NetConfig) {
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
