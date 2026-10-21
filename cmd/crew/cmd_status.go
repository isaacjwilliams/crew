package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// crew status [--done]: the tasks of the current project, or of all of them,
// with their workers' state. Stacked tasks are listed under their parent, and a
// goal's tasks under the goal.
func cmdStatus(c *Crew, args []string) error {
	s := statusView{Crew: c, doneToo: len(args) > 0 && args[0] == "--done"}
	projects := c.projectsInScope(false)
	if holder := c.holder(); holder != c.Host {
		// Another machine runs the crew and pushes the ledger; catch up with it. No
		// crew command changes the ledger here, so this needs no lock.
		gitOK(c.Home, "pull", "-q", "--ff-only")
		holder = c.holder()
		if holder == "" {
			holder = "no machine"
		}
		fmt.Println("read-only: crew runs on " + holder)
		projects = c.projectsInScope(false)
	}
	if len(projects) == 0 {
		fmt.Println("No projects registered. Run 'crew project add' in a repo.")
		return nil
	}
	s.agents = c.agents()
	fmt.Printf("workers running: %d/%s   tasks in flight: %d/%s\n", c.runningWorkers(s.agents), c.Settings["MAX_WORKERS"], c.inFlight(), c.Settings["MAX_IN_FLIGHT"])
	for _, p := range projects {
		fmt.Println()
		fmt.Println(p)
		s.project(p)
	}
	return nil
}

type statusView struct {
	*Crew
	doneToo  bool
	agents   Agents
	branches Branches
	repo     string            // the project's clone here, for restack reasons; empty when there's none
	kids     map[string][]Task // a task's file → the tasks stacked on it
}

func (s *statusView) shown(status string) bool {
	return s.doneToo || (status != "merged" && status != "done" && status != "abandoned")
}

func (s *statusView) project(name string) {
	s.branches = s.Crew.branches(name)
	s.repo = ""
	if p, err := s.loadProject(name); err == nil {
		s.repo = p.Repo()
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
		fmt.Printf("  open gardener proposals: %d  (crew proposals)\n", open)
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
	total, merged := 0, 0
	for _, t := range s.goalTasks(g) {
		total++
		if t.Status() == Merged {
			merged++
		}
	}
	fmt.Printf("  %s %-20s %-26s %d of %d task(s) merged\n", padID("goal "+g.ID()), g.Get("status"), lead, merged, total)
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
