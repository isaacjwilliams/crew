package main

// The ledger goes to the home's origin, the person's private repo, soon after each
// commit, so a machine that dies loses seconds of decisions. A command never waits
// for that: pushSoon pushes in a process of its own. crew tick ends with a push in
// the foreground, which catches anything a background push missed.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// commit commits the ledger, and nothing else in the home, then starts a push in
// the background. It never fails the command.
func (c *Crew) commit(msg string) {
	top, _ := git(c.Home, "rev-parse", "--show-toplevel")
	if real, err := filepath.EvalSymlinks(c.Home); err != nil || top != real {
		warn("%s is not its own git repo; not committing", c.Home)
		return
	}
	var paths []string
	for _, p := range statePaths {
		if _, err := os.Stat(c.path(p)); err == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return
	}
	if !gitOK(c.Home, append([]string{"add", "-A", "--"}, paths...)...) {
		warn("ledger commit failed (continuing)")
		return
	}
	if gitOK(c.Home, append([]string{"diff", "--cached", "--quiet", "--"}, paths...)...) {
		return
	}
	if !gitOK(c.Home, append([]string{"commit", "-qm", msg, "--"}, paths...)...) {
		warn("ledger commit failed (continuing)")
		return
	}
	c.pushSoon()
}

// ledgerPushed reports whether origin has every ledger commit this machine has.
func (c *Crew) ledgerPushed() bool {
	head, err1 := git(c.Home, "rev-parse", "@")
	up, err2 := git(c.Home, "rev-parse", "@{u}")
	return err1 == nil && err2 == nil && head == up
}

// adoptOriginCommits takes in the commits origin has and this machine hasn't (one
// made on GitHub, or pushed from another clone), by rebasing the ledger onto them
// under the ledger lock, so no crew command commits meanwhile. Call it without the
// ledger lock. It fails if they moved the lease to another machine: that marks
// the lease lost, and this machine must not push over them.
func (c *Crew) adoptOriginCommits(w io.Writer) bool {
	if !gitTo(w, c.Home, "fetch", "-q", "origin") {
		return false
	}
	if holder := c.originHolder(); holder != c.Host {
		os.WriteFile(c.leaseLost(), nil, 0o644)
		fmt.Fprintf(w, "origin says crew runs on %s now; not pushing. The next crew tick stands this machine down.\n", holder)
		return false
	}
	lock, err := flock(c.path(".crew.lock"), 30*time.Second)
	if err == nil {
		defer lock.Close()
		if gitTo(w, c.Home, "pull", "-q", "--rebase", "--autostash") {
			return true
		}
		gitOK(c.Home, "rebase", "--abort")
	}
	fmt.Fprintln(w, "couldn't rebase the ledger onto origin; leaving it for crew tick")
	return false
}

// pushLedger pushes until origin has every ledger commit: up to five tries, a
// second apart when the push fails for a reason like the network. A push origin
// rejects for commits it has that this machine lacks takes those in first.
func (c *Crew) pushLedger(w io.Writer) bool {
	for range 5 {
		if c.ledgerPushed() {
			return true
		}
		out, err := exec.Command("git", "-C", c.Home, "push", "-q", "-u", "origin", "HEAD").CombinedOutput()
		if err == nil {
			continue
		}
		fmt.Fprintln(w, strings.TrimRight(string(out), "\n"))
		if strings.Contains(string(out), "[rejected]") || strings.Contains(string(out), "fetch first") || strings.Contains(string(out), "non-fast-forward") {
			if !c.adoptOriginCommits(w) {
				return false
			}
		} else {
			time.Sleep(time.Second)
		}
	}
	return c.ledgerPushed()
}

// pushNow pushes in the foreground, after any background push that's running.
// Call it without the ledger lock.
func (c *Crew) pushNow(w io.Writer) bool {
	lock, err := flock(c.path(".crew-push.lock"), 60*time.Second)
	if err != nil {
		return false
	}
	defer lock.Close()
	return c.pushLedger(w)
}

// pushSoon starts `crew push --soon` in a session of its own, logged to
// logs/push.log, and doesn't wait for it.
func (c *Crew) pushSoon() {
	if !gitOK(c.Home, "remote", "get-url", "origin") {
		return
	}
	os.MkdirAll(c.path("logs"), 0o755)
	log, err := os.OpenFile(c.path("logs/push.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		warn("can't write %s, so the ledger isn't pushed until the next crew tick: %v", c.path("logs/push.log"), err)
		return
	}
	defer log.Close()
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(log, "can't find crew's binary to push with: %v\n", err)
		return
	}
	cmd := exec.Command(exe, "push", "--soon")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(log, "couldn't start a push: %v\n", err)
		return
	}
	cmd.Process.Release()
}

// crew push [--soon]. With --soon, what each change starts in the background: a
// push that finds another running leaves the work to it. So the running one,
// after it lets go of the lock, checks again whether origin has everything: a
// commit made while it held the lock would otherwise wait for the next commit or
// tick.
func cmdPush(c *Crew, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "--soon":
		for {
			lock, err := flock(c.path(".crew-push.lock"), 0)
			if err != nil {
				return nil
			}
			pushed := c.pushLedger(os.Stdout)
			lock.Close()
			if !pushed {
				return exitQuietly(1)
			}
			if c.ledgerPushed() {
				return nil
			}
		}
	case len(args) == 0:
		if err := c.requireHost(); err != nil {
			return err
		}
		if !c.pushNow(os.Stderr) {
			return fail("couldn't push the ledger to origin; crew tick tries again")
		}
		return nil
	}
	return usage("crew push")
}

// gitTo runs git in dir with its output going to w, and reports whether it succeeded.
func gitTo(w io.Writer, dir string, args ...string) bool {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run() == nil
}
