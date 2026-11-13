package main

// Pausing a project's work: crew pause stops it without losing any, and crew
// resume lets it go on. While a project is paused, no command starts a session
// for it, and crew tick sends its workers and goal leads nothing.
//
// A paused project has a mark in the ledger, projects/<p>/paused, so the pause is
// committed, pushed, and holds on whichever machine runs the crew. Sessions that
// are mid-turn when it's written are told to wrap up by their pause hook (crew
// pause --hook), and stopped if they're still mid-turn when the wait runs out.
// Their files get an interrupted key, which crew followup and crew goal followup
// deliver once the project is resumed.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// pausedByUpgrade is the by of the mark crew upgrade writes, and lifts once
// it's done. Any other pause replaces it, so the user's pause outlasts the
// upgrade.
const pausedByUpgrade = "crew upgrade"

// carryOn is what a worker or goal lead is told when it's followed up after a
// pause cut its turn short.
const carryOn = "crew was paused while you were working. Carry on from where your Log says you stopped."

// pauseMark is the file that says the project is paused. It has only front
// matter: by (what paused it), at and host.
func (p *Project) pauseMark() Doc { return Doc{filepath.Join(p.Dir, "paused")} }

// Paused: the project's work is paused, and no session may start for it.
func (p *Project) Paused() bool { return isFile(p.pauseMark().Path) }

// PausedBy is what paused the project: crew pause, crew pause --force or crew
// upgrade. It's empty when the project isn't paused.
func (p *Project) PausedBy() string { return p.pauseMark().Get("by") }

// refuseIfPaused holds back a command that would start a session for p while
// it's paused. Session-starting commands call it before their first side effect,
// and again under the ledger lock.
func refuseIfPaused(p *Project) error {
	if p.Paused() {
		return notYet("%s is paused; 'crew resume --project %s' lets its work go on", p.Name, p.Name)
	}
	return nil
}

// crew pause [--project P|--all] [--force] | crew pause --hook
func cmdPause(c *Crew, args []string) error {
	if len(args) == 1 && args[0] == "--hook" {
		c.pauseHook(os.Stdin)
		return nil
	}
	const use = "crew pause [--project P|--all] [--force]"
	proj, all, force := "", false, false
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all, args = true, args[1:]
		case args[0] == "--force":
			force, args = true, args[1:]
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[2:]
		default:
			return usage(use)
		}
	}
	if err := need("claude"); err != nil {
		return err
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	projects, err := c.ledgerProjects(scope)
	if err != nil {
		return err
	}
	by := "crew pause"
	if force {
		by += " --force"
	}
	if err := c.pause(projects, by, force); err != nil {
		return err
	}
	fmt.Println("'crew resume " + scopeFlag(scope) + "' lets the work go on")
	return nil
}

// crew resume [--project P|--all]
func cmdResume(c *Crew, args []string) error {
	proj, all := "", false
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all, args = true, args[1:]
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[2:]
		default:
			return usage("crew resume [--project P|--all]")
		}
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	projects, err := c.ledgerProjects(scope)
	if err != nil {
		return err
	}
	return c.resume(projects)
}

// scopeFlag is the flag that names scope on a command line: --project P, or
// --all for every project.
func scopeFlag(scope string) string {
	if scope == "" {
		return "--all"
	}
	return "--project " + scope
}

