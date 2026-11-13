package main

import (
	"errors"
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
// wrap up before the release is merged (pauseAndMerge). Once the release is
// installed, it lifts the pause it made, pushes, restarts the lead's server, and
// removes the binaries of other versions.
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
	// Not held on through the wait for sessions to wrap up, so they can log
	// where they stopped. mergeRelease takes it again.
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
	// A release the home has merged already, by a run that stopped after its
	// merge, only needs installing, so nothing is paused for it.
	merged := gitOK(c.Home, "merge-base", "--is-ancestor", "refs/tags/"+tag, "HEAD")
	if err := c.confirmUpgrade(projects, next, merged, yes); err != nil {
		return err
	}
	if !merged {
		if err := c.pauseAndMerge(tag, projects); err != nil {
			return err
		}
	}
	// Once the release is merged, the crew stays paused until it's installed.
	if err := c.installNewVersion(yes); err != nil {
		return err
	}
	if err := c.liftUpgradePause(projects); err != nil {
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
func (c *Crew) confirmUpgrade(projects []*Project, next string, merged, yes bool) error {
	if merged {
		fmt.Printf("crew %s is merged into the home already, so nothing is paused: this installs it, and lifts any pause an upgrade left.\n", next)
	} else {
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
	}
	fmt.Println("Workers and goal leads whose instructions changed start fresh when they next have work. Your own leads will tell you if they need restarting.")
	switch {
	case yes:
		return nil
	case !stdinIsTerminal():
		return fail("nothing has changed; run crew upgrade --yes to go ahead")
	case confirm("Upgrade now?", false):
		return nil
	default:
		return fail("nothing has changed")
	}
}

// pauseAndMerge pauses every project, and merges the release once their
// sessions have wrapped up. If it stops before the merge, it lifts the pause
// again. A merge left in progress keeps it: git takes no ledger commit until
// the merge is settled, and the crew upgrade run after that lifts it.
func (c *Crew) pauseAndMerge(tag string, projects []*Project) error {
	err := c.pause(projects, pausedByUpgrade, false)
	if err == nil {
		err = c.mergeRelease(tag, projects)
	}
	if err == nil || gitOK(c.Home, "rev-parse", "-q", "--verify", "MERGE_HEAD") {
		return err
	}
	return errors.Join(err, c.liftUpgradePause(projects))
}

// installNewVersion runs the merged version's crew install, so a change to
// installing takes effect now.
func (c *Crew) installNewVersion(yes bool) error {
	install := exec.Command(c.path("bin/crew"), "install")
	if yes {
		install.Args = append(install.Args, "--yes")
	}
	install.Stdin, install.Stdout, install.Stderr = os.Stdin, os.Stdout, os.Stderr
	if install.Run() != nil {
		return fail("couldn't install the new version, so the crew stays paused; run crew upgrade again to finish")
	}
	return nil
}

// liftUpgradePause lifts the pauses crew upgrade made: this run's, and any a
// run cut short left. It knows them by their mark, so it leaves the user's own.
// It reads the marks under the ledger lock, so a crew pause that takes one over
// meanwhile keeps it.
func (c *Crew) liftUpgradePause(projects []*Project) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
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
// It also refuses if the pause no longer holds (stillPaused).
// The ledger and the person's settings are paths the public repo never has, so
// the merge only meets their own edits to crew's code. A conflict there is
// theirs to settle: it leaves the merge in progress and says how.
func (c *Crew) mergeRelease(tag string, paused []*Project) error {
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
	// Fetched before the last look at the sessions, so the network's time
	// isn't added to the moments a session could start a turn before the merge.
	if gitRun(c.Home, "fetch", "-q", "upstream", "tag", tag) != nil {
		return fail("couldn't fetch %s from upstream", tag)
	}
	if err := c.stillPaused(paused); err != nil {
		return err
	}
	if gitRun(c.Home, "merge", "-q", "--no-edit", "refs/tags/"+tag) != nil {
		status := exec.Command("git", "-C", c.Home, "status", "--short")
		status.Stdout = os.Stderr
		status.Run()
		var ours []string
		for _, p := range paused {
			if p.PausedBy() == pausedByUpgrade {
				ours = append(ours, p.Name)
			}
		}
		lift := ""
		if len(ours) > 0 {
			lift = fmt.Sprintf(", and lift the pause with 'crew resume --project P' for each of %s", strings.Join(ours, ", "))
		}
		return fail("upstream's changes conflict with edits in your home. The merge is left in progress, and the crew stays paused until it's done: resolve the files above, then 'git -C %s commit' and run crew upgrade again, which lifts the pause. Or undo the merge with 'git -C %s merge --abort'%s.", c.Home, c.Home, lift)
	}
	return nil
}

// stillPaused checks, under the ledger lock, that the pause crew upgrade made
// still holds: no project resumed or added since, and no session mid-turn, such
// as one idle when the pause began that a background review has woken since.
func (c *Crew) stillPaused(paused []*Project) error {
	stopped := func(what string) error {
		return fail("%s while sessions wrapped up, so crew upgrade stopped before merging; run it again", what)
	}
	var names, resumed []string
	for _, p := range paused {
		names = append(names, p.Name)
		if !p.Paused() {
			resumed = append(resumed, p.Name)
		}
	}
	if len(resumed) > 0 {
		return stopped(strings.Join(resumed, ", ") + " was resumed")
	}
	added := slices.DeleteFunc(c.projects(""), func(name string) bool { return slices.Contains(names, name) })
	if len(added) > 0 {
		return stopped(strings.Join(added, ", ") + " was added")
	}
	busy, err := c.busySessions(paused)
	if err != nil {
		return err
	}
	if len(busy) > 0 {
		return stopped(labels(busy) + " started working")
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
