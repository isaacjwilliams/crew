package main

import "errors"

// stopProcessesIn would end the background processes still running in a
// directory; see proc_linux.go. macOS has no /proc to find them in.
func stopProcessesIn(dir string) error {
	return errors.New("stopping the processes still running in " + dir + " isn't supported on macOS yet")
}
