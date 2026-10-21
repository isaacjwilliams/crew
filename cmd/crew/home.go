package main

import (
	"os"
	"path/filepath"
)

// The ledger: what crew and the agents write in the home. The rest of the home is
// crew's own code and config, which comes from upstream.
var statePaths = []string{"projects", "reports"}

// crewHome is the crew home: a clone of the crew repo, with crew's code, its
// config and the ledger. It's $CREW_HOME, which bin/crew sets to its own checkout
// unless it's set already, else the checkout this binary is in.
func crewHome() string {
	if h := os.Getenv("CREW_HOME"); h != "" {
		return h
	}
	return checkoutOfBinary()
}

// checkoutOfBinary is the checkout this binary was run from: it's
// <checkout>/bin/release/crew-<version>.
func checkoutOfBinary() string {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(exe)))
}
