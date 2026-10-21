package main

// A goal is a batch of items the user hands the crew to work through without
// them. Its goal lead, a background session, writes and plan-reviews a brief for
// each item and marks it ready, answers its workers' questions, and parks what it
// shouldn't decide alone: a draft task with open questions for the user. crew
// tick dispatches the ready tasks and resumes the goal lead when there's input
// for it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func cmdGoal(c *Crew, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "new":
		return c.goalNew(args)
	case "start", "note", "followup", "done", "abandon", "path":
		if len(args) == 0 {
			return usage(map[string]string{"start": "crew goal start GOAL", "note": "crew goal note GOAL TEXT", "followup": "crew goal followup GOAL",
				"done": "crew goal done GOAL", "abandon": "crew goal abandon GOAL", "path": "crew goal path GOAL"}[sub])
		}
		g, err := c.resolveGoal(args[0])
		if err != nil {
			return err
		}
		switch sub {
		case "start":
			return c.goalStart(g)
		case "note":
			return c.goalNote(g, strings.Join(args[1:], " "))
		case "followup":
			return c.goalFollowup(g)
		case "done":
			return c.goalDone(g)
		case "abandon":
			return c.goalAbandon(g)
		}
		fmt.Println(g.Path)
		return nil
	}
	return usage("crew goal new|start|note|followup|done|abandon|path … (see 'crew help')")
}

// crew goal new "<title>" [--project P]
func (c *Crew) goalNew(args []string) error {
	var title, proj string
	for len(args) > 0 {
		if args[0] == "--project" && len(args) > 1 {
			proj, args = args[1], args[2:]
			continue
		}
		if title == "" {
			title = args[0]
		} else {
			title += " " + args[0]
		}
		args = args[1:]
	}
	if title == "" {
		return usage(`crew goal new "<title>" [--project P]`)
	}
	if proj == "" {
		var err error
		if proj, err = c.currentProject(); err != nil {
			return err
		}
	}
	p, err := c.loadProject(proj)
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	n := c.nextNumber("goals", proj)
	s := strings.TrimSuffix(cut(slugify(title), 40), "-")
	if s == "" {
		s = fmt.Sprintf("goal-%d", n)
	}
	id := fmt.Sprintf("%03d-%s", n, s)
	g := Goal{Doc{filepath.Join(p.Dir, "goals", id+".md")}}
	if err := c.fromTemplate("goal.md", g.Path, map[string]string{
		"ID": id, "TITLE": strings.ReplaceAll(title, `"`, "'"), "PROJECT": proj, "NOW": now(),
	}); err != nil {
		return err
	}
	g.Log("created")
	c.commit("new goal " + proj + "/" + id)
	fmt.Println(g.Path)
	return nil
}

// goalCheckout is the goal lead's working directory: a detached checkout of
// origin/DEFAULT_BRANCH, which crew refreshes before each of its turns.
func goalCheckout(p *Project, g Goal) string {
	return filepath.Join(p.WorkRoot(), p.Name, "_goal-"+g.ID())
}

// launchGoalLead starts or resumes a goal lead. It's the only place a goal
// lead's command line is built. A goal lead runs with the lead's settings,
// lead/.claude/settings.json, all but their "agent": the lead's session takes its
// agent from that file, a goal lead from --agent. Not the lead's
// settings.local.json: a goal lead works in its own checkout.
func (c *Crew) launchGoalLead(p *Project) launcher {
	return func(d Doc, wt, prompt string, resume bool) (launch, error) {
		settings := ""
		if !resume {
			b, err := os.ReadFile(c.path("lead/.claude/settings.json"))
			var v any
			if err == nil {
				v, err = parseJSON(b)
			}
			o, ok := v.(*object)
			if err != nil || !ok {
				return launch{}, fail("can't read %s", c.path("lead/.claude/settings.json"))
			}
			o.del("agent")
			settings = encodeJSON(o, false)
		}
		return c.launchSession(d, "lead", wt, prompt, resume, "--agent", "goal-lead",
			"--permission-mode", p.Settings["LEAD_PERMISSION_MODE"], "--settings", settings)
	}
}

