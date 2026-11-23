package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
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
	feed := newStatusFeed(c, scope, doneToo)
	if plain || !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		printReport(os.Stdout, feed.report(true))
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
// each project, and before what waits in the others.
func printReport(w io.Writer, r statusReport) {
	for _, l := range reportLines(r) {
		if len(l.kids) > 0 {
			fmt.Fprintln(w)
		}
		printLine(w, l, "")
	}
}

func printLine(w io.Writer, l statusLine, indent string) {
	fmt.Fprintln(w, l.text(indent, idColumn))
	if !l.folded {
		for _, k := range l.kids {
			printLine(w, k, indent+"  ")
		}
	}
}

// idColumn is how wide the text before a task's or goal's columns is, its ID
// cut to fit: room for a group's indent, a number column and a 32-character ID.
const idColumn = 2 + 4 + 3 + 32

// statusLine is one line of crew status (a project, a goal, a group of a goal's
// tasks, a task, or a count), with the lines that belong under it, its kids.
type statusLine struct {
	key    string // names the line from one report to the next
	head   string // what the line says; for a task or a goal, its ID
	tail   string // a task's or goal's columns, lined up after its ID
	kids   []statusLine
	folded bool // the text shows only this line, which counts its kids
}

// text is the line after the indent, with its columns, if any, starting after
// idColumn.
func (l statusLine) text(indent string, idColumn int) string {
	head := indent + l.head
	if l.tail == "" {
		return head
	}
	// Padded by rune: a └ is one character wide but three bytes long.
	head = cut(head, idColumn)
	return head + strings.Repeat(" ", idColumn-utf8.RuneCountInString(head)) + " " + l.tail
}

func leafLine(key, head string) statusLine { return statusLine{key: key, head: head} }

// reportLines lays the report out as lines: a header, the projects in scope,
// then what waits on the user in the others.
func reportLines(r statusReport) []statusLine {
	var lines []statusLine
	if r.ReadOnlyOn != "" {
		lines = append(lines, leafLine("read-only", readOnlyLine(r.ReadOnlyOn)))
	}
	if len(r.Projects) == 0 {
		return append(lines, leafLine("no projects", noProjects))
	}
	lines = append(lines, leafLine("workers", workersLine(r)))
	for _, p := range r.Projects {
		if p.InScope {
			lines = append(lines, projectLines(p))
		}
	}
	if elsewhere := r.elsewhere(); len(elsewhere) > 0 {
		l := leafLine("elsewhere", elsewhereHeading)
		for _, e := range elsewhere {
			name, _, _ := strings.Cut(e, ":")
			l.kids = append(l.kids, leafLine("elsewhere "+name, e))
		}
		lines = append(lines, l)
	}
	return lines
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

// projectLines is one project's goals and tasks under its name. Drafts it can
// fold are counted, and listed only under the count.
func projectLines(p projectReport) statusLine {
	key := "project " + p.Name
	l := leafLine(key, projectLine(p))
	if len(p.Rows)+len(p.Goals) == 0 {
		l.kids = append(l.kids, leafLine(key+" none", noActiveTasks))
	}
	for _, g := range p.Goals {
		l.kids = append(l.kids, goalLines(g))
	}
	rows, folded := p.listed()
	for _, row := range rows {
		l.kids = append(l.kids, rowLines(row, ""))
	}
	if len(folded) > 0 {
		drafts := statusLine{key: key + " drafts", head: draftsLine(folded, p.Name), folded: true}
		for _, row := range folded {
			drafts.kids = append(drafts.kids, rowLines(row, ""))
		}
		l.kids = append(l.kids, drafts)
	}
	for _, f := range p.footer() {
		label, _, _ := strings.Cut(f, ":")
		l.kids = append(l.kids, leafLine(key+" "+label, f))
	}
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

const noActiveTasks = "(no active tasks)"

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

// goalLines is a goal's line, with its groups of tasks under it.
func goalLines(g goalReport) statusLine {
	l := statusLine{key: "goal " + g.Goal.Path, head: "goal " + g.Goal.ID(), tail: goalColumns(g)}
	for _, group := range g.Groups {
		l.kids = append(l.kids, groupLines(l.key, group))
	}
	return l
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
	// A goal's ready tasks become its next ones when it starts: still one group.
	kind := g.Kind
	if kind == groupNext {
		kind = groupReady
	}
	key := fmt.Sprintf("%s group %d", goalKey, kind)
	l := leafLine(key, g.Kind.heading())
	rows, more := g.listed()
	for _, row := range rows {
		l.kids = append(l.kids, rowLines(row, g.numberColumn(row)))
	}
	if len(more) > 0 {
		m := statusLine{key: key + " more", head: moreLine(more), folded: true}
		for _, row := range more {
			m.kids = append(m.kids, rowLines(row, g.numberColumn(row)))
		}
		l.kids = append(l.kids, m)
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

// rowLines is a task's line, with the tasks stacked on it under it. The number
// is its group's number column.
func rowLines(r taskRow, number string) statusLine {
	l := statusLine{key: "task " + r.Task.Path, head: number + r.Task.ID(), tail: rowColumns(r)}
	for _, k := range r.Kids {
		l.kids = append(l.kids, rowLines(k, "└ "))
	}
	return l
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
