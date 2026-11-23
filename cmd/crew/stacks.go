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

// raiseRisk makes a task high risk when its own work (ownWork) changes a file
// that matches one of the project's SENSITIVE_PATHS (git pathspecs), and logs
// the first such file. When the raise turns the task's automated review back on
// (AutomatedReviewOff), the line says so too. It never lowers a tier. It returns
// the line it logged, or nothing, and an error when it couldn't look. Call it
// with the lock held.
func raiseRisk(t Task, p *Project) (string, error) {
	pathspecs := strings.Fields(p.Settings["SENSITIVE_PATHS"])
	if len(pathspecs) == 0 || t.Risk() == RiskHigh {
		return "", nil
	}
	work, err := ownWork(t, p.Repo())
	if err != nil {
		return "", fmt.Errorf("couldn't check SENSITIVE_PATHS: %w", err)
	}
	var touched []string
	for _, w := range work {
		out, err := git(p.Repo(), append([]string{"diff", "-z", "--name-only", w.since, w.tip, "--"}, pathspecs...)...)
		if err != nil {
			return "", fmt.Errorf("couldn't check SENSITIVE_PATHS (%s): git diff failed; is each one a pathspec?", strings.Join(pathspecs, " "))
		}
		for _, f := range strings.Split(out, "\x00") {
			if f != "" && !slices.Contains(touched, f) {
				touched = append(touched, f)
			}
		}
	}
	if len(touched) == 0 {
		return "", nil
	}
	slices.Sort(touched)
	line := "risk → high: touches " + touched[0]
	if len(touched) > 1 {
		line += fmt.Sprintf(" (+%d more)", len(touched)-1)
	}
	if t.AutomatedReviewOff() && atoiOr0(p.Settings["AUTO_REVIEWS"]) > 0 {
		line += "; automated review back on"
	}
	t.Set("risk", string(RiskHigh))
	t.Touch()
	t.Log(line)
	return line, nil
}

// branchWork is a task's own work in one commit of its branch, tip: what tip
// changes since the commit where that work starts.
type branchWork struct{ since, tip string }

// here says whether the work is the branch's here, rather than on origin.
func (w branchWork) here() bool { return strings.HasPrefix(w.tip, "refs/heads/") }

// ownWork is a task's own work at each tip of its branch: here and on origin, as
// last fetched, since either may have commits the other lacks.
func ownWork(t Task, repo string) ([]branchWork, error) {
	var work []branchWork
	for _, tip := range []string{"refs/heads/" + t.Branch(), "refs/remotes/origin/" + t.Branch()} {
		if !gitOK(repo, "show-ref", "--verify", "--quiet", tip) {
			continue
		}
		w, err := workIn(t, repo, tip)
		if err != nil {
			return nil, err
		}
		work = append(work, w)
	}
	if len(work) == 0 {
		return nil, fmt.Errorf("branch %s isn't here or on origin", t.Branch())
	}
	return work, nil
}

// workIn is the task's own work in commit c, which may be from before a restack:
// it starts where c left origin/<base>, or at the newest cut c contains when
// that's later. The cuts are base_sha and the earlier_base_shas it moved from:
// a rebased parent, or a merged one whose branch is gone, leaves the merge base
// behind the parent's work.
func workIn(t Task, repo, c string) (branchWork, error) {
	if !gitOK(repo, "rev-parse", "--verify", "--quiet", c+"^{commit}") {
		return branchWork{}, fmt.Errorf("%s isn't a commit in %s", c, repo)
	}
	base := "refs/remotes/origin/" + t.Base()
	since, err := git(repo, "merge-base", base, c)
	if err != nil {
		since = ""
	}
	for _, cut := range strings.Fields(t.Get("base_sha") + " " + t.Get("earlier_base_shas")) {
		if gitOK(repo, "merge-base", "--is-ancestor", cut, c) &&
			(since == "" || !gitOK(repo, "merge-base", "--is-ancestor", cut, since)) {
			since = cut
		}
	}
	if since == "" {
		if !gitOK(repo, "rev-parse", "--verify", "--quiet", base+"^{commit}") {
			return branchWork{}, fmt.Errorf("origin/%s is gone, and no base_sha says where %s starts", t.Base(), c)
		}
		return branchWork{}, fmt.Errorf("%s has no merge base with origin/%s", c, t.Base())
	}
	return branchWork{since, c}, nil
}