// crew goal start GOAL: starts its goal lead, which plans every item.
func (c *Crew) goalStart(g Goal) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	p, err := c.loadProject(g.Project())
	if err != nil {
		return err
	}
	if st := g.Status(); st != GoalDraft {
		return fail("goal %s is '%s'; give a running goal input with 'crew goal note'", g.ID(), st)
	}
	if g.Section("## Goal") == "" {
		return fail("goal %s has no Goal yet: write the user's own words into its ## Goal section", g.ID())
	}
	co := goalCheckout(p, g)
	if err := p.detachedCheckout(co); err != nil {
		return err
	}
	name := cut("crew-"+p.Name+"-goal-"+g.ID(), 80)
	if err := c.lock(); err != nil {
		return err
	}
	g.Set("checkout", co)
	g.Set("lead_name", name)
	l, err := c.launchGoalLead(p)(g.Doc, co, fmt.Sprintf("You are the goal lead for goal %s in project %s.\nGoal file: %s\nYour working directory is a checkout of origin/%s; read the code there.\nRead the goal file completely, then follow your workflow for a new goal.",
		g.ID(), p.Name, g.Path, p.DefaultBranch()), false)
	if err == errNotStarted {
		return fail("could not start the goal lead (see output above)")
	} else if err != nil {
		return err
	}
	g.Set("lead_job", l.job)
	g.Set("lead_session", l.session)
	g.Set("status", string(GoalRunning))
	g.Touch()
	g.Log(fmt.Sprintf("started: goal lead %s (session %s) in %s", l.job, orUnknown(l.session), co))
	c.commit("start goal " + p.Name + "/" + g.ID())
	fmt.Printf("started goal %s → goal lead %s (%s)\n", g.ID(), l.job, name)
	fmt.Printf("  watch: claude attach %s   |   claude agents\n", l.job)
	return nil
}

// crew goal note GOAL TEXT: records the user's words under the goal's Decisions
// and passes them to the goal lead, now if it's free, or through crew tick once
// it is. A note to a done goal reopens it, for work the user adds later ("pick up
// the new cards").
func (c *Crew) goalNote(g Goal, text string) error {
	if text == "" {
		return usage("crew goal note GOAL TEXT")
	}
	st := g.Status()
	if st == GoalAbandoned {
		return fail("goal %s is abandoned", g.ID())
	}
	if err := c.lock(); err != nil {
		return err
	}
	g.AppendToSection("## Decisions", "- "+time.Now().Format("2006-01-02 15:04")+" — from the user: "+text+"\n")
	if st == GoalDraft {
		g.Touch()
		g.Log("note recorded")
		c.commit("note goal " + g.ID())
		fmt.Println("recorded under Decisions; the goal lead reads it when the goal starts")
		return nil
	}
	if st == GoalDone {
		g.Set("status", string(GoalRunning))
		g.Log("reopened by a note")
	}
	g.Set("unsent_notes", fmt.Sprint(atoiOr0(g.Get("unsent_notes"))+1))
	g.Touch()
	g.Log("note recorded")
	c.commit("note goal " + g.ID())
	c.unlock()
	if err := c.goalFollowup(g); err != nil {
		report(err)
		fmt.Println("recorded under Decisions; crew tick passes it to the goal lead once it's free")
	}
	return nil
}

// workerQuestions are a task's open questions from its worker: the unchecked
// items under Open questions, leaving out the collaborators' PR comments crew
// feedback records there ("@login …"), which are for the user.
func workerQuestions(t Task) string {
	var qs []string
	for _, l := range strings.Split(t.Section("## Open questions"), "\n") {
		if strings.HasPrefix(l, "- [ ]") && !strings.HasPrefix(l, "- [ ] @") {
			qs = append(qs, l)
		}
	}
	return strings.Join(qs, "\n")
}

// unroutedQuestions: a task of the goal in flight whose worker has questions the
// goal lead hasn't been sent. questions_routed is a checksum of the questions last
// sent, so one the goal lead left for the user isn't sent again, but a new one
// is. It returns the checksum to record once they're sent.
func unroutedQuestions(t Task) (string, bool) {
	if !t.Status().InFlight() {
		return "", false
	}
	q := workerQuestions(t)
	if q == "" {
		return "", false
	}
	sum := fmt.Sprint(cksum([]byte(q + "\n")))
	return sum, sum != t.Get("questions_routed")
}

