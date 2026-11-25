package main

// Commands that act on a task's worker session: crew followup resumes it,
// crew attach opens it in this terminal, crew restack starts a fresh one with a
// rebase or a retarget to run, and crew nudge resumes it when its turn ended
// mid-task.

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
	_, err = c.followup(t, followupWith{fresh: fresh, note: note})
	return err
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
// fresh worker when with.fresh is set, when the resume would cost more than a
// fresh start (coldResume), or when relaunch finds it must.
//
// It's an operation (operation.go). A session claude started that crew couldn't
// record ends in an unrecordedSession, and a worktree followup couldn't bring
// back to this machine in a worktreeError.
func (c *Crew) followup(t Task, with followupWith) (followupSent, error) {
	p, err := c.loadProject(t.Project())
	if err != nil {
		return followupSent{}, err
	}
	// Building the launcher reads the worker settings, so a bad one stops here,
	// before anything changes.
	start, err := c.launchWorker(p, t.Model(), t.Effort())
	if err != nil {
		return followupSent{}, err
	}
	// A restack session ran one restack and nothing else, so the worker after it
	// starts fresh, on the task's own model.
	restack, err := c.launchRestack(p)
	if err != nil {
		return followupSent{}, err
	}
	afterRestack := t.Get("worker_fingerprint") == restack.fingerprint
	// A bad mcp.json would stop the launch; stop here instead, before anything changes.
	if _, err := c.projectMCPServers(p.Name, p.Settings); err != nil {
		return followupSent{}, err
	}
	as := c.agents()
	if err := c.followupRefusal(p, t, as); err != nil {
		return followupSent{}, err
	}
	rounds, sent := atoiOr0(t.Get("review_rounds")), atoiOr0(t.Get("last_round_sent"))
	newRound, interrupted := rounds > sent, t.Get("interrupted") != ""
	if !newRound && with.note == "" && !interrupted && !with.stalled {
		return followupSent{}, fail("nothing new for %s: no new review round (run 'crew feedback %s') and no --note", t.ID(), t.ID())
	}
	c.warnManyRounds(p, t)
	// The worktree may not be on this machine: the task moved here, say.
	rebuilt, err := c.ensureWorktree(p, t)
	if err != nil {
		return followupSent{}, err
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
		return followupSent{}, err
	}
	// Again under the lock: a task merged or abandoned since crew tick looked at
	// it, or while an earlier tick step read GitHub, keeps its worker stopped, and
	// so does a pause. It reads the workers from the same listing, since listing
	// them again would cost another claude agents call.
	if err := c.followupRefusal(p, t, as); err != nil {
		return followupSent{}, err
	}
	// Why crew starts a fresh worker where the caller didn't ask for one: after
	// a restack, or when resuming would cost more. A fresh start the caller
	// asked for, or one a changed model, effort or settings needs, keeps the
	// label relaunch gives it.
	why := ""
	switch {
	case afterRestack:
		why = "its last session only ran a restack"
	case !with.fresh && start.changedSince(t.Doc) == "":
		why = coldResume(t.Get("worker_session"), time.Now())
	}
	l, err := c.relaunch(start, t.Doc, wt, prompt, pickUpPrompt(t, p.Name, wt, newRound), with.fresh || why != "")
	if err != nil {
		return followupSent{}, err
	}
	if why != "" {
		l.mode = "fresh: " + why
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
	c.printf("followup sent to %s → worker %s (%s)\n", t.ID(), l.job, l.mode)
	return followupSent{job: l.job, mode: l.mode, worktree: rebuilt}, nil
}

// followupSent is what followup sent: the worker it went to and how ("resume",
// "fresh" or "fresh: <why>"), as the task's Log says, and, when the task's
// worktree wasn't on this machine, how followup brought it back.
type followupSent struct {
	job      string
	mode     string
	worktree string // "" when it was here
}

// followupRefusal is why followup would send t's worker nothing now, whatever
// it was given to send, or nil when it would send: the task has no worker,
// it's finished, its project is paused, its worker is busy (as is what claude
// agents lists), or this machine doesn't run the crew, in that order. followup
// asks it before it sends and again under the ledger lock, and noteRefusal for
// a caller that asks first.
func (c *Crew) followupRefusal(p *Project, t Task, as Agents) error {
	if !t.Dispatched() {
		return fail("%s has no worker yet; dispatch it first", t.ID())
	}
	// Before the pause and busy checks: crew never resumes a finished task's
	// worker, so "resume the project" or "wait for it" would be bad advice.
	if st := t.Status(); st.Finished() {
		return fail("%s is %s; not resuming its worker", t.ID(), st)
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	if job := t.WorkerJob(); as.jobBusy(job) {
		// A round sent any other way would go out again: only crew records last_round_sent.
		if atoiOr0(t.Get("review_rounds")) > atoiOr0(t.Get("last_round_sent")) {
			return fail("worker %s is busy (%s). crew tick sends %s once it's free", job, as.jobState(job), unsentRounds(t))
		}
		return fail("worker %s is busy (%s). Wait for it, answer it in your terminal with crew attach %s, or SendMessage %s a note", job, as.jobState(job), c.taskArg(t), t.Get("worker_name"))
	}
	return c.requireHost()
}

// noteRefusal is why followup would refuse a note for t now, or nil when it
// would send one, for a caller that asks before the user writes the note. It
// gives followup's own reasons, with the workers as claude agents lists them
// now. What only sending can find, such as claude starting no session, is
// still followup's to report.
func (c *Crew) noteRefusal(t Task) error {
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	return c.followupRefusal(p, t, c.agents())
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
// targeting another branch, or both. crew only works out what to run; a fresh
// worker (startRestack) does the rebase, the push and the retarget, and records a
// new base when it has succeeded, so a failed restack still shows as one that's
// needed. restack_sent records the commit the last rebase was asked onto, and
// retarget_sent the base the last retarget asked for, so neither is sent twice
// unless --again.
//
// A restack waits until the task is pr-open, as crew tick's does. Before its
// first hand-over, the task's own worker moves it (refuseOffBase).
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
		return fail("%s is %s; not restacking it", t.ID(), st)
	}
	parent, stacked := idx.parentOf(t)
	if !stacked && !idx.prOffBase(t) {
		return fail("%s has nothing to restack: it isn't stacked (its base '%s' isn't another task's branch), and no open PR of its targets another branch", t.ID(), t.Base())
	}
	if st := t.Status(); st != PROpen {
		if !t.HandedOver() {
			return notYet("%s hasn't been handed over: %s", t.ID(), movesBeforeHandOver)
		}
		return notYet("%s is %s; a restack waits until it's %s", t.ID(), st, PROpen)
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	// On a machine the task moved to, its branch may be on origin only.
	if _, err := c.ensureWorktree(p, t); err != nil {
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

// sendRetarget has a fresh worker point its PR at the task's base, with
// nothing to rebase. retarget_sent is that base, not the PR's: moving the task
// to yet another branch is a new retarget, to be sent.
func (c *Crew) sendRetarget(p *Project, t Task, reason string, again bool) error {
	base := t.Base()
	if !again && t.Get("retarget_sent") == base {
		return fail("%s's worker was already asked to retarget its PR to %s; check its Open questions and log, or send it again with --again", t.ID(), base)
	}
	note := fmt.Sprintf("Retarget (%s): your PR belongs on your base, %s, and there's nothing to rebase. Run `gh pr edit --base %s`, then follow your restack workflow.", reason, base, base)
	return c.startRestack(p, t, note, "", base)
}

// sendRebase has a fresh worker rebase a stacked task's branch onto its
// parent's latest commit, or onto what the parent merged into, and retarget
// its PR when that's needed too. retarget is retargetReason's answer for t.
func (c *Crew) sendRebase(p *Project, idx Branches, t Task, parent Task, retarget string, again bool) error {
	repo, pid := p.Repo(), parent.ID()
	if idx.parentNeedsRestack(t, repo) {
		return fail("%s needs a restack itself (%s); restack it first, a stack goes bottom-up", pid, idx.restackReason(parent, repo))
	}
	if parent.Status() == Abandoned {
		return fail("%s is abandoned; give %s another base with 'crew set %s base <branch>' and tell its worker", pid, t.ID(), t.ID())
	}
	r, err := idx.rebaseFor(t, parent, repo)
	if err != nil {
		return err
	}
	if r.reason == "" {
		fmt.Printf("%s already sits on %s's latest commit; nothing to restack\n", t.ID(), pid)
		return nil
	}
	if !again && t.Get("restack_sent") == r.onto {
		return fail("%s's worker was already asked to restack onto %s; check its Open questions and log, or send it again with --again", t.ID(), cut(r.onto, 12))
	}
	then, retargetTo := r.afterPush(t, retarget)
	return c.startRestack(p, t, "Restack ("+r.reason+"): "+r.steps("follow your restack workflow")+then, r.onto, retargetTo)
}

// restackModel is the model a restack runs on, whatever the task's own: a
// rebase or a retarget, with the exact commands to run, needs no more.
const restackModel = "sonnet"

// pickUpPrompt opens a fresh worker's first message, before its input: the
// task and project, the task file and the worktree, and where to start. One
// sent review rounds starts from them and the brief's bounds, as the
// implementer's exception for "You are picking up review rounds" says; any
// other reads the whole task file and the branch first. Either is led to
// changes an earlier session left uncommitted, which the branch doesn't show.
func pickUpPrompt(t Task, project, wt string, newRound bool) string {
	prompt := fmt.Sprintf("You are picking up task %s in project %s, which already has work on its branch.\nTask file: %s\nWorktree (your working directory): %s\nRead the whole task file (including Decisions, Review rounds and Log) and inspect the branch (git log, gh pr view) before acting.",
		t.ID(), project, t.Path, wt)
	if newRound {
		prompt = fmt.Sprintf("You are picking up review rounds on task %s in project %s, which already has work on its branch.\nTask file: %s\nWorktree (your working directory): %s\nStart from the rounds named below and the task file's Goal, Acceptance criteria, Out of scope and Decisions: you judge the rounds against them. Don't read the whole task file or review the branch first; read the rest only as far as the rounds, or the rest of this message, need.",
			t.ID(), project, t.Path, wt)
	}
	if out, _ := git(wt, "status", "--porcelain"); out != "" {
		prompt += "\nAn earlier session left changes uncommitted in your worktree. Read git status and git diff before you act, and carry on from them: don't redo or discard them."
	}
	return prompt
}

// restackPrompt opens a restack session's first message, before the note: the
// task and project, then the task file and the worktree. implementer.md lets a
// session whose prompt opens this way skip reading the whole task file first.
const restackPrompt = "You are restacking task %s in project %s: this session takes over as its worker, for the restack below and nothing else.\nTask file: %s\nWorktree (your working directory): %s\nDon't read the whole task file or review the branch first; read only what a step of your restack workflow needs."

// launchRestack is the launcher of a restack session: a worker on restackModel,
// at the user's own effort whatever the task's, that restackPrompt tells to run
// one restack and nothing else. Its fingerprint takes in that prompt, since it's
// part of what the session loads. That sets a restack session's fingerprint apart from a
// worker's, so followup can tell one and start a fresh worker after it, even on
// a task whose model is restackModel.
func (c *Crew) launchRestack(p *Project) (launcher, error) {
	l, err := c.launchWorker(p, restackModel, "")
	if err != nil {
		return l, err
	}
	l.fingerprint = c.fingerprint([]string{l.fingerprint, restackPrompt})
	return l, nil
}

// startRestack starts a fresh session on restackModel to run note, a rebase or
// a retarget, and makes it the task's worker. Resuming the worker's own
// conversation would cost far more than the restack does, since it's long.
//
// It records what it sent as restack_sent (the commit to rebase onto) and
// retarget_sent (the PR's new base), leaving out an empty one.
//
// The session gets the restack and nothing else. Review rounds and word of a
// pause wait for the next followup, which starts a fresh worker on the task's
// own model (launchRestack).
func (c *Crew) startRestack(p *Project, t Task, note, onto, retargetTo string) error {
	// Building the launcher reads the worker settings, and a bad mcp.json would
	// stop the launch: either stops here, before anything changes.
	start, err := c.launchRestack(p)
	if err != nil {
		return err
	}
	if _, err := c.projectMCPServers(p.Name, p.Settings); err != nil {
		return err
	}
	// A busy worker would be working in the same worktree.
	if as, job := c.agents(), t.WorkerJob(); as.jobBusy(job) {
		return fail("worker %s is busy (%s). crew tick sends the restack once it's free", job, as.jobState(job))
	}
	if err := c.lock(); err != nil {
		return err
	}
	// Again under the lock: the task may have moved on since crew restack
	// looked at it, and the project may have paused.
	if st := t.Status(); st != PROpen {
		return fail("%s is %s now; not restacking it", t.ID(), st)
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}
	wt := t.Worktree()
	// fresh: never resume the worker's conversation.
	l, err := c.relaunch(start, t.Doc, wt, note, fmt.Sprintf(restackPrompt, t.ID(), p.Name, t.Path, wt), true)
	if err != nil {
		return err
	}
	l.record(t.Doc)
	if onto != "" {
		t.Set("restack_sent", onto)
	}
	if retargetTo != "" {
		t.Set("retarget_sent", retargetTo)
	}
	t.SetStatus(Working)
	t.Touch()
	t.Log(fmt.Sprintf("restack: fresh worker %s on %s — %s", l.job, restackModel, note))
	c.commit("restack " + p.Name + "/" + t.ID())
	fmt.Printf("restack sent to %s → fresh worker %s on %s\n", t.ID(), l.job, restackModel)
	return nil
}

// stallAfter is how long a worker's transcript must have been quiet before its
// task counts as stalled. A session the user is talking to with crew attach ends
// its turn after every reply, and a nudge would end the attach, so a short quiet
// spell doesn't count.
const stallAfter = 8 * time.Minute

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
	if _, err := c.followup(t, followupWith{stalled: true}); err != nil {
		return err
	}
	// Last, so NudgedWithoutProgress finds it only once the worker was resumed.
	t.Log(nudgedAt + head)
	c.commit("nudge " + p.Name + "/" + t.ID())
	return nil
}
