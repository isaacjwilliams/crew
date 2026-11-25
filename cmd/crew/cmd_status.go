package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// crew status [--done] [--plain] [--all|--project P]: the tasks of one project
// (P, or the default project) or of all of them, with their workers' state.
// Stacked tasks are listed under their parent, and a goal's tasks under the
// goal, grouped by where they stand. Each task not yet dispatched says what it
// waits for. Showing one project, it ends with what's waiting on you in the
// others. In a terminal it's a live view (status_live.go); anywhere else, or with
// --plain, it prints the text and exits.
func cmdStatus(c *Crew, args []string) error {
	all, proj, doneToo, plain := false, "", false, false
	for len(args) > 0 {
		switch {
		case args[0] == "--done":
			doneToo = true
		case args[0] == "--plain":
			plain = true
		case args[0] == "--all":
			all = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		default:
			return usage("crew status [--done] [--plain] [--all|--project P]")
		}
		args = args[1:]
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	feed := feedOf(c, scope, doneToo)
	if plain || !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		feed.pullLedger()
		r, err := feed.build(statusReads{prs: feed.readPRs(), workers: feed.listWorkers(), branches: feed.readBranches()})
		if err != nil {
			warn("went by the settings crew started with: %v", err)
		}
		printReport(os.Stdout, r)
		return nil
	}
	return watchStatus(feed)
}

// catchUpLedger pulls the ledger when another machine runs the crew and pushes
// it. No crew command changes the ledger here, so this needs no lock.
func (c *Crew) catchUpLedger() {
	if c.holder() != c.Host {
		gitOK(c.Home, "pull", "-q", "--ff-only")
	}
}

// printReport prints a status report as plain text: its lines, each under the
// line it belongs to, with folded lines' kids left out. A blank line comes before
// each top-level line with lines under it: a project, or what waits in the
// others. Task and goal lines are a table, each column as wide as its widest
// value, with its headings at the top of each project.
func printReport(w io.Writer, r statusReport) {
	lines := reportLines(r)
	t := measure(lines, func(depth int) int { return 2 * depth })
	for _, l := range lines {
		if len(l.kids) > 0 {
			fmt.Fprintln(w)
		}
		printLine(w, t, l, "")
	}
}

func printLine(w io.Writer, t table, l statusLine, indent string) {
	if l.fields == nil {
		fmt.Fprintln(w, indent+l.head)
	} else {
		// One line: no column is narrower than its values.
		fmt.Fprintln(w, t.row(indent, l)[0])
	}
	if l.folded {
		return
	}
	if indent == "" && slices.ContainsFunc(l.kids, statusLine.hasFields) {
		printLine(w, t, headings(), "  ") // a project's rows go under the headings
	}
	for _, k := range l.kids {
		printLine(w, t, k, indent+"  ")
	}
}

// statusLine is one line of crew status (a project, a goal, a group of a goal's
// tasks, a task, or a count), with the lines that belong under it, its kids.
type statusLine struct {
	key  string // names the line from one report to the next, so a view can keep its place
	head string // what the line says; for a task or a goal, its ID
	// fields are a task's or a goal's values, one for each of columns, in
	// order; nil for any other line.
	fields []string
	kids   []statusLine
	folded bool // the text shows only this line, which counts its kids
}

func (l statusLine) hasFields() bool { return l.fields != nil }

func leafLine(key, head string) statusLine { return statusLine{key: key, head: head} }

// reportLines lays the report out as lines: a header, the projects in scope,
// then what waits on the user in the others.
func reportLines(r statusReport) []statusLine {
	var lines []statusLine
	if r.ReadOnlyOn != "" {
		lines = append(lines, leafLine("read-only", "read-only: crew runs on "+r.ReadOnlyOn))
	}
	if len(r.Projects) == 0 {
		return append(lines, leafLine("no projects", "No projects registered. Run 'crew project add' in a repo."))
	}
	lines = append(lines, leafLine("workers", workersLine(r)))
	for _, p := range r.Projects {
		if p.InScope {
			lines = append(lines, projectLines(p))
		}
	}
	if elsewhere := r.elsewhere(); len(elsewhere) > 0 {
		lines = append(lines, statusLine{key: "elsewhere", head: elsewhereHeading, kids: elsewhere})
	}
	return lines
}

// workersLine counts the workers running across the crew, and says when no
// project can dispatch.
func workersLine(r statusReport) string {
	count := slotsText(r.Workers, r.WorkersErr)
	if r.WorkersUnlisted {
		count = "not listed yet"
	}
	line := "workers running: " + count
	if r.NoneDispatch {
		line += ": full, nothing dispatches"
	}
	return line
}

const elsewhereHeading = "waiting on you in other projects  (crew status --all)"

// elsewhere is a line for each project out of scope with something waiting on
// the user: "q: 1 PR(s) to review".
func (r statusReport) elsewhere() []statusLine {
	var lines []statusLine
	for _, p := range r.Projects {
		if !p.InScope && p.Waiting != (waitingOnUser{}) {
			lines = append(lines, leafLine("elsewhere "+p.Name, p.Name+": "+p.Waiting.String()))
		}
	}
	return lines
}