// crew goal followup GOAL: resumes the goal lead with what's waiting for it: the
// user's unsent notes, and questions its tasks' workers have asked. Exits 3 if
// there's nothing.
func (c *Crew) goalFollowup(g Goal) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	p, err := c.loadProject(g.Project())
	if err != nil {
		return err
	}
	if st := g.Status(); st != GoalRunning {
		return fail("goal %s is '%s', not running", g.ID(), st)
	}
	input := ""
	if n := atoiOr0(g.Get("unsent_notes")); n != 0 {
		input += fmt.Sprintf("\n- %d new note(s) from the user under Decisions in the goal file", n)
	}
	type asked struct {
		task Task
		sum  string // of the questions, for questions_routed
	}
	var routed []asked
	for _, t := range c.goalTasks(g) {
		if sum, ok := unroutedQuestions(t); ok {
			input += fmt.Sprintf("\n- %s: its worker has asked something (task file: %s)", t.ID(), t.Path)
			routed = append(routed, asked{t, sum})
		}
	}
	if input == "" {
		fmt.Printf("nothing new for goal %s\n", g.ID())
		return exitQuietly(3)
	}
	as, job := c.agents(), g.LeadJob()
	if as.busy(job) {
		return fail("goal lead %s is busy (%s); crew tick passes this on once it's free", job, as.state(job))
	}
	co := goalCheckout(p, g)
	if err := p.detachedCheckout(co); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	g.Set("checkout", co)
	l, err := c.relaunch(c.launchGoalLead(p), "lead", g.Doc, co,
		fmt.Sprintf("New input for goal %s (goal file: %s):%s\nFollow your workflow for new input.", g.ID(), g.Path, input),
		fmt.Sprintf("You are the goal lead for goal %s in project %s, which is already under way.\nGoal file: %s\nYour working directory is a checkout of origin/%s; read the code there.\nRead the goal file completely, and the files of the tasks under its Items, before acting.",
			g.ID(), p.Name, g.Path, p.DefaultBranch()), false)
	if err != nil {
		return err
	}
	g.Set("lead_job", l.job)
	if l.session != "" {
		g.Set("lead_session", l.session)
	}
	g.Set("unsent_notes", "0")
	g.Touch()
	g.Log(fmt.Sprintf("followup (%s): goal lead %s", l.mode, l.job))
	for _, r := range routed {
		r.task.Set("questions_routed", r.sum)
		r.task.Log("open questions sent to the goal lead")
	}
	c.commit("followup goal " + p.Name + "/" + g.ID())
	fmt.Printf("goal %s: input sent to its goal lead (%s)\n", g.ID(), l.mode)
	return nil
}

// goalFinished: the goal has tasks, and every one of them is merged or abandoned.
func (c *Crew) goalFinished(g Goal) bool {
	ts := c.goalTasks(g)
	for _, t := range ts {
		if !t.Status().Finished() {
			return false
		}
	}
	return len(ts) > 0
}

// crew goal done GOAL: closes a goal whose tasks are all merged or abandoned.
func (c *Crew) goalDone(g Goal) error {
	p, err := c.loadProject(g.Project())
	if err != nil {
		return err
	}
	if st := g.Status(); st != GoalRunning {
		return fail("goal %s is '%s', not running", g.ID(), st)
	}
	if !c.goalFinished(g) {
		return fail("goal %s has tasks that aren't merged or abandoned yet", g.ID())
	}
	return c.retireGoal(p, g, GoalDone)
}

// crew goal abandon GOAL: stops the goal lead; its tasks stay as they are.
func (c *Crew) goalAbandon(g Goal) error {
	p, err := c.loadProject(g.Project())
	if err != nil {
		return err
	}
	if g.Status() == GoalAbandoned {
		return fail("goal %s is already abandoned", g.ID())
	}
	return c.retireGoal(p, g, GoalAbandoned)
}

// retireGoal stops and removes the goal lead, what it left running in its
// checkout, and the checkout. The goal's tasks stay as they are.
func (c *Crew) retireGoal(p *Project, g Goal, st GoalStatus) error {
	job, co := g.LeadJob(), g.Get("checkout")
	if !c.removeSession(job) {
		return fail("goal lead %s is still running; stop it in claude agents, then try again", job)
	}
	if co != "" && isDir(co) {
		if err := stopProcessesIn(co); err != nil {
			warn("%v", err)
		}
		if gitDo(p.Repo(), "worktree", "remove", "--force", co) != nil {
			warn("couldn't remove %s", co)
		}
	}
	if err := c.lock(); err != nil {
		return err
	}
	g.Set("checkout", "")
	g.Set("status", string(st))
	g.Touch()
	g.Log(fmt.Sprintf("status → %s; goal lead and its checkout removed", st))
	c.commit(fmt.Sprintf("%s goal %s/%s", st, p.Name, g.ID()))
	fmt.Printf("goal %s: %s\n", g.ID(), st)
	return nil
}

// cksum is the POSIX cksum of b, the CRC crew has always recorded in
// questions_routed.
func cksum(b []byte) uint32 {
	var crc uint32
	add := func(c byte) {
		crc ^= uint32(c) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	for _, c := range b {
		add(c)
	}
	for n := len(b); n > 0; n >>= 8 {
		add(byte(n))
	}
	return ^crc
}
