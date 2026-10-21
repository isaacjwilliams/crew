package main

import (
	"fmt"
)

// crew followup TASK [--fresh] [--note TEXT]
func cmdFollowup(c *Crew, args []string) error {
	if err := need("claude"); err != nil {
		return err
	}
	var arg, note string
	fresh := false
	for len(args) > 0 {
		switch {
		case args[0] == "--fresh":
			fresh, args = true, args[1:]
		case args[0] == "--note" && len(args) > 1:
			note, args = args[1], args[2:]
		default:
			arg, args = args[0], args[1:]
		}
	}
	if arg == "" {
		return usage("crew followup TASK [--fresh] [--note TEXT]")
	}
	t, err := c.resolveTask(arg)
	if err != nil {
		return err
	}
	return c.followup(t, fresh, note)
}

// followup resumes a task's worker with its newest review round, or a note, or
// both; or starts a fresh worker when fresh is set or the resume fails.
func (c *Crew) followup(t Task, fresh bool, note string) error {
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	if _, err := c.workerSettings(p); err != nil {
		return err
	}
	job := t.WorkerJob()
	if !t.Dispatched() {
		return fail("%s has no worker yet; dispatch it first", t.ID())
	}
	as := c.agents()
	rounds, sent := atoiOr0(t.Get("review_rounds")), atoiOr0(t.Get("last_round_sent"))
	newRound := rounds > sent
	if as.jobBusy(job) {
		// A round sent any other way would go out again: only crew records last_round_sent.
		if newRound {
			return fail("worker %s is busy (%s). crew tick sends round %d once it's free", job, as.jobState(job), rounds)
		}
		return fail("worker %s is busy (%s). Wait for it, answer it in agent view (claude attach %s), or SendMessage %s a note", job, as.jobState(job), job, t.Get("worker_name"))
	}
	if !newRound && note == "" {
		return fail("nothing new for %s: no new review round (run 'crew feedback %s') and no --note", t.ID(), t.ID())
	}
	if max, err := intSetting(p.Settings, "MAX_REVIEW_ROUNDS"); err == nil && rounds > max {
		warn("round %d exceeds MAX_REVIEW_ROUNDS=%d", rounds, max)
	}
	// The worktree may not be on this machine: the task moved here, say.
	if err := c.ensureWorktree(p, t); err != nil {
		return err
	}
	wt := t.Worktree()

	prompt := fmt.Sprintf("New input for task %s (task file: %s).", t.ID(), t.Path)
	if newRound {
		prompt += fmt.Sprintf(" New review feedback is in Round %d under '## Review rounds'; follow your review-round workflow.", rounds)
		if p.Settings["REPLY_TO_THREADS"] == "yes" {
			prompt += " Reply to each comment in the round with `crew reply`."
		} else {
			prompt += " Don't reply to the comments on the PR: this project has REPLY_TO_THREADS off."
		}
	}
	if note != "" {
		prompt += " Note from the lead (also check Decisions in the task file): " + note
	}
	if err := c.lock(); err != nil {
		return err
	}
	l, err := c.relaunch(c.launchWorker(p), "worker", t.Doc, wt, prompt, fmt.Sprintf(
		"You are picking up task %s in project %s, which already has work on its branch.\nTask file: %s\nWorktree (your working directory): %s\nRead the whole task file (including Decisions, Review rounds and Log) and inspect the branch (git log, gh pr view) before acting.",
		t.ID(), p.Name, t.Path, wt), fresh)
	if err != nil {
		return err
	}
	t.Set("worker_job", l.job)
	if l.session != "" {
		t.Set("worker_session", l.session)
	}
	t.Set("last_round_sent", fmt.Sprint(rounds))
	if newRound {
		t.Set("status", string(AddressingFeedback))
	} else {
		t.Set("status", string(Working))
	}
	t.Touch()
	msg := fmt.Sprintf("followup (%s): worker %s", l.mode, l.job)
	if note != "" {
		msg += " — note: " + note
	}
	t.Log(msg)
	c.commit("followup " + p.Name + "/" + t.ID())
	fmt.Printf("followup sent to %s → worker %s (%s)\n", t.ID(), l.job, l.mode)
	return nil
}

