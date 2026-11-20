package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// crew status [--done] [--all|--project P]: the tasks of one project (P, or the
// default project) or of all of them, with their workers' state. Stacked tasks
// are listed under their parent, and a goal's tasks under the goal, grouped by
// where they stand. Each task not yet dispatched says what it waits for. Showing
// one project, it ends with what's waiting on you in the others.
func cmdStatus(c *Crew, args []string) error {
	all, proj, doneToo := false, "", false
	for len(args) > 0 {
		switch {
		case args[0] == "--done":
			doneToo = true
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
	if c.holder() != c.Host {
		// Another machine runs the crew and pushes the ledger; catch up with it. No
		// crew command changes the ledger here, so this needs no lock.
		gitOK(c.Home, "pull", "-q", "--ff-only")
	}
	printReport(buildReport(c, c.projects(scope), doneToo))
	return nil
}

// printReport prints a status report as plain text: the projects in scope,
// then the others that have something waiting on the user.
func printReport(r statusReport) {
	if r.ReadOnlyOn != "" {
		fmt.Println("read-only: crew runs on " + r.ReadOnlyOn)
	}
	if len(r.Projects) == 0 {
		fmt.Println("No projects registered. Run 'crew project add' in a repo.")
		return
	}
	header := "workers running: " + slotsText(r.Workers, r.WorkersErr)
	if r.NoneDispatch {
		header += ": full, nothing dispatches"
	}
	fmt.Println(header)
	var elsewhere []string
	for _, p := range r.Projects {
		if p.InScope {
			fmt.Println()
			printProject(p)
		} else if p.Waiting != (waitingOnUser{}) {
			elsewhere = append(elsewhere, "  "+p.Name+": "+p.Waiting.String())
		}
	}
	if len(elsewhere) > 0 {
		fmt.Println()
		fmt.Println("waiting on you in other projects  (crew status --all)")
		fmt.Println(strings.Join(elsewhere, "\n"))
	}
}

// printProject lists one project's goals and tasks under its name, which says
// when it's paused or full. Drafts it can fold are counted, not listed.
func printProject(p projectReport) {
	// A paused project says so. A project that can't dispatch, or whose
	// MAX_WORKERS isn't a number, says so with its own limit, which may not be
	// the crew's.
	line := p.Name
	if p.Paused {
		line += "  paused (crew resume --project " + p.Name + ")"
	}
	if p.WorkersErr != nil {
		line += "  " + p.WorkersErr.Error()
	} else if p.Workers.full() {
		line += fmt.Sprintf("  workers full (MAX_WORKERS %d here)", p.Workers.limit)
	}
	fmt.Println(line)
	if len(p.Rows)+len(p.Goals) == 0 {
		fmt.Println("  (no active tasks)")
	}
	for _, g := range p.Goals {
		printGoal(g)
	}
	drafts := 0
	for _, row := range p.Rows {
		if row.Folded {
			drafts += row.stackSize()
		} else {
			printRow(row, "")
		}
	}
	if drafts > 0 {
		fmt.Printf("  %d draft(s)  (crew list --project %s --status draft)\n", drafts, p.Name)
	}
	if p.OpenProposals > 0 {
		fmt.Printf("  open proposals: %d  (crew proposals --project %s)\n", p.OpenProposals, p.Name)
	}
	if n := p.Waiting.MemorySuggestions; n > 0 {
		fmt.Printf("  memory suggestions: %d  (projects/%s/memory-suggestions.md)\n", n, p.Name)
	}
	if len(p.ToReview) > 0 {
		fmt.Println("  " + toReviewLine(p.ToReview))
	}
}

// stackSize counts the row and every row stacked on it.
func (r taskRow) stackSize() int {
	n := 1
	for _, k := range r.Kids {
		n += k.stackSize()
	}
	return n
}

// toReviewLine words the PRs waiting on the user, in toReview's order:
// "to review: #41 low, #38 medium, #35 high".
func toReviewLine(open []Task) string {
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

func printGoal(g goalReport) {
	lead := g.Lead.stateText()
	if lead != "" {
		lead = "lead: " + lead
	}
	full := ""
	if g.CapErr != nil {
		full = "  " + g.CapErr.Error()
	} else if g.InFlight.full() {
		full = ": full"
	}
	fmt.Printf("  %s %-20s %-26s %d of %d task(s) merged, %s in flight%s\n", padID("goal "+g.Goal.ID()), g.Goal.Get("status"), lead, g.Merged, g.Total, slotsText(g.InFlight, g.CapErr), full)
	for _, group := range g.Groups {
		printGroup(group)
	}
}

// slotsText is "used/limit", with ? for a limit set to something that isn't a
// whole number.
func slotsText(sl slots, err error) string {
	if err != nil {
		return fmt.Sprintf("%d/?", sl.used)
	}
	return fmt.Sprintf("%d/%d", sl.used, sl.limit)
}

const nextShown = 5 // how many of a running goal's next tasks are listed

// groupIndent is how far a goal's group headings, and its rows, are indented.
const groupIndent = "    "

// heading is what a goal's group is listed under.
func (k groupKind) heading() string {
	switch k {
	case groupInFlight:
		return "in flight"
	case groupNext:
		return "next, in dispatch order"
	case groupReady:
		return "ready"
	case groupWaiting:
		return "waiting on other tasks"
	case groupParked:
		return "parked"
	case groupPlanning:
		return "planning"
	}
	return "finished"
}

// printGroup lists a goal's group under its heading. Of a numbered group it lists
// only the first few, and any later one with an open question in its stack,
// since that waits on the user.
func printGroup(g taskGroup) {
	fmt.Println(groupIndent + g.Kind.heading())
	more := 0
	for _, row := range g.Rows {
		if row.Number > nextShown && !row.stackAsks() {
			more++
			continue
		}
		// Rows in flight start at the indent. Every other group's rows have a
		// number column three wide before the ID, blank when they aren't numbered.
		indent := groupIndent
		if g.Kind != groupInFlight {
			number := ""
			if row.Number > 0 {
				number = fmt.Sprint(row.Number)
			}
			indent = fmt.Sprintf("%s%-3s", groupIndent, number)
		}
		printRow(row, indent)
	}
	if more > 0 {
		fmt.Printf("  %s   … (%d more)\n", groupIndent, more)
	}
}

// printRow prints one task, then the tasks stacked on it. The indent is what
// goes before the ID: spaces, a number, a └.
func printRow(r taskRow, indent string) {
	worker := r.Worker.stateText()
	if worker != "" {
		worker = "worker: " + worker
	}
	pr := "-"
	if r.Task.PR() != "" {
		pr = "#" + r.Task.PRNumber()
	}
	notes := r.Notes
	if r.Merge != nil {
		notes = append([]string{r.Merge.String()}, notes...)
	}
	fmt.Printf("  %s %-20s %-26s %-6s %s\n", padID(indent+r.Task.ID()), r.Task.Status(), worker, pr, strings.Join(notes, "  "))
	for _, k := range r.Kids {
		printRow(k, strings.Repeat(" ", utf8.RuneCountInString(indent))+"└ ")
	}
}
