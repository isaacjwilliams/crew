package main

// An operation is a crew command that code inside a long-lived crew process,
// such as crew status's live view, can call directly. Its cmd… function parses
// the command line and calls it; the operation does the work and returns what
// happened as a value, with a refusal as its error. On the way, it tells the
// user things only through its Crew: c.warn for what doesn't stop it, c.printf
// for what it did. For a command run from a shell, those go to the terminal. A
// function on its path that has no Crew (a Project method, say) takes the
// writer c.out() gives.
//
// runInProcess runs an operation on a Crew of its own, which writes nothing to
// the terminal, keeps its warnings for the caller, and reads the shared settings
// afresh. That Crew takes the ledger lock with a file of its own, so two
// operations running at once wait for each other, as two processes do, and it
// lets go of the lock when the operation returns.
//
// followup is the first operation. A command joins when the live view needs it,
// the same way: its path reports through c.warn and c.printf, it returns a
// value, and anything that can only be known by trying goes in that value or in
// an error of its own type.

import (
	"fmt"
	"io"
	"os"
)

// runInProcess runs op for code inside a long-lived crew process. It returns
// op's result, what op warned about, and op's refusal. It reads the shared
// settings again, as a crew process would, since crew.env may have changed
// since the long-lived process started.
func runInProcess[R any](c *Crew, op func(*Crew) (R, error)) (R, []string, error) {
	var none R
	settings, err := sharedSettings(c.Home)
	if err != nil {
		return none, nil, err
	}
	in := &Crew{Home: c.Home, Settings: settings, Host: c.Host, login: c.login, inProcess: true}
	defer in.unlock()
	r, err := op(in)
	return r, in.warnings, err
}

// warn reports what doesn't stop the operation: on stderr, as warn does, or,
// in-process, to the operation's caller.
func (c *Crew) warn(format string, a ...any) {
	if c.inProcess {
		c.warnings = append(c.warnings, fmt.Sprintf(format, a...))
		return
	}
	warn(format, a...)
}

// out is where an operation says what it did: stdout, or nowhere in-process,
// where its result says it instead.
func (c *Crew) out() io.Writer {
	if c.inProcess {
		return io.Discard
	}
	return os.Stdout
}

func (c *Crew) printf(format string, a ...any) { fmt.Fprintf(c.out(), format, a...) }
