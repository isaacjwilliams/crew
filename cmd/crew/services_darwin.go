package main

// The crew's services on macOS: not supported yet. services_linux.go has the
// systemd ones.

var crewServices = []string{"crew-tick", "crew-gardener", "crew-lead"}

const servicesUnsupported = "the crew's services aren't supported on macOS yet"

func enableServices()                { warn(servicesUnsupported) }
func disableServices()               { warn(servicesUnsupported) }
func serviceEnabled(job string) bool { return false }
func restartLeadService()            { warn(servicesUnsupported) }

func (c *Crew) servicePath(job string) (service, path, fix string) {
	return job, "", servicesUnsupported
}

func (in *installer) installServices() error {
	warn(servicesUnsupported)
	return nil
}
