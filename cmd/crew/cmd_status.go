package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// crew status [--done] [--all|--project P]: the tasks of one project (P, or the
// default project) or of all of them, with their workers' state. Stacked tasks
// are listed under their parent, and a goal's tasks under the goal. Showing one
// project, it ends with what's waiting on you in the others.
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
	fmt.Printf("workers running: %d/%s\n", c.runningWorkers(s.agents), c.Settings["MAX_WORKERS"])
	for _, p := range projects {
		fmt.Println()
		fmt.Println(p)
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

type statusView struct {
	*Crew
	doneToo  bool
	agents   Agents
	branches Branches
	repo     string            // the project's clone here, for restack reasons; empty when there's none
	settings Settings          // the project's, or the crew's when there's no clone here
	kids     map[string][]Task // a task's file → the tasks stacked on it
}

func (s *statusView) shown(status string) bool {
	return s.doneToo || (status != "merged" && status != "done" && status != "abandoned")
}

func (s *statusView) project(name string) {
	s.branches = s.Crew.branches(name)
	s.repo, s.settings = "", s.Settings
	if p, err := s.loadProject(name); err == nil {
		s.repo, s.settings = p.Repo(), p.Settings
	}
	s.kids = map[string][]Task{}
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
		if p, ok := s.branches.parentOf(t); ok && s.shown(p.Get("status")) {
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
	for _, t := range roots {
		s.taskRow(t, "")
	}
	open := 0
	for _, f := range s.files("proposals", name) {
		if (Doc{f}).Get("status") == "open" {
			open++
		}
	}
	if open > 0 {
		fmt.Printf("  open proposals: %d  (crew proposals)\n", open)
	}
	if n := s.memorySuggestions(name); n > 0 {
		fmt.Printf("  memory suggestions: %d  (projects/%s/memory-suggestions.md)\n", n, name)
	}
}

// padID is an ID column 34 characters wide. It's padded by hand: the tree marker
// is one character wide but three bytes long.
func padID(id string) string {
	id = cut(id, 34)
	return id + strings.Repeat(" ", 34-utf8.RuneCountInString(id))
}

func (s *statusView) goalRow(g Goal, tasks []Task) {
	lead := s.agents.jobState(g.LeadJob())
	if lead != "" {
		lead = "lead: " + lead
	}
	total, merged, inFlight := 0, 0, 0
	for _, t := range s.goalTasks(g) {
		total++
		if t.Status() == Merged {
			merged++
		}
		if t.Status().InFlight() {
			inFlight++
		}
	}
	limit := "?"
	if n, err := goalMaxInFlight(s.settings, g); err == nil {
		limit = fmt.Sprint(n)
	}
	fmt.Printf("  %s %-20s %-26s %d of %d task(s) merged, %d/%s in flight\n", padID("goal "+g.ID()), g.Get("status"), lead, merged, total, inFlight, limit)
	for _, t := range tasks {
		s.taskRow(t, "  ")
	}
}

// taskRow prints one task, then the tasks stacked on it.
func (s *statusView) taskRow(t Task, indent string) {
	st := t.Get("status")
	worker := s.agents.jobState(t.WorkerJob())
	if worker != "" {
		worker = "worker: " + worker
	}
	pr := "-"
	if t.PR() != "" {
		pr = "#" + t.PRNumber()
	}
	var notes []string
	if prs := t.Get("pr_state"); prs != "" {
		notes = append(notes, "("+prs+")")
	}
	if Status(st).InFlight() && s.repo != "" {
		if reason := s.branches.restackReason(t, s.repo); reason != "" {
			notes = append(notes, "needs restack: "+reason)
		}
	}
	if q := t.OpenQuestions(); q > 0 {
		notes = append(notes, fmt.Sprintf("%d open question(s)", q))
	}
	fmt.Printf("  %s %-20s %-26s %-6s %s\n", padID(indent+t.ID()), st, worker, pr, strings.Join(notes, "  "))
	for _, k := range s.kids[t.Path] {
		s.taskRow(k, strings.ReplaceAll(indent, "└ ", "  ")+"└ ")
	}
}
