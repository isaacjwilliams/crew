package main

import (
	"fmt"
	"io"
	"os"
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
	c.catchUpLedger()
	inScope := c.projects(scope)
	printReport(os.Stdout, buildReport(c, inScope, doneToo, c.readPRs(inScope)))
	return nil
}

// catchUpLedger pulls the ledger when another machine runs the crew and pushes
// it. No crew command changes the ledger here, so this needs no lock.
func (c *Crew) catchUpLedger() {
	if c.holder() != c.Host {
		gitOK(c.Home, "pull", "-q", "--ff-only")
	}
}

// printReport prints a status report as plain text: the projects in scope,
// then the others that have something waiting on the user.
func printReport(w io.Writer, r statusReport) {
	if r.ReadOnlyOn != "" {
		fmt.Fprintln(w, readOnlyLine(r.ReadOnlyOn))
	}
	if len(r.Projects) == 0 {
		fmt.Fprintln(w, noProjects)
		return
	}
	fmt.Fprintln(w, workersLine(r))
	for _, p := range r.Projects {
		if p.InScope {
			fmt.Fprintln(w)
			printProject(w, p)
		}
	}
	if elsewhere := r.elsewhere(); len(elsewhere) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, elsewhereHeading)
		for _, line := range elsewhere {
			fmt.Fprintln(w, "  "+line)
		}
	}
}

func readOnlyLine(machine string) string { return "read-only: crew runs on " + machine }

const noProjects = "No projects registered. Run 'crew project add' in a repo."

// workersLine counts the workers running across the crew, and says when no
// project can dispatch.
func workersLine(r statusReport) string {
	line := "workers running: " + slotsText(r.Workers, r.WorkersErr)
	if r.NoneDispatch {
		line += ": full, nothing dispatches"
	}
	return line
}

const elsewhereHeading = "waiting on you in other projects  (crew status --all)"

// elsewhere words what waits on the user in each project out of scope that has
// something: "q: 1 PR(s) to review".
func (r statusReport) elsewhere() []string {
	var lines []string
	for _, p := range r.Projects {
		if !p.InScope && p.Waiting != (waitingOnUser{}) {
			lines = append(lines, p.Name+": "+p.Waiting.String())
		}
	}
	return lines
}

// printProject lists one project's goals and tasks under its name, which says
// when it's paused or full. Drafts it can fold are counted, not listed.
func printProject(w io.Writer, p projectReport) {
	fmt.Fprintln(w, projectLine(p))
	if len(p.Rows)+len(p.Goals) == 0 {
		fmt.Fprintln(w, "  "+noActiveTasks)
	}
	for _, g := range p.Goals {
		printGoal(w, g)
	}
	rows, folded := p.listed()
	for _, row := range rows {
		printRow(w, row, "")
	}
	if len(folded) > 0 {
		fmt.Fprintln(w, "  "+draftsLine(folded, p.Name))
	}
	for _, line := range p.footer() {
		fmt.Fprintln(w, "  "+line)
	}
}

// projectLine is the project's name, and says when it's paused or full. A
// project that can't dispatch, or whose MAX_WORKERS isn't a number, says so
// with its own limit, which may not be the crew's.
func projectLine(p projectReport) string {
	line := p.Name
	if p.Paused {
		line += "  paused (crew resume --project " + p.Name + ")"
	}
	if p.WorkersErr != nil {
		line += "  " + p.WorkersErr.Error()
	} else if p.Workers.full() {
		line += fmt.Sprintf("  workers full (MAX_WORKERS %d here)", p.Workers.limit)
	}
	return line
}

const noActiveTasks = "(no active tasks)"

// listed splits the project's tasks outside a goal into the rows the plain text
// lists and the folded drafts it only counts.
func (p projectReport) listed() (rows, folded []taskRow) {
	for _, row := range p.Rows {
		if row.Folded {
			folded = append(folded, row)
		} else {
			rows = append(rows, row)
		}
	}
	return rows, folded
}

