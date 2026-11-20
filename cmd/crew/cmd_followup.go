package main

// Commands that act on a task's worker session: crew followup resumes it,
// crew attach opens it in this terminal, crew restack resumes it with a rebase or
// a retarget to run, and crew nudge resumes it when its turn ended mid-task.

import (
	"fmt"
	"os"
	"slices"
	"time"
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
	return c.followup(t, followupWith{fresh: fresh, note: note})
}

// followupWith is what a followup's caller adds to what the task itself holds
// (review rounds not yet sent, word of a pause).
type followupWith struct {
	fresh   bool   // start a fresh worker rather than resume the last one
	stalled bool   // tell the worker its turn ended mid-task (crew nudge)
	note    string // a note from the lead
}

// followup resumes a task's worker with the review rounds it hasn't been sent, a
// note from the lead, word that a pause cut its last turn short (interrupted), or
// word that its turn ended mid-task (stalled), or several of these; or starts a
// fresh worker when with.fresh is set or the resume fails.
func (c *Crew) followup(t Task, with followupWith) error {
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	// Building the launcher reads the worker settings, so a bad one stops here,
	// before anything changes.
	start, err := c.launchWorker(p, t.Model())
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
	if !newRound && with.note == "" && !interrupted && !with.stalled {
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
	if with.stalled {
		prompt += " " + stalledNote
	}
	if with.note != "" {
		prompt += " Note from the lead (also check Decisions in the task file): " + with.note
	}
	if err := c.lock(); err != nil {
		return err
	}
	// Again under the lock: a task merged or abandoned since crew tick looked at
	// it, or while an earlier tick step read GitHub, keeps its worker stopped,
	// and so does a pause.
	if st := t.Status(); st.Finished() {
		return fail("%s is %s; not resuming its worker", t.ID(), st)
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	l, err := c.relaunch(start, t.Doc, wt, prompt, fmt.Sprintf(
		"You are picking up task %s in project %s, which already has work on its branch.\nTask file: %s\nWorktree (your working directory): %s\nRead the whole task file (including Decisions, Review rounds and Log) and inspect the branch (git log, gh pr view) before acting.",
		t.ID(), p.Name, t.Path, wt), with.fresh)
	if err != nil {
		return err
	}
	l.record(t.Doc)
	t.Set("last_round_sent", fmt.Sprint(rounds))
	t.Set("interrupted", "")
	// Word of a pause alone leaves the status as the worker last set it: a pause
	// can land after it set pr-open.
	switch {
	case newRound:
		t.SetStatus(AddressingFeedback)
	case with.note != "" && !t.Status().WorkersTurn():
		// A note hands a task that waits on you back to its worker.
		t.SetStatus(backToWorker(t))
	}
	t.Touch()
	msg := fmt.Sprintf("followup (%s): worker %s", l.mode, l.job)
	if interrupted {
		msg += ", carrying on after a pause"
	}
	if with.stalled {
		msg += ", as its turn ended mid-task"
	}
	if with.note != "" {
		msg += " — note: " + with.note
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

// backToWorker is the status a note gives a task that waits on you. A question
// goes back to the worker's turn it was asked in, which the Log tells, so one
// asked in a review round goes back to addressing-feedback. Anything else goes
// to working.
func backToWorker(t Task) Status {
	if t.Status() == NeedsInput {
		for _, e := range slices.Backward(t.LogEntries()) {
			if s, ok := e.status(); ok && s != NeedsInput {
				if s.WorkersTurn() {
					return s
				}
				break
			}
		}
	}
	return Working
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

// crew restack TASK [--again]: has a task's worker rebase its branch after its
// parent moved or merged, or point its PR at the task's base when GitHub has it
// targeting another branch, or both. crew only works out what to run; the worker
// does the rebase, the push and the retarget, and records a new base when it has
// succeeded, so a failed restack still shows as one that's needed. restack_sent
// records the commit the last rebase was asked onto, and retarget_sent the base
// the last retarget asked for, so neither is sent twice unless --again.
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
	if !t.Dispatched() {
		return fail("%s has no branch yet; dispatch it first", t.ID())
	}
	// Refuse a task there's nothing to send for before making its worktree.
	if st := t.Status(); st.Finished() {
		return fail("%s is %s; not resuming its worker", t.ID(), st)
	}
	parent, stacked := idx.parentOf(t)
	if !stacked && !idx.prOffBase(t) {
		return fail("%s has nothing to restack: it isn't stacked (its base '%s' isn't another task's branch), and no open PR of its targets another branch", t.ID(), t.Base())
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
	if stacked {
		if err := c.lock(); err != nil {
			return err
		}
		if noteBaseSHA(t, parent, repo) {
			c.commit("base_sha " + p.Name + "/" + t.ID())
		}
		c.unlock()
	}
	// With nothing to rebase, a retarget goes on its own, whatever the parent's
	// state. Otherwise it goes in the rebase note, and waits with it.
	retarget := idx.retargetReason(t, repo)
	if retarget != "" && idx.onBase(t, repo) {
		return c.sendRetarget(p, t, retarget, again)
	}
	if stacked {
		return c.sendRebase(p, idx, t, parent, retarget, again)
	}
	if retarget == "" {
		return fail("origin has no branch %s yet, so %s's PR can't target it", t.Base(), t.ID())
	}
	return fail("%s's branch doesn't start on %s (base_sha %s isn't on it), so its PR would bring other commits there. crew restack rebases only stacked tasks; tell its worker what to do with 'crew followup %s --note'", t.ID(), t.Base(), cut(t.Get("base_sha"), 12), t.ID())
}

// sendRetarget resumes a task's worker to point its PR at the task's base, with
// nothing to rebase. retarget_sent is that base, not the PR's: moving the task
// to yet another branch is a new retarget, to be sent.
func (c *Crew) sendRetarget(p *Project, t Task, reason string, again bool) error {
	base := t.Base()
	if !again && t.Get("retarget_sent") == base {
		return fail("%s's worker was already asked to retarget its PR to %s; check its Open questions and log, or send it again with --again", t.ID(), base)
	}
	note := fmt.Sprintf("Retarget (%s): your PR belongs on your base, %s, and there's nothing to rebase. Run `gh pr edit --base %s`, then follow your restack workflow.", reason, base, base)
	if err := c.followup(t, followupWith{note: note}); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	t.Set("retarget_sent", base)
	c.commit("retarget " + p.Name + "/" + t.ID())
	return nil
}

// sendRebase resumes a stacked task's worker to rebase its branch onto its
// parent's latest commit, or onto what the parent merged into, and to retarget
// its PR when that's needed too. retarget is retargetReason's answer for t.
func (c *Crew) sendRebase(p *Project, idx Branches, t Task, parent Task, retarget string, again bool) error {
	repo, base, branch, pid := p.Repo(), t.Base(), t.Branch(), parent.ID()
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
	retargetTo := ""
	switch {
	case newBase != base: // The parent merged: the task moves to what it merged into, PR and all.
		retargetTo = newBase
		note += fmt.Sprintf(" Your base is now %s: after pushing, run `gh pr edit --base %s` and `crew set %s base %s`.", newBase, newBase, t.ID(), newBase)
	case retarget != "": // The task moved already, and its PR hasn't followed.
		retargetTo = base
		note += fmt.Sprintf(" Your PR targets %s, not your base: after pushing, run `gh pr edit --base %s`.", t.Get("pr_base"), base)
	}
	if err := c.followup(t, followupWith{note: note}); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	t.Set("restack_sent", target)
	if retargetTo != "" {
		t.Set("retarget_sent", retargetTo)
	}
	c.commit("restack " + p.Name + "/" + t.ID())
	return nil
}

// stallAfter is how long a worker's transcript must have been quiet before its
// task counts as stalled. A session the user is talking to with crew attach ends
// its turn after every reply, and a nudge would end the attach, so a short quiet
// spell doesn't count.
const stallAfter = 15 * time.Minute

// stalledNote is what crew nudge tells a worker whose turn ended mid-task: a
// worker seen stalled had waited on a sub-agent, or just stopped after a step.
const stalledNote = "Your last turn ended while this task's status still says you're working on it. If you're waiting on an answer, record it under Open questions and set the task's status to needs-input. If you started a sub-agent or a background command, check whether it has finished before you rely on its result. Then carry on from your last step."

// stalled reports whether a task's worker a (nil when claude agents doesn't list
// it) ended its turn by mistake. It did when:
//   - the task is working or addressing-feedback, with no open question: a worker
//     waiting on an answer would have asked one and set needs-input;
//   - a is listed and not stopped: a stopped worker was stopped on purpose, by
//     crew pause, say;
//   - a isn't mid-turn, whatever other state claude agents gives it;
//   - its transcript has been quiet for stallAfter.
func stalled(t Task, a *Agent) bool {
	if !t.Status().WorkersTurn() || t.OpenQuestions() > 0 {
		return false
	}
	if a == nil || a.State == "stopped" || a.busy() {
		return false
	}
	quiet, ok := a.quietFor()
	return ok && quiet >= stallAfter
}

// crew nudge TASK: resumes the worker of a stalled task (see stalled) with
// stalledNote, and logs the nudge with its branch's head commit. When the task
// stalls again with no new commit and no change of status since
// (NudgedWithoutProgress), or its worktree isn't on this machine, it adds an
// Open question for the lead instead, and crew tick's "waiting on you" rule makes
// the task needs-input. Exits 3 when the task isn't stalled.
func cmdNudge(c *Crew, args []string) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	if len(args) != 1 {
		return usage("crew nudge TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	if !t.Dispatched() {
		return fail("%s has no worker yet; dispatch it first", t.ID())
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	// Decided under the ledger lock, held until this command exits (followup
	// takes it again at no cost), so two runs can't both act.
	if err := c.lock(); err != nil {
		return err
	}
	worker := c.agents().find(t.WorkerJob())
	if !stalled(t, worker) {
		return nothingToDo("%s hasn't stalled", t.ID())
	}
	ask := func(question, log string) error {
		t.AppendToSection("## Open questions", "- [ ] "+question+"\n")
		t.Log(log)
		t.Touch()
		c.commit("nudge " + p.Name + "/" + t.ID())
		fmt.Printf("%s: %s; recorded under Open questions\n", t.ID(), log)
		return nil
	}
	// Making the worktree again can take minutes of a repo's hooks, and isn't
	// a tick's to do: it's left to the lead.
	if wt := t.Worktree(); !isDir(wt) {
		return ask(fmt.Sprintf("Its worker's turn ended mid-task, but its worktree, %s, isn't on this machine, so crew didn't nudge it. To have a fresh worker carry on here, run `crew followup %s --fresh --note \"Carry on from where your Log says you stopped.\"`: it makes the worktree again.", wt, c.taskArg(t)),
			"nudge: worktree missing; for the lead")
	}
	sha, err := git(p.Repo(), "rev-parse", "--verify", "--quiet", "refs/heads/"+t.Branch()+"^{commit}")
	if err != nil {
		return fail("%s's branch %s isn't in %s", t.ID(), t.Branch(), p.Repo())
	}
	head := shortSHA(sha)
	if t.NudgedWithoutProgress(head) {
		return ask(fmt.Sprintf("Its worker's turn ended mid-task again after a nudge, with no new commit (the branch is still at %s) and no change of status. Its last turn is in its transcript, %s. Answer it with `crew followup %s --note \"…\"`, or look into it yourself.", head, transcript(worker.SessionID), c.taskArg(t)),
			"nudge: stalled again at "+head+"; for the lead")
	}
	if err := c.followup(t, followupWith{stalled: true}); err != nil {
		return err
	}
	// Last, so NudgedWithoutProgress finds it only once the worker was resumed.
	t.Log(nudgedAt + head)
	c.commit("nudge " + p.Name + "/" + t.ID())
	return nil
}