// crew restack TASK [--again]: has a stacked task's worker rebase it after its
// parent moved or merged. crew only works out where to cut and what to rebase
// onto; the worker does the rebase, the push and the PR retarget, and records the
// new base when it has succeeded, so a failed restack still shows as one that's
// needed. restack_sent records the commit the last restack asked for, so the same
// restack isn't sent twice unless --again.
func cmdRestack(c *Crew, args []string) error {
	if err := need("git"); err != nil {
		return err
	}
	arg, again := "", false
	for _, a := range args {
		if a == "--again" {
			again = true
		} else {
			arg = a
		}
	}
	if arg == "" {
		return usage("crew restack TASK [--again]")
	}
	t, err := c.resolveTask(arg)
	if err != nil {
		return err
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	repo, idx := p.Repo(), c.branches(p.Name)
	base, branch := t.Base(), t.Branch()
	parent, ok := idx.parentOf(t)
	if !ok {
		return fail("%s isn't stacked: its base '%s' isn't another task's branch", t.ID(), base)
	}
	pid := parent.ID()
	if !t.Dispatched() {
		return fail("%s has no branch yet; dispatch it first", t.ID())
	}
	// On a machine the task moved to, its branch may be on origin only.
	if err := c.ensureWorktree(p, t); err != nil {
		return err
	}
	if !gitOK(repo, "fetch", "--quiet", "origin") {
		warn("git fetch failed; using local refs")
	}
	if err := c.lock(); err != nil {
		return err
	}
	if noteBaseSHA(t, parent, repo) {
		c.commit("base_sha " + p.Name + "/" + t.ID())
	}
	c.unlock()
	if idx.parentNeedsRestack(t, repo) {
		return fail("%s needs a restack itself (%s); restack it first, a stack goes bottom-up", pid, idx.restackReason(parent, repo))
	}
	reason := idx.restackReason(t, repo)
	if reason == "" {
		fmt.Printf("%s already sits on %s's latest commit; nothing to restack\n", t.ID(), pid)
		return nil
	}
	if parent.Status() == Abandoned {
		return fail("%s is abandoned; give %s another base with 'crew set %s base <branch>' and tell its worker", pid, t.ID(), t.ID())
	}
	newBase := idx.effectiveBase(t)
	target := idx.restackTarget(t, repo)
	if target == "" {
		return fail("origin/%s doesn't exist, so there's nothing to restack %s onto", newBase, t.ID())
	}
	if !again && t.Get("restack_sent") == target {
		return fail("%s's worker was already asked to restack onto %s; check its Open questions and log, or send it again with --again", t.ID(), cut(target, 12))
	}
	old := t.Get("base_sha")
	if old == "" {
		return fail("%s has no base_sha (it was dispatched before crew recorded one). Find the last commit of %s's in %s and run 'crew set %s base_sha <sha>'", t.ID(), pid, branch, t.ID())
	}
	if !gitOK(repo, "merge-base", "--is-ancestor", old, "refs/heads/"+branch) {
		return fail("%s no longer contains %s's commits (base_sha %s), so it looks restacked already. If it is, record it with 'crew set %s base %s'", branch, pid, cut(old, 12), t.ID(), newBase)
	}
	note := fmt.Sprintf("Restack (%s): rebase your branch with `git fetch origin && git rebase --onto %s %s`, then follow your restack workflow. %s is origin/%s now; %s is the last of %s's commits in your branch.",
		reason, target, old, target, newBase, old, pid)
	if newBase != base {
		note += fmt.Sprintf(" Your base is now %s: after pushing, run `gh pr edit --base %s` and `crew set %s base %s`.", newBase, newBase, t.ID(), newBase)
	}
	if err := c.followup(t, false, note); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	t.Set("restack_sent", target)
	c.commit("restack " + p.Name + "/" + t.ID())
	return nil
}
