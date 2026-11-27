package main

// How crew status lays out a task's or a goal's line: its ID, then its fields
// in columns under named headings. The plain text gives each column the width
// of its widest value; the live view caps them to fit the screen, wrapping a
// longer value inside its column, or shows each row as a card when even that
// doesn't fit. Each lays out the fields as it words them (wording).

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The fields of a task's or a goal's line, in the order of their columns.
const (
	statusField = iota
	workerField
	prField
	modelField
	effortField
	notesField
)

// columns are the columns after a line's ID, one per field: its heading, and
// how wide the live view lets it grow before its values wrap. Notes, the last,
// takes the rest of the screen.
var columns = [...]struct {
	heading string
	cap     int
}{
	statusField: {"status", 11},
	workerField: {"worker", 16},
	prField:     {"PR", 6},
	modelField:  {"model", 8},
	effortField: {"effort", 6},
	notesField:  {"notes", 0},
}

// The ID column is idHeading under the headings, and the live view lets it grow
// to idCap, its indent included. Notes must get at least minNotes, unless its
// values need less. With the caps above, every report fits in columns in 120
// (TestTheCappedColumnsFitIn120).
const (
	idHeading = "task"
	idCap     = 40
	minNotes  = 20
	columnGap = "  "
)

// headings is the line the column headings make: a line in the table, with a
// heading in each column.
func headings() statusLine {
	l := statusLine{head: idHeading}
	for _, c := range columns {
		l.fields = append(l.fields, same(c.heading))
	}
	return l
}

// table is how wide each column is: the ID's, with the line's indent, then each
// field's, in columns' order. live: it holds the fields as the live view words
// them, not as the plain text does.
type table struct {
	live   bool
	id     int
	fields [len(columns)]int
}

// value is a field as the table words it.
func (t table) value(f wording) string {
	if t.live {
		return f.live
	}
	return f.text
}

// measure is the table each line holding fields fits in unwrapped, with the
// headings: every such line in lines and under them, folded lines' kids too.
// The live view measures them all, so opening a fold never moves the columns.
// indent is how wide a line's indent is at a depth. A project is at depth 0,
// and its rows and their headings at depth 1. live: measure the fields as the
// live view words them.
func measure(lines []statusLine, indent func(depth int) int, live bool) table {
	t := table{live: live}
	var add func(l statusLine, depth int)
	add = func(l statusLine, depth int) {
		if l.hasFields() {
			t.id = max(t.id, indent(depth)+lipgloss.Width(l.head))
			for i, f := range l.fields {
				t.fields[i] = max(t.fields[i], lipgloss.Width(t.value(f)))
			}
		}
		for _, k := range l.kids {
			add(k, depth+1)
		}
	}
	add(headings(), 1)
	for _, l := range lines {
		add(l, 0)
	}
	return t
}

// fit is the table on a screen width wide: each column as wide as its widest
// value up to its cap, and notes as wide as the rest. It's false when notes
// would get less than minNotes and its values need more: the rows don't fit in
// columns.
func (t table) fit(width int) (table, bool) {
	f := t
	f.id = min(t.id, idCap)
	used := f.id
	for i, c := range columns[:notesField] {
		f.fields[i] = min(t.fields[i], c.cap)
		used += len(columnGap) + f.fields[i]
	}
	f.fields[notesField] = width - used - len(columnGap)
	needs := min(t.fields[notesField], minNotes)
	return f, f.fields[notesField] >= needs
}

// row lays out a line with fields as a row of the table: its head after indent
// in the ID's column, and each field in its own. A value wider than its column
// wraps inside it, so the row takes more than one line.
func (t table) row(indent string, l statusLine) []string {
	cells := [][]string{hang(indent, l.head, t.id)}
	for i, f := range l.fields {
		cells = append(cells, strings.Split(lipgloss.Wrap(t.value(f), t.fields[i], ""), "\n"))
	}
	widths := append([]int{t.id}, t.fields[:]...)
	var blocks []string
	for i, cell := range cells {
		if i > 0 {
			blocks = append(blocks, columnGap)
		}
		blocks = append(blocks, lipgloss.NewStyle().Width(widths[i]).Render(strings.Join(cell, "\n")))
	}
	lines := strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, blocks...), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
}

// card lays out a line with fields for a screen too narrow for the table, as
// the live view words them: its head after indent, then each field that has a
// value, on a line of its own under the head as "heading: value". When the head doesn't fit, the indent's
// spaces give way first, and only a head wider than the screen is cut.
func card(indent string, l statusLine, width int) []string {
	// Drop as many of the indent's leading spaces as the head is too wide by.
	over := max(0, lipgloss.Width(indent+l.head)-width)
	spaces := len(indent) - len(strings.TrimLeft(indent, " "))
	head := indent[min(spaces, over):] + l.head
	lines := []string{cutWithEllipsis(head, width)}
	under := strings.Repeat(" ", lipgloss.Width(indent)+2)
	for i, f := range l.fields {
		if f.live != "" {
			lines = append(lines, hang(under, columns[i].heading+": "+f.live, width)...)
		}
	}
	return lines
}

// hang wraps text to fit width after indent. The lines it wraps onto start two
// columns further in, so they read as part of the line above. To keep those
// two columns on the first line, it wraps the text once to find where the first
// line ends, then wraps the rest two columns narrower.
func hang(indent, text string, width int) []string {
	room := max(1, width-lipgloss.Width(indent))
	first, _, wrapped := strings.Cut(lipgloss.Wrap(text, room, ""), "\n")
	lines := []string{indent + first}
	if !wrapped {
		return lines
	}
	under := strings.Repeat(" ", lipgloss.Width(indent)+2)
	// Wrapping only puts line breaks in, so the first line starts the text.
	rest := strings.TrimLeft(strings.TrimPrefix(text, first), " ")
	for _, l := range strings.Split(lipgloss.Wrap(rest, max(1, room-2), ""), "\n") {
		lines = append(lines, under+l)
	}
	return lines
}

// cutWithEllipsis cuts s to width, ending it with … when it's cut.
func cutWithEllipsis(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return cut(s, max(0, width-1)) + "…"
}
