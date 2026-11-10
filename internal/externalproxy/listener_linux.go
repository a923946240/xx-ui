//go:build linux

package externalproxy

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}

func stopChild(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

func ownsListener(pid int, address string) bool {
	if pid <= 0 {
		return false
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return false
	}
	return ownsSocket(pid, "tcp", map[string]bool{fmt.Sprintf("0100007F:%04X", port): true}, "0A")
}

// ownsUDPRelay reports whether pid holds an IPv4 UDP socket bound to port on
// loopback or the wildcard address (Mieru binds its SOCKS UDP relay on 0.0.0.0).
func ownsUDPRelay(pid, port int) bool {
	if pid <= 0 || port <= 0 || port > 65535 {
		return false
	}
	return ownsSocket(pid, "udp", map[string]bool{fmt.Sprintf("0100007F:%04X", port): true, fmt.Sprintf("00000000:%04X", port): true}, "")
}

func ownsSocket(pid int, table string, endpoints map[string]bool, state string) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/net/%s", pid, table))
	if err != nil {
		return false
	}
	inodes := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 9 && endpoints[fields[1]] && (state == "" || fields[3] == state) {
			inodes["socket:["+fields[9]+"]"] = true
		}
	}
	if len(inodes) == 0 {
		return false
	}
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if err == nil && inodes[link] {
			return true
		}
	}
	return false
}