// projectLines is one project's goals and tasks under its name. Drafts it can
// fold are counted, and listed only under the count.
func projectLines(p projectReport) statusLine {
	key := "project " + p.Name
	l := leafLine(key, projectLine(p))
	if len(p.Rows)+len(p.Goals) == 0 {
		l.kids = append(l.kids, leafLine(key+" none", "(no active tasks)"))
	}
	for _, g := range p.Goals {
		l.kids = append(l.kids, goalLines(g))
	}
	rows, folded := p.listed()
	l.kids = append(l.kids, rowsLines(rows, noNumber)...)
	if len(folded) > 0 {
		l.kids = append(l.kids, statusLine{key: key + " drafts", head: draftsLine(folded, p.Name), kids: rowsLines(folded, noNumber), folded: true})
	}
	l.kids = append(l.kids, p.footer(key)...)
	return l
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

// listed splits the project's tasks outside a goal into the rows it lists and
// the folded drafts it counts.
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
// suggestions and PRs to review, each only when there are some. Their keys
// follow the project's.
func (p projectReport) footer(key string) []statusLine {
	var lines []statusLine
	if p.OpenProposals > 0 {
		lines = append(lines, leafLine(key+" proposals", fmt.Sprintf("open proposals: %d  (crew proposals --project %s)", p.OpenProposals, p.Name)))
	}
	if n := p.Waiting.MemorySuggestions; n > 0 {
		lines = append(lines, leafLine(key+" memory", fmt.Sprintf("memory suggestions: %d  (projects/%s/memory-suggestions.md)", n, p.Name)))
	}
	if len(p.ToReview) > 0 {
		lines = append(lines, leafLine(key+" to review", toReviewLine(p.ToReview)))
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

// goalLines is a goal's line, with its groups of tasks under it.
func goalLines(g goalReport) statusLine {
	l := statusLine{key: "goal " + g.Goal.Path, head: "goal " + g.Goal.ID(), fields: goalFields(g)}
	for _, group := range g.Groups {
		l.kids = append(l.kids, groupLines(l.key, group))
	}
	return l
}

// goalFields are what a goal's line says after its ID: its status, its lead's
// state under worker, and in its notes how many of its tasks have merged and
// how many are in flight.
func goalFields(g goalReport) []string {
	notes := fmt.Sprintf("%d of %d task(s) merged, %s in flight", g.Merged, g.Total, slotsText(g.InFlight, g.CapErr))
	if g.CapErr != nil {
		notes += "  " + g.CapErr.Error()
	} else if g.InFlight.full() {
		notes += ": full"
	}
	return []string{statusField: g.Goal.Get("status"), workerField: g.Lead.stateText(), notesField: notes}
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

// groupLines is a goal's group under its heading. The rows it leaves out are
// counted in a folded line under it.
func groupLines(goalKey string, g taskGroup) statusLine {
	// A goal's ready tasks become its next ones when it starts. It's still one
	// group, so its key stays the same though its heading changes.
	kind := g.Kind
	if kind == groupNext {
		kind = groupReady
	}
	key := goalKey + " group " + kind.heading()
	rows, more := g.listed()
	l := statusLine{key: key, head: g.Kind.heading(), kids: rowsLines(rows, g.numberColumn)}
	if len(more) > 0 {
		l.kids = append(l.kids, statusLine{key: key + " more", head: fmt.Sprintf("… (%d more)", len(more)), kids: rowsLines(more, g.numberColumn), folded: true})
	}
	return l
}

// listed splits a group's rows into those it lists and those it leaves out: of
// a numbered group, it lists only the first few, and any later one with an open
// question in its stack, since that waits on the user.
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

// rowsLines is a line for each row, with the tasks stacked on it under it.
// number is each row's number column.
func rowsLines(rows []taskRow, number func(taskRow) string) []statusLine {
	var lines []statusLine
	for _, row := range rows {
		lines = append(lines, rowLines(row, number(row)))
	}
	return lines
}

// noNumber is the number column of a row outside a goal: there's none.
func noNumber(taskRow) string { return "" }

// rowLines is a task's line, with the tasks stacked on it under it. before is
// what goes before its ID: its group's number column, or └ for a stacked task.
func rowLines(r taskRow, before string) statusLine {
	l := statusLine{key: "task " + r.Task.Path, head: before + r.Task.ID(), fields: rowFields(r)}
	for _, k := range r.Kids {
		l.kids = append(l.kids, rowLines(k, "└ "))
	}
	return l
}

// rowFields are what a task's row says after its ID: its status, its worker's
// state, its PR, the model and effort it runs with, and in its notes its merge
// check and the rest.
func rowFields(r taskRow) []string {
	pr := ""
	if r.Task.PR() != "" {
		pr = "#" + r.Task.PRNumber()
	}
	notes := r.Notes
	if r.Merge != nil {
		notes = append([]string{r.Merge.String()}, notes...)
	}
	return []string{
		statusField: string(r.Task.Status()),
		workerField: r.Worker.stateText(),
		prField:     pr,
		modelField:  r.Task.Model(),
		effortField: r.Task.Effort(),
		notesField:  strings.Join(notes, "  "),
	}
}
