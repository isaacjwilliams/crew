package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// crew gardener [--all|PROJECT]: the gardener over each project's commits merged
// since its last run. It exits 1 if any project's run fails.
func cmdGardener(c *Crew, args []string) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	if err := c.requireHost(); err != nil {
		return err
	}
	var projects []string
	switch {
	case len(args) > 0 && args[0] == "--all":
		projects = c.projectsInScope(true)
	case len(args) > 0:
		projects = []string{args[0]}
	default:
		p, err := c.currentProject()
		if err != nil {
			return err
		}
		projects = []string{p}
	}
	os.MkdirAll(c.path("logs"), 0o755)
	failed := false
	for _, name := range projects {
		if err := c.garden(name); err != nil {
			report(err)
			warn("%s: gardener run failed", name)
			failed = true
		}
	}
	if failed {
		return exitQuietly(1)
	}
	return nil
}

// garden runs the gardener over one project.
func (c *Crew) garden(name string) error {
	p, err := c.loadProject(name)
	if err != nil {
		return err
	}
	wt := filepath.Join(p.WorkRoot(), name, "_gardener")
	if err := p.detachedCheckout(wt); err != nil {
		return err
	}
	head, _ := git(wt, "rev-parse", "HEAD")
	lastFile := filepath.Join(p.Dir, "gardener.last")
	b, _ := os.ReadFile(lastFile)
	last := strings.TrimRight(string(b), "\n")
	if last == head {
		fmt.Printf("%s: nothing new since last run\n", name)
		return nil
	}
	var base string
	if last != "" && gitOK(wt, "merge-base", "--is-ancestor", last, head) {
		base = last
	} else {
		base, _ = git(wt, "rev-list", "-1", "--before="+p.Settings["GARDENER_LOOKBACK"], head)
	}
	rng := head
	if base != "" {
		rng = base + ".." + head
	}
	count, _ := git(wt, "rev-list", "--count", rng)
	if count == "0" {
		os.WriteFile(lastFile, []byte(head+"\n"), 0o644)
		fmt.Printf("%s: no commits in range\n", name)
		return nil
	}
	prompt := fmt.Sprintf("Project: %s\nProposals directory: %s\nTasks directory: %s\nProposal template: %s\nCommit range to review: %s (%s commits on origin/%s)\nToday's date: %s\nFollow your process and write proposals only into the proposals directory.",
		name, filepath.Join(p.Dir, "proposals"), filepath.Join(p.Dir, "tasks"), c.path("templates/proposal.md"), rng, count, p.DefaultBranch(), time.Now().Format("2006-01-02"))
	logf := c.path("logs", "gardener-"+name+"-"+time.Now().Format("20060102-1504")+".log")
	fmt.Printf("%s: reviewing %s commits (%s) — log: %s\n", name, count, rng, logf)
	log, err := os.Create(logf)
	if err != nil {
		return err
	}
	cmd := c.claude("-p", "--add-dir", c.Home, "--agent", "gardener", "--permission-mode", "acceptEdits",
		"--permission-prompts", "none", "--max-turns", p.Settings["GARDENER_MAX_TURNS"],
		"--settings", c.path("config/readonly-settings.json"), prompt)
	cmd.Dir, cmd.Stdout, cmd.Stderr = wt, log, log
	runErr := cmd.Run()
	log.Close()
	tailTo(os.Stdout, logf, 15)
	if runErr != nil {
		rc := 127 // claude didn't start
		var x *exec.ExitError
		if errors.As(runErr, &x) {
			rc = x.ExitCode()
		}
		warn("%s: gardener exited with %d; see %s", name, rc, logf)
		return exitQuietly(1)
	}
	os.WriteFile(lastFile, []byte(head+"\n"), 0o644)
	if err := c.lock(); err != nil {
		return err
	}
	c.commit("gardener " + name + " (" + rng + ")")
	c.unlock()
	return nil
}
