package main

import (
	"fmt"
	"slices"
)

// crew cleanup TASK|--merged [--force]: stops a finished task's worker and
// removes it from agent view, ends what it left running in the worktree,
// removes the worktree, and deletes the local branch of a task that delivered
// (merged, or a spike done). --merged cleans up every task that delivered.
// A task it leaves alone is warned of last, "<task>: worktree stays: <why>", and
// the exit code is exitNotYet. crew tick runs it for one task and reads that
// warning, the last line of stderr, to tell the leads why.
func cmdCleanup(c *Crew, args []string) error {
	if err := need("git"); err != nil {
		return err
	}
	arg, proj, force, merged := "", "", false, false
	for len(args) > 0 {
		switch {
		case args[0] == "--force":
			force = true
		case args[0] == "--merged":
			merged = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		default:
			arg = args[0]
		}
		args = args[1:]
	}
	var tasks []Task
	if merged {
		p, err := c.oneProject(proj)
		if err != nil {
			return err
		}
		for _, t := range c.tasks(p) {
			if t.Status().Delivered() && t.Worktree() != "" {
				tasks = append(tasks, t)
			}
		}
		if len(tasks) == 0 {
			fmt.Println("Nothing to clean up.")
			return nil
		}
	} else {
		if arg == "" {
			return usage("crew cleanup TASK|--merged [--force]")
		}
		t, err := c.resolveTask(arg)
		if err != nil {
			return err
		}
		tasks = []Task{t}
	}
	if err := c.lock(); err != nil {
		return err
	}
	var stays []string
	stay := func(t Task, why string) {
		stays = append(stays, fmt.Sprintf("%s: worktree stays: %s", t.ID(), why))
	}
	// Warned of after the ledger commit, so these are the last lines of stderr;
	// deferred, so an error on a later task doesn't hide them.
	defer func() {
		for _, line := range stays {
			warn("%s", line)
		}
	}()
	for _, t := range tasks {
		p, err := c.loadProject(t.Project())
		if err != nil {
			return err
		}
		st, wt, job := t.Status(), t.Worktree(), t.WorkerJob()
		// A dispatch that failed while setting up its worktree left one at the
		// task's own path without recording it, and a task moved from another
		// machine records that machine's path.
		var dirs []string
		for _, dir := range []string{wt, p.taskWorktree(t)} {
			if isDir(dir) && !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
		if !st.Finished() && !force {
			stay(t, fmt.Sprintf("it is '%s' (crew cleanup --force removes it)", st))
			continue
		}
		if dir := firstDirty(dirs); dir != "" && !force {
			stay(t, fmt.Sprintf("%s has uncommitted changes (crew cleanup --force removes it)", dir))
			continue
		}
		// The worker goes first, so it doesn't carry on in a deleted directory, and
		// the worktree tool's remove hooks run without it or the processes it left.
		if !c.removeSession(job) {
			stay(t, fmt.Sprintf("worker %s is still running (stop it in claude agents)", job))
			continue
		}
		stuck := ""
		for _, dir := range dirs {
			stopProcessesIn(dir)
			removed, err := p.worktreeRemove(dir, force, p.worktreeLog(t))
			if err != nil {
				return err
			}
			if !removed {
				stuck = dir
				break
			}
		}
		if stuck != "" {
			stay(t, fmt.Sprintf("the worktree tool couldn't remove %s", stuck))
			continue
		}
		if st.Delivered() && t.Branch() != "" {
			gitOK(p.Repo(), "branch", "-D", t.Branch())
		}
		if wt == "" && len(dirs) == 0 {
			fmt.Println(t.ID() + " has no worktree to clean up")
			continue
		}
		if wt == "" {
			p.dropUnstartedBranch(t)
		}
		t.Set("worktree", "")
		t.Touch()
		if st != "" {
			t.Log("cleaned up worktree (" + string(st) + ")")
		} else {
			t.Log("cleaned up worktree")
		}
		fmt.Println("cleaned up " + t.ID())
	}
	c.commit("cleanup")
	if len(stays) > 0 {
		return exitQuietly(exitNotYet)
	}
	return nil
}

// dropUnstartedBranch deletes the branch a failed dispatch's setup created, so
// that the next dispatch starts a new one at the base's current tip. A branch
// with commits of its own stays.
func (p *Project) dropUnstartedBranch(t Task) {
	base := t.Base()
	if base == "" {
		base = p.DefaultBranch()
	}
	if t.Branch() != "" && gitOK(p.Repo(), "merge-base", "--is-ancestor", "refs/heads/"+t.Branch(), "origin/"+base) {
		gitOK(p.Repo(), "branch", "-D", t.Branch())
	}
}

// firstDirty is the first of the worktrees that has uncommitted changes.
func firstDirty(dirs []string) string {
	for _, dir := range dirs {
		if out, _ := git(dir, "status", "--porcelain"); out != "" {
			return dir
		}
	}
	return ""
}
