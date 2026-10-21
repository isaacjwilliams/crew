// crew is the CLI for the agent crew: the lead, its workers, the gardener and goal
// leads, coordinated through the ledger in the crew home. Run `crew help` for usage.
//
// bin/crew, a small launcher, runs this binary from bin/release/ with CREW_HOME set.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// version is stamped in at build time: -ldflags "-X main.version=X.Y.Z".
var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Println(version)
			return
		case "guard":
			cmdGuard(args[1:])
			return
		}
	}
	handOff(args)
}

// handOff runs a command that hasn't moved to Go yet: libexec/crew.sh, in the
// checkout this binary came with, takes over the process.
func handOff(args []string) {
	script := filepath.Join(checkoutOfBinary(), "libexec", "crew.sh")
	err := syscall.Exec(script, append([]string{script}, args...), os.Environ())
	fmt.Fprintf(os.Stderr, "crew: can't run %s: %v\n", script, err)
	os.Exit(1)
}