// draftsLine counts folded drafts, with the tasks stacked on them.
func draftsLine(folded []taskRow, project string) string {
	n := 0
	for _, row := range folded {
		n += row.stackSize()
	}
	return fmt.Sprintf("%d draft(s)  (crew list --project %s --status draft)", n, project)
}

// footer is what the project's listing ends with: its open proposals, memory
// suggestions and PRs to review, each only when there are some.
func (p projectReport) footer() []string {
	var lines []string
	if p.OpenProposals > 0 {
		lines = append(lines, fmt.Sprintf("open proposals: %d  (crew proposals --project %s)", p.OpenProposals, p.Name))
	}
	if n := p.Waiting.MemorySuggestions; n > 0 {
		lines = append(lines, fmt.Sprintf("memory suggestions: %d  (projects/%s/memory-suggestions.md)", n, p.Name))
	}
	if len(p.ToReview) > 0 {
		lines = append(lines, toReviewLine(p.ToReview))
	}
	return lines
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

func printGoal(w io.Writer, g goalReport) {
	fmt.Fprintln(w, "  "+padID("goal "+g.Goal.ID())+" "+goalColumns(g))
	for _, group := range g.Groups {
		printGroup(w, group)
	}
}

// goalColumns is what a goal's line says after its ID: its status, its lead,
// how many of its tasks have merged and how many are in flight.
func goalColumns(g goalReport) string {
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
	return fmt.Sprintf("%-20s %-26s %d of %d task(s) merged, %s in flight%s", g.Goal.Get("status"), lead, g.Merged, g.Total, slotsText(g.InFlight, g.CapErr), full)
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

// printGroup lists a goal's group under its heading, then counts the rows it
// leaves out.
func printGroup(w io.Writer, g taskGroup) {
	fmt.Fprintln(w, groupIndent+g.Kind.heading())
	rows, more := g.listed()
	for _, row := range rows {
		printRow(w, row, groupIndent+g.numberColumn(row))
	}
	if len(more) > 0 {
		fmt.Fprintf(w, "  %s   %s\n", groupIndent, moreLine(more))
	}
}

// listed splits a group's rows into those the plain text lists and those it
// leaves out: of a numbered group, it lists only the first few, and any later one
// with an open question in its stack, since that waits on the user.
func (g taskGroup) listed() (rows, more []taskRow) {
	for _, row := range g.Rows {
		if row.Number > nextShown && !row.stackAsks() {
			more = append(more, row)
		} else {
			rows = append(rows, row)
		}
	}
	return rows, more
}

// moreLine counts the rows a group leaves out.
func moreLine(more []taskRow) string { return fmt.Sprintf("… (%d more)", len(more)) }

// numberColumn is what goes before a row's ID in its group. Rows in flight have
// none. Every other group's rows have a number column three wide, blank when
// they aren't numbered.
func (g taskGroup) numberColumn(row taskRow) string {
	if g.Kind == groupInFlight {
		return ""
	}
	number := ""
	if row.Number > 0 {
		number = fmt.Sprint(row.Number)
	}
	return fmt.Sprintf("%-3s", number)
}

// printRow prints one task, then the tasks stacked on it. The indent is what
// goes before the ID: spaces, a number, a └.
func printRow(w io.Writer, r taskRow, indent string) {
	fmt.Fprintln(w, "  "+padID(indent+r.Task.ID())+" "+rowColumns(r))
	for _, k := range r.Kids {
		printRow(w, k, strings.Repeat(" ", utf8.RuneCountInString(indent))+"└ ")
	}
}

// rowColumns is what a task's row says after its ID: its status, its worker,
// its PR, its merge check and its notes.
func rowColumns(r taskRow) string {
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
	return fmt.Sprintf("%-20s %-26s %-6s %s", r.Task.Status(), worker, pr, strings.Join(notes, "  "))
}
