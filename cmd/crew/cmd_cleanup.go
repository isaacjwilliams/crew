package main

import "fmt"

// crew cleanup TASK|--merged [--force]: stops a merged or abandoned task's
// worker and removes it from agent view, ends what it left running in the
// worktree, removes the worktree, and deletes a merged task's local branch.
func cmdCleanup(c *Crew, args []string) error {
	if err := need("git"); err != nil {
		return err
	}
	arg, force, merged := "", false, false
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "--merged":
			merged = true
		default:
			arg = a
		}
	}
	var tasks []Task
	if merged {
		p, err := c.currentProject()
		if err != nil {
			return err
		}
		for _, t := range c.tasks(p) {
			if t.Status() == Merged && t.Worktree() != "" {
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
	for _, t := range tasks {
		p, err := c.loadProject(t.Project())
		if err != nil {
			return err
		}
		st, wt, job := t.Status(), t.Worktree(), t.WorkerJob()
		if !st.Finished() && !force {
			warn("%s is '%s'; skipping (use --force)", t.ID(), st)
			continue
		}
		if wt != "" && isDir(wt) && !force {
			if out, _ := git(wt, "status", "--porcelain"); out != "" {
				warn("%s: %s has uncommitted changes; skipping (use --force)", t.ID(), wt)
				continue
			}
		}
		// The worker goes first, so it doesn't carry on in a deleted directory, and
		// the worktree tool's remove hooks run without it or the processes it left.
		if !c.removeSession(job) {
			warn("%s: worker %s is still running; skipping (stop it in claude agents)", t.ID(), job)
			continue
		}
		if wt != "" && isDir(wt) {
			stopProcessesIn(wt)
			removed, err := p.worktreeRemove(wt, force, p.worktreeLog(t))
			if err != nil {
				return err
			}
			if !removed {
				warn("%s: couldn't remove %s", t.ID(), wt)
				continue
			}
		}
		if st == Merged && t.Branch() != "" {
			gitOK(p.Repo(), "branch", "-D", t.Branch())
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
	return nil
}