// ledgerProjects are the projects in scope, cloned on this machine or not: a
// pause needs only their ledger files. It fails before anything changes if one
// can't be read.
func (c *Crew) ledgerProjects(scope string) ([]*Project, error) {
	var ps []*Project
	for _, name := range c.projects(scope) {
		p, err := c.ledgerProject(name)
		if err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, nil
}

// A session is the background session a ledger file records: a task's worker
// or a goal's goal lead. (Not a session ID, like worker_session: its job is
// what crew stops and asks claude agents about.)
type session struct {
	Doc              // the task or goal file
	project *Project // the project it works on
	job     string   // its job in claude agents
	label   string   // the task's ID, or "goal <ID>"
	id      string   // its session ID (worker_session or lead_session)
	dir     string   // where it works: the task's worktree, or the goal lead's checkout
	// finished: its task is merged or abandoned, or its goal isn't running, so
	// nothing will carry its work on. Its process may still be running.
	finished bool
}

// sessions are the sessions recorded for the projects' tasks and goals,
// finished or not.
func (c *Crew) sessions(projects []*Project) []session {
	var ss []session
	for _, p := range projects {
		for _, t := range c.tasks(p.Name) {
			if t.WorkerJob() != "" {
				ss = append(ss, session{t.Doc, p, t.WorkerJob(), t.ID(), t.Get("worker_session"), t.Worktree(), t.Status().Finished()})
			}
		}
		for _, g := range c.goals(p.Name) {
			if g.LeadJob() != "" {
				ss = append(ss, session{g.Doc, p, g.LeadJob(), "goal " + g.ID(), g.Get("lead_session"), g.Get("checkout"), g.Status() != GoalRunning})
			}
		}
	}
	return ss
}

// busySessions are the projects' sessions that are mid-turn, or holding a
// prompt open (see busy). It fails if claude agents can't say.
func (c *Crew) busySessions(projects []*Project) ([]session, error) {
	as, err := c.listAgents()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(c.sessions(projects), func(s session) bool { return !as.jobBusy(s.job) }), nil
}

func labels(ss []session) string {
	var ls []string
	for _, s := range ss {
		ls = append(ls, s.label)
	}
	return strings.Join(ls, ", ")
}

// pause marks the projects paused, by by (markPaused). Then it gives the
// sessions that were mid-turn up to WRAP_UP_SECONDS to end their turn, and
// stops the ones that haven't. With force, it stops every open session of the
// projects at once instead, busy or idle, and ends what closed ones left
// running in the background. It holds the ledger lock only while it
// writes, never while it waits, so workers can log where they stopped.
func (c *Crew) pause(projects []*Project, by string, force bool) error {
	wait, err := intSetting(c.Settings, "WRAP_UP_SECONDS")
	if err != nil {
		return err
	}
	busy, err := c.markPaused(projects, by)
	if err != nil {
		return err
	}
	if force {
		as, err := c.listAgents()
		if err != nil {
			return err
		}
		ss := c.sessions(projects)
		open := slices.DeleteFunc(slices.Clone(ss), func(s session) bool { return !as.jobOpen(s.job) })
		if err := c.stopSessions(open, "stopped by "+by); err != nil {
			return err
		}
		return c.endBackgroundWork(slices.DeleteFunc(ss, func(s session) bool { return as.jobOpen(s.job) }))
	}
	if len(busy) == 0 {
		return nil
	}
	fmt.Printf("waiting up to %s for %d session(s) to wrap up: %s\n", plainDuration(wait), len(busy), labels(busy))
	left := c.waitForTurnsToEnd(busy, time.Duration(wait)*time.Second)
	return c.stopSessions(left, "stopped by "+by+" before it ended its turn")
}

// markPaused writes the projects' marks and commits them, and returns the
// sessions mid-turn. It sets interrupted on their files, unless their work is
// finished, before any wait, so a pause cut short still leaves the record. It
// changes nothing if claude agents can't say which sessions are mid-turn.
func (c *Crew) markPaused(projects []*Project, by string) ([]session, error) {
	if err := c.lock(); err != nil {
		return nil, err
	}
	defer c.unlock()
	// Under the lock, so no followup starts a turn between this and the mark.
	busy, err := c.busySessions(projects)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
		// crew upgrade's pause gives way to any other: the upgrade lifts only a
		// mark it wrote, so it won't lift the user's.
		if p.Paused() && p.PausedBy() != pausedByUpgrade {
			fmt.Printf("%s was already paused\n", p.Name)
			continue
		}
		mark := fmt.Sprintf("---\nby: %s\nat: %s\nhost: %s\n---\n", by, now(), c.Host)
		if err := os.WriteFile(p.pauseMark().Path, []byte(mark), 0o644); err != nil {
			return nil, err
		}
		fmt.Printf("paused %s\n", p.Name)
	}
	for _, s := range busy {
		if !s.finished {
			s.Set("interrupted", now())
			s.Touch()
		}
	}
	c.commit("pause " + strings.Join(names, " "))
	return busy, nil
}

// waitForTurnsToEnd waits up to wait for the sessions to end their turn, saying
// so as each one does, and returns those still mid-turn. It stops waiting for a
// session whose project is resumed meanwhile. A poll claude agents fails counts
// for nothing: those sessions are still mid-turn as far as it knows.
func (c *Crew) waitForTurnsToEnd(busy []session, wait time.Duration) []session {
	deadline := time.Now().Add(wait)
	for len(busy) > 0 && time.Now().Before(deadline) {
		time.Sleep(min(5*time.Second, time.Until(deadline)))
		as, err := c.listAgents()
		if err != nil {
			continue
		}
		busy = slices.DeleteFunc(busy, func(s session) bool {
			switch {
			case !s.project.Paused():
				fmt.Printf("%s was resumed; %s goes on\n", s.project.Name, s.label)
			case !as.jobBusy(s.job):
				fmt.Printf("%s ended its turn\n", s.label)
			default:
				return false
			}
			return true
		})
	}
	return busy
}

