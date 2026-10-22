package main

// The crew's services on macOS: launchd jobs, LaunchAgents in the user's gui
// domain, installed in ~/Library/LaunchAgents/ from the home's launchd/. Each
// label is its unit's name without the suffix. These functions are the only
// place crew runs launchctl. services_linux.go has the same ones for systemd.
// Callers use the names in crewServices only to pass back to these functions.
//
// A plist in ~/Library/LaunchAgents/ loads at the next login unless it's
// disabled, so the shipped ones say Disabled. Turning a job on is launchctl
// enable, which launchd keeps over the plist's own Disabled, then bootstrap;
// turning it off is disable, then bootout. launchd never rereads a plist, so a
// loaded job whose plist changes is booted out and bootstrapped again.
//
// Booting out a job can end crew itself, when crew runs in that job: the tick's
// stand-down runs in crew-tick, and a phone lead's crew upgrade in crew-lead. So
// for each job, the bootout is the last thing crew asks of launchd.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// crewServices are the jobs crew host up turns on and handing over turns off.
// The tick's comes last: its stand-down turns the jobs off from inside it, and
// booting it out ends the tick.
var crewServices = []string{"crew-lead", "crew-gardener", "crew-tick"}

// enableServices loads the crew's jobs now, and at every login.
func enableServices() {
	for _, job := range crewServices {
		if launchctl("enable", target(job)) != nil || !bootstrap(job) {
			warn("couldn't turn on %s (launchctl enable, bootstrap)", job)
		}
	}
}

// disableServices unloads the crew's jobs now, and keeps them off.
func disableServices() {
	for _, job := range crewServices {
		turnOff(job)
	}
}

// serviceEnabled: crew host up has turned the job on here, which for launchd
// means it's loaded (launchctl print).
func serviceEnabled(job string) bool { return launchctl("print", target(job)) == nil }

// restartLeadService restarts the lead's Remote Control server, so new phone
// sessions run the lead this home has now.
func restartLeadService() {
	if launchctl("kickstart", "-k", target("crew-lead")) != nil {
		warn("couldn't restart crew-lead (launchctl kickstart)")
	}
}

// servicePath is the PATH a loaded job runs crew with, read from its installed
// plist, and how to add a directory to it.
func (c *Crew) servicePath(job string) (service, path, fix string) {
	fix = fmt.Sprintf("add its directory to PATH in %s, commit it, and run crew install", c.path("launchd", job+".plist"))
	if b, err := os.ReadFile(plistFile(job)); err == nil {
		if p, err := readPlist(b); err == nil {
			env, _ := p["EnvironmentVariables"].(map[string]any)
			path, _ = env["PATH"].(string)
		}
	}
	return job, path, fix
}

// installServices installs the home's launchd/ jobs, reloading the loaded ones
// whose plist changed, and removes the ones it no longer ships, turning them off
// first.
func (in *installer) installServices() error {
	say("Installing the crew's launchd jobs → ~/Library/LaunchAgents/")
	// launchd writes the jobs' output into the home's logs/, and won't make it.
	if err := os.MkdirAll(in.path("logs"), 0o755); err != nil {
		return err
	}
	plists, _ := filepath.Glob(in.path("launchd", "*.plist"))
	for _, f := range plists {
		job := strings.TrimSuffix(filepath.Base(f), ".plist")
		dst := plistFile(job)
		old, _ := os.ReadFile(dst)
		content := renderPlist(f, in.Home, in.home)
		if err := in.installContent("launchd", f, content, dst); err != nil {
			return err
		}
		if !bytes.Equal(old, content) && serviceEnabled(job) {
			reload(job)
		}
	}
	in.prune("launchd", launchAgents(), func(path string) {
		turnOff(strings.TrimSuffix(filepath.Base(path), ".plist"))
	})
	return nil
}

// turnOff keeps a job off, then unloads it.
func turnOff(job string) {
	if launchctl("disable", target(job)) != nil {
		warn("couldn't turn off %s (launchctl disable)", job)
	}
	launchctl("bootout", target(job)) // fails when it isn't loaded, which is fine
}

// reload loads a job again, so launchd reads its new plist. A shell in a
// session of its own does it, so it bootstraps the job even when the bootout
// ends crew.
func reload(job string) {
	script := `/bin/launchctl bootout "$1"
for i in 1 2 3 4 5; do /bin/launchctl bootstrap "$2" "$3" && exit; sleep 1; done
exit 1`
	sh := exec.Command("/bin/sh", "-c", script, "sh", target(job), domain(), plistFile(job))
	sh.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if sh.Run() != nil {
		warn("couldn't load %s again (launchctl bootout, bootstrap)", job)
	}
}

// bootstrap loads a job, unless it's loaded already. Just after a bootout,
// launchd can refuse it for a moment, so it tries for a few seconds.
func bootstrap(job string) bool {
	for range 5 {
		if serviceEnabled(job) || launchctl("bootstrap", domain(), plistFile(job)) == nil {
			return true
		}
		time.Sleep(time.Second)
	}
	return serviceEnabled(job)
}

func launchctl(args ...string) error { return exec.Command("/bin/launchctl", args...).Run() }

// domain is the user's gui domain, where LaunchAgents run while they're logged in.
func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func target(job string) string { return domain() + "/" + job }

func launchAgents() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/LaunchAgents")
}

func plistFile(job string) string { return filepath.Join(launchAgents(), job+".plist") }
