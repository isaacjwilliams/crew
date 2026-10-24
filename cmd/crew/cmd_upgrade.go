package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// crew upgrade brings in the newest release of crew from the public repo
// (upstream): the newest v<version> tag there, not its main. The release's
// binary is fetched first, by the launcher, so a release whose binaries aren't
// up yet changes nothing. The ledger and the person's settings are paths the
// public repo never has, so the merge only meets their own edits to crew's code.
// A conflict there is theirs to settle: crew stops with the merge in progress and
// says how. Then the new version installs itself, the merge is pushed, the lead's
// server restarted, so new phone sessions run the new lead, and the binaries of
// other versions go.
func cmdUpgrade(c *Crew, args []string) error {
	if len(args) > 0 {
		return usage("crew upgrade")
	}
	if !gitOK(c.Home, "remote", "get-url", "upstream") {
		return fail("the home has no upstream (the public crew repo) to upgrade from")
	}
	if err := c.lock(); err != nil {
		return err
	}
	if out, _ := git(c.Home, "status", "--porcelain"); out != "" {
		return fail("the home has uncommitted changes; commit or discard them first (git -C %s status)", c.Home)
	}
	tag, err := newestRelease(c.Home)
	if err != nil {
		return fail("couldn't fetch upstream")
	}
	if tag == "" {
		return fail("upstream has no release to upgrade to (no v<version> tag)")
	}
	next := strings.TrimPrefix(tag, "v")
	// Only the launcher knows how to download a binary, so it fetches this one.
	fetch := exec.Command(c.path("bin/crew"), "version")
	fetch.Env, fetch.Stderr = append(os.Environ(), "CREW_VERSION="+next), os.Stderr
	if out, err := fetch.Output(); err != nil || strings.TrimSpace(string(out)) != next {
		return fail("couldn't get crew %s's binary, so nothing has changed. Its release may still be building; try again in a few minutes.", next)
	}
	if gitRun(c.Home, "fetch", "-q", "upstream", "tag", tag) != nil {
		return fail("couldn't fetch %s from upstream", tag)
	}
	if gitRun(c.Home, "merge", "-q", "--no-edit", "refs/tags/"+tag) != nil {
		status := exec.Command("git", "-C", c.Home, "status", "--short")
		status.Stdout = os.Stderr
		status.Run()
		return fail("upstream's changes conflict with edits in your home. The merge is left in progress: resolve the files above, then 'git -C %s commit' and run crew upgrade again. Or undo it with 'git -C %s merge --abort'.", c.Home, c.Home)
	}
	// The merge needed the lock (no ledger commits in the middle of it); installing
	// doesn't, and the push takes the lock itself if it has to rebase.
	c.unlock()
	// The new version installs itself, so a change to installing takes effect now.
	install := exec.Command(c.path("bin/crew"), "install")
	install.Stdin, install.Stdout, install.Stderr = os.Stdin, os.Stdout, os.Stderr
	if install.Run() != nil {
		return fail("couldn't install the new version; run crew install to try again")
	}
	if !c.pushNow(os.Stdout) {
		warn("couldn't push the upgrade to origin; crew tick pushes it later")
	}
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
