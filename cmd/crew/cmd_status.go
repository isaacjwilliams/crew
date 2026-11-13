package main

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// crew status [--done] [--all|--project P]: the tasks of one project (P, or the
// default project) or of all of them, with their workers' state. Stacked tasks
// are listed under their parent, and a goal's tasks under the goal, grouped by
// where they stand. Each task not yet dispatched says what it waits for. Showing
// one project, it ends with what's waiting on you in the others.
func cmdStatus(c *Crew, args []string) error {
	s := statusView{Crew: c}
	all, proj := false, ""
	for len(args) > 0 {
		switch {
		case args[0] == "--done":
			s.doneToo = true
		case args[0] == "--all":
			all = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		default:
			return usage("crew status [--done] [--all|--project P]")
		}
		args = args[1:]
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	projects := c.projects(scope)
	if holder := c.holder(); holder != c.Host {
		// Another machine runs the crew and pushes the ledger; catch up with it. No
		// crew command changes the ledger here, so this needs no lock.
		gitOK(c.Home, "pull", "-q", "--ff-only")
		holder = c.holder()
		if holder == "" {
			holder = "no machine"
		}
		fmt.Println("read-only: crew runs on " + holder)
		projects = c.projects(scope)
	}
	if len(projects) == 0 {
		fmt.Println("No projects registered. Run 'crew project add' in a repo.")
		return nil
	}
	s.agents = c.agents()
	s.running = c.runningWorkers(s.agents)
	workers, err := workerSlots(c.Settings, s.running)
	header := "workers running: " + slotsText(workers, err)
	if s.noProjectDispatches() {
		header += ": full, nothing dispatches"
	}
	fmt.Println(header)
	for _, p := range projects {
		fmt.Println()
		s.project(p)
	}
	if scope != "" {
		s.elsewhere(scope)
	}
	return nil
}

// noProjectDispatches: every project, shown or not, has reached its MAX_WORKERS.
// A project can set its own, higher or lower than the crew's.
func (s *statusView) noProjectDispatches() bool {
	for _, p := range s.projects("") {
		if w, err := workerSlots(s.settingsOf(p), s.running); err != nil || !w.full() {
			return false
		}
	}
	return true
}

// settingsOf is a project's settings, or the crew's when they can't be read.
func (s *statusView) settingsOf(project string) Settings {
	if ps, err := s.projectSettings(project); err == nil {
		return ps
	}
	return s.Settings
}

// elsewhere lists the other projects with something waiting on the user: tasks
// with open questions, PRs to review, and memory suggestions.
func (s *statusView) elsewhere(shown string) {
	var lines []string
	for _, p := range s.projects("") {
		if p == shown {
			continue
		}
		questions, prs := 0, 0
		for _, t := range s.tasks(p) {
			if t.Status().Finished() {
				continue
			}
			if t.OpenQuestions() > 0 {
				questions++
			}
			if t.Status() == PROpen {
				prs++
			}
		}
		var waiting []string
		if questions > 0 {
			waiting = append(waiting, fmt.Sprintf("%d task(s) with open questions", questions))
		}
		if prs > 0 {
			waiting = append(waiting, fmt.Sprintf("%d PR(s) to review", prs))
		}
		if n := s.memorySuggestions(p); n > 0 {
			waiting = append(waiting, fmt.Sprintf("%d memory suggestion(s)", n))
		}
		if len(waiting) > 0 {
			lines = append(lines, fmt.Sprintf("  %s: %s", p, strings.Join(waiting, ", ")))
		}
	}
	if len(lines) > 0 {
		fmt.Println()
		fmt.Println("waiting on you in other projects  (crew status --all)")
		fmt.Println(strings.Join(lines, "\n"))
	}
}

type statusView struct {
	*Crew
	doneToo  bool
	agents   Agents
	running  int // the workers running across the crew, counted once
	branches Branches
	repo     string            // the project's clone here, for restack reasons; empty when there's none
	settings Settings          // the project's, or the crew's when they can't be read (settingsOf)
	kids     map[string][]Task // a task's file → the tasks stacked on it
	deps     map[string]depsOf // a task's file → the tasks it waits on
	caps     map[string]capOf  // a goal's ID → its tasks in flight against its max_in_flight
}

// depsOf and capOf keep what status has worked out for a task or a goal, error
// and all, so it's worked out once.
type depsOf struct {
	tasks []Task
	err   error
}

type capOf struct {
	slots
	err error
}

func (s *statusView) shown(status string) bool {
	return s.doneToo || (status != "merged" && status != "done" && status != "abandoned")
}

// project lists one project's goals and tasks under its name, which says when
// it's paused or full.
func (s *statusView) project(name string) {
	s.branches = s.Crew.branches(name)
	s.repo, s.settings = "", s.settingsOf(name)
	if p, err := s.loadProject(name); err == nil {
		s.repo = p.Repo()
	}
	// A paused project says so. A project that can't dispatch, or whose
	// MAX_WORKERS isn't a number, says so with its own limit, which may not be
	// the crew's.
	line := name
	if p, err := s.ledgerProject(name); err == nil && p.Paused() {
		line += "  paused (crew resume --project " + name + ")"
	}
	if w, err := workerSlots(s.settings, s.running); err != nil {
		line += "  " + err.Error()
	} else if w.full() {
		line += fmt.Sprintf("  workers full (MAX_WORKERS %d here)", w.limit)
	}
	fmt.Println(line)
	s.kids, s.deps, s.caps = map[string][]Task{}, map[string]depsOf{}, map[string]capOf{}
	var goals []Goal
	shownGoals := map[string]bool{}
	for _, g := range s.goals(name) {
		if s.shown(g.Get("status")) {
			goals = append(goals, g)
			shownGoals[g.ID()] = true
		}
	}
	var roots []Task
	goalRoots := map[string][]Task{}
	for _, t := range s.tasks(name) {
		if !s.shown(t.Get("status")) {
			continue
		}
		if p, ok := s.branches.liveParent(t); ok && !p.Status().Finished() && !leavesStack(t, p) {
			s.kids[p.Path] = append(s.kids[p.Path], t)
		} else if g := t.Goal(); g != "" && shownGoals[g] {
			goalRoots[g] = append(goalRoots[g], t)
		} else {
			roots = append(roots, t)
		}
	}
	if len(roots)+len(goals) == 0 {
		fmt.Println("  (no active tasks)")
	}
	for _, g := range goals {
		s.goalRow(g, goalRoots[g.ID()])
	}
	drafts := 0
	for _, t := range roots {
		if n := s.foldedDrafts(t); n > 0 {
			drafts += n
		} else {
			s.taskRow(t, "")
		}
	}
	if drafts > 0 {
		fmt.Printf("  %d draft(s)  (crew list --project %s --status draft)\n", drafts, name)
	}
	open := 0
	for _, f := range s.files("proposals", name) {
		if (Doc{f}).Get("status") == "open" {
			open++
		}
	}
	if open > 0 {
		fmt.Printf("  open proposals: %d  (crew proposals --project %s)\n", open, name)
	}
	if n := s.memorySuggestions(name); n > 0 {
		fmt.Printf("  memory suggestions: %d  (projects/%s/memory-suggestions.md)\n", n, name)
	}
}

// idWidth is the ID column's width, with whatever goes before the ID: a goal
// group's indent and number column, then 32 characters of the ID itself.
const idWidth = len(groupIndent) + 3 + 32

// padID cuts or pads an ID column to idWidth. It's padded by hand: the tree
// marker is one character wide but three bytes long.
func padID(id string) string {
	id = cut(id, idWidth)
	return id + strings.Repeat(" ", idWidth-utf8.RuneCountInString(id))
}

// leavesStack: a ready task whose parent is in flight can dispatch as soon as the
// parent's branch is pushed. Listed under its parent, it would hide that, so it's
// listed on its own instead (in its goal's next or waiting group), and its row
// says "on <parent>".
func leavesStack(t, parent Task) bool {
	return t.Status() == Ready && parent.Status().InFlight()
}

// foldedDrafts counts a draft outside a goal, with the drafts stacked on it, when
// none of them has an open question: they're counted in one line, not listed.
// It's 0 for a task that's listed.
func (s *statusView) foldedDrafts(t Task) int {
	if t.Status() != Draft || t.OpenQuestions() > 0 {
		return 0
	}
	n := 1
	for _, k := range s.kids[t.Path] {
		m := s.foldedDrafts(k)
		if m == 0 {
			return 0
		}
		n += m
	}
	return n
}

func (s *statusView) goalRow(g Goal, tasks []Task) {
	lead := s.agents.jobState(g.LeadJob())
	if lead != "" {
		lead = "lead: " + lead
	}
	total, merged := 0, 0
	for _, t := range s.goalTasks(g) {
		total++
		if t.Status() == Merged {
			merged++
		}
	}
	inFlight, err := s.goalCap(g)
	full := ""
	if err != nil {
		full = "  " + err.Error()
	} else if inFlight.full() {
		full = ": full"
	}
	fmt.Printf("  %s %-20s %-26s %d of %d task(s) merged, %s in flight%s\n", padID("goal "+g.ID()), g.Get("status"), lead, merged, total, slotsText(inFlight, err), full)
	s.goalGroups(g, tasks)
}

// slotsText is "used/limit", with ? for a limit set to something that isn't a
// whole number.
func slotsText(sl slots, err error) string {
	if err != nil {
		return fmt.Sprintf("%d/?", sl.used)
	}
	return fmt.Sprintf("%d/%d", sl.used, sl.limit)
}

const (
	nextShown  = 5 // how many of a running goal's next tasks are listed
	afterNamed = 4 // how many tasks "after" names before it only counts them
)

// groupIndent is how far a goal's group headings, and its rows, are indented.
const groupIndent = "    "

// goalGroups lists a goal's tasks in groups, by where they stand: in flight; the
// ready ones that can go, in the order crew tick dispatches them (numbered, for a
// running goal); the ready ones waiting on other tasks; the drafts parked on
// questions for the user; and the drafts still being planned.
func (s *statusView) goalGroups(g Goal, tasks []Task) {
	var inFlight, next, waiting, parked, planning, finished []Task
	for _, t := range tasks {
		switch st := t.Status(); {
		case st.InFlight():
			inFlight = append(inFlight, t)
		case st == Ready && s.waits(t):
			waiting = append(waiting, t)
		case st == Ready:
			next = append(next, t)
		case st == Draft && t.OpenQuestions() > 0:
			parked = append(parked, t)
		case st == Draft:
			planning = append(planning, t)
		default: // merged or abandoned, listed with --done
			finished = append(finished, t)
		}
	}
	sortForDispatch(next)
	sortForDispatch(waiting)
	if len(inFlight) > 0 {
		fmt.Println(groupIndent + "in flight")
		for _, t := range inFlight {
			s.taskRow(t, groupIndent)
		}
	}
	if g.Status() == GoalRunning {
		s.group("next, in dispatch order", next, true)
	} else {
		s.group("ready", next, false)
	}
	s.group("waiting on other tasks", waiting, false)
	s.group("parked", parked, false)
	s.group("planning", planning, false)
	s.group("finished", finished, false)
}

// group lists tasks under a heading, if there are any. A
// numbered group lists only the first few, and any later one with an open
// question in its stack, since that waits on the user.
func (s *statusView) group(heading string, ts []Task, numbered bool) {
	if len(ts) == 0 {
		return
	}
	fmt.Println(groupIndent + heading)
	more := 0
	for i, t := range ts {
		if numbered && i >= nextShown && !s.stackAsks(t) {
			more++
			continue
		}
		// Rows have a number column three wide before the ID, blank when the
		// group isn't numbered.
		number := ""
		if numbered {
			number = fmt.Sprint(i + 1)
		}
		s.taskRow(t, fmt.Sprintf("%s%-3s", groupIndent, number))
	}
	if more > 0 {
		fmt.Printf("  %s   … (%d more)\n", groupIndent, more)
	}
}

// stackAsks: the task, or one stacked on it, has open questions.
func (s *statusView) stackAsks(t Task) bool {
	if t.OpenQuestions() > 0 {
		return true
	}
	return slices.ContainsFunc(s.kids[t.Path], s.stackAsks)
}

// waitsOn are the tasks this one waits on, as crew dispatch sees them. A task's
// row and its group both ask, so the answer is kept.
func (s *statusView) waitsOn(t Task) ([]Task, error) {
	if d, ok := s.deps[t.Path]; ok {
		return d.tasks, d.err
	}
	deps, err := s.unmergedDeps(s.branches, t)
	s.deps[t.Path] = depsOf{deps, err}
	return deps, err
}

// waits: the task waits on other tasks, or on a dependency it can't name.
func (s *statusView) waits(t Task) bool {
	deps, err := s.waitsOn(t)
	return err != nil || len(deps) > 0
}

// goalCap is the goal's tasks in flight against its max_in_flight. The goal's
// row and its tasks' rows can ask, so the answer is kept.
func (s *statusView) goalCap(g Goal) (slots, error) {
	if c, ok := s.caps[g.ID()]; ok {
		return c.slots, c.err
	}
	sl, err := s.goalSlots(s.settings, g)
	s.caps[g.ID()] = capOf{sl, err}
	return sl, err
}

// goalDispatches: crew tick dispatches the task for its goal, now or once the
// goal starts.
func (s *statusView) goalDispatches(t Task) bool {
	g, ok := s.taskGoal(t)
	return ok && (g.Status() == GoalRunning || g.Status() == GoalDraft)
}

// taskRow prints one task, then the tasks stacked on it. The indent is what goes
// before the ID: spaces, a number, a └.
func (s *statusView) taskRow(t Task, indent string) {
	st := t.Status()
	worker := s.agents.jobState(t.WorkerJob())
	if worker != "" {
		worker = "worker: " + worker
	}
	pr := "-"
	if t.PR() != "" {
		pr = "#" + t.PRNumber()
	}
	fmt.Printf("  %s %-20s %-26s %-6s %s\n", padID(indent+t.ID()), st, worker, pr, strings.Join(s.notes(t), "  "))
	for _, k := range s.kids[t.Path] {
		s.taskRow(k, strings.Repeat(" ", utf8.RuneCountInString(indent))+"└ ")
	}
}

// notes are what a task's row says after its PR: its PR's state, and what it
// waits for or on.
func (s *statusView) notes(t Task) []string {
	st := t.Status()
	var notes []string
	if prs := t.Get("pr_state"); prs != "" {
		notes = append(notes, "("+prs+")")
	}
	if st.InFlight() && s.repo != "" {
		if reason := s.branches.restackReason(t, s.repo); reason != "" {
			notes = append(notes, "needs restack: "+reason)
		}
	}
	if q := t.OpenQuestions(); q > 0 {
		notes = append(notes, fmt.Sprintf("%d open question(s)", q))
	}
	if t.Get("interrupted") != "" {
		notes = append(notes, "interrupted by a pause")
	}
	if st == Ready && t.Goal() != "" {
		// Only a goal's ready tasks are dispatched in priority order.
		if n, err := t.Priority(); err != nil {
			notes = append(notes, "priority ?")
		} else if n != 0 {
			notes = append(notes, fmt.Sprintf("priority %d", n))
		}
	}
	if p, ok := s.branches.liveParent(t); ok && leavesStack(t, p) {
		notes = append(notes, "on "+taskNumber(p))
	}
	if !st.InFlight() && !st.Finished() {
		if deps, err := s.waitsOn(t); err != nil {
			notes = append(notes, err.Error())
		} else if len(deps) > 0 {
			notes = append(notes, after(deps))
		}
	}
	// crew tick dispatches only a goal's ready tasks, so someone has to run crew
	// dispatch for the rest. A goal that's done or abandoned isn't listed without
	// --done, but its cap still holds its tasks, so their rows say what its line
	// would.
	if st == Ready && !s.goalDispatches(t) {
		if g, ok := s.taskGoal(t); ok && !s.shown(g.Get("status")) {
			if c, err := s.goalCap(g); err != nil {
				notes = append(notes, err.Error())
			} else if c.full() {
				notes = append(notes, "goal "+g.ID()+" full")
			}
		}
		notes = append(notes, "dispatch by hand")
	}
	return notes
}

// after names the tasks a task waits on by number, or counts them when there are
// more than a few. An abandoned one will never merge, so it's always named.
func after(deps []Task) string {
	var ns, abandoned []string
	for _, d := range deps {
		n := taskNumber(d)
		if d.Status() == Abandoned {
			abandoned = append(abandoned, n)
			n += " (abandoned)"
		}
		ns = append(ns, n)
	}
	if len(deps) <= afterNamed {
		return "after " + strings.Join(ns, ", ")
	}
	note := fmt.Sprintf("after %d tasks", len(deps))
	if len(abandoned) > 0 {
		note += ", " + strings.Join(abandoned, ", ") + " abandoned"
	}
	return note
}

// taskNumber is the NNN a task's ID starts with, or its whole ID when it has none.
func taskNumber(t Task) string {
	if _, ok := fileNumber(t.Path); !ok {
		return t.ID()
	}
	n, _, _ := strings.Cut(t.ID(), "-")
	return n
}
