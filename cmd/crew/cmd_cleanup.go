package main

import (
	"fmt"
	"strings"
)

// crew cleanup TASK|--merged [--force]: stops a finished task's worker and
// removes it from agent view, ends what it left running in the worktree,
// removes the worktree, and deletes the local branch of a task that delivered
// (merged, or a spike done). --merged cleans up every task that delivered.
// A task it leaves alone is named on stderr, one line for all of them, and the
// exit code is exitNotYet: crew tick reads that line to tell the leads why.
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
	for _, t := range tasks {
		p, err := c.loadProject(t.Project())
		if err != nil {
			return err
		}
		st, wt, job := t.Status(), t.Worktree(), t.WorkerJob()
		if !st.Finished() && !force {
			stays = append(stays, fmt.Sprintf("%s: worktree stays: it is '%s' (crew cleanup --force removes it)", t.ID(), st))
			continue
		}
		if wt != "" && isDir(wt) && !force {
			if out, _ := git(wt, "status", "--porcelain"); out != "" {
				stays = append(stays, fmt.Sprintf("%s: worktree stays: %s has uncommitted changes (crew cleanup --force removes it)", t.ID(), wt))
				continue
			}
		}
		// The worker goes first, so it doesn't carry on in a deleted directory, and
		// the worktree tool's remove hooks run without it or the processes it left.
		if !c.removeSession(job) {
			stays = append(stays, fmt.Sprintf("%s: worktree stays: worker %s is still running (stop it in claude agents)", t.ID(), job))
			continue
		}
		if wt != "" && isDir(wt) {
			stopProcessesIn(wt)
			removed, err := p.worktreeRemove(wt, force, p.worktreeLog(t))
			if err != nil {
				return err
			}
			if !removed {
				stays = append(stays, fmt.Sprintf("%s: worktree stays: the worktree tool couldn't remove %s", t.ID(), wt))
				continue
			}
		}
		if st.Delivered() && t.Branch() != "" {
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
	if len(stays) > 0 {
		return notYet("%s", strings.Join(stays, "; "))
	}
	return nil
}
