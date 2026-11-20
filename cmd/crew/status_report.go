package main

// What crew status shows, worked out into a report before anything is printed,
// so every view of it (the plain text, cmd_status.go) groups the same tasks the
// same way.

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// statusReport is everything crew status shows.
type statusReport struct {
	ReadOnlyOn   string // the machine that runs the crew, when it isn't this one
	Workers      slots  // the workers running across the crew against MAX_WORKERS
	WorkersErr   error
	NoneDispatch bool // every project has reached its MAX_WORKERS
	Projects     []projectReport
}

// projectReport is one project. Every project has its Name and Waiting; the rest
// is set only for a project in scope, which status shows in full.
type projectReport struct {
	Name          string
	Waiting       waitingOnUser
	InScope       bool
	Paused        bool
	Workers       slots // against this project's MAX_WORKERS
	WorkersErr    error
	Goals         []goalReport
	Rows          []taskRow // the tasks outside a goal, folded drafts included
	OpenProposals int
	ToReview      []Task // the PRs waiting on the user, cheapest look first (toReview)
}

// waitingOnUser counts what waits on the user in a project: tasks with open
// questions, PRs to review, and memory suggestions.
type waitingOnUser struct {
	Questions, PRs, MemorySuggestions int
}

// waitingOf is one task's share of what waits on the user: 1 in Questions when
// it has any open question, 1 in PRs when its PR is waiting, so a project's sum
// counts tasks. A finished task's questions and PR wait on no one.
func waitingOf(t Task) waitingOnUser {
	var w waitingOnUser
	if t.Status().Finished() {
		return w
	}
	if t.OpenQuestions() > 0 {
		w.Questions = 1
	}
	if t.Status() == PROpen {
		w.PRs = 1
	}
	return w
}

// waitsOnUser: the task has an open question or a PR to review.
func waitsOnUser(t Task) bool {
	return waitingOf(t) != waitingOnUser{}
}

// String words what's waiting: "5 task(s) with open questions, 2 PR(s) to
// review, 2 memory suggestion(s)". It's empty when nothing is.
func (w waitingOnUser) String() string {
	var parts []string
	if w.Questions > 0 {
		parts = append(parts, fmt.Sprintf("%d task(s) with open questions", w.Questions))
	}
	if w.PRs > 0 {
		parts = append(parts, fmt.Sprintf("%d PR(s) to review", w.PRs))
	}
	if w.MemorySuggestions > 0 {
		parts = append(parts, fmt.Sprintf("%d memory suggestion(s)", w.MemorySuggestions))
	}
	return strings.Join(parts, ", ")
}

// goalReport is one goal and its tasks, in groups.
type goalReport struct {
	Goal          Goal
	Lead          Agent // the goal lead, as claude agents lists it; zero when it isn't
	Merged, Total int   // of the goal's tasks that make a PR
	InFlight      slots // against the goal's max_in_flight
	CapErr        error
	Groups        []taskGroup // only the groups that have tasks, in groupKind order
}

// taskGroup is a goal's tasks that stand in the same place.
type taskGroup struct {
	Kind groupKind
	Rows []taskRow
}

// groupKind is where a goal's tasks stand, one kind per group.
type groupKind int

const (
	groupInFlight groupKind = iota
	groupNext               // a running goal's ready tasks, numbered in dispatch order
	groupReady              // the ready tasks of a goal that isn't running
	groupWaiting            // ready tasks waiting on other tasks
	groupParked             // drafts with open questions
	groupPlanning           // the other drafts
	groupFinished           // merged, done or abandoned, listed with --done
)

// taskRow is one task, with the tasks stacked on it.
type taskRow struct {
	Task   Task
	Number int   // its place in a numbered group, 0 for none
	Worker Agent // its worker, as claude agents lists it; zero when it isn't
	// Merge says whether a pr-open task's PR can merge now; nil for any other task.
	Merge *mergeCheck
	Notes []string
	Kids  []taskRow // the tasks stacked on it
	// Folded: a draft outside a goal that the plain text counts, with its whole
	// stack, rather than lists (folds).
	Folded bool
}

