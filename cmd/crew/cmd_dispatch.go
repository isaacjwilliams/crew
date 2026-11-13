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
	// Building the launcher reads the worker settings, so a bad one stops here,
	// before anything changes.
	start, err := c.launchWorker(p)
	if err != nil {
		return err
	}
	// A bad mcp.json would stop the launch; stop here instead, before anything changes.
	if _, err := c.projectMCPServers(p.Name, p.Settings); err != nil {
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
	// Before the stacked base below is rewritten: dispatchChecks checks again
	// after the worktree is made.
	if err := refuseIfPaused(p); err != nil {
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
	l, err := c.launchSession(start, t.Doc, wt, prompt, false)
	if err == errNotStarted {
		return fail("could not start the worker session (see output above)")
	} else if err != nil {
		return err
	}
	l.record(t.Doc)
	t.Set("status", string(Working))
	t.Touch()
	t.Log(fmt.Sprintf("dispatched: worker %s (session %s) in %s on %s", l.job, orUnknown(l.session), wt, branch))
	c.commit("dispatch " + p.Name + "/" + t.ID())
	fmt.Printf("dispatched %s → worker %s (%s)\n", t.ID(), l.job, name)
	fmt.Printf("  worktree: %s\n", wt)
	fmt.Printf("  watch:    crew attach %s   |   claude agents\n", c.taskArg(t))
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// dispatchChecks refuses a dispatch the user hasn't readied, or one stacked on an
// abandoned task, and holds back (notYet) one for a paused project, one whose
// dependencies haven't merged, or one that would go over MAX_WORKERS or its
// goal's max_in_flight. The caps are counted only once the dependencies have
// merged, since counting the workers running asks claude. Call it with the lock
// held.
func (c *Crew) dispatchChecks(p *Project, t Task, force bool) error {
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	if st := t.Status(); st != Ready && !force {
		return fail("%s is '%s'; run 'crew ready %s' first", t.ID(), st, t.ID())
	}
	idx := c.branches(p.Name)
	if parent, ok := idx.abandonedParent(t); ok {
		return fail("%s is stacked on %s, which is abandoned; give it another base with 'crew set %s base <branch>'", t.ID(), parent.ID(), t.ID())
	}
	deps, err := c.unmergedDeps(idx, t)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		return notYet("%s depends on %s, which is '%s' (must be merged)", t.ID(), deps[0].ID(), deps[0].Status())
	}
	workers, err := workerSlots(p.Settings, c.runningWorkers(c.agents()))
	if err != nil {
		return err
	}
	if workers.full() {
		return notYet("%d workers already running (MAX_WORKERS=%d); leaving %s ready", workers.used, workers.limit, t.ID())
	}
	if g, ok := c.taskGoal(t); ok {
		goal, err := c.goalSlots(p.Settings, g)
		if err != nil {
			return err
		}
		if goal.full() {
			return notYet("goal %s already has %d task(s) in flight (max_in_flight %d); leaving %s ready", g.ID(), goal.used, goal.limit, t.ID())
		}
	}
	return nil
}

// unmergedDeps are the tasks this one waits on: its dependencies that haven't
// merged. A stacked task's parent needn't merge, since dispatch needs only its
// branch on origin, so it's listed only when it's abandoned, whether or not it's
// in depends_on. A dependency that doesn't name exactly one task is an error.
// idx indexes the task's project, or every project: building it reads every
// task file, so a caller checking many tasks builds it once.
func (c *Crew) unmergedDeps(idx Branches, t Task) ([]Task, error) {
	var deps []Task
	project := taskProjectDir(t)
	if parent, ok := idx.abandonedParent(t); ok {
		deps = append(deps, parent)
	}
	parent, stacked := idx.parentOf(t)
	names := strings.NewReplacer(",", " ", "[", "", "]", "").Replace(t.Get("depends_on"))
	files := c.files("tasks", project)
	for _, dep := range strings.Fields(names) {
		f, matches := resolveIn(dep, files)
		if f == "" {
			return nil, ambiguousFail(dep, matches, "%s depends on '%s', which doesn't match exactly one task in %s", t.ID(), dep, project)
		}
		if stacked && f == parent.Path {
			continue
		}
		if d := (Task{Doc{f}}); d.Status() != Merged {
			deps = append(deps, d)
		}
	}
	return deps, nil
}

// slots is a count against its limit: workers running against MAX_WORKERS, or a
// goal's tasks in flight against its max_in_flight.
type slots struct{ used, limit int }

func (s slots) full() bool { return s.used >= s.limit }

// workerSlots are the workers running across the crew (runningWorkers) against
// MAX_WORKERS in these settings. The count is there even when the setting is bad.
func workerSlots(settings Settings, running int) (slots, error) {
	limit, err := intSetting(settings, "MAX_WORKERS")
	return slots{running, limit}, err
}

// goalSlots are the goal's tasks in flight against its max_in_flight. The count
// is there even when the limit is bad.
func (c *Crew) goalSlots(settings Settings, g Goal) (slots, error) {
	limit, err := goalMaxInFlight(settings, g)
	return slots{c.goalInFlight(g), limit}, err
}