// stopSessions stops the sessions whose project is still paused, and logs why
// on each file, with interrupted set unless the work is finished. It holds the
// ledger lock throughout, so no resume or followup comes between the check and
// the stop. Their conversations and worktrees are kept, but what they left
// running in the background there (a crew codex review, a dev server) is ended,
// as crew cleanup does: a stopped session's background shells would otherwise
// run on. It fails if any of them didn't stop.
func (c *Crew) stopSessions(ss []session, why string) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	ss = slices.DeleteFunc(ss, func(s session) bool { return !s.project.Paused() })
	if len(ss) == 0 {
		return nil
	}
	var jobs []string
	for _, s := range ss {
		jobs = append(jobs, s.job)
	}
	open := c.stopJobs(jobs)
	var stopped, failed []session
	var dirs []string
	for _, s := range ss {
		if slices.Contains(open, s.job) {
			failed = append(failed, s)
			continue
		}
		stopped = append(stopped, s)
		dirs = append(dirs, s.dir)
		// An idle session too: it may have been waiting on a background
		// review, which the stop ends.
		if !s.finished && s.Get("interrupted") == "" {
			s.Set("interrupted", now())
		}
		s.Log(why)
		s.Touch()
	}
	stopProcessesIn(dirs...)
	if len(stopped) > 0 {
		c.commit("pause: stopped " + labels(stopped))
		fmt.Printf("stopped %d session(s): %s\n", len(stopped), labels(stopped))
	}
	if len(failed) > 0 {
		return fail("%d session(s) didn't stop: %s; stop them in claude agents", len(failed), labels(failed))
	}
	return nil
}

// endBackgroundWork ends what the sessions, already closed, left running in the
// background in their worktrees, if their project is still paused: a session's
// background shells outlive its process, so a crew codex review can run on after
// the session that started it is gone.
func (c *Crew) endBackgroundWork(closed []session) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	var dirs []string
	for _, s := range closed {
		if s.project.Paused() {
			dirs = append(dirs, s.dir)
		}
	}
	stopProcessesIn(dirs...)
	return nil
}

// plainDuration is a number of seconds as a person says it: "3 minutes", or
// "45 seconds" when it isn't whole minutes.
func plainDuration(secs int) string {
	n, unit := secs, "second"
	if secs >= 60 && secs%60 == 0 {
		n, unit = secs/60, "minute"
	}
	if n != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s", n, unit)
}

// resume removes the projects' pause marks. It starts no session: crew tick
// follows up the work a pause interrupted.
func (c *Crew) resume(projects []*Project) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	var resumed []string
	for _, p := range projects {
		if !p.Paused() {
			fmt.Printf("%s isn't paused\n", p.Name)
			continue
		}
		if err := os.Remove(p.pauseMark().Path); err != nil {
			return err
		}
		resumed = append(resumed, p.Name)
		fmt.Printf("resumed %s\n", p.Name)
	}
	if len(resumed) == 0 {
		return nil
	}
	c.commit("resume " + strings.Join(resumed, " "))
	fmt.Println("the next crew tick follows up the work the pause interrupted")
	return nil
}

// crew pause --hook, the PostToolUse hook of every worker and goal lead (added
// by addPauseHook, and in no settings file): once the session's project is
// paused, Claude Code hands the session the text it prints, telling it to wrap
// up, and the hook marks the session's work interrupted (markInterrupted). Until
// then it reads only the environment and the mark, as it runs after every tool
// call.
func (c *Crew) pauseHook(in io.Reader) {
	name := os.Getenv("CREW_PROJECT")
	if name == "" || name == allProjects {
		return
	}
	p, err := c.ledgerProject(name)
	if err != nil || !p.Paused() {
		return
	}
	var input struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(in).Decode(&input)
	c.markInterrupted(p, input.SessionID)
	type output struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	b, _ := json.Marshal(map[string]output{"hookSpecificOutput": {"PostToolUse", fmt.Sprintf(
		"crew is pausing project %s. Stop at the next safe point. Write in your task file's Log (a goal lead: the goal file's Log) "+
			"where you are and what comes next, so a fresh session can pick up from there. Leave uncommitted work in your worktree: "+
			"it's kept. Start nothing new, and end your turn now.", name)}})
	fmt.Println(string(b))
}

// markInterrupted sets interrupted on the file of the session the pause hook
// tells to stop, once, unless its work is finished. The pause marks the
// sessions mid-turn when it starts, but one that was idle then can start a turn
// later, when a background review it waited on reports back.
func (c *Crew) markInterrupted(p *Project, id string) {
	if id == "" {
		return
	}
	for _, s := range c.sessions([]*Project{p}) {
		if s.id != id || s.finished || s.Get("interrupted") != "" {
			continue
		}
		if c.lock() != nil {
			return
		}
		defer c.unlock()
		s.Set("interrupted", now())
		s.Touch()
		c.commit("pause: " + s.label + " interrupted")
		return
	}
}