// unchangedSince: w is the work that was, unchanged. Replayed onto where w
// starts, then's work gives exactly w's tree, so a restack or a merge of the
// base that git made on its own keeps it, and any other change doesn't.
func (w branchWork) unchangedSince(repo string, then branchWork) (bool, error) {
	// git writes the tree on the first line, and exits 1 when the replay
	// conflicts. With nothing on that line, it couldn't replay at all.
	out, err := git(repo, "merge-tree", "--write-tree", "--merge-base="+then.since, w.since, then.tip)
	replayed, _, _ := strings.Cut(out, "\n")
	if replayed == "" {
		return false, fmt.Errorf("git merge-tree couldn't replay %s onto %s in %s (crew needs git 2.40 or later)", then.tip, w.since, repo)
	}
	if err != nil {
		return false, nil
	}
	tree, err := git(repo, "rev-parse", w.tip+"^{tree}")
	if err != nil {
		return false, fmt.Errorf("git rev-parse %s^{tree} failed in %s", w.tip, repo)
	}
	return replayed == tree, nil
}

// retargetReason says why a task's open PR has to be pointed at another branch,
// or nothing: "PR targets <pr_base>, not <base>" when the base crew sync last read
// from GitHub isn't the one the task belongs on now (effectiveBase), so a PR that
// GitHub moved itself when a parent merged isn't sent back. It applies to any
// task, stacked or not, and never asks for a rebase: that's restackReason's. It
// stays quiet until origin has the base, as last fetched, since GitHub can't
// target a branch it doesn't have.
func (idx Branches) retargetReason(t Task, repo string) string {
	if !idx.prOffBase(t) {
		return ""
	}
	if !gitOK(repo, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+idx.effectiveBase(t)) {
		return ""
	}
	return idx.offBaseReason(t, t.Get("pr_base"))
}

// prOffBase: crew sync last saw the task's open PR target a branch other than
// the one the task belongs on now.
func (idx Branches) prOffBase(t Task) bool {
	return t.HasOpenPR() && idx.offBaseReason(t, t.Get("pr_base")) != ""
}

// offBaseReason says "PR targets <prBase>, not <base>" when the task's PR targets
// prBase but the task belongs on another branch now (effectiveBase). It's empty
// when prBase is that branch, or unknown.
func (idx Branches) offBaseReason(t Task, prBase string) string {
	if base := idx.effectiveBase(t); prBase != "" && prBase != base {
		return fmt.Sprintf("PR targets %s, not %s", prBase, base)
	}
	return ""
}

// onBase: the task's branch needs no rebase for its PR to target its base. A
// stacked task's holds its parent's latest commit (restackReason has nothing to
// say), and an unstacked task's starts on its base: base_sha is on origin/<base>.
// An unstacked task moved off an abandoned parent isn't, since it still holds
// that parent's commits.
func (idx Branches) onBase(t Task, repo string) bool {
	if _, stacked := idx.parentOf(t); stacked {
		return idx.restackReason(t, repo) == ""
	}
	bs := t.Get("base_sha")
	return bs != "" && gitOK(repo, "merge-base", "--is-ancestor", bs, "refs/remotes/origin/"+t.Base())
}

// restackTarget is the commit a stacked task's branch belongs on now: the tip of
// its effective base on origin, as last fetched. Empty if that branch isn't on
// origin.
func (idx Branches) restackTarget(t Task, repo string) string {
	sha, _ := git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+idx.effectiveBase(t)+"^{commit}")
	return sha
}

// rebase is what moves a stacked task's branch onto its parent's latest commit,
// or onto the branch the parent merged into, and how a worker is told to run it.
type rebase struct {
	reason  string // why it's needed: restackReason's answer
	onto    string // origin's tip of newBase
	baseSHA string // base_sha: the last of the parent's commits in the branch
	newBase string // the branch the task belongs on now (effectiveBase)
	parent  string // the parent's ID
	branch  string // the task's branch
	lease   string // where origin has the branch now; empty when it has none
}