// stackAsks: the task, or one stacked on it, has open questions.
func (r taskRow) stackAsks() bool {
	if r.Task.OpenQuestions() > 0 {
		return true
	}
	return slices.ContainsFunc(r.Kids, taskRow.stackAsks)
}

// buildReport works out what crew status shows: the projects in scope in full,
// and only what waits on the user in the others. The projects in scope come
// first, in the order given, then the rest. doneToo adds finished goals and
// tasks.
func buildReport(c *Crew, inScope []string, doneToo bool) statusReport {
	var r statusReport
	if holder := c.holder(); holder != c.Host {
		r.ReadOnlyOn = cmp.Or(holder, "no machine")
	}
	if len(inScope) == 0 {
		return r
	}
	s := statusView{Crew: c, doneToo: doneToo, readOnly: r.ReadOnlyOn != ""}
	s.agents = c.agents()
	s.running = c.runningWorkers(s.agents)
	r.Workers, r.WorkersErr = workerSlots(c.Settings, s.running)
	r.NoneDispatch = s.noProjectDispatches()
	s.prs = s.readPRs(inScope)
	for _, p := range inScope {
		r.Projects = append(r.Projects, s.project(p))
	}
	for _, p := range c.projects("") {
		if !slices.Contains(inScope, p) {
			r.Projects = append(r.Projects, projectReport{Name: p, Waiting: s.waiting(p, s.tasks(p))})
		}
	}
	return r
}

