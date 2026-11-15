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

// crew gardener [--all|--project P]: the gardener over each project's commits
// merged since its last run. It exits 1 if any project's run fails.
func cmdGardener(c *Crew, args []string) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	if err := c.requireHost(); err != nil {
		return err
	}
	proj, all := "", false
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all, args = true, args[1:]
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[2:]
		default:
			return usage("crew gardener [--all|--project P]")
		}
	}
	if !all {
		var err error
		if proj, err = c.oneProject(proj); err != nil {
			return err
		}
	}
	projects := c.projects(proj)
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

// garden runs the gardener over one project, unless it's paused.
func (c *Crew) garden(name string) error {
	p, err := c.loadProject(name)
	if err != nil {
		return err
	}
	if p.Paused() {
		fmt.Printf("%s: paused; skipped\n", name)
		return nil
	}
	wt := filepath.Join(p.WorkRoot(), name, "_gardener")
	if err := p.detachedCheckout(wt); err != nil {
		return err
	}
	return c.reviewCommits(p, wt)
}

// reviewCommits runs the gardener over the default branch's commits since the
// one in gardener.last, in wt, a checkout of the branch. The first run starts
// from GARDENER_LOOKBACK.
func (c *Crew) reviewCommits(p *Project, wt string) error {
	head, _ := git(wt, "rev-parse", "HEAD")
	lastFile := filepath.Join(p.Dir, "gardener.last")
	b, _ := os.ReadFile(lastFile)
	last := strings.TrimRight(string(b), "\n")
	if last == head {
		fmt.Printf("%s: nothing new since last run\n", p.Name)
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
		fmt.Printf("%s: no commits in range\n", p.Name)
		return nil
	}
	prompt := fmt.Sprintf("Project: %s\nProposals directory: %s\nTasks directory: %s\nProposal template: %s\nCommit range to review: %s (%s commits on origin/%s)\nToday's date: %s\nFollow your process and write proposals only into the proposals directory.",
		p.Name, filepath.Join(p.Dir, "proposals"), filepath.Join(p.Dir, "tasks"), c.path("templates/proposal.md"), rng, count, p.DefaultBranch(), time.Now().Format("2006-01-02"))
	if err := c.runAgent(p, wt, "gardener", fmt.Sprintf("reviewing %s commits (%s)", count, rng), prompt); err != nil {
		return err
	}
	return c.advance(lastFile, head, "gardener "+p.Name+" ("+rng+")")
}

// runAgent runs agent once in wt, with the gardener's settings and powers, and
// writes its output to a log of its own. doing says what the run is for. It
// warns with the agent's exit code when the run fails.
func (c *Crew) runAgent(p *Project, wt, agent, doing, prompt string) error {
	settings, err := readSettings(c.path("config/gardener-settings.json"))
	if err != nil {
		return err
	}
	logf := c.path("logs", agent+"-"+p.Name+"-"+time.Now().Format("20060102-1504")+".log")
	fmt.Printf("%s: %s — log: %s\n", p.Name, doing, logf)
	log, err := os.Create(logf)
	if err != nil {
		return err
	}
	cmd := c.claude("-p", "--add-dir", c.Home, "--agent", agent, "--permission-mode", "acceptEdits",
		"--permission-prompts", "none", "--max-turns", p.Settings["GARDENER_MAX_TURNS"],
		"--settings", c.sessionSettings(settings, p), prompt)
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
		warn("%s: %s exited with %d; see %s", p.Name, agent, rc, logf)
		return exitQuietly(1)
	}
	return nil
}

// advance records a finished run: it writes the run's cursor, file, and
// commits the ledger with msg.
func (c *Crew) advance(file, cursor, msg string) error {
	os.WriteFile(file, []byte(cursor+"\n"), 0o644)
	if err := c.lock(); err != nil {
		return err
	}
	c.commit(msg)
	c.unlock()
	return nil
}
