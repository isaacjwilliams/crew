package main

// A task is stacked when its base is another task's branch; that task is its
// parent. Nothing else records the relationship, so it can't disagree with base.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

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
func (idx Branches) parentOf(t Task) (Task, bool) { return idx.parentWith(t, t.Base()) }

// parentWith is the task t would be stacked on with this base, if any.
func (idx Branches) parentWith(t Task, base string) (Task, bool) {
	if base == "" {
		return Task{}, false
	}
	p, ok := idx[taskProjectDir(t)+":"+base]
	if !ok || p.Path == t.Path {
		return Task{}, false
	}
	return p, true
}

// childrenOf are the tasks stacked on t.
func (idx Branches) childrenOf(t Task) []Task {
	var children []Task
	for _, c := range idx {
		if p, ok := idx.parentOf(c); ok && p.Path == t.Path {
			children = append(children, c)
		}
	}
	return children
}

// stackRefusal says why a task of kind child can't be stacked on parent, of kind
// parentKind, or nothing. Only kinds that make a PR take part in stacks: a
// restack has its worker push, and a task waits for its parent's branch on
// origin. A kind crew doesn't know is left to the commands that report it.
func stackRefusal(child Kind, parent Task, parentKind Kind) error {
	if rules, ok := child.rules(); ok && !rules.pr {
		return fail("a %s's branch stays local, so it can't be stacked", child)
	}
	if rules, ok := parentKind.rules(); ok && !rules.pr {
		return fail("a %s's branch is never pushed, so nothing can stack on %s", parentKind, parent.ID())
	}
	return nil
}

// liveParent is the task t is stacked on once crew dispatch has moved it past
// any parents that have merged (effectiveBase): its parent, or the first task
// further down the stack that hasn't merged.
func (idx Branches) liveParent(t Task) (Task, bool) {
	for range 50 {
		p, ok := idx.parentOf(t)
		if !ok || p.Status() != Merged {
			return p, ok
		}
		t = p
	}
	return Task{}, false
}

// abandonedParent is the task t is stacked on (liveParent), when that task is
// abandoned: it will never push or merge, so t can't dispatch on it.
func (idx Branches) abandonedParent(t Task) (Task, bool) {
	p, ok := idx.liveParent(t)
	return p, ok && p.Status() == Abandoned
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

// raiseRisk makes a task high risk when its branch changes a file that matches
// one of the project's SENSITIVE_PATHS (git pathspecs), and logs the first such
// file. It never lowers a tier. It looks at the branch here and on origin, as last
// fetched, since either may have commits the other lacks: each one's changes since
// it left origin/<base>. It starts at base_sha instead when a rebased parent left
// the merge base behind the parent's work (as noteBaseSHA does), or when a merged
// parent's branch is gone from origin (as crew observe does). It returns the line
// it logged, or nothing, and an error when it couldn't look. Call it with the lock
// held.
func raiseRisk(t Task, p *Project) (string, error) {
	pathspecs := strings.Fields(p.Settings["SENSITIVE_PATHS"])
	if len(pathspecs) == 0 || t.Risk() == RiskHigh {
		return "", nil
	}
	repo, base := p.Repo(), "refs/remotes/origin/"+t.Base()
	var touched []string
	looked := false
	for _, tip := range []string{"refs/heads/" + t.Branch(), "refs/remotes/origin/" + t.Branch()} {
		if !gitOK(repo, "show-ref", "--verify", "--quiet", tip) {
			continue
		}
		since, err := git(repo, "merge-base", base, tip)
		bs := t.Get("base_sha")
		if bs != "" && gitOK(repo, "merge-base", "--is-ancestor", bs, tip) &&
			(err != nil || !gitOK(repo, "merge-base", "--is-ancestor", bs, since)) {
			since, err = bs, nil
		}
		if err != nil {
			if !gitOK(repo, "rev-parse", "--verify", "--quiet", base+"^{commit}") {
				return "", fmt.Errorf("couldn't check SENSITIVE_PATHS: origin/%s is gone, and base_sha doesn't say where %s starts", t.Base(), t.Branch())
			}
			return "", fmt.Errorf("couldn't check SENSITIVE_PATHS: %s has no merge base with origin/%s", t.Branch(), t.Base())
		}
		out, err := git(repo, append([]string{"diff", "-z", "--name-only", since, tip, "--"}, pathspecs...)...)
		if err != nil {
			return "", fmt.Errorf("couldn't check SENSITIVE_PATHS (%s): git diff failed; is each one a pathspec?", strings.Join(pathspecs, " "))
		}
		for _, f := range strings.Split(out, "\x00") {
			if f != "" && !slices.Contains(touched, f) {
				touched = append(touched, f)
			}
		}
		looked = true
	}
	if !looked {
		return "", fmt.Errorf("couldn't check SENSITIVE_PATHS: branch %s isn't here or on origin", t.Branch())
	}
	if len(touched) == 0 {
		return "", nil
	}
	slices.Sort(touched)
	line := "risk → high: touches " + touched[0]
	if len(touched) > 1 {
		line += fmt.Sprintf(" (+%d more)", len(touched)-1)
	}
	t.Set("risk", string(RiskHigh))
	t.Touch()
	t.Log(line)
	return line, nil
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

// taskGoal is the goal the task belongs to, if any.
func (c *Crew) taskGoal(t Task) (Goal, bool) {
	g := t.Goal()
	return Goal{Doc{c.path("projects", t.Project(), "goals", g+".md")}}, g != ""
}
