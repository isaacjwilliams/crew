package main

// The crew's services on Linux: systemd user units, installed in
// ~/.config/systemd/user/ from the home's systemd/. These functions are the only
// place crew runs systemctl. services_darwin.go has the same ones for launchd.
// Callers use the names in crewServices only to pass back to these functions.

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// crewServices are the units crew host up enables and handing over disables.
var crewServices = []string{"crew-tick.timer", "crew-gardener.timer", "crew-lead.service"}

// enableServices starts the crew's units now, and at every boot.
func enableServices() { systemctlNow("enable") }

// disableServices stops the crew's units now, and keeps them off.
func disableServices() { systemctlNow("disable") }

func systemctlNow(action string) {
	if exec.Command("systemctl", append([]string{"--user", action, "--now"}, crewServices...)...).Run() != nil {
		warn("couldn't %s %s (systemctl --user)", action, strings.Join(crewServices, " "))
	}
}

// serviceEnabled: crew host up has turned the unit on here (systemctl
// is-enabled).
func serviceEnabled(unit string) bool {
	return exec.Command("systemctl", "--user", "is-enabled", "--quiet", unit).Run() == nil
}

// restartLeadService restarts the lead's Remote Control server, so new phone
// sessions run the lead this home has now.
func restartLeadService() {
	if exec.Command("systemctl", "--user", "restart", "crew-lead.service").Run() != nil {
		warn("couldn't restart crew-lead.service")
	}
}

// servicePath is the PATH an enabled unit runs crew with: service is what runs
// (crew-tick.timer starts crew-tick.service), and fix says how to add a
// directory to it.
func (c *Crew) servicePath(unit string) (service, path, fix string) {
	service = strings.TrimSuffix(unit, filepath.Ext(unit)) + ".service"
	out, _ := exec.Command("systemctl", "--user", "show", service, "-p", "Environment", "--value").Output()
	for _, kv := range strings.Fields(string(out)) {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	return service, path, "add its directory to PATH: systemctl --user edit " + service
}

// installServices installs the home's systemd/ units, and removes the ones it no
// longer ships, disabling them first.
func (in *installer) installServices() error {
	say("Installing the crew's systemd units → ~/.config/systemd/user/")
	dir := filepath.Join(in.home, ".config/systemd/user")
	// The units name ~/crew, the usual home. Only a home elsewhere needs its path
	// written into them.
	custom := in.Home != filepath.Join(in.home, "crew")
	units, _ := filepath.Glob(in.path("systemd", "*"))
	for _, f := range units {
		var content []byte
		if strings.HasSuffix(f, ".service") && custom {
			content = renderLines(f, func(l string) string {
				if strings.HasPrefix(l, "# Environment=CREW_HOME=") {
					return "Environment=CREW_HOME=" + in.Home
				}
				return strings.ReplaceAll(l, "%h/crew", in.Home)
			})
		}
		if err := in.installContent("systemd", f, content, filepath.Join(dir, filepath.Base(f))); err != nil {
			return err
		}
	}
	in.prune("systemd", dir, func(path string) {
		exec.Command("systemctl", "--user", "disable", "--now", filepath.Base(path)).Run()
	})
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}
