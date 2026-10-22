package main

// One machine runs the crew at a time: the one the home's `host` file names. A git
// push claims it (crew host up), so two machines can't both think they do. Every
// change checks it (lock), and so do the commands that start sessions, before they
// start one. Reads work on any machine.

import (
	"os"
	"strings"
)

// The machines' units: crew host up enables them, and handing over disables them.
var crewUnits = []string{"crew-tick.timer", "crew-gardener.timer", "crew-lead.service"}

// leaseLost is made by a push that found on origin that another machine claimed
// the lease. requireHost refuses every change while it exists; the next crew tick
// stands this machine down and removes it, as does crew host up.
func (c *Crew) leaseLost() string { return c.path(".crew-lease-lost") }

// holder is the machine the home's host file names, or nothing.
func (c *Crew) holder() string {
	b, _ := os.ReadFile(c.path("host"))
	return strings.TrimRight(string(b), "\n")
}

// originHolder is the holder as origin last told this machine, or nothing (after
// crew host down, say). ok is false when there's no origin branch to read it
// from: that says nothing about who holds the lease.
func (c *Crew) originHolder() (holder string, ok bool) {
	up, err := git(c.Home, "rev-parse", "--verify", "--quiet", "@{u}")
	if err != nil {
		return "", false
	}
	h, _ := git(c.Home, "show", up+":host")
	return h, true
}

func (c *Crew) leaseSince() string {
	s, _ := git(c.Home, "log", "-1", "--format=%cr", "--", "host")
	return s
}

// previousHolder is the last machine before this one that ran the crew. The host
// file names this machine now, so it's found in the file's history: the newest
// other name.
func (c *Crew) previousHolder() string {
	commits, _ := git(c.Home, "log", "--format=%H", "--", "host")
	for _, commit := range strings.Fields(commits) {
		if h, _ := git(c.Home, "show", commit+":host"); h != "" && h != c.Host {
			return h
		}
	}
	return "another machine"
}

// requireHost fails unless this machine holds the lease: the host file names it,
// and no push has found that origin moved it elsewhere.
func (c *Crew) requireHost() error {
	if exists(c.leaseLost()) {
		moved, _ := c.originHolder()
		if moved == "" {
			moved = "another machine"
		}
		return fail("crew has moved to %s; this machine can only read until its next crew tick stands it down", moved)
	}
	holder := c.holder()
	if holder == "" {
		return fail("no machine runs the crew; this one can only read (crew host up runs it here)")
	}
	if holder != c.Host {
		return fail("crew runs on %s; this machine can only read (crew host up --take moves it here)", holder)
	}
	return nil
}