// rebaseFor is t's rebase onto its parent, or why there's none to run. It's the
// zero rebase when t's branch already holds its parent's latest commit.
func (idx Branches) rebaseFor(t, parent Task, repo string) (rebase, error) {
	r := rebase{reason: idx.restackReason(t, repo), newBase: idx.effectiveBase(t), parent: parent.ID(), branch: t.Branch()}
	if r.reason == "" {
		return rebase{}, nil
	}
	if r.onto = idx.restackTarget(t, repo); r.onto == "" {
		return rebase{}, fail("origin/%s doesn't exist, so there's nothing to restack %s onto", r.newBase, t.ID())
	}
	if r.baseSHA = t.Get("base_sha"); r.baseSHA == "" {
		return rebase{}, fail("%s has no base_sha (it was dispatched before crew recorded one). Find the last commit of %s's in %s and run 'crew set %s base_sha <sha>'", t.ID(), r.parent, r.branch, t.ID())
	}
	if !gitOK(repo, "merge-base", "--is-ancestor", r.baseSHA, "refs/heads/"+r.branch) {
		return rebase{}, fail("%s no longer contains %s's commits (base_sha %s), so it looks restacked already. If it is, record it with 'crew set %s base %s'", r.branch, r.parent, cut(r.baseSHA, 12), t.ID(), r.newBase)
	}
	// The push's lease is where origin has the branch now, so the push fails if
	// anyone else moved it. A branch that was never pushed gets an empty lease,
	// which tells git it must not exist on origin yet.
	r.lease, _ = git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+r.branch+"^{commit}")
	return r, nil
}

// steps tells a worker to run the rebase, then what it does next, and how to
// push the result. then is a clause that follows "then", such as
// `follow your restack workflow`.
func (r rebase) steps(then string) string {
	return fmt.Sprintf("rebase your branch with `git fetch origin && git rebase --onto %s %s`, then %s. %s is origin/%s now; %s is the last of %s's commits in your branch. Push the result with `git push --force-with-lease=%s:%s origin %s`, exactly as written and as a command on its own.",
		r.onto, r.baseSHA, then, r.onto, r.newBase, r.baseSHA, r.parent, r.branch, r.lease, r.branch)
}

// afterPush tells the worker of t what to run once it has pushed the rebase,
// when the task or its PR has to move to another branch too, and names that
// branch (retargetTo). retarget is retargetReason's answer for t.
func (r rebase) afterPush(t Task, retarget string) (then, retargetTo string) {
	switch {
	case r.newBase != t.Base(): // The parent merged: the task moves to what it merged into, PR and all.
		pr := ""
		if t.PR() != "" {
			pr = fmt.Sprintf("`gh pr edit --base %s` and ", r.newBase)
		}
		return fmt.Sprintf(" Your base is now %s: after pushing, run %s`crew set %s base %s`.", r.newBase, pr, t.ID(), r.newBase), r.newBase
	case retarget != "": // The task moved already, and its PR hasn't followed.
		return fmt.Sprintf(" Your PR targets %s, not your base: after pushing, run `gh pr edit --base %s`.", t.Get("pr_base"), r.newBase), r.newBase
	}
	return "", ""
}

// movesBeforeHandOver is why crew doesn't restack a task that hasn't been
// handed over: crew hand-over refuses its first hand-over while its branch or
// its PR is off the task's base (refuseOffBase), and tells its worker how to
// move them.
const movesBeforeHandOver = "its own worker moves its branch and PR before its first hand-over; crew hand-over tells it how"

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
// long as the task's branch still contains it. The one it moves from goes to the
// front of earlier_base_shas, so evidence from before the move can still be
// checked (workIn). It reports whether it changed base_sha. Call it with the
// lock held.
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
	if old != "" {
		t.Set("earlier_base_shas", strings.TrimSpace(old+" "+t.Get("earlier_base_shas")))
	}
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
