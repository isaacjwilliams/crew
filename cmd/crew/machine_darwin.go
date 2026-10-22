package main

import (
	"os/exec"
	"strings"
)

// machineName is this machine's name in the lease when CREW_HOST isn't set: its
// local hostname, set in System Settings → General → Sharing. Not its hostname,
// which can change with the network (a DHCP server may rename the Mac): a
// machine whose name changes no longer holds its own lease.
func machineName() string {
	out, err := exec.Command("/usr/sbin/scutil", "--get", "LocalHostName").Output()
	if name := strings.TrimSpace(string(out)); err == nil && name != "" {
		return name
	}
	return shortHostname()
}
