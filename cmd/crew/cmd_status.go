package main

import (
	"cmp"
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
	s.spikes = map[string][]Task{}
	for _, t := range s.tasks(name) {
		if d := t.Get("informs"); d != "" {
			s.spikes[d] = append(s.spikes[d], t)
		}
	}
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
	if line := toReview(s.tasks(name)); line != "" {
		fmt.Println("  " + line)
	}
}

// toReview lists the PRs waiting on the user, cheapest look first, so the ones
// that need only the evidence can be cleared, and what's stacked on them freed,
// before those that need the diff read: "to review: #41 low, #38 medium, #35 high".
func toReview(tasks []Task) string {
	var open []Task
	for _, t := range tasks {
		if t.Status() == PROpen {
			open = append(open, t)
		}
	}
	if len(open) == 0 {
		return ""
	}
	slices.SortStableFunc(open, func(a, b Task) int {
		return cmp.Or(cmp.Compare(a.Risk().order(), b.Risk().order()), cmp.Compare(atoiOr0(a.PRNumber()), atoiOr0(b.PRNumber())))
	})
	items := make([]string, len(open))
	for i, t := range open {
		ref := "#" + t.PRNumber()
		if t.PR() == "" {
			ref = taskNumber(t)
		}
		items[i] = ref + " " + t.Risk().Label()
		if t.AwaitsSecurityReview() {
			items[i] += " (no security review yet)"
		}
	}
	return "to review: " + strings.Join(items, ", ")
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

func (s *statusView) goalRow(g Goal, tasks []Task) {
	lead := s.agents.jobState(g.LeadJob())
	if lead != "" {
		lead = "lead: " + lead
	}
	total, merged := 0, 0 // of the tasks that make a PR; a spike's is never merged
	for _, t := range s.goalTasks(g) {
		if rules, err := t.rules(); err == nil && !rules.pr {
			continue
		}
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
		default: // merged, done or abandoned, listed with --done
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
