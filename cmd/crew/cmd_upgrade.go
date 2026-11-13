package main

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// crew upgrade brings in the newest release of crew from the public repo
// (upstream): the newest v<version> tag there, not its main. The release's
// binary is fetched first, by the launcher, so a release whose binaries aren't
// up yet changes nothing. Then crew upgrade says what the upgrade does to the
// sessions working now and asks to go ahead, and pauses every project, so they
// wrap up before the release is merged and installed (installRelease). Then it
// lifts the pause it made, pushes, restarts the lead's server, and removes the
// binaries of other versions.
func cmdUpgrade(c *Crew, args []string) error {
	yes := false
	switch {
	case len(args) == 1 && (args[0] == "--yes" || args[0] == "-y"):
		yes = true
	case len(args) > 0:
		return usage("crew upgrade [--yes]")
	}
	if !gitOK(c.Home, "remote", "get-url", "upstream") {
		return fail("the home has no upstream (the public crew repo) to upgrade from")
	}
	if err := c.lock(); err != nil {
		return err
	}
	tag, err := c.upgradeTarget()
	c.unlock()
	if err != nil {
		return err
	}
	next := strings.TrimPrefix(tag, "v")
	// Only the launcher knows how to download a binary, so it fetches this one.
	fetch := exec.Command(c.path("bin/crew"), "version")
	fetch.Env, fetch.Stderr = append(os.Environ(), "CREW_VERSION="+next), os.Stderr
	if out, err := fetch.Output(); err != nil || strings.TrimSpace(string(out)) != next {
		return fail("couldn't get crew %s's binary, so nothing has changed. Its release may still be building; try again in a few minutes.", next)
	}
	projects, err := c.ledgerProjects("")
	if err != nil {
		return err
	}
	if err := c.confirmUpgrade(projects, next, yes); err != nil {
		return err
	}
	err = c.pause(projects, pausedByUpgrade, false)
	if err == nil {
		err = c.installRelease(tag, yes)
	}
	// A merge left in progress keeps the crew paused: git takes no ledger commit
	// until it's settled. The crew upgrade run after it lifts the pause.
	if gitOK(c.Home, "rev-parse", "-q", "--verify", "MERGE_HEAD") {
		return err
	}
	if err := cmp.Or(err, c.liftUpgradePause(projects)); err != nil {
		return err
	}
	if !c.pushNow(os.Stdout) {
		warn("couldn't push the upgrade to origin; crew tick pushes it later")
	}
	// The lead's server runs the new lead from now on, for new phone sessions.
	restartLeadService()
	now := c.crewVersion()
	old, _ := filepath.Glob(c.path("bin/release/crew-*"))
	for _, f := range old {
		if filepath.Base(f) != "crew-"+now {
			os.Remove(f)
		}
	}
	fmt.Println("crew upgraded to " + now)
	return nil
}

// confirmUpgrade says which sessions are working now, and what the upgrade will
// do to them, and asks to go ahead. --yes is the go-ahead given beforehand.
// Without it, it asks on the terminal, and fails if there's none rather than
// read an answer from a pipe.
func (c *Crew) confirmUpgrade(projects []*Project, next string, yes bool) error {
	wait, err := intSetting(c.Settings, "WRAP_UP_SECONDS")
	if err != nil {
		return err
	}
	busy, err := c.busySessions(projects)
	if err != nil {
		return err
	}
	fmt.Printf("crew %s → %s\n", c.crewVersion(), next)
	if len(busy) == 0 {
		fmt.Println("No sessions are working.")
	} else {
		fmt.Printf("Working now: %s. They'll be asked to wrap up, and stopped after up to %s.\n", labels(busy), plainDuration(wait))
	}
	fmt.Println("Workers and goal leads whose instructions changed start fresh when they next have work. Your own leads will tell you if they need restarting.")
	switch {
	case yes:
		return nil
	case !stdinIsTerminal():
		return fail("nothing has changed; run crew upgrade --yes to go ahead")
	case !confirm("Upgrade now?", false):
		return fail("nothing has changed")
	}
	return nil
}

