package main

// crew tick is one pass over every project, doing what needs no decision: sync
// with GitHub, record new PR comments and red CI on PRs handed to the user, send
// review rounds and restacks to idle workers, nudge workers whose turn ended
// mid-task, mark tasks that wait on the user, clean up those that delivered
// (merged, or a spike done), pass goal leads their input and close finished
// goals. Last, it dispatches the ready tasks of running goals, highest priority
// first, and those outside a goal whose go-ahead is met.
// The crew-tick service runs it every few minutes; the lead runs it too. It
// prints one line per thing it did, and one per thing it held back and why, and
// keeps them as news for the leads (news.go).
//
// Every step is a crew command run as a process of its own, so one that refuses
// skips that step and not the rest. The stand-down and the lease check are gates,
// not steps: they decide whether the tick does anything, so they run here.

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func cmdTick(c *Crew, args []string) error {
	for _, tool := range []string{"gh", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	if len(args) > 0 {
		return usage("crew tick")
	}
	lock, err := flock(c.path(".crew-tick.lock"), 0)
	if errors.Is(err, errLocked) {
		fmt.Println("another crew tick is running")
		return nil
	} else if err != nil {
		return err
	}
	defer lock.Close()
	news := &tickNews{c: c}
	// A machine that ran the crew, and finds on origin that another one does now,
	// steps back and does nothing else.
	if holder, taken := c.leaseTakenElsewhere(); taken {
		return c.standDown(holder, news)
	}
	if err := c.requireHost(); err != nil {
		return err
	}
	if _, err := c.ghLogin(); err != nil {
		return err
	}
	// Sync reports its own problems, a project it can't load among them, so its exit
	// code is left unread: the rest of the tick goes by the ledger as sync left it.
	c.step(nil, "sync", "--quiet", "--all")
	// So an edit to a project's mcp.json reaches the leads within a tick. It reports
	// its own problems, which stop nothing else.
	c.step(nil, "mcp", "sync")
	as := c.agents()
	for _, name := range c.projects("") {
		p, err := c.loadProject(name)
		if err != nil {
			continue // sync has said why, and that it skipped it
		}
		// A paused project still gets the steps that only record (cleanup,
		// feedback, ci-fix, catch-up, needs-input, goal done); those that would start a
		// session are held back, saying so, until crew resume.
		paused, idx := p.Paused(), c.branches(name)
		for _, t := range c.tasks(name) {
			c.tickTask(p, idx, t, as, paused, news)
		}
		for _, g := range c.goals(name) {
			c.tickGoal(p, g, as, paused, news)
		}
	}
	c.dispatchReady(news)
	news.save()
	c.pruneNews()
	// A catch-all for background pushes that failed, or that a crash cut short.
	if gitOK(c.Home, "remote", "get-url", "origin") && !c.pushNow(os.Stderr) {
		warn("couldn't push the ledger to origin; the next tick tries again")
	}
	return nil
}

// step runs a crew command in a process of its own and returns its exit code. Its
// output is thrown away; its errors are shown, or collected in errs when it's set.
func (c *Crew) step(errs *strings.Builder, args ...string) int {
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	if errs != nil {
		cmd.Stderr = errs
	}
	if err := cmd.Run(); err != nil {
		var x *exec.ExitError
		if errors.As(err, &x) {
			return x.ExitCode()
		}
		return 1
	}
	return 0
}

// tickCleanup cleans up a delivered task's worktree. When cleanup leaves it
// (uncommitted changes, say), cleanup warns why on its last stderr line. That
// line is held back for the leads, who hear it once, and cleanup is tried again
// next tick.
func (c *Crew) tickCleanup(p *Project, t Task, news *tickNews) {
	var errs strings.Builder
	switch c.step(&errs, "cleanup", t.Path) {
	case 0:
		fmt.Fprint(os.Stderr, errs.String())
		news.did(p.Name, "%s: %s; worktree cleaned up", t.ID(), t.Status())
	case exitNotYet:
		// The reason is held back, which prints it; what came before it is shown here.
		rest, why := splitRefusal(errs.String())
		fmt.Fprint(os.Stderr, rest)
		news.heldBack(p.Name, "%s", why)
	default:
		fmt.Fprint(os.Stderr, errs.String())
	}
}

// splitRefusal splits what a step wrote to stderr into everything before its
// last line, and that line without the "crew: " that crew puts before its
// messages.
func splitRefusal(stderr string) (rest, why string) {
	stderr = strings.TrimRight(stderr, "\n")
	cut := strings.LastIndex(stderr, "\n") + 1
	rest = stderr[:cut]
	return rest, strings.TrimPrefix(stderr[cut:], "crew: ")
}

// tickTask is one task's share of a tick, for a task already under way: its
// cleanup once it has delivered (Status.Delivered), or what its worker is sent
// while it's in flight, which is nothing while its project is paused.
// Dispatching ready tasks is dispatchReady's.
func (c *Crew) tickTask(p *Project, idx Branches, t Task, as Agents, paused bool, news *tickNews) {
	id, st := t.ID(), t.Status()
	if st.Delivered() {
		if t.Worktree() != "" {
			c.tickCleanup(p, t, news)
		}
		return
	}
	if !st.InFlight() {
		return
	}
	if t.PR() != "" && prMayHaveNewComments(t) {
		// crew feedback reports what it recorded only in the task file, so compare
		// before and after.
		rounds, asked := t.Get("review_rounds"), t.OpenQuestions()
		if c.step(nil, "feedback", t.Path) == 0 {
			if now := t.Get("review_rounds"); now != rounds {
				news.did(p.Name, "%s: review round %s recorded", id, now)
			}
			if n := t.OpenQuestions(); n != asked {
				news.did(p.Name, "%s: %d comment(s) from collaborators, for you", id, n-asked)
			}
		}
	}
	// crew sync's ci_state is only a filter: crew ci-fix reads the checks again,
	// and decides. It exits 0 when it recorded a round or a question.
	if st == PROpen && t.Get("ci_state") == string(ciFailing) {
		rounds := t.Get("review_rounds")
		if c.step(nil, "ci-fix", t.Path) == 0 {
			if now := t.Get("review_rounds"); now != rounds {
				failures := t.CIFailures()
				news.did(p.Name, "%s: CI round %s recorded: red on %s", id, now, failures[len(failures)-1])
			} else {
				news.did(p.Name, "%s: CI still red after %d CI rounds the branch caused, for you", id, t.CIFixes())
			}
		}
	}
	// Likewise mergeable: crew catch-up reads the PR again, and decides. It exits
	// 0 when it recorded a round. A stacked task's conflicts are a restack's.
	if _, stacked := idx.parentOf(t); st == PROpen && t.Get("mergeable") == conflicting && !stacked {
		if c.step(nil, "catch-up", t.Path) == 0 {
			news.did(p.Name, "%s: catch-up round %s recorded: conflicts with %s", id, t.Get("review_rounds"), t.Base())
		}
	}
	job := t.WorkerJob()
	if movedHere(t, as) {
		// A paused project's task goes on below: it can still be marked as
		// waiting on the user.
		if paused {
			news.heldBack(p.Name, "%s: a fresh worker for it here waits: %s is paused", id, p.Name)
		} else {
			var errs strings.Builder
			if c.step(&errs, "followup", t.Path, "--fresh", "--note", "This task moved to "+c.Host+"; your earlier session is gone. Rebuild context from the task file and the branch.") == 0 {
				news.did(p.Name, "%s: its worker and worktree aren't on this machine; a fresh worker picks it up", id)
			} else {
				warn("%s: couldn't start a fresh worker for it here: %s", id, strings.TrimRight(errs.String(), "\n"))
			}
			return
		}
	}
	// The rest needs the worker free (see busy); a busy one is left for a later
	// tick. It does at most one thing, in this order: send what the worker hasn't
	// had (forWorker), or mark the task as waiting on the user. A nudge that
	// asks the lead instead does both: the question it adds is what the task
	// then waits on.
	send, held := c.forWorker(p, idx, t, as.find(job))
	busy := as.jobBusy(job)
	switch {
	case held != "":
		news.heldBack(p.Name, "%s: %s", id, held)
	case send == nil: // nothing to send
	case busy:
		news.heldBack(p.Name, "%s: %s waiting for its worker (%s)", id, send.what, as.jobState(job))
	case paused:
		news.heldBack(p.Name, "%s: %s waits: %s is paused", id, send.what, p.Name)
	default:
		// The command runs in a process of its own; a question it adds for the
		// lead lands in the task file, which t reads afresh. A command that fails
		// counts as tried, so the task isn't marked waiting on the strength of it.
		asked := t.OpenQuestions()
		if c.step(nil, append([]string{send.command}, t.Path)...) != 0 {
			return
		}
		if t.OpenQuestions() == asked {
			news.did(p.Name, "%s: %s", id, send.sent)
			return
		}
		news.did(p.Name, "%s: %s", id, send.asked)
	}
	if busy { // it may still be answering its open questions
		return
	}
	// Read the status again: the task may have been merged or abandoned while
	// the steps above waited on GitHub.
	if st, waiting := t.Status(), t.OpenQuestions(); st.InFlight() && st != NeedsInput && waiting > 0 {
		if c.step(nil, "set", t.Path, "status", string(NeedsInput)) == 0 {
			news.did(p.Name, "%s: waiting on you (%d open question(s))", id, waiting)
		}
	}
}

// workerSend is what crew tick resumes a task's idle worker with: the crew
// command that sends it, what it is, and what tick says once it's sent. A nudge
// can ask the lead instead of resuming the worker, and asked is what tick says
// then.
type workerSend struct{ command, what, sent, asked string }

// forWorker is the first of these that a task's worker, a (nil when claude
// agents doesn't list it), hasn't had: unsent review rounds, word that a pause
// cut its last turn short, a nudge when its turn ended mid-task, or a restack (a
// rebase, a retarget or both). Nothing goes out twice: last_round_sent,
// interrupted, restack_sent and retarget_sent record what was sent, and the
// nudge's Log line records a nudge. A restack that's due but can't go yet is
// held back instead, and held says why.
func (c *Crew) forWorker(p *Project, idx Branches, t Task, a *Agent) (send *workerSend, held string) {
	if atoiOr0(t.Get("review_rounds")) > atoiOr0(t.Get("last_round_sent")) {
		unsent := unsentRounds(t)
		return &workerSend{command: "followup", what: unsent, sent: unsent + " sent to its worker"}, ""
	}
	if t.Get("interrupted") != "" {
		return &workerSend{command: "followup", what: "word that the pause is over", sent: "its worker carries on after the pause"}, ""
	}
	if stalled(t, a) {
		return &workerSend{command: "nudge", what: "a nudge", sent: "its worker's turn ended mid-task; nudged it",
			asked: "its worker stalled and can't be nudged, for the lead"}, ""
	}
	repo, st := p.Repo(), t.Status()
	restack := func(reason string) (*workerSend, string) {
		if st != PROpen {
			if !t.HandedOver() {
				return nil, fmt.Sprintf("restack (%s) isn't crew's to send: %s", reason, movesBeforeHandOver)
			}
			return nil, fmt.Sprintf("restack (%s) waits until the task is %s; it's %s", reason, PROpen, st)
		}
		return &workerSend{command: "restack", what: "restack (" + reason + ")", sent: "restack sent to its worker (" + reason + ")"}, ""
	}
	// As in crew restack: a retarget goes with a rebase when there is one, and on
	// its own only when the branch needs no rebase (onBase). So a rebase that has
	// to wait (for its parent's, say) holds its retarget back with it.
	if reason := idx.restackReason(t, repo); reason != "" {
		parent, _ := idx.parentOf(t)
		target := idx.restackTarget(t, repo)
		switch {
		case parent.Status() == Abandoned: // before sent already: a restack sent onto it can't help now
			return nil, fmt.Sprintf("restack held back: %s was abandoned; give it another base with 'crew set %s base <branch>'", parent.ID(), t.ID())
		case target != "" && t.Get("restack_sent") == target:
			return nil, "" // sent already
		case idx.parentNeedsRestack(t, repo):
			return nil, fmt.Sprintf("restack (%s) waits for %s's own restack", reason, parent.ID())
		case target == "":
			return nil, fmt.Sprintf("restack (%s) waits: origin has no branch %s", reason, idx.effectiveBase(t))
		}
		return restack(reason)
	}
	if reason := idx.retargetReason(t, repo); reason != "" && idx.onBase(t, repo) && t.Get("retarget_sent") != t.Base() {
		return restack(reason)
	}
	return nil, ""
}

// dispatchReady dispatches the ready tasks tick dispatches (tickDispatches),
// across every project but the paused ones, highest priority first and then in
// ledger order. It's the tick's last step, so workers made busy by this tick's
// review rounds and restacks count against MAX_WORKERS. A task waiting on other
// tasks is skipped without running crew dispatch, which saves a process; one
// that dispatch holds back (exit 4), by a cap say, is left for the next tick.
// Either way the next task is tried: dependencies are per task, and caps per
// goal. Each task is checked again just before its dispatch, since an earlier
// one can take minutes (worktree hooks) while the ledger is unlocked. That's
// time enough for someone to dispatch it by hand, abandon its goal or withdraw
// its go-ahead.
func (c *Crew) dispatchReady(news *tickNews) {
	var ready []Task
	for _, name := range c.projects("") {
		// No clone here, say: sync named it, and the tick skipped it above.
		p, err := c.loadProject(name)
		if err != nil {
			continue
		}
		for _, t := range c.tasks(name) {
			switch {
			case !c.tickDispatches(t):
			case p.Paused():
				news.heldBack(name, "%s: dispatch waits: %s is paused", t.ID(), name)
			default:
				ready = append(ready, t)
			}
		}
	}
	sortForDispatch(ready)
	idx := c.branches("")
	for _, t := range ready {
		if !c.tickDispatches(t) {
			continue
		}
		project := taskProjectDir(t)
		// A dependency that doesn't name exactly one task is crew dispatch's to report.
		if deps, err := c.pendingDeps(idx, t); err == nil && len(deps) > 0 {
			news.heldBack(project, "%s: dispatch waits: it comes %s", t.ID(), after(deps))
			continue
		}
		// Dispatching ends the go-ahead, so say what it was first. tickDispatches
		// has read it without an error.
		because := "for goal " + t.Goal()
		if g, _ := t.GoAhead(); g != nil {
			because = fmt.Sprintf("on its go-ahead: %s reached %s", g.After.ID(), g.Status)
		}
		var errs strings.Builder
		switch c.step(&errs, "dispatch", t.Path, "--tick") {
		case 0:
			news.did(project, "%s: dispatched %s", t.ID(), because)
		case exitNotYet: // held back, by a cap say; the next tick tries again
			_, why := splitRefusal(errs.String())
			news.heldBack(project, "%s: dispatch held back: %s", t.ID(), why)
		default:
			warn("%s: dispatch failed: %s", t.ID(), strings.TrimRight(errs.String(), "\n"))
		}
	}
}

// sortForDispatch puts tasks in the order crew tick tries them: highest priority
// first, the order they came in breaking ties. It warns of a priority that isn't
// a whole number, and treats it as 0.
func sortForDispatch(ts []Task) {
	priority := map[string]int{}
	for _, t := range ts {
		n, err := t.Priority()
		if err != nil {
			warn("%v; treating it as 0", err)
		}
		priority[t.Path] = n
	}
	slices.SortStableFunc(ts, func(a, b Task) int { return cmp.Compare(priority[b.Path], priority[a.Path]) })
}

// tickDispatches: the task is ready, and crew tick dispatches it: its goal is
// running (the goal lead marked it ready, which is the go-ahead), or outside a
// goal, the user's go-ahead is met.
func (c *Crew) tickDispatches(t Task) bool {
	if t.Status() != Ready {
		return false
	}
	if g, ok := c.taskGoal(t); ok {
		return g.Status() == GoalRunning
	}
	g, err := t.GoAhead()
	return err == nil && g != nil && g.Met()
}

// prMayHaveNewComments: GitHub moves a PR's updatedAt (recorded as pr_updated by
// crew sync) whenever a comment is posted or a review submitted. A PR that hasn't
// changed since crew feedback last read it has no new comments, and reading them
// costs three API calls. Without either time, assume it may have some.
func prMayHaveNewComments(t Task) bool {
	updated, since := t.Get("pr_updated"), t.Get("feedback_since")
	return updated == "" || since == "" || updated > since
}

// movedHere: a task in progress that moved here from another machine. crew records
// a worktree for it that isn't here, and this machine's claude agents doesn't know
// its worker, whose session stayed behind. The fresh worker crew tick then starts
// is recorded, and listed by the next tick, so this holds once.
func movedHere(t Task, as Agents) bool {
	st, wt := t.Status(), t.Worktree()
	return st.WorkersTurn() && wt != "" && !isDir(wt) && !as.jobListed(t.WorkerJob())
}

// tickGoal is one running goal's share of a tick: pass the goal lead its input
// once it's free and its project isn't paused, or close the goal once its tasks
// are all finished.
func (c *Crew) tickGoal(p *Project, g Goal, as Agents, paused bool, news *tickNews) {
	if g.Status() != GoalRunning {
		return
	}
	id, job := g.ID(), g.LeadJob()
	if c.goalHasInput(g) {
		switch {
		case as.jobBusy(job):
			news.heldBack(p.Name, "goal %s: input waits for its goal lead (%s)", id, as.jobState(job))
		case paused:
			news.heldBack(p.Name, "goal %s: input waits: %s is paused", id, p.Name)
		case c.step(nil, "goal", "followup", g.Path) == 0:
			news.did(p.Name, "goal %s: input sent to its goal lead", id)
		}
	} else if c.goalFinished(g) && !as.jobBusy(job) {
		if c.step(nil, "goal", "done", g.Path) == 0 {
			news.did(p.Name, "goal %s: done, every task finished", id)
		}
	}
}

// goalHasInput: the user's notes, a worker's questions, a spike's report, or word
// that a pause cut its last turn short wait for the goal lead.
func (c *Crew) goalHasInput(g Goal) bool {
	if atoiOr0(g.Get("unsent_notes")) != 0 || g.Get("interrupted") != "" {
		return true
	}
	for _, t := range c.goalTasks(g) {
		if _, ok := unroutedQuestions(t); ok || unsentReport(t) {
			return true
		}
	}
	return false
}

// leaseTakenElsewhere: this machine ran the crew, and origin, freshly fetched,
// names another holder now, or none. A machine that never held the lease has
// nothing to stand down from, and one that can't read origin's branch doesn't
// know it lost it.
func (c *Crew) leaseTakenElsewhere() (holder string, taken bool) {
	if !gitOK(c.Home, "fetch", "-q", "origin") {
		return "", false
	}
	holder, ok := c.originHolder()
	if !ok || holder == c.Host {
		return "", false
	}
	return holder, c.holder() == c.Host || exists(c.leaseLost())
}

// standDown: another machine has claimed the lease, so this one steps back: its
// sessions stopped, what origin lacks kept on lost/<host>-<time>, its home reset
// to origin, and its services off. Run with the tick lock held. The services go
// off last, so a stand-down that fails on the way leaves the tick on to try
// again.
func (c *Crew) standDown(holder string, news *tickNews) error {
	if holder == "" {
		holder = "no machine"
	}
	news.did("", "crew now runs on %s; standing down here", holder)
	news.save() // this machine holds nothing back now
	c.stopCrewJobs()
	if err := c.takeLock(); err != nil {
		return err
	}
	defer c.unlock()
	dirty, _ := git(c.Home, "status", "--porcelain")
	unpushed, _ := git(c.Home, "rev-list", "@{u}..@")
	if dirty != "" || unpushed != "" {
		lost := "lost/" + c.Host + "-" + time.Now().Format("20060102-150405")
		if err := c.saveLostWork(lost); err != nil {
			return err
		}
		fmt.Println("kept what origin didn't have on " + lost)
	}
	if err := gitDo(c.Home, "reset", "-q", "--hard", "@{u}"); err != nil {
		return err
	}
	os.Remove(c.leaseLost())
	fmt.Println("stood down: crew sessions stopped, home reset to origin, services disabled")
	disableServices()
	return nil
}

// saveLostWork keeps what origin doesn't have, ledger commits and uncommitted
// changes alike, on a new branch, before standDown resets the home. The working
// tree is committed through a scratch index, so the home's own index and files
// stay as they are.
func (c *Crew) saveLostWork(branch string) error {
	dir, err := os.MkdirTemp("", "crew-index")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	scratch := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", c.Home}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(dir, "index"))
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := scratch("read-tree", "HEAD"); err != nil {
		return err
	}
	if _, err := scratch("add", "-A"); err != nil {
		return err
	}
	tree, err := scratch("write-tree")
	if err != nil {
		return err
	}
	tip := "HEAD"
	if head, _ := git(c.Home, "rev-parse", "HEAD^{tree}"); tree != head {
		if tip, err = git(c.Home, "commit-tree", tree, "-p", "HEAD", "-m", "Uncommitted when "+c.Host+" stood down"); err != nil {
			return err
		}
	}
	return gitDo(c.Home, "branch", "-q", branch, tip)
}