// statusView builds a report. The fields from branches on belong to the project
// it's on: project sets them afresh for each one, and the methods that work out
// rows, notes and caps read them.
type statusView struct {
	*Crew
	doneToo  bool
	readOnly bool // another machine runs the crew, so its workers can't be seen here
	agents   Agents
	running  int // the workers running across the crew, counted once
	branches Branches
	repo     string              // the project's clone here, for restack reasons; empty when there's none
	settings Settings            // the project's, or the crew's when they can't be read (settingsOf)
	kids     map[string][]Task   // a task's file → the tasks stacked on it
	spikes   map[string][]Task   // a task's ID → the spikes that inform it
	deps     map[string]depsOf   // a task's file → the tasks it waits on
	caps     map[string]capOf    // a goal's ID → its tasks in flight against its max_in_flight
	prs      map[string]gitHubPR // a pr-open task's file → its PR as GitHub has it now (readPRs)
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

// waiting is what waits on the user in a project, from its tasks.
func (s *statusView) waiting(project string, tasks []Task) waitingOnUser {
	w := waitingOnUser{MemorySuggestions: s.memorySuggestions(project)}
	for _, t := range tasks {
		tw := waitingOf(t)
		w.Questions += tw.Questions
		w.PRs += tw.PRs
	}
	return w
}

// project works out one project in scope: whether it's paused or full, its goals
// and tasks, and what waits on the user.
func (s *statusView) project(name string) projectReport {
	tasks := s.tasks(name)
	r := projectReport{Name: name, Waiting: s.waiting(name, tasks), InScope: true}
	s.branches = s.Crew.branches(name)
	s.repo, s.settings = "", s.settingsOf(name)
	if p, err := s.loadProject(name); err == nil {
		s.repo = p.Repo()
	}
	if p, err := s.ledgerProject(name); err == nil {
		r.Paused = p.Paused()
	}
	// A project's own MAX_WORKERS may not be the crew's.
	r.Workers, r.WorkersErr = workerSlots(s.settings, s.running)
	s.kids, s.deps, s.caps = map[string][]Task{}, map[string]depsOf{}, map[string]capOf{}
	s.spikes = map[string][]Task{}
	for _, t := range tasks {
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
	for _, t := range tasks {
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
	for _, g := range goals {
		r.Goals = append(r.Goals, s.goal(g, goalRoots[g.ID()]))
	}
	for _, t := range roots {
		row := s.row(t)
		row.Folded = s.folds(t)
		r.Rows = append(r.Rows, row)
	}
	for _, f := range s.files("proposals", name) {
		if (Doc{f}).Get("status") == "open" {
			r.OpenProposals++
		}
	}
	r.ToReview = toReview(tasks)
	return r
}

// toReview are the PRs waiting on the user, cheapest look first, so the ones
// that need only the evidence can be cleared, and what's stacked on them freed,
// before those that need the diff read.
func toReview(tasks []Task) []Task {
	var open []Task
	for _, t := range tasks {
		if t.Status() == PROpen {
			open = append(open, t)
		}
	}
	slices.SortStableFunc(open, func(a, b Task) int {
		return cmp.Or(cmp.Compare(a.Risk().order(), b.Risk().order()), cmp.Compare(atoiOr0(a.PRNumber()), atoiOr0(b.PRNumber())))
	})
	return open
}

// goal works out one goal: its lead, how many of its tasks have merged, its
// tasks in flight against its max_in_flight, and its tasks in groups.
func (s *statusView) goal(g Goal, tasks []Task) goalReport {
	r := goalReport{Goal: g, Lead: s.agent(g.LeadJob())}
	for _, t := range s.goalTasks(g) {
		if rules, err := t.rules(); err == nil && !rules.pr {
			continue // a spike's PR is never merged
		}
		r.Total++
		if t.Status() == Merged {
			r.Merged++
		}
	}
	r.InFlight, r.CapErr = s.goalCap(g)
	r.Groups = s.goalGroups(g, tasks)
	return r
}

// agent is a job as claude agents lists it, or the zero Agent when it isn't.
func (s *statusView) agent(job string) Agent {
	if a := s.agents.find(job); a != nil {
		return *a
	}
	return Agent{}
}

// goalGroups puts a goal's tasks in groups, by where they stand: in flight; the
// ready ones that can go, in the order crew tick dispatches them (numbered, for a
// running goal); the ready ones waiting on other tasks; the drafts parked on
// questions for the user; and the drafts still being planned.
func (s *statusView) goalGroups(g Goal, tasks []Task) []taskGroup {
	byKind := map[groupKind][]Task{}
	for _, t := range tasks {
		var k groupKind
		switch st := t.Status(); {
		case st.InFlight():
			k = groupInFlight
		case st == Ready && s.waits(t):
			k = groupWaiting
		case st == Ready && g.Status() == GoalRunning:
			k = groupNext
		case st == Ready:
			k = groupReady
		case st == Draft && t.OpenQuestions() > 0:
			k = groupParked
		case st == Draft:
			k = groupPlanning
		default: // merged, done or abandoned, listed with --done
			k = groupFinished
		}
		byKind[k] = append(byKind[k], t)
	}
	var groups []taskGroup
	for k := groupInFlight; k <= groupFinished; k++ {
		ts := byKind[k]
		if len(ts) == 0 {
			continue
		}
		if k == groupNext || k == groupReady || k == groupWaiting {
			sortForDispatch(ts)
		}
		group := taskGroup{Kind: k}
		for i, t := range ts {
			row := s.row(t)
			if k == groupNext {
				row.Number = i + 1
			}
			group.Rows = append(group.Rows, row)
		}
		groups = append(groups, group)
	}
	return groups
}

// row works out one task's row, then the rows of the tasks stacked on it.
func (s *statusView) row(t Task) taskRow {
	r := taskRow{Task: t, Worker: s.agent(t.WorkerJob()), Notes: s.notes(t)}
	r.Merge = s.mergeCheck(t, r.Worker)
	for _, k := range s.kids[t.Path] {
		r.Kids = append(r.Kids, s.row(k))
	}
	return r
}

// lookNote says what look a PR waiting on the user needs: its tier and the
// tier's look. A high-risk task still waiting for its security review has only
// its tier: its merge check says what's missing.
func lookNote(t Task) string {
	if t.AwaitsSecurityReview() {
		return t.Risk().Label()
	}
	return t.Risk().Label() + ": " + t.Risk().Look()
}

// leavesStack: a ready task whose parent is in flight can dispatch as soon as the
// parent's branch is pushed. Listed under its parent, it would hide that, so it's
// listed on its own instead (in its goal's next or waiting group), and its row
// says "on <parent>".
func leavesStack(t, parent Task) bool {
	return t.Status() == Ready && parent.Status().InFlight()
}

// folds: a draft outside a goal, and every draft stacked on it, has no open
// question and nothing still to learn, so the plain text counts the stack in one
// line rather than listing it.
func (s *statusView) folds(t Task) bool {
	if t.Status() != Draft || t.OpenQuestions() > 0 || s.learning(t) != "" {
		return false
	}
	for _, k := range s.kids[t.Path] {
		if !s.folds(k) {
			return false
		}
	}
	return true
}

// waitsOn are the tasks this one waits on, as crew dispatch sees them. A task's
// row and its group both ask, so the answer is kept.
func (s *statusView) waitsOn(t Task) ([]Task, error) {
	if d, ok := s.deps[t.Path]; ok {
		return d.tasks, d.err
	}
	deps, err := s.pendingDeps(s.branches, t)
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

// notes are what a task's row says after its PR and its merge check: its PR's
// state, and what it waits for or on. A pr-open task's merge check already says
// what stands in its PR's way, so its notes give only the look it needs.
func (s *statusView) notes(t Task) []string {
	st := t.Status()
	var notes []string
	if st == PROpen {
		notes = append(notes, lookNote(t))
	} else {
		notes = append(notes, s.prNotes(t)...)
	}
	if l := s.learning(t); l != "" {
		notes = append(notes, l)
	}
	// informs holds the draft's full ID, as crew new writes it: NNN-<name>.
	if d := t.Get("informs"); d != "" {
		n, _, _ := strings.Cut(d, "-")
		notes = append(notes, "informs "+n)
	}
	if t.Get("interrupted") != "" {
		notes = append(notes, "interrupted by a pause")
	}
	// The lead's choices at intake, so the user sees what each worker runs with.
	if m := t.Model(); m != "" {
		notes = append(notes, "model "+m)
	}
	if t.AutomatedReviewOff() {
		notes = append(notes, "automated review off")
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

// prNotes is what a task that isn't pr-open says of its PR and its branch: the
// PR's state as crew sync last read it, a restack or retarget it needs, and its
// open questions. A pr-open task's merge check says these instead.
func (s *statusView) prNotes(t Task) []string {
	var notes []string
	if prs := t.Get("pr_state"); prs != "" {
		notes = append(notes, "("+prs+")")
	}
	if t.Status().InFlight() && s.repo != "" {
		if reason := s.branches.restackReason(t, s.repo); reason != "" {
			notes = append(notes, "needs restack: "+reason)
		}
		if reason := s.branches.retargetReason(t, s.repo); reason != "" {
			notes = append(notes, reason)
		}
	}
	if q := t.OpenQuestions(); q > 0 {
		notes = append(notes, fmt.Sprintf("%d open question(s)", q))
	}
	return notes
}

// learning is what a draft is still learning: how many items under Still to
// learn are open, and the spikes that inform it, with their status: "2 to learn;
// 041 done, 042 working". It's empty when there's neither.
func (s *statusView) learning(t Task) string {
	if t.Status() != Draft {
		return ""
	}
	var parts []string
	if n := t.StillToLearn(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d to learn", n))
	}
	var spikes []string
	for _, sp := range s.spikes[t.ID()] {
		spikes = append(spikes, taskNumber(sp)+" "+string(sp.Status()))
	}
	if len(spikes) > 0 {
		parts = append(parts, strings.Join(spikes, ", "))
	}
	return strings.Join(parts, "; ")
}

const afterNamed = 4 // how many tasks "after" names before it only counts them

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
