package main

// A task is stacked when its base is another task's branch; that task is its
// parent. Nothing else records the relationship, so it can't disagree with base.

import "path/filepath"

// Branches indexes task files by project and branch, for parentOf.
type Branches map[string]Task

// branches indexes the tasks of a project, or of every project when it's empty.
func (c *Crew) branches(project string) Branches {
	idx := Branches{}
	for _, t := range c.tasks(project) {
		if b := t.Branch(); b != "" {
			idx[taskProjectDir(t)+":"+b] = t
		}
	}
	return idx
}

// taskProjectDir is the name of the project directory a task file is in.
func taskProjectDir(t Task) string { return filepath.Base(filepath.Dir(filepath.Dir(t.Path))) }

// parentOf is the task t is stacked on, if any.
func (idx Branches) parentOf(t Task) (Task, bool) {
	base := t.Base()
	if base == "" {
		return Task{}, false
	}
	p, ok := idx[taskProjectDir(t)+":"+base]
	if !ok || p.Path == t.Path {
		return Task{}, false
	}
	return p, true
}

// effectiveBase is the branch a task belongs on now: its own base, or, once its
// parent has merged, whatever the parent was based on (the default branch when a
// stack merges bottom-up).
func (idx Branches) effectiveBase(t Task) string {
	for range 50 {
		p, ok := idx.parentOf(t)
		if !ok || p.Status() != Merged {
			break
		}
		t = p
	}
	return t.Base()
}

// restackReason says why a stacked task's branch has to be rebased, or nothing:
// "<parent> merged", "<parent> abandoned", or "<parent> moved" when the parent's
// pushed branch has commits this one lacks. It reads the refs as last fetched.
func (idx Branches) restackReason(t Task, repo string) string {
	p, ok := idx.parentOf(t)
	if !ok {
		return ""
	}
	branch := "refs/heads/" + t.Branch()
	if !gitOK(repo, "show-ref", "--verify", "--quiet", branch) {
		return ""
	}
	switch p.Status() {
	case Merged:
		return p.ID() + " merged"
	case Abandoned:
		return p.ID() + " abandoned"
	}
	tip, _ := git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+p.Branch()+"^{commit}")
	if tip == "" || gitOK(repo, "merge-base", "--is-ancestor", tip, branch) {
		return ""
	}
	return p.ID() + " moved"
}

// restackTarget is the commit a stacked task's branch belongs on now: the tip of
// its effective base on origin, as last fetched. Empty if that branch isn't on
// origin.
func (idx Branches) restackTarget(t Task, repo string) string {
	sha, _ := git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+idx.effectiveBase(t)+"^{commit}")
	return sha
}

// parentNeedsRestack: a stack restacks bottom-up, and a parent that is about to
// be rebased would only move this task again.
func (idx Branches) parentNeedsRestack(t Task, repo string) bool {
	p, ok := idx.parentOf(t)
	return ok && p.Status().InFlight() && idx.restackReason(p, repo) != ""
}

// noteBaseSHA moves base_sha, the newest commit of the parent's branch that the
// task's own branch contains: where `git rebase --onto` has to cut. Once the
// parent is squash-merged or rebased, the merge base of the two branches falls
// back to a commit before the parent's work, so the recorded one is kept for as
// long as the task's branch still contains it. It reports whether it changed
// base_sha. Call it with the lock held.
func noteBaseSHA(t, parent Task, repo string) bool {
	old, branch := t.Get("base_sha"), "refs/heads/"+t.Branch()
	mb, err := git(repo, "merge-base", "refs/remotes/origin/"+parent.Branch(), branch)
	if err != nil || mb == old {
		return false
	}
	if old != "" && gitOK(repo, "merge-base", "--is-ancestor", old, branch) && !gitOK(repo, "merge-base", "--is-ancestor", old, mb) {
		return false
	}
	t.Set("base_sha", mb)
	return true
}

// inFlight counts the tasks in flight. A stack counts once: a task stacked on
// another in-flight task isn't counted.
func (c *Crew) inFlight() int {
	idx := c.branches("")
	n := 0
	for _, t := range c.tasks("") {
		if !t.Status().InFlight() {
			continue
		}
		if p, ok := idx.parentOf(t); ok && p.Status().InFlight() {
			continue
		}
		n++
	}
	return n
}

// goalTasks are the tasks that belong to the goal.
func (c *Crew) goalTasks(g Goal) []Task {
	var ts []Task
	for _, t := range c.tasks(g.Project()) {
		if t.Goal() == g.ID() {
			ts = append(ts, t)
		}
	}
	return ts
}
