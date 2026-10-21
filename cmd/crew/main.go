// crew is the CLI for the agent crew: the lead, its workers, the gardener and goal
// leads, coordinated through the ledger in the crew home. Run `crew help` for usage.
//
// bin/crew, a small launcher, runs this binary from bin/release/ with CREW_HOME set.
package main

import (
	"errors"
	"fmt"
	"os"
)

// version is stamped in at build time: -ldflags "-X main.version=X.Y.Z".
var version = "dev"

// Every crew command but version and guard, which need no crew home.
var commands = map[string]func(*Crew, []string) error{
	"help":      cmdHelp,
	"-h":        cmdHelp,
	"--help":    cmdHelp,
	"config":    cmdConfig,
	"list":      cmdList,
	"ls":        cmdList,
	"show":      cmdShow,
	"path":      cmdPath,
	"find":      cmdFind,
	"get":       cmdGet,
	"set":       cmdSet,
	"log":       cmdLog,
	"commit":    cmdCommit,
	"push":      cmdPush,
	"proposals": cmdProposals,
	"new":       cmdNew,
	"ready":     cmdReady,
	"promote":   cmdPromote,
	"status":    cmdStatus,
	"project":   cmdProject,
	"checkout":  cmdCheckout,
	"observe":   cmdObserve,
	"feedback":  cmdFeedback,
	"reply":     cmdReply,
	"sync":      cmdSync,
	"dispatch":  cmdDispatch,
	"followup":  cmdFollowup,
	"restack":   cmdRestack,
	"cleanup":   cmdCleanup,
	"goal":      cmdGoal,
	"gardener":  cmdGardener,
	"install":   cmdInstall,
	"doctor":    cmdDoctor,
	"host":      cmdHost,
	"serve":     cmdServe,
	"setup":     cmdSetup,
	"upgrade":   cmdUpgrade,
	"tick":      cmdTick,
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	name := "help"
	if len(args) > 0 {
		name, args = args[0], args[1:]
	}
	switch name {
	case "version":
		fmt.Println(version)
		return 0
	case "guard":
		cmdGuard(args)
		return 0
	}
	cmd, ok := commands[name]
	if !ok {
		return exitCode(fail("unknown command '%s' (see 'crew help')", name))
	}
	c, err := newCrew()
	if err == nil {
		err = cmd(c, args)
	}
	// crew commit --hook runs after an agent's every edit, and must never fail its
	// tool call: not even when the home's settings can't be read. So its errors
	// are dropped, unreported.
	if name == "commit" && len(args) > 0 && args[0] == "--hook" {
		return 0
	}
	return exitCode(err)
}

// exitError ends a command with a code: 1 for a refusal, 4 for one that only has
// to wait (notYet). Its message, if it has one, goes to stderr as "crew: <msg>".
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func fail(format string, a ...any) error {
	return &exitError{1, fmt.Sprintf(format, a...)}
}

// exitNotYet is the exit code of a refusal that only has to wait, such as a
// dispatch held by a cap, so crew tick can try again later without reporting it
// as a failure.
const exitNotYet = 4

func notYet(format string, a ...any) error {
	return &exitError{exitNotYet, fmt.Sprintf(format, a...)}
}

// exitQuietly ends a command with code and no message of its own.
func exitQuietly(code int) error { return &exitError{code, ""} }

func usage(text string) error { return fail("usage: %s", text) }

func warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "crew: "+format+"\n", a...)
}

// exitCode reports err and returns the code crew exits with.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	report(err)
	var e *exitError
	if errors.As(err, &e) {
		return e.code
	}
	return 1
}

// report prints an error's message, if it has one, as crew does when it exits.
func report(err error) {
	if msg := err.Error(); msg != "" {
		fmt.Fprintln(os.Stderr, "crew: "+msg)
	}
}
