package main

// Commands that act on a task's worker session: crew followup resumes it,
// crew attach opens it in this terminal, and crew restack resumes it with a
// rebase to run.

import (
	"fmt"
	"os"
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

// followup resumes a task's worker with the review rounds it hasn't been sent, a
// note, or word that a pause cut its last turn short (interrupted), or several of
// these; or starts a fresh worker when fresh is set or the resume fails.
func (c *Crew) followup(t Task, fresh bool, note string) error {
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
	job := t.WorkerJob()
	if !t.Dispatched() {
		return fail("%s has no worker yet; dispatch it first", t.ID())
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	as := c.agents()
	rounds, sent := atoiOr0(t.Get("review_rounds")), atoiOr0(t.Get("last_round_sent"))
	newRound, interrupted := rounds > sent, t.Get("interrupted") != ""
	if as.jobBusy(job) {
		// A round sent any other way would go out again: only crew records last_round_sent.
		if newRound {
			return fail("worker %s is busy (%s). crew tick sends %s once it's free", job, as.jobState(job), unsentRounds(t))
		}
		return fail("worker %s is busy (%s). Wait for it, answer it in your terminal with crew attach %s, or SendMessage %s a note", job, as.jobState(job), c.taskArg(t), t.Get("worker_name"))
	}
	if !newRound && note == "" && !interrupted {
		return fail("nothing new for %s: no new review round (run 'crew feedback %s') and no --note", t.ID(), t.ID())
	}
	if st := t.Status(); st.Finished() {
		return fail("%s is %s; not resuming its worker", t.ID(), st)
	}
	warnManyRounds(p, t)
	// The worktree may not be on this machine: the task moved here, say.
	if err := c.ensureWorktree(p, t); err != nil {
		return err
	}
	wt := t.Worktree()

	prompt := fmt.Sprintf("New input for task %s (task file: %s).", t.ID(), t.Path)
	if newRound {
		prompt += fmt.Sprintf(" New review feedback is in %s under '## Review rounds'; follow your review-round workflow.", unsentRounds(t))
		if p.Settings["REPLY_TO_THREADS"] == "yes" {
			prompt += " Reply to each comment in those rounds with `crew reply`."
		} else {
			prompt += " Don't reply to the comments on the PR: this project has REPLY_TO_THREADS off."
		}
	}
	if interrupted {
		prompt += " " + carryOn
	}
	if note != "" {
		prompt += " Note from the lead (also check Decisions in the task file): " + note
	}
	if err := c.lock(); err != nil {
		return err
	}
	// Again under the lock: a task merged or abandoned since crew tick looked at
	// it, or while crew feedback or crew ci-fix read GitHub, keeps its worker
	// stopped, and so does a pause.
	if st := t.Status(); st.Finished() {
		return fail("%s is %s; not resuming its worker", t.ID(), st)
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	l, err := c.relaunch(start, t.Doc, wt, prompt, fmt.Sprintf(
		"You are picking up task %s in project %s, which already has work on its branch.\nTask file: %s\nWorktree (your working directory): %s\nRead the whole task file (including Decisions, Review rounds and Log) and inspect the branch (git log, gh pr view) before acting.",
		t.ID(), p.Name, t.Path, wt), fresh)
	if err != nil {
		return err
	}
	l.record(t.Doc)
	t.Set("last_round_sent", fmt.Sprint(rounds))
	t.Set("interrupted", "")
	if newRound {
		t.Set("status", string(AddressingFeedback))
	} else {
		t.Set("status", string(Working))
	}
	t.Touch()
	msg := fmt.Sprintf("followup (%s): worker %s", l.mode, l.job)
	if interrupted {
		msg += ", carrying on after a pause"
	}
	if note != "" {
		msg += " — note: " + note
	}
	t.Log(msg)
	c.commit("followup " + p.Name + "/" + t.ID())
	fmt.Printf("followup sent to %s → worker %s (%s)\n", t.ID(), l.job, l.mode)
	return nil
}

// unsentRounds names the review rounds the task's worker hasn't been sent yet:
// "round 3", or "rounds 2 to 4" when several piled up while it was busy.
func unsentRounds(t Task) string {
	from, to := atoiOr0(t.Get("last_round_sent"))+1, atoiOr0(t.Get("review_rounds"))
	if from == to {
		return fmt.Sprintf("round %d", to)
	}
	return fmt.Sprintf("rounds %d to %d", from, to)
}

// crew attach TASK: attaches this terminal to the task's worker, as `claude
// attach <job>` does. crew holds no lock while attached: a followup or a tick that
// resumes the worker stops its idle process first, and that ends the attach.
func cmdAttach(c *Crew, args []string) error {
	if len(args) != 1 {
		return usage("crew attach TASK")
	}
	// A session's Bash has no terminal to hand over, so a lead that follows a
	// hint would get a claude that can't be used.
	if os.Getenv("CLAUDECODE") != "" {
		return fail("crew attach needs your own terminal, not a Claude Code session; run 'crew attach %s' in one", args[0])
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	if t.WorkerJob() == "" {
		return fail("no worker has run for %s yet", t.ID())
	}
	return execClaude(c.claude("attach", t.WorkerJob()))
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
	if err := refuseIfPaused(p); err != nil {
		return err
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
	// The push's lease is where origin has the branch now, so the push fails if
	// anyone else moved it. A branch that was never pushed gets an empty lease,
	// which tells git it must not exist on origin yet.
	lease, _ := git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch+"^{commit}")
	note := fmt.Sprintf("Restack (%s): rebase your branch with `git fetch origin && git rebase --onto %s %s`, then follow your restack workflow. %s is origin/%s now; %s is the last of %s's commits in your branch. Push the result with `git push --force-with-lease=%s:%s origin %s`, exactly as written and as a command on its own.",
		reason, target, old, target, newBase, old, pid, branch, lease, branch)
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
