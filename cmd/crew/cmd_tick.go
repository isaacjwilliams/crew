package main

// crew tick is one pass over every project, doing what needs no decision: sync
// with GitHub, record new PR comments, send review rounds and restacks to idle
// workers, mark tasks that wait on the user, clean up merged ones, and dispatch
// and close goals. The crew-tick service runs it every few minutes; the lead runs
// it too. It prints one line per thing it did.
//
// Every step is a crew command run as a process of its own, so one that refuses
// skips that step and not the rest. The stand-down and the lease check are gates,
// not steps: they decide whether the tick does anything, so they run here.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	// A machine that ran the crew, and finds on origin that another one does now,
	// steps back and does nothing else.
	if holder, taken := c.leaseTakenElsewhere(); taken {
		return c.standDown(holder)
	}
	if err := c.requireHost(); err != nil {
		return err
	}
	if _, err := c.ghLogin(); err != nil {
		return err
	}
	if c.step(nil, "sync", "--quiet", "--all") != 0 {
		warn("sync failed; carrying on with what the ledger has")
	}
	as := c.agents()
	for _, name := range c.projectsInScope(true) {
		p, err := c.loadProject(name)
		if err != nil {
			report(err)
			warn("%s: skipped", name)
			continue
		}
		idx := c.branches(name)
		for _, t := range c.tasks(name) {
			c.tickTask(p, idx, t, as)
		}
		for _, g := range c.goals(name) {
			c.tickGoal(g, as)
		}
	}
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

// tickTask is one task's share of a tick.
func (c *Crew) tickTask(p *Project, idx Branches, t Task, as Agents) {
	id, st := t.ID(), t.Status()
	if st == Ready && c.goalRunning(t) {
		// The goal lead marked it ready, which is the go-ahead. A dispatch that has to
		// wait for a cap or a dependency (exit 4) is tried again on the next tick.
		var errs strings.Builder
		switch c.step(&errs, "dispatch", t.Path) {
		case 0:
			fmt.Printf("%s: dispatched for goal %s\n", id, t.Goal())
		case exitNotYet: // held by a cap or a dependency; the next tick tries again
		default:
			warn("%s: dispatch failed: %s", id, strings.TrimRight(errs.String(), "\n"))
		}
		return
	}
	if st == Merged {
		// A cleanup that fails (uncommitted changes, say) warns, and is tried again next tick.
		if t.Worktree() != "" && c.step(nil, "cleanup", t.Path) == 0 {
			fmt.Printf("%s: merged; worktree cleaned up\n", id)
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
				fmt.Printf("%s: review round %s recorded\n", id, now)
			}
			if n := t.OpenQuestions(); n != asked {
				fmt.Printf("%s: %d comment(s) from collaborators, for you\n", id, n-asked)
			}
		}
	}
	job := t.WorkerJob()
	if movedHere(t, as) {
		var errs strings.Builder
		if c.step(&errs, "followup", t.Path, "--fresh", "--note", "This task moved to "+c.Host+"; your earlier session is gone. Rebuild context from the task file and the branch.") == 0 {
			fmt.Printf("%s: its worker and worktree aren't on this machine; a fresh worker picks it up\n", id)
		} else {
			warn("%s: couldn't start a fresh worker for it here: %s", id, strings.TrimRight(errs.String(), "\n"))
		}
		return
	}
	// The rest needs the worker free (see busy); a busy one is left for a later
	// tick. It does at most one thing, in this order: send an unsent review round,
	// send a restack, or mark the task as waiting on the user. Nothing goes out
	// twice: last_round_sent and restack_sent record what was sent.
	rounds, sent := atoiOr0(t.Get("review_rounds")), atoiOr0(t.Get("last_round_sent"))
	if as.jobBusy(job) {
		if rounds > sent {
			fmt.Printf("%s: round %d waits for its worker (%s)\n", id, rounds, as.jobState(job))
		}
		return
	}
	if rounds > sent {
		if c.step(nil, "followup", t.Path) == 0 {
			fmt.Printf("%s: round %d sent to its worker\n", id, rounds)
		}
		return
	}
	reason := idx.restackReason(t, p.Repo())
	if st == PROpen && reason != "" && !strings.HasSuffix(reason, " abandoned") && !idx.parentNeedsRestack(t, p.Repo()) {
		if target := idx.restackTarget(t, p.Repo()); target != "" && t.Get("restack_sent") != target {
			if c.step(nil, "restack", t.Path) == 0 {
				fmt.Printf("%s: restack sent to its worker (%s)\n", id, reason)
			}
			return
		}
	}
	if waiting := t.OpenQuestions(); st != NeedsInput && waiting > 0 {
		if c.step(nil, "set", t.Path, "status", string(NeedsInput)) == 0 {
			fmt.Printf("%s: waiting on you (%d open question(s))\n", id, waiting)
		}
	}
}

// goalRunning: the task belongs to a running goal.
func (c *Crew) goalRunning(t Task) bool {
	g := t.Goal()
	return g != "" && (Goal{Doc{c.path("projects", t.Project(), "goals", g+".md")}}).Status() == GoalRunning
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
	return (st == Working || st == AddressingFeedback) && wt != "" && !isDir(wt) && !as.jobListed(t.WorkerJob())
}

// tickGoal is one running goal's share of a tick: pass the goal lead its input
// once it's free, or close the goal once its tasks are all merged or abandoned.
func (c *Crew) tickGoal(g Goal, as Agents) {
	if g.Status() != GoalRunning {
		return
	}
	id, job := g.ID(), g.LeadJob()
	if c.goalHasInput(g) {
		if as.jobBusy(job) {
			fmt.Printf("goal %s: input waits for its goal lead (%s)\n", id, as.jobState(job))
		} else if c.step(nil, "goal", "followup", g.Path) == 0 {
			fmt.Printf("goal %s: input sent to its goal lead\n", id)
		}
	} else if c.goalFinished(g) && !as.jobBusy(job) {
		if c.step(nil, "goal", "done", g.Path) == 0 {
			fmt.Printf("goal %s: done, every task merged or abandoned\n", id)
		}
	}
}

// goalHasInput: the user's notes, or a worker's questions, wait for the goal lead.
func (c *Crew) goalHasInput(g Goal) bool {
	if atoiOr0(g.Get("unsent_notes")) != 0 {
		return true
	}
	for _, t := range c.goalTasks(g) {
		if _, ok := unroutedQuestions(t); ok {
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
func (c *Crew) standDown(holder string) error {
	if holder == "" {
		holder = "no machine"
	}
	fmt.Printf("crew now runs on %s; standing down here\n", holder)
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
