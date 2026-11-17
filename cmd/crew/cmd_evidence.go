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
	p, err := c.ledgerProject(t.Project())
	if err != nil {
		return err
	}
	remote := p.EvidenceRepo()
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
	// Beside the project's worktrees and outside every one, so no worker writes to it.
	clone := filepath.Join(p.WorkRoot(), p.Name, "_evidence")
	sha, err := publishEvidence(remote, clone, t.ID(), files, names)
	if err != nil {
		return err
	}

	owner, repo := ownerAndRepo(remote)
	for _, name := range names {
		link := fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s/%s", owner, repo, sha, t.ID(), url.PathEscape(name))
		if isImage(name) {
			// GitHub shows a blob page, not the image, unless asked for the raw file.
			fmt.Printf("![%s](%s?raw=true)\n", name, link)
		} else {
			fmt.Printf("[%s](%s)\n", name, link)
		}
	}
	return nil
}

// publishEvidence copies files into dir/ of the evidence repo's default branch,
// each under its name in names, in one commit, and pushes it. It returns the
// commit that has them, which is what links pin to. The clone never holds work
// of its own: each publish starts from origin as it is now.
func publishEvidence(remote, clone, dir string, files, names []string) (string, error) {
	// The URL as written, as clone stored it: get-url would apply any insteadOf.
	if url, _ := git(clone, "config", "--get", "remote.origin.url"); url != remote {
		// No clone yet, or one of a repo EVIDENCE_REPO no longer names, whose
		// branches must never reach this one: clone it afresh.
		os.RemoveAll(clone)
		os.MkdirAll(filepath.Dir(clone), 0o755)
		if !gitTo(os.Stderr, ".", "clone", "-q", remote, clone) {
			return "", fail("couldn't clone the evidence repo %s", remote)
		}
	} else if !gitTo(os.Stderr, clone, "fetch", "-q", "--prune", "origin") {
		return "", fail("couldn't fetch the evidence repo %s", remote)
	} else {
		// The repo's default branch may have changed since the clone. An empty
		// repo has none to read, which the check below reports.
		gitOK(clone, "remote", "set-head", "origin", "--auto")
	}
	branch := defaultBranch(clone)
	origin := "origin/" + branch
	if !gitOK(clone, "rev-parse", "-q", "--verify", origin) {
		return "", fail("the evidence repo %s has no commit on %s yet; push a first commit to it", remote, branch)
	}
	if !gitOK(clone, "reset", "-q", "--hard", origin) || !gitOK(clone, "clean", "-qfdx") {
		return "", fail("couldn't reset %s to %s", clone, origin)
	}

	os.MkdirAll(filepath.Join(clone, dir), 0o755)
	paths := []string{"add", "-f", "--"}
	for i, f := range files {
		data, err := os.ReadFile(f)
		if err == nil {
			err = os.WriteFile(filepath.Join(clone, dir, names[i]), data, 0o644)
		}
		if err != nil {
			return "", err
		}
		paths = append(paths, dir+"/"+names[i])
	}
	// Each file by name, and -f: git skips a file an ignore rule matches when
	// it adds a folder, which would leave a link to a file never published.
	if !gitOK(clone, paths...) {
		return "", fail("couldn't add the evidence in %s", clone)
	}
	// Files exactly like the ones there already need no commit: origin's has them.
	if !gitOK(clone, "diff", "--cached", "--quiet") {
		if !gitTo(os.Stderr, clone, "commit", "-qm", dir+": "+strings.Join(names, ", ")) {
			return "", fail("couldn't commit the evidence in %s", clone)
		}
		if err := pushEvidence(clone, branch); err != nil {
			return "", err
		}
	}
	return git(clone, "rev-parse", "HEAD")
}

// pushEvidence pushes the clone's commit to the default branch. When origin has
// moved on (another machine or person published), it rebases onto it and tries
// again, up to five tries in all, like pushLedger. Each task has a folder of its
// own, so the rebase can't conflict.
func pushEvidence(clone, branch string) error {
	for range 5 {
		out, err := exec.Command("git", "-C", clone, "push", "-q", "origin", "HEAD:"+branch).CombinedOutput()
		if err == nil {
			return nil
		}
		if !pushRejected(string(out)) {
			fmt.Fprint(os.Stderr, string(out))
			return fail("couldn't push the evidence from %s", clone)
		}
		if !gitTo(os.Stderr, clone, "pull", "-q", "--rebase", "origin", branch) {
			gitOK(clone, "rebase", "--abort")
			return fail("couldn't rebase the evidence in %s onto origin's %s", clone, branch)
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
