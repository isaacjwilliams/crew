package main

// crew evidence publishes a task's screenshots and other evidence to its
// project's evidence repo (EVIDENCE_REPO), so its PR can link to them. Workers
// can't write outside their worktree, so crew keeps the clone, one per project,
// and is the only one that pushes to it. Nothing there is ever deleted or
// rewritten: links in merged PRs must keep working.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// crew evidence TASK FILE...
func cmdEvidence(c *Crew, args []string) error {
	if len(args) < 2 {
		return usage("crew evidence TASK FILE...")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	files, names := args[1:], []string{}
	for _, f := range files {
		if !isFile(f) {
			return fail("%s isn't a file", f)
		}
		name := filepath.Base(f)
		if slices.Contains(names, name) {
			return fail("two of the files are named %s; each goes in %s/ under its own name", name, t.ID())
		}
		names = append(names, name)
	}
	settings, err := c.projectSettings(t.Project())
	if err != nil {
		return err
	}
	remote := settings["EVIDENCE_REPO"]
	if remote == "" {
		return fail("%s has no evidence repo: set EVIDENCE_REPO in its project.env", t.Project())
	}

	// One publish per project at a time: they share the clone.
	lock, err := flock(c.path(".crew-evidence-"+t.Project()+".lock"), 2*time.Minute)
	if errors.Is(err, errLocked) {
		return fail("another crew evidence for %s has been running for 2 minutes; try again", t.Project())
	} else if err != nil {
		return err
	}
	defer lock.Close()
	clone := filepath.Join(settings["WORK_ROOT"], t.Project(), "_evidence")
	sha, err := publishEvidence(remote, clone, t.ID(), files)
	if err != nil {
		return err
	}

	owner, repo := ownerAndRepo(remote)
	for _, name := range names {
		link := fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s/%s", owner, repo, sha, t.ID(), url.PathEscape(name))
		if isImage(name) {
			fmt.Printf("![%s](%s?raw=true)\n", name, link)
		} else {
			fmt.Printf("[%s](%s)\n", name, link)
		}
	}
	return nil
}

// publishEvidence copies files into dir/ of the evidence repo's default branch,
// in one commit, and pushes it. It returns the commit that has them, which is
// what links pin to. Its clone is cloned on first use, and never holds work of
// its own: each publish starts from origin.
func publishEvidence(remote, clone, dir string, files []string) (string, error) {
	if !isDir(filepath.Join(clone, ".git")) {
		os.MkdirAll(filepath.Dir(clone), 0o755)
		if !gitTo(os.Stderr, ".", "clone", "-q", remote, clone) {
			return "", fail("couldn't clone the evidence repo %s", remote)
		}
	}
	if !gitTo(os.Stderr, clone, "fetch", "-q", "origin") {
		return "", fail("couldn't fetch the evidence repo %s", remote)
	}
	origin, err := git(clone, "rev-parse", "--abbrev-ref", "origin/HEAD")
	if err != nil {
		return "", fail("the evidence repo %s has no default branch yet; push a first commit to it", remote)
	}
	branch := strings.TrimPrefix(origin, "origin/")
	if !gitOK(clone, "reset", "-q", "--hard", origin) || !gitOK(clone, "clean", "-qfdx") {
		return "", fail("couldn't reset %s to %s", clone, origin)
	}

	os.MkdirAll(filepath.Join(clone, dir), 0o755)
	var names []string
	for _, f := range files {
		name := filepath.Base(f)
		data, err := os.ReadFile(f)
		if err == nil {
			err = os.WriteFile(filepath.Join(clone, dir, name), data, 0o644)
		}
		if err != nil {
			return "", err
		}
		names = append(names, name)
	}
	gitOK(clone, "add", "--", dir)
	// Files exactly like the ones there already need no commit: origin's has them.
	if !gitOK(clone, "diff", "--cached", "--quiet") {
		if !gitTo(os.Stderr, clone, "commit", "-qm", dir+": "+strings.Join(names, ", ")) {
			return "", fail("couldn't commit the evidence in %s", clone)
		}
		if err := pushEvidence(clone, origin, branch); err != nil {
			return "", err
		}
	}
	return git(clone, "rev-parse", "HEAD")
}

// pushEvidence pushes the clone's commit to the default branch. When origin has
// moved on (another machine or person published), it rebases onto it and tries
// again: each task has a folder of its own, so the rebase can't conflict.
func pushEvidence(clone, origin, branch string) error {
	for range 5 {
		out, err := exec.Command("git", "-C", clone, "push", "-q", "origin", "HEAD:"+branch).CombinedOutput()
		if err == nil {
			return nil
		}
		if !pushRejected(string(out)) {
			fmt.Fprint(os.Stderr, string(out))
			return fail("couldn't push the evidence from %s", clone)
		}
		if !gitTo(os.Stderr, clone, "fetch", "-q", "origin") || !gitTo(os.Stderr, clone, "rebase", "-q", origin) {
			gitOK(clone, "rebase", "--abort")
			return fail("couldn't rebase the evidence in %s onto %s", clone, origin)
		}
	}
	return fail("origin kept moving; couldn't push the evidence from %s", clone)
}

func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return true
	}
	return false
}
