package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// crew dispatch TASK [--force]: makes the task's worktree and branch, and starts
// its worker.
func cmdDispatch(c *Crew, args []string) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	if err := c.requireHost(); err != nil {
		return err
	}
	arg, force := "", false
	for _, a := range args {
		if a == "--force" {
			force = true
		} else {
			arg = a
		}
	}
	if arg == "" {
		return usage("crew dispatch TASK")
	}
	t, err := c.resolveTask(arg)
	if err != nil {
		return err
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	if _, err := c.workerSettings(p); err != nil {
		return err
	}
	base := t.Base()
	if base == "" {
		base = p.DefaultBranch()
	}
	branch := t.Branch()
	wt := filepath.Join(p.WorkRoot(), p.Name, t.ID())

	if err := c.lock(); err != nil {
		return err
	}
	idx := c.branches(p.Name)
	parent, stacked := idx.parentOf(t)
	// A parent that merged before this task started leaves nothing to stack on, so
	// start from whatever the parent was based on.
	if stacked && parent.Status() == Merged && !gitOK(p.Repo(), "show-ref", "--verify", "--quiet", "refs/heads/"+branch) {
		base = idx.effectiveBase(t)
		t.Set("base", base)
		t.Log(parent.ID() + " has merged; based on " + base + " instead of its branch")
		parent, stacked = idx.parentOf(t)
	}
	if err := c.dispatchChecks(p, t, force); err != nil {
		return err
	}
	// Setting up the worktree can take minutes when the project's Worktrunk hooks
	// install dependencies or create databases, so don't hold the ledger lock (and
	// block every worker's `crew log`) meanwhile. The checks run again afterwards.
	c.unlock()
	if !gitOK(p.Repo(), "fetch", "--quiet", "origin", base) {
		// A stacked task's PR targets its parent's branch, so that has to be pushed first.
		if stacked && parent.Status() != Merged {
			return notYet("%s is stacked on %s, whose branch %s isn't on origin yet; dispatch %s once that task's worker has pushed", t.ID(), parent.ID(), base, t.ID())
		}
		warn("git fetch origin %s failed; using local refs", base)
	}
	_, startSHA, err := p.setupWorktree(t, wt, branch, base)
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	t.Set("worktree", wt)
	if startSHA != "" {
		t.Set("base_sha", startSHA)
	}
	if err := c.dispatchChecks(p, t, force); err != nil {
		return err
	}

	name := cut("crew-"+p.Name+"-"+t.ID(), 80)
	t.Set("worker_name", name)
	prompt := fmt.Sprintf("You are the implementer for task %s in project %s.\nTask file: %s\nWorktree (your working directory): %s\nBranch: %s, based on origin/%s.\nRead the task file completely, then follow your first-run workflow. Update the ledger with `crew log %s \"...\"` and `crew set %s <key> <value>`.",
		t.ID(), p.Name, t.Path, wt, branch, base, t.ID(), t.ID())
	if stacked {
		pr := ""
		if parent.PR() != "" {
			pr = " (PR #" + parent.PRNumber() + ")"
		}
		prompt += fmt.Sprintf("\nThis task is stacked on task %s%s: %s is that task's branch, not the default branch. Your PR targets %s; see \"Stacked tasks\" in your instructions.", parent.ID(), pr, base, base)
	}
	l, err := c.launchWorker(p)(t.Doc, wt, prompt, false)
	if err == errNotStarted {
		return fail("could not start the worker session (see output above)")
	} else if err != nil {
		return err
	}
	t.Set("worker_job", l.job)
	t.Set("worker_session", l.session)
	t.Set("status", string(Working))
	t.Touch()
	t.Log(fmt.Sprintf("dispatched: worker %s (session %s) in %s on %s", l.job, orUnknown(l.session), wt, branch))
	c.commit("dispatch " + p.Name + "/" + t.ID())
	fmt.Printf("dispatched %s → worker %s (%s)\n", t.ID(), l.job, name)
	fmt.Printf("  worktree: %s\n", wt)
	fmt.Printf("  watch:    claude attach %s   |   claude agents\n", l.job)
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// dispatchChecks refuses a dispatch the user hasn't readied, and holds back
// (notYet) one whose dependencies aren't merged, or that would exceed MAX_WORKERS
// or MAX_IN_FLIGHT. The task's parent, if it's stacked, is the one dependency
// that needn't be merged: dispatch requires its branch on origin instead. Call it
// with the lock held.
func (c *Crew) dispatchChecks(p *Project, t Task, force bool) error {
	if st := t.Status(); st != Ready && !force {
		return fail("%s is '%s'; run 'crew ready %s' first", t.ID(), st, t.ID())
	}
	parent, stacked := c.branches(p.Name).parentOf(t)
	if stacked && parent.Status() == Abandoned {
		return fail("%s is stacked on %s, which is abandoned; give it another base with 'crew set %s base <branch>'", t.ID(), parent.ID(), t.ID())
	}
	deps := strings.NewReplacer(",", " ", "[", "", "]", "").Replace(t.Get("depends_on"))
	files := c.files("tasks", p.Name)
	for _, dep := range strings.Fields(deps) {
		f, matches := resolveIn(dep, files)
		if f == "" {
			return ambiguousFail(dep, matches, "%s depends on '%s', which doesn't match exactly one task in %s", t.ID(), dep, p.Name)
		}
		if stacked && f == parent.Path {
			continue
		}
		if d := (Task{Doc{f}}); d.Status() != Merged {
			return notYet("%s depends on %s, which is '%s' (must be merged)", t.ID(), d.ID(), d.Status())
		}
	}
	maxWorkers, err := intSetting(p.Settings, "MAX_WORKERS")
	if err != nil {
		return err
	}
	maxInFlight, err := intSetting(p.Settings, "MAX_IN_FLIGHT")
	if err != nil {
		return err
	}
	if running := c.runningWorkers(c.agents()); running >= maxWorkers {
		return notYet("%d workers already running (MAX_WORKERS=%d); leaving %s ready", running, maxWorkers, t.ID())
	}
	// A task stacked on one that's in flight joins a stack that is already counted.
	if n := c.inFlight(); !(stacked && parent.Status().InFlight()) && n >= maxInFlight {
		return notYet("%d tasks already in flight (MAX_IN_FLIGHT=%d); leaving %s ready", n, maxInFlight, t.ID())
	}
	return nil
}