// installRelease merges the release tag into the home (mergeRelease), and the
// new version installs itself, so a change to installing takes effect now.
func (c *Crew) installRelease(tag string, yes bool) error {
	if err := c.mergeRelease(tag); err != nil {
		return err
	}
	install := exec.Command(c.path("bin/crew"), "install")
	if yes {
		install.Args = append(install.Args, "--yes")
	}
	install.Stdin, install.Stdout, install.Stderr = os.Stdin, os.Stdout, os.Stderr
	if install.Run() != nil {
		return fail("couldn't install the new version; run crew install to try again")
	}
	return nil
}

// liftUpgradePause lifts the pauses crew upgrade made: this run's, and any a
// run cut short left. It knows them by their mark, so it leaves the user's own.
func (c *Crew) liftUpgradePause(projects []*Project) error {
	ours := slices.DeleteFunc(slices.Clone(projects), func(p *Project) bool { return p.PausedBy() != pausedByUpgrade })
	return c.resume(ours)
}

// upgradeTarget is the release crew upgrade is to merge: the newest one upstream
// has. It refuses a home with uncommitted changes. Its callers hold the ledger
// lock.
func (c *Crew) upgradeTarget() (string, error) {
	if out, _ := git(c.Home, "status", "--porcelain"); out != "" {
		return "", fail("the home has uncommitted changes; commit or discard them first (git -C %s status)", c.Home)
	}
	tag, err := newestRelease(c.Home)
	if err != nil {
		return "", fail("couldn't fetch upstream")
	}
	if tag == "" {
		return "", fail("upstream has no release to upgrade to (no v<version> tag)")
	}
	return tag, nil
}

// mergeRelease merges the release tag into the home. It holds the ledger lock,
// so no ledger commit comes in the middle, and first checks again that the home
// is clean and tag is still the newest release: crew upgrade let go of the lock
// after its first check, so the sessions it paused could log where they stopped.
// The ledger and the person's settings are paths the public repo never has, so
// the merge only meets their own edits to crew's code. A conflict there is
// theirs to settle: it leaves the merge in progress and says how.
func (c *Crew) mergeRelease(tag string) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	newest, err := c.upgradeTarget()
	if err != nil {
		return err
	}
	if newest != tag {
		return fail("upstream has released %s since this upgrade began; run crew upgrade again", newest)
	}
	if gitRun(c.Home, "fetch", "-q", "upstream", "tag", tag) != nil {
		return fail("couldn't fetch %s from upstream", tag)
	}
	if gitRun(c.Home, "merge", "-q", "--no-edit", "refs/tags/"+tag) != nil {
		status := exec.Command("git", "-C", c.Home, "status", "--short")
		status.Stdout = os.Stderr
		status.Run()
		return fail("upstream's changes conflict with edits in your home. The merge is left in progress, and the crew stays paused until it's done: resolve the files above, then 'git -C %s commit' and run crew upgrade again, which lifts the pause. Or undo the merge with 'git -C %s merge --abort', and lift the pause with 'crew resume --all'.", c.Home, c.Home)
	}
	return nil
}

var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// newestRelease is the highest v<major>.<minor>.<patch> tag upstream has.
func newestRelease(home string) (string, error) {
	out, err := git(home, "ls-remote", "--tags", "--refs", "upstream")
	if err != nil {
		return "", err
	}
	newest := ""
	for _, line := range strings.Split(out, "\n") {
		_, ref, _ := strings.Cut(line, "\t")
		tag := strings.TrimPrefix(ref, "refs/tags/")
		if releaseTag.MatchString(tag) && (newest == "" || !versionAtLeast(newest[1:], tag[1:])) {
			newest = tag
		}
	}
	return newest, nil
}
