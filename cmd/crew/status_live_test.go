package main

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// says is a line's fields, as the plain text has them, for a test that looks
// for a value in any of them.
func says(l statusLine) string { return strings.Join(texts(l.fields), "  ") }

// showsLive is a line's fields as the live view shows them without color.
func showsLive(l statusLine) string {
	var lives []string
	for _, f := range l.fields {
		lives = append(lives, uncolored(f.live))
	}
	return strings.Join(lives, "  ")
}

// liveOn is the live view showing r, as it is once its first report comes in.
func liveOn(r statusReport) *liveStatus {
	m := newLiveStatus(statusFeed{})
	m.Update(reportBuilt{report: r})
	return m
}

// keyCodes are the keys press takes by name.
var keyCodes = map[string]rune{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"enter": tea.KeyEnter, "end": tea.KeyEnd, "home": tea.KeyHome, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown}

// press sends keys to the view, as a person types them, and returns the last
// key's command.
func press(m *liveStatus, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		key := tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		if code, ok := keyCodes[k]; ok {
			key = tea.KeyPressMsg{Code: code}
		}
		_, cmd = m.Update(key)
	}
	return cmd
}

// lineOf is where the line that ends with text is on show, or -1.
func lineOf(m *liveStatus, text string) int {
	return slices.IndexFunc(m.shown, func(l shownLine) bool { return strings.HasSuffix(l.head, text) })
}

// goTo puts the cursor on the line that ends with text.
func goTo(t *testing.T, m *liveStatus, text string) {
	t.Helper()
	i := lineOf(m, text)
	if i < 0 {
		t.Fatalf("no line %q on show", text)
	}
	m.moveTo(i)
}

// openAll opens every line.
func openAll(m *liveStatus) {
	_, keys := treeOf(m.lines)
	for _, k := range keys {
		m.open[k] = true
	}
	m.layOut()
}

// screen is what the view shows, a line each, with the cursor's style left in.
func screen(m *liveStatus) []string {
	return strings.Split(m.View().Content, "\n")
}

// body is the screen lines that show the report's lines, between the headings
// and the strip.
func body(m *liveStatus) []string {
	lines := screen(m)
	return lines[len(m.headings) : len(lines)-1-len(m.strip())]
}

// stripOn is the screen lines the strip takes, above the bar.
func stripOn(m *liveStatus) []string {
	lines := screen(m)
	return lines[len(lines)-1-len(m.strip()) : len(lines)-1]
}

// ansiCodes are the escapes that style the view: its colors and the cursor's
// line.
var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// uncolored is s as a screen without color shows it.
func uncolored(s string) string { return ansiCodes.ReplaceAllString(s, "") }

// uncoloredLines are lines as a screen without color shows them.
func uncoloredLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		out = append(out, uncolored(l))
	}
	return out
}

// words is a line's words, without the tree's markers or the cursor's style.
func words(line string) string {
	line = uncolored(line)
	return strings.Join(strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '▸' || r == '▾'
	}), " ")
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// settle runs the commands the view gives one at a time, and hands the view
// their messages, until it gives no more.
func settle(m *liveStatus, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		cmd, queue = queue[0], queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		_, then := m.Update(msg)
		queue = append(queue, then)
	}
}

// inBackground runs the commands the view gives each on its own, as bubbletea
// does, and sends their messages to msgs.
func inBackground(cmd tea.Cmd, msgs chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				inBackground(c, msgs)
			}
			return
		}
		msgs <- msg
	}()
}

// next is the next message a command in the background sends.
func next(t *testing.T, msgs <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-msgs:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("no command the view gave has finished")
		return nil
	}
}

// release lets one of the feed's builds finish.
func release(t *testing.T, letBuild chan<- struct{}) {
	t.Helper()
	select {
	case letBuild <- struct{}{}:
	case <-time.After(10 * time.Second):
		t.Fatal("the view isn't building a report")
	}
}

// tenOClock is when the tests' views open.
var tenOClock = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func TestLiveStatusShowsWhatTheTextShows(t *testing.T) {
	r := readReport(fullStatusLedger(t), "p")
	r.ReadOnlyOn = "laptop"
	m := liveOn(r)
	m.Update(tea.WindowSizeMsg{Width: 400, Height: 200})
	openAll(m)
	var view []string
	for _, l := range screen(m) {
		view = append(view, words(l))
	}
	// Every line the text prints is a line of the view, as the text words it:
	// the headings, each project's and goal's line, every task with its status,
	// worker, PR, model, effort and notes, the counts and what waits in the
	// other projects.
	headings := len(m.headings)
	lines := slices.Clone(view[:headings])
	for _, l := range m.shown {
		lines = append(lines, words(l.head+" "+says(l.statusLine)))
	}
	for _, l := range strings.Split(plainText(r), "\n") {
		if l != "" && !slices.Contains(lines, words(l)) {
			t.Errorf("the text's line %q isn't in the view:\n%s", l, strings.Join(screen(m), "\n"))
		}
	}
	// Every line of the tree, the ones the text only counts too, with its
	// fields as the live view words them, under the line it's under in the
	// tree. At this width, each takes one line, under the headings. A task's
	// status, worker, model and effort read as the text has them, and its PR
	// starts with the text's number.
	parents, _ := treeOf(reportLines(r))
	for i, l := range m.shown {
		if want := words(l.head + " " + showsLive(l.statusLine)); view[headings+i] != want {
			t.Errorf("the view shows %q where the tree has %q", view[headings+i], want)
		}
		for _, f := range []int{statusField, workerField, modelField, effortField} {
			if l.hasFields() && l.fields[f].live != l.fields[f].text {
				t.Errorf("the view shows %s's %s as %q, where the text has %q", l.head, columns[f].heading, l.fields[f].live, l.fields[f].text)
			}
		}
		if l.hasFields() && !strings.HasPrefix(l.fields[prField].live, l.fields[prField].text) {
			t.Errorf("the view shows %s's PR as %q, where the text has %q", l.head, l.fields[prField].live, l.fields[prField].text)
		}
		parent := ""
		if j := m.above(i); j >= 0 {
			parent = m.shown[j].head
		}
		if want := parents[l.head]; parent != want {
			t.Errorf("%q is under %q, want %q", l.head, parent, want)
		}
	}
	if len(m.shown) != len(parents) {
		t.Errorf("the view shows %d lines, want the tree's %d", len(m.shown), len(parents))
	}
}

func TestLiveStatusListsAGoalsTasksInTheTextsOrder(t *testing.T) {
	r := buildReport(goalLedger(t, boardsGoal...), []string{"p"}, false, statusReads{})
	m := liveOn(r)
	m.Update(tea.WindowSizeMsg{Width: 400, Height: 200})
	openAll(m)
	goalTasks := map[string]bool{}
	for _, task := range boardsGoal {
		goalTasks[strings.Fields(task)[0]] = true
	}
	// The goal's tasks a listing names, in its order.
	tasks := func(lines []string) []string {
		var ids []string
		for _, l := range lines {
			if f := strings.Fields(strings.ReplaceAll(words(l), "└", "")); len(f) > 0 && goalTasks[f[0]] {
				ids = append(ids, f[0])
			}
		}
		return ids
	}
	want := []string{"160", "259", "195", "235", "165", "236", "170", "242", "166", "148", "243", "244"}
	if text := tasks(strings.Split(plainText(r), "\n")); !slices.Equal(text, want) {
		t.Errorf("the text lists the goal's tasks as %v, want %v", text, want)
	}
	if view := tasks(screen(m)); !slices.Equal(view, want) {
		t.Errorf("the view lists the goal's tasks as %v, want %v", view, want)
	}
}

func TestLiveStatusFoldsGoalsStacksAndWhatTheTextCounts(t *testing.T) {
	m := liveOn(readReport(statusLedger(t), "p"))
	for _, folded := range []string{"007-next", "008-next", "012-idea"} {
		if lineOf(m, folded) >= 0 {
			t.Errorf("%s is on show before its fold is opened", folded)
		}
	}
	goTo(t, m, "… (2 more)")
	press(m, "right")
	goTo(t, m, "1 draft(s)  (crew list --project p --status draft)")
	press(m, "enter")
	for _, folded := range []string{"007-next", "008-next", "012-idea"} {
		if lineOf(m, folded) < 0 {
			t.Errorf("%s isn't on show once its fold is opened", folded)
		}
	}

	goTo(t, m, "goal 001-launch")
	press(m, "left")
	if lineOf(m, "001-in-flight") >= 0 || lineOf(m, "009-parked") >= 0 {
		t.Error("a closed goal still shows its tasks")
	}
	press(m, "right")
	if lineOf(m, "001-in-flight") < 0 || lineOf(m, "007-next") < 0 {
		t.Error("an opened goal doesn't show its tasks as they were")
	}

	goTo(t, m, "010-parent")
	press(m, "h")
	if lineOf(m, "011-child") >= 0 {
		t.Error("a closed stack still shows the task stacked on it")
	}
	press(m, "h")
	if got := m.shown[m.cursor].head; got != "p" {
		t.Errorf("left on a closed line goes to %q, want the line it's under", got)
	}
	goTo(t, m, "010-parent")
	press(m, "space")
	if lineOf(m, "011-child") < 0 {
		t.Error("an opened stack doesn't show the task stacked on it")
	}
}

func TestLiveStatusKeepsItsPlaceAcrossReports(t *testing.T) {
	c := statusLedger(t)
	m := liveOn(readReport(c, "p"))
	goTo(t, m, "goal 001-launch")
	press(m, "left")
	goTo(t, m, "014-later")
	before := m.cursor
	reportTask(t, c, "p", "010-also", false, "status: working", "base: main")
	m.Update(reportBuilt{report: readReport(c, "p")})
	if got := m.shown[m.cursor].head; got != "014-later" || m.cursor != before+1 {
		t.Errorf("after a task came in above it, the cursor is on %q at %d, want 014-later at %d", got, m.cursor, before+1)
	}
	if lineOf(m, "001-in-flight") >= 0 {
		t.Error("a closed goal opened again with a new report")
	}

	at := m.cursor
	reportTask(t, c, "p", "014-later", false, "status: merged")
	m.Update(reportBuilt{report: readReport(c, "p")})
	if m.cursor != at || m.shown[m.cursor].head != "015-earlier" {
		t.Errorf("once its task was gone, the cursor went to %q at %d, want the next line, at %d", m.shown[m.cursor].head, m.cursor, at)
	}
}

// TestLiveStatusStaysUsableWhileItsFeedHangs runs the view on a feed whose
// every part hangs until the test lets a build finish.
func TestLiveStatusStaysUsableWhileItsFeedHangs(t *testing.T) {
	c := statusLedger(t)
	hang, letBuild := make(chan struct{}), make(chan struct{})
	defer close(hang)
	var pulls, listings, branchesRead, reads atomic.Int32
	feed := feedOf(c, "p", false)
	build := feed.build
	feed.pullLedger = func() { pulls.Add(1); <-hang }
	feed.listWorkers = func() *workerList { listings.Add(1); <-hang; return nil }
	feed.readBranches = func() branchReads { branchesRead.Add(1); <-hang; return nil }
	feed.readPRs = func() prReads { reads.Add(1); <-hang; return nil }
	feed.build = func(r statusReads) (statusReport, error) { <-letBuild; return build(r) }
	clock := tenOClock
	m := newLiveStatus(feed)
	m.now = func() time.Time { return clock }
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	msgs := make(chan tea.Msg, 8)

	inBackground(m.Init(), msgs)
	if got := m.bar(); !strings.HasPrefix(got, "loading…") {
		t.Errorf("before its first report, the view says %q, want loading…", got)
	}
	press(m, "j", "down", "l", "enter", "h")
	if !quits(press(m, "q")) {
		t.Error("q doesn't quit before the first report")
	}

	release(t, letBuild)
	m.Update(next(t, msgs))
	if lineOf(m, "010-parent") < 0 {
		t.Fatal("the first report isn't on show")
	}
	clock = clock.Add(buildEvery)
	_, cmd := m.Update(beat{})
	if !m.building.running {
		t.Fatal("the view doesn't build a report when one is due")
	}
	inBackground(cmd, msgs)
	goTo(t, m, "goal 001-launch")
	press(m, "j")
	if got := m.shown[m.cursor].head; got != "in flight" {
		t.Errorf("down went to %q while a report was being built", got)
	}
	press(m, "h", "h")
	if lineOf(m, "001-in-flight") >= 0 || m.shown[m.cursor].head != "goal 001-launch" {
		t.Error("the goal didn't close while a report was being built")
	}
	if !slices.ContainsFunc(screen(m), func(l string) bool { return strings.Contains(l, "010-parent") }) {
		t.Error("the view doesn't show the last report while the next is built")
	}
	// A refresh, and a beat long after every job fell due again, start none of
	// them a second time while the first is under way.
	refresh := press(m, "r")
	clock = clock.Add(time.Hour)
	if _, later := m.Update(beat{}); refresh != nil || later != nil {
		t.Error("the view started a job while the same one was under way")
	}
	if pulls.Load() != 1 || listings.Load() != 1 || branchesRead.Load() != 1 || reads.Load() != 1 {
		t.Errorf("the view pulled the ledger %d times, listed the workers %d times, read the branches %d times and read GitHub %d times, want once each",
			pulls.Load(), listings.Load(), branchesRead.Load(), reads.Load())
	}
	if !quits(press(m, "q")) {
		t.Error("q doesn't quit while a report is being built")
	}
	release(t, letBuild)
}

// TestLiveStatusKeepsBuildingWhileItsReadsHang changes a task while the view's
// first reads (GitHub, the workers and the branches) and its pull of the ledger
// hang, and sees it on screen within 5 seconds.
func TestLiveStatusKeepsBuildingWhileItsReadsHang(t *testing.T) {
	c := statusLedger(t)
	hang := make(chan struct{})
	defer close(hang)
	feed := feedOf(c, "p", false)
	feed.pullLedger = func() { <-hang }
	feed.listWorkers = func() *workerList { <-hang; return nil }
	feed.readBranches = func() branchReads { <-hang; return nil }
	feed.readPRs = func() prReads { <-hang; return nil }
	clock := tenOClock
	m := newLiveStatus(feed)
	m.now = func() time.Time { return clock }
	msgs := make(chan tea.Msg, 8)
	inBackground(m.Init(), msgs)
	m.Update(next(t, msgs)) // the first report; the reads and the pull still hang

	reportTask(t, c, "p", "010-parent", false, "status: needs-input", "base: main", "branch: agent/parent")
	for range 5 {
		clock = clock.Add(time.Second)
		_, cmd := m.Update(beat{})
		if cmd != nil {
			inBackground(cmd, msgs)
			m.Update(next(t, msgs))
		}
	}
	if i := lineOf(m, "010-parent"); i < 0 || m.shown[i].fields[statusField].text != "needs-input" {
		t.Error("5 seconds after a task changed, while the reads and the pull hang, the view doesn't show it")
	}
}

func TestLiveStatusReadsTheSharedSettingsForEachReport(t *testing.T) {
	c := statusLedger(t)
	writeFile(t, c, "MAX_WORKERS=3\nGOAL_MAX_IN_FLIGHT=2\n", "config", "defaults.env")
	feed := feedOf(c, "p", false)
	feed.pullLedger = func() {}
	feed.readPRs = func() prReads { return nil }
	m := newLiveStatus(feed)
	settle(m, m.Init())
	writeFile(t, c, "MAX_WORKERS=6\n", "config", "crew.env")
	settle(m, press(m, "r"))
	if l := m.shown[0].head; !strings.Contains(l, "1/6") {
		t.Errorf("after MAX_WORKERS went from 3 to 6 in crew.env, the view says %q", l)
	}
}

func TestLiveStatusSaysWhenItCantReadTheSharedSettings(t *testing.T) {
	c := statusLedger(t)
	writeFile(t, c, "MAX_WORKERS=3\nGOAL_MAX_IN_FLIGHT=2\n", "config", "defaults.env")
	feed := feedOf(c, "p", false)
	feed.pullLedger = func() {}
	feed.readPRs = func() prReads { return nil }
	m := newLiveStatus(feed)
	m.width = 200
	settle(m, m.Init())
	writeFile(t, c, "MAX_WORKERS = 6\n", "config", "crew.env")
	settle(m, press(m, "r"))
	if got := m.bar(); !strings.Contains(got, "crew.env:1") {
		t.Errorf("with a line crew.env can't parse, the bar says %q, want it to name crew.env:1", got)
	}
	writeFile(t, c, "MAX_WORKERS=6\n", "config", "crew.env")
	settle(m, press(m, "r"))
	if got := m.bar(); strings.Contains(got, "crew.env") {
		t.Errorf("once crew.env parses again, the bar still says %q", got)
	}
}

func TestLiveStatusFitsTheScreen(t *testing.T) {
	m := liveOn(readReport(fullStatusLedger(t), "p"))
	openAll(m)
	for _, width := range []int{60, 100, 160} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		for _, keys := range [][]string{{"end"}, {"pgup"}, {"home"}, {"pgdown"}, {"G", "k", "k"}} {
			press(m, keys...)
			lines := screen(m)
			if len(lines) != 12 {
				t.Errorf("at width %d, after %v, the view is %d lines high, want 12", width, keys, len(lines))
			}
			for _, l := range lines {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("at width %d, a line is %d wide: %q", width, w, l)
				}
			}
			if !onScreen(m) {
				t.Errorf("at width %d, after %v, the cursor's line %q isn't all on screen:\n%s", width, keys, m.shown[m.cursor].head, strings.Join(lines, "\n"))
			}
		}
	}
}

// onScreen: every line the cursor's line is drawn on is on the screen, between
// the headings and the strip.
func onScreen(m *liveStatus) bool {
	var cursor []string
	for _, l := range body(m) {
		if strings.Contains(l, "\x1b[7m") { // the cursor's style: reversed
			cursor = append(cursor, strings.TrimRight(uncolored(l), " "))
		}
	}
	return slices.Equal(cursor, uncoloredLines(m.shown[m.cursor].drawn))
}

// variedReport is fullStatusLedger's report with values of every length a
// column meets: a status longer than its column, a worker idle while a
// background command it started runs, and a goal whose lead is idle and whose slots are
// full. Its rows also have a long merge check, a stack and a goal's group.
func variedReport(t *testing.T) statusReport {
	c := fullStatusLedger(t)
	reportTask(t, c, "p", "016-feedback", false, "status: addressing-feedback", "pr: https://github.com/o/r/pull/1234", "model: opus", "effort: medium")
	r := readReport(c, "p")
	p := &r.Projects[slices.IndexFunc(r.Projects, func(p projectReport) bool { return p.Name == "p" })]
	g := &p.Goals[0]
	g.InFlight = slots{2, 2}
	inFlight := &g.Groups[0].Rows[0]
	if inFlight.Task.ID() != "001-in-flight" {
		t.Fatalf("the goal's first row is %s, want 001-in-flight", inFlight.Task.ID())
	}
	inFlight.Worker = Agent{ID: "w1", State: "working", PID: true, TurnEnded: true, Background: []string{"bin/rails server"}}
	return r
}

// squeezed is s without its spaces, to compare a value with the same value
// wrapped onto more than one line.
func squeezed(s string) string { return strings.ReplaceAll(s, " ", "") }

// cellsOf reads a row of the table back from the lines it's drawn on: the
// text in each column, uncolored, with its lines joined, the ID's first.
func cellsOf(tb table, drawn []string) []string {
	widths := append([]int{tb.id}, tb.fields[:]...)
	cells := make([]string, len(widths))
	for _, line := range drawn {
		runes := []rune(uncolored(line))
		start := 0
		for i, w := range widths {
			if end := min(len(runes), start+w); start < end {
				cells[i] += string(runes[start:end]) + " "
			}
			start += w + len(columnGap)
		}
	}
	for i := range cells {
		cells[i] = strings.Join(strings.Fields(cells[i]), " ")
	}
	return cells
}

func TestTheCappedColumnsFitIn120(t *testing.T) {
	wide := table{id: 200}
	for i := range wide.fields {
		wide.fields[i] = 200
	}
	if _, inColumns := wide.fit(120); !inColumns {
		t.Fatal("a report whose every column is past its cap should still fit in columns at 120")
	}
}

func TestLiveStatusLinesUpItsColumns(t *testing.T) {
	m := liveOn(variedReport(t))
	openAll(m)
	for _, width := range []int{120, 160} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 60})
		if !m.inColumns {
			t.Fatalf("at width %d, the rows are cards", width)
		}
		want := append([]string{idHeading}, texts(headings().fields)...)
		if got := cellsOf(m.table, m.headings); !slices.Equal(got, want) {
			t.Errorf("at width %d, the headings read %q, want %q", width, got, want)
		}
		// Every value starts in its column and stays inside it, on every line
		// of its row: read back column by column, each row has its values.
		for _, l := range m.shown {
			if !l.hasFields() {
				continue
			}
			got := cellsOf(m.table, l.drawn)
			if !strings.HasSuffix(got[0], words(l.head)) {
				t.Errorf("at width %d, the ID column reads %q, want %q", width, got[0], l.head)
			}
			for i, f := range l.fields {
				if squeezed(got[i+1]) != squeezed(uncolored(f.live)) {
					t.Errorf("at width %d, %s's %s column reads %q, want %q:\n%s", width, l.head, columns[i].heading, got[i+1], f.live, strings.Join(l.drawn, "\n"))
				}
			}
		}
		for id, field := range map[string]string{"016-feedback": "addressing-feedback", "001-in-flight": "idle (bin/rails server)"} {
			if l := m.shown[lineOf(m, id)]; len(l.drawn) < 2 {
				t.Errorf("at width %d, %s shows %q on one line: %q", width, id, field, l.drawn)
			}
		}
	}
}

func TestLiveStatusShowsEveryFieldAtAnyWidth(t *testing.T) {
	m := liveOn(variedReport(t))
	openAll(m)
	for _, width := range []int{60, 100, 160} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 200})
		for _, line := range screen(m) {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("at width %d, a line is %d wide: %q", width, w, line)
			}
		}
		// Every line shows its whole head and every field, on lines that fit
		// without cutting: in its column, or as "heading: value" on a card.
		for _, l := range m.shown {
			for _, line := range l.drawn {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("at width %d, %s is drawn %d wide: %q", width, l.head, w, line)
				}
			}
			drawn := squeezed(strings.Join(l.drawn, ""))
			if !strings.Contains(drawn, squeezed(l.head)) {
				t.Errorf("at width %d, %q isn't all shown: %q", width, l.head, l.drawn)
			}
			for i, f := range l.fields {
				switch {
				case m.inColumns:
					if got := cellsOf(m.table, l.drawn)[i+1]; squeezed(got) != squeezed(uncolored(f.live)) {
						t.Errorf("at width %d, %s shows %q, want %q", width, l.head, got, f.live)
					}
				case f.live != "" && !strings.Contains(drawn, squeezed(columns[i].heading+": "+f.live)):
					t.Errorf("at width %d, %s doesn't show %s %q: %q", width, l.head, columns[i].heading, f.live, l.drawn)
				}
			}
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 200})
	if m.inColumns {
		t.Error("at width 60, the rows are in columns")
	}
	if got := m.shown[lineOf(m, "001-in-flight")].drawn; !slices.Equal(got, []string{
		"        001-in-flight",
		"          status: working",
		"          worker: idle (bin/rails server)",
	}) {
		t.Errorf("at width 60, 001-in-flight's card is %q", got)
	}

	// On a screen narrower than an ID with its indent, the indent gives way,
	// and only an ID wider than the screen is cut.
	m.Update(tea.WindowSizeMsg{Width: 12, Height: 200})
	for id, want := range map[string]string{"010-parent": "▾ 010-parent", "goal 001-launch": "▾ goal 001-…"} {
		if got := m.shown[lineOf(m, id)].drawn[0]; got != want {
			t.Errorf("at width 12, %s's card starts %q, want %q", id, got, want)
		}
	}
}

// A card wraps a field whose words are styled, as the user's are, showing each
// word once.
func TestACardWrapsStyledWordsOnce(t *testing.T) {
	l := statusLine{head: "101-merge-now", fields: make([]wording, len(columns))}
	l.fields[notesField] = joinNotes([]wording{
		forUser("merge now", "merge"),
		forUser("2 finding(s) left for you", "2 findings"),
		forUser("no risk tier: read the diff", "no risk tier"),
	})
	want := "notes: merge 2 findings no risk tier"
	for width := 12; width <= len(want)+2; width++ {
		got := card("", l, width)
		if under := uncolored(strings.Join(got[1:], " ")); squeezed(under) != squeezed(want) {
			t.Errorf("at width %d, the card says %q, want %q", width, under, want)
		}
		for _, line := range got {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("at width %d, a line of the card is %d wide: %q", width, w, line)
			}
		}
	}
}

func TestLiveStatusPinsTheHeadingsOverRowsUnderAFold(t *testing.T) {
	c := statusLedger(t)
	writeFile(t, c, "", "projects", "s", "project.env")
	reportTask(t, c, "s", "001-idea", false, "status: draft")
	m := liveOn(readReport(c, "s"))
	openAll(m)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	if lineOf(m, "001-idea") < 0 || !m.inColumns {
		t.Fatalf("001-idea isn't shown in columns:\n%s", strings.Join(screen(m), "\n"))
	}
	if len(m.headings) == 0 {
		t.Errorf("no headings over the drafts' rows:\n%s", strings.Join(screen(m), "\n"))
	}
}

func TestLiveStatusKeepsTheHeadingsOnScreen(t *testing.T) {
	m := liveOn(variedReport(t))
	openAll(m)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 8})
	want := words(strings.Join(append([]string{idHeading}, texts(headings().fields)...), " "))
	scrolled := false
	for range m.shown {
		if m.shown[m.cursor].hasFields() {
			if got := words(screen(m)[0]); got != want {
				t.Errorf("with the cursor on %s, the screen starts %q, want the headings", m.shown[m.cursor].head, got)
			}
		}
		scrolled = scrolled || m.top > 0
		press(m, "j")
	}
	if !scrolled {
		t.Error("the report fits on the screen, so nothing scrolled")
	}
}

// unseen is the lines of the report never on screen while keys are pressed,
// over and over until the screen stops changing. A line counts as seen only
// at its own place in the report, so a line with the same text as another,
// such as a card's "status: ready", doesn't stand in for it.
func unseen(m *liveStatus, keys ...string) []string {
	var report []string // every shown line's drawn lines, top to bottom
	for _, l := range m.shown {
		report = append(report, uncoloredLines(l.drawn)...)
	}
	seen := make([]bool, len(report))
	for last := ""; ; {
		lines := screen(m)
		for i, l := range body(m) {
			if n := m.top + i; n < len(report) && strings.TrimRight(uncolored(l), " ") == report[n] {
				seen[n] = true
			}
		}
		if now := strings.Join(lines, "\n"); now == last {
			break
		} else {
			last = now
		}
		press(m, keys...)
	}
	var missed []string
	for n, line := range report {
		if !seen[n] {
			missed = append(missed, line)
		}
	}
	return missed
}

// reversedCells counts a screen line's cells, and how many of them show
// reversed, following the styles its escapes turn on and off.
func reversedCells(line string) (reversed, cells int) {
	on, at := false, 0
	count := func(text string) {
		w := lipgloss.Width(text)
		cells += w
		if on {
			reversed += w
		}
	}
	for _, code := range ansiCodes.FindAllStringIndex(line, -1) {
		count(line[at:code[0]])
		params := strings.Split(line[code[0]+2:code[1]-1], ";")
		switch {
		case slices.Equal(params, []string{""}) || slices.Contains(params, "0") || slices.Contains(params, "27"):
			on = false
		case slices.Contains(params, "7"):
			on = true
		}
		at = code[1]
	}
	count(line[at:])
	return reversed, cells
}

// The cursor's row shows reversed across the screen, on every line it takes:
// its colored marks, and what comes after them.
func TestLiveStatusHighlightsTheCursorsRowAcrossTheScreen(t *testing.T) {
	m := liveOn(variedReport(t))
	openAll(m)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
	m.moveTo(lineOf(m, "013-risky"))
	drawn := m.shown[m.cursor].drawn
	if !ansiCodes.MatchString(strings.Join(drawn, "")) {
		t.Fatalf("013-risky's row has nothing colored: %q", drawn)
	}
	highlighted := 0
	for _, line := range screen(m) {
		if !strings.Contains(line, "\x1b[7m") {
			continue
		}
		highlighted++
		if reversed, cells := reversedCells(line); reversed != m.width || cells != m.width {
			t.Errorf("%d of the line's %d cells are reversed, want all %d: %q", reversed, cells, m.width, line)
		}
	}
	if highlighted != len(drawn) {
		t.Errorf("%d lines are highlighted, want 013-risky's %d", highlighted, len(drawn))
	}
}

// stripSays is what the strip says, its lines joined.
func stripSays(m *liveStatus) string { return words(strings.Join(stripOn(m), " ")) }

// notesOf is what the cursor's line's notes say in the plain text, or nothing
// for a line without notes.
func notesOf(m *liveStatus) string {
	if l := m.shown[m.cursor]; l.hasFields() {
		return words(l.fields[notesField].text)
	}
	return ""
}

// The strip words the cursor's line's notes as the plain text does, wrapped
// to the screen: on every line, in columns and on cards.
func TestLiveStatusStripWordsTheCursorsNotesAsTheTextDoes(t *testing.T) {
	r := variedReport(t)
	text := strings.Split(plainText(r), "\n")
	for _, width := range []int{120, 60} {
		m := liveOn(r)
		openAll(m)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 60})
		wrapped := false
		for i := range m.shown {
			m.moveTo(i)
			got := stripOn(m)
			if strip := stripSays(m); strip != notesOf(m) {
				t.Errorf("at width %d, on %s the strip says %q, want %q", width, m.shown[i].head, strip, notesOf(m))
			}
			// A row the text prints ends with the same notes.
			at := slices.IndexFunc(text, func(l string) bool { return strings.HasPrefix(words(l), words(m.shown[i].head)+" ") })
			if m.shown[i].hasFields() && at >= 0 && !strings.HasSuffix(words(text[at]), stripSays(m)) {
				t.Errorf("at width %d, the text's line %q doesn't end with the strip's %q", width, text[at], stripSays(m))
			}
			for _, line := range got {
				if lipgloss.Width(line) > width {
					t.Errorf("at width %d, a line of the strip is %d wide: %q", width, lipgloss.Width(line), line)
				}
			}
			wrapped = wrapped || len(got) > 1
		}
		if !wrapped && width == 60 {
			t.Errorf("at width %d, no line's notes wrap in the strip", width)
		}
		if inColumns := width == 120; m.inColumns != inColumns {
			t.Errorf("at width %d, rows in columns: %v, want %v", width, m.inColumns, inColumns)
		}
	}
}

// The strip follows the cursor as it moves, and words the notes of the next
// report once it's built.
func TestLiveStatusStripFollowsTheCursorAndTheReport(t *testing.T) {
	c := statusLedger(t)
	m := liveOn(readReport(c, "p"))
	openAll(m)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	goTo(t, m, "014-later")
	if got, want := stripSays(m), notesOf(m); got != want || want == "" {
		t.Fatalf("on 014-later, the strip says %q, want %q", got, want)
	}
	for _, key := range []string{"j", "k"} {
		press(m, key)
		if got := stripSays(m); got != notesOf(m) {
			t.Errorf("after %s onto %s, the strip says %q, want %q", key, m.shown[m.cursor].head, got, notesOf(m))
		}
	}
	reportTask(t, c, "p", "014-later", true, "status: working", "base: main")
	m.Update(reportBuilt{report: readReport(c, "p")})
	if got := stripSays(m); got != "1 open question(s)" {
		t.Errorf("once 014-later asks, the strip says %q, want its question", got)
	}
}

// However tall the strip grows on a short screen, the cursor's line stays on
// screen, as much of it as fits, and every line is reachable, a line at a time
// or a page at a time.
func TestLiveStatusReachesEveryLineAsTheStripGrows(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 5}, {Width: 120, Height: 7}, {Width: 60, Height: 5}, {Width: 60, Height: 9}} {
		m := liveOn(variedReport(t))
		openAll(m)
		m.Update(size)
		grew := false
		for _, keys := range [][]string{{"j"}, {"k"}, {"pgdown"}, {"pgup"}} {
			if missed := unseen(m, keys...); len(missed) > 0 {
				t.Errorf("at %dx%d, %s never shows %q", size.Width, size.Height, keys[0], missed)
			}
		}
		for i := range m.shown {
			m.moveTo(i)
			grew = grew || len(m.strip()) > 1
			if len(m.shown[i].drawn) <= m.bodyHeight() && !onScreen(m) {
				t.Errorf("at %dx%d, %s isn't all on screen:\n%s", size.Width, size.Height, m.shown[i].head, strings.Join(screen(m), "\n"))
			}
			if got := len(screen(m)); got != size.Height {
				t.Errorf("at %dx%d, the screen is %d lines", size.Width, size.Height, got)
			}
		}
		if !grew && size.Width == 60 {
			t.Errorf("at %dx%d, the strip never takes more than a line", size.Width, size.Height)
		}
	}
}

// On a screen too short for a line's notes, ↓ scrolls through them in the
// strip before it moves on, and ↑ scrolls back, so every word can be read.
func TestLiveStatusScrollsThroughNotesTooLongForTheStrip(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 60, Height: 3}, {Width: 40, Height: 4}} {
		m := liveOn(variedReport(t))
		openAll(m)
		m.Update(size)
		overflowed := false
		for i := range m.shown {
			m.moveTo(i)
			want := m.notes()
			overflowed = overflowed || len(want) > len(m.strip())
			// readAll presses key until the cursor leaves line i, and says
			// whether the strip showed every line of its notes on the way.
			readAll := func(key string) bool {
				seen := map[string]bool{}
				for m.cursor == i {
					for _, l := range stripOn(m) {
						seen[strings.TrimRight(strings.TrimSuffix(strings.TrimRight(uncolored(l), " "), "…"), " ")] = true
					}
					press(m, key)
				}
				for _, l := range want {
					if !seen[strings.TrimRight(l, " ")] {
						return false
					}
				}
				return true
			}
			if i+1 < len(m.shown) && !readAll("j") {
				t.Errorf("at %dx%d, j leaves %s before the strip shows all of %q", size.Width, size.Height, m.shown[i].head, want)
			}
			if i > 0 {
				m.moveTo(i)
				m.stripTop = len(want) // as if scrolled down to the notes' end
				if !readAll("k") {
					t.Errorf("at %dx%d, k leaves %s before the strip shows all of %q", size.Width, size.Height, m.shown[i].head, want)
				}
			}
		}
		if !overflowed {
			t.Errorf("at %dx%d, no line's notes are too long for the strip", size.Width, size.Height)
		}
	}
}

func TestLiveStatusScrollsThroughARowTallerThanTheScreen(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 3}, {Width: 60, Height: 4}} {
		m := liveOn(variedReport(t))
		openAll(m)
		m.Update(size)
		if missed := unseen(m, "j"); len(missed) > 0 {
			t.Errorf("at %dx%d, j never shows %q", size.Width, size.Height, missed)
		}
		if missed := unseen(m, "k"); len(missed) > 0 {
			t.Errorf("at %dx%d, k never shows %q", size.Width, size.Height, missed)
		}
	}
}

func TestLiveStatusPagesThroughEveryLine(t *testing.T) {
	for _, width := range []int{60, 120} {
		m := liveOn(variedReport(t))
		openAll(m)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		if missed := unseen(m, "pgdown"); len(missed) > 0 {
			t.Errorf("at width %d, pgdown never shows %q", width, missed)
		}
		if m.cursor != len(m.shown)-1 {
			t.Errorf("at width %d, pgdown stops on %q, not the last line", width, m.shown[m.cursor].head)
		}
		if missed := unseen(m, "pgup"); len(missed) > 0 {
			t.Errorf("at width %d, pgup never shows %q", width, missed)
		}
		if m.cursor != 0 {
			t.Errorf("at width %d, pgup stops on %q, not the first line", width, m.shown[m.cursor].head)
		}
	}
}

func TestLiveStatusMovesAWrappedRowAsOne(t *testing.T) {
	m := liveOn(variedReport(t))
	openAll(m)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 7})
	if got := len(m.shown[lineOf(m, "016-feedback")].drawn); got < 2 {
		t.Fatalf("016-feedback takes %d line(s), want a row that wraps", got)
	}
	for _, key := range []string{"j", "k"} {
		step := map[string]int{"j": 1, "k": -1}[key]
		for {
			from := m.cursor
			press(m, key)
			if m.cursor == from {
				break
			}
			if m.cursor != from+step {
				t.Fatalf("%s from %q went %d lines", key, m.shown[from].head, m.cursor-from)
			}
			if !onScreen(m) {
				t.Errorf("after %s onto %q, its row isn't all on screen:\n%s", key, m.shown[m.cursor].head, strings.Join(screen(m), "\n"))
			}
		}
	}
}

// mouseLayouts are the sizes the mouse tests run at: the table, and the cards a
// narrower screen shows instead, whose lines are taller.
var mouseLayouts = map[string]tea.WindowSizeMsg{"table": {Width: 120, Height: 14}, "cards": {Width: 60, Height: 14}}

// mouseOn is the live view at size, showing the varied report with every line
// open.
func mouseOn(t *testing.T, size tea.WindowSizeMsg) *liveStatus {
	m := liveOn(variedReport(t))
	openAll(m)
	m.Update(size)
	return m
}

// wheeled turns the mouse wheel n times, down or up.
func wheeled(m *liveStatus, button tea.MouseButton, n int) {
	for range n {
		m.Update(tea.MouseWheelMsg{Button: button})
	}
}

// clicked clicks the left button at column x of screen line y.
func clicked(m *liveStatus, x, y int) {
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// startOf is the first screen line the shown line i is drawn on, counted from
// the first shown line.
func startOf(m *liveStatus, i int) int { return m.drawnHeight(0, i) }

func TestLiveStatusAsksTheTerminalForTheMouse(t *testing.T) {
	if got := liveOn(variedReport(t)).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("the view asks for mouse mode %v, want clicks and the wheel", got)
	}
}

func TestLiveStatusWheelMovesTheScreenAndNotTheHighlight(t *testing.T) {
	for name, size := range mouseLayouts {
		m := mouseOn(t, size)
		m.moveTo(m.lineAt(6))
		cursor, top := m.cursor, m.top
		for _, button := range []tea.MouseButton{tea.MouseWheelDown, tea.MouseWheelUp} {
			wheeled(m, button, 1)
			if m.top == top {
				t.Errorf("%s: the wheel (%v) leaves the same lines on screen", name, button)
			}
			if m.cursor != cursor {
				t.Errorf("%s: the wheel (%v) moves the highlight from line %d to %d", name, button, cursor, m.cursor)
			}
			if !onScreen(m) {
				t.Errorf("%s: the wheel (%v) leaves the highlighted line off screen", name, button)
			}
			top = m.top
		}
	}
}

func TestLiveStatusWheelMovesTheHighlightOnlyToKeepItOnScreen(t *testing.T) {
	for name, size := range mouseLayouts {
		m := mouseOn(t, size)
		wheeled(m, tea.MouseWheelDown, 1)
		if m.cursor == 0 || !onScreen(m) {
			t.Fatalf("%s: wheeling down from the top leaves the highlight on line %d, want the first line wholly on screen", name, m.cursor)
		}
		if start := startOf(m, m.cursor); start < m.top || (m.cursor > 0 && startOf(m, m.cursor-1) >= m.top) {
			t.Errorf("%s: wheeling down puts the highlight on line %d, not the nearest on screen", name, m.cursor)
		}

		press(m, "end")
		wheeled(m, tea.MouseWheelUp, 1)
		last := len(m.shown) - 1
		if m.cursor == last || !onScreen(m) {
			t.Fatalf("%s: wheeling up from the bottom leaves the highlight on line %d, want the last line wholly on screen", name, m.cursor)
		}
		if end := startOf(m, m.cursor+1); end > m.top+m.bodyHeight() || startOf(m, m.cursor+2) <= m.top+m.bodyHeight() && m.cursor+1 < last {
			t.Errorf("%s: wheeling up puts the highlight on line %d, not the nearest on screen", name, m.cursor)
		}
	}
}

func TestLiveStatusWheelStopsAtTheEnds(t *testing.T) {
	for name, size := range mouseLayouts {
		m := mouseOn(t, size)
		wheeled(m, tea.MouseWheelUp, 3)
		if m.top != 0 || m.cursor != 0 {
			t.Errorf("%s: wheeling up at the top moves to top %d, line %d", name, m.top, m.cursor)
		}
		wheeled(m, tea.MouseWheelDown, 100)
		if want := m.drawnHeight(0, len(m.shown)) - m.bodyHeight(); m.top != want {
			t.Errorf("%s: wheeling down stops at top %d, want %d: the last line at the bottom", name, m.top, want)
		}
		if !onScreen(m) {
			t.Errorf("%s: the highlight is off screen at the bottom", name)
		}
		wheeled(m, tea.MouseWheelDown, 3)
		if want := m.drawnHeight(0, len(m.shown)) - m.bodyHeight(); m.top != want {
			t.Errorf("%s: wheeling down at the bottom moves to top %d, want %d", name, m.top, want)
		}
	}
}

func TestLiveStatusKeepsTheWheelsPlaceAcrossReports(t *testing.T) {
	for name, size := range mouseLayouts {
		m := liveOn(variedReport(t))
		m.Update(size)
		wheeled(m, tea.MouseWheelDown, 2)
		top, cursor, was := m.top, m.cursor, body(m)
		m.Update(reportBuilt{report: variedReport(t)})
		if m.top != top || m.cursor != cursor || !slices.Equal(body(m), was) {
			t.Errorf("%s: a new report moves the screen from top %d, line %d to top %d, line %d", name, top, cursor, m.top, m.cursor)
		}
	}
}

func TestLiveStatusClickHighlightsAnyLineOfAShownLineAndKeepsTheScreen(t *testing.T) {
	for name, size := range mouseLayouts {
		m := mouseOn(t, size)
		wheeled(m, tea.MouseWheelDown, 1)
		top := m.top
		for i := range m.shown {
			for row := range m.shown[i].drawn {
				y := len(m.headings) + startOf(m, i) + row - m.top
				if y < len(m.headings) || y >= len(m.headings)+m.bodyHeight() {
					continue
				}
				clicked(m, m.width-1, y)
				if m.cursor != i || m.top != top {
					t.Errorf("%s: a click on line %d, row %d, puts the highlight on line %d at top %d, want top %d", name, i, row, m.cursor, m.top, top)
				}
			}
		}
	}
}

func TestLiveStatusClickOnAnythingButALineChangesNothing(t *testing.T) {
	for name, size := range mouseLayouts {
		size.Height = 100 // room below the last line
		m := mouseOn(t, size)
		m.moveTo(3)
		before := screen(m)
		rows := []int{len(m.headings) + m.drawnHeight(0, len(m.shown)), size.Height - 2, size.Height - 1}
		if len(m.headings) > 0 {
			rows = append(rows, 0)
		}
		for _, y := range rows {
			clicked(m, 1, y)
			if m.cursor != 3 || !slices.Equal(screen(m), before) {
				t.Errorf("%s: a click on screen line %d changes the view", name, y)
			}
		}
	}
	clicked(newLiveStatus(statusFeed{}), 0, 0) // before the first report, there's no line
}

func TestLiveStatusClickOnTheMarkerFoldsAndOnlyThere(t *testing.T) {
	for name, size := range mouseLayouts {
		m := mouseOn(t, size)
		const goal = 1 // a line with others under it
		row := len(m.headings) + startOf(m, goal)
		marker := strings.Index(uncolored(m.shown[goal].drawn[0]), "▾")
		x := lipgloss.Width(uncolored(m.shown[goal].drawn[0])[:marker])
		count := len(m.shown)

		clicked(m, x+2, row)
		if m.cursor != goal || len(m.shown) != count {
			t.Errorf("%s: a click beside the marker folds the line or misses it", name)
		}
		m.moveTo(0)
		clicked(m, x, row)
		if m.cursor != goal || m.shown[goal].open || len(m.shown) >= count {
			t.Errorf("%s: a click on the marker doesn't highlight and close the line", name)
		}
		clicked(m, x, row)
		if !m.shown[goal].open || len(m.shown) != count {
			t.Errorf("%s: a second click on the marker doesn't open the line again", name)
		}

		leaf := slices.IndexFunc(m.shown, func(l shownLine) bool { return len(l.kids) == 0 && l.depth > 0 })
		leafRow := len(m.headings) + startOf(m, leaf) - m.top
		if leafRow < len(m.headings) || leafRow >= len(m.headings)+m.bodyHeight() {
			m.moveTo(leaf)
			leafRow = len(m.headings) + startOf(m, leaf) - m.top
		}
		m.moveTo(0)
		clicked(m, indentWidth(m.shown[leaf].depth)-2, leafRow)
		if m.cursor != leaf || len(m.shown) != count {
			t.Errorf("%s: a click where a line with nothing under it has no marker does more than highlight it", name)
		}
	}
}

// TestLiveStatusReadsGitHubAndPullsTheLedgerWhenDue leaves the view open for two
// minutes on a machine that doesn't run the crew, with a refresh asked for
// along the way, and GitHub failing from 3 seconds on.
func TestLiveStatusReadsGitHubAndPullsTheLedgerWhenDue(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	c := statusLedger(t)
	writeFile(t, c, "laptop\n", "host") // another machine runs the crew
	writeFile(t, c, "REPO="+t.TempDir()+"\n", "projects", "p", "project.env")
	outside := t.TempDir() // the stubs' notes, kept out of the home
	log, ghFails := outside+"/calls.log", outside+"/gh-fails"
	writeFile(t, c, `#!/bin/sh
echo "gh $*" >>`+log+`
[ -e `+ghFails+` ] && exit 1
echo '{"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}'
`, "bin", "gh")
	writeFile(t, c, `#!/bin/sh
case " $* " in *" pull "*) echo "git $*" >>`+log+`;; esac
exec `+realGit+` "$@"
`, "bin", "git")
	g := gitRepo{t, c.Home}
	g.git("init", "-q")
	g.git("add", "-A")
	g.git("commit", "-qm", "home")
	before := homeState(t, c)

	clock := tenOClock
	start := clock
	m := newLiveStatus(feedOf(c, "p", false))
	m.now = func() time.Time { return clock }
	var reads, pulls []time.Duration
	var lastSync []bool // for each second, whether 013-risky's merge check went by the last sync
	calls := 0
	settle(m, m.Init())
	for {
		since := clock.Sub(start)
		b, _ := os.ReadFile(log)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for _, l := range lines[calls:] {
			switch {
			case strings.HasPrefix(l, "gh pr view https://github.com/o/r/pull/9 "):
				reads = append(reads, since)
			case strings.HasPrefix(l, "git ") && strings.Contains(l, " pull "):
				pulls = append(pulls, since)
			}
		}
		calls = len(lines)
		i := lineOf(m, "013-risky")
		lastSync = append(lastSync, i >= 0 && strings.Contains(says(m.shown[i].statusLine), "as of the last sync"))

		if since == 2*time.Minute {
			break
		}
		clock = clock.Add(time.Second)
		switch clock.Sub(start) {
		case 3 * time.Second: // GitHub stops answering, from here on
			if err := os.WriteFile(ghFails, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		case 99 * time.Second:
			settle(m, press(m, "r"))
		}
		_, cmd := m.Update(beat{})
		settle(m, cmd)
	}
	s := time.Second
	if want := []time.Duration{0, 30 * s, 60 * s, 90 * s, 99 * s}; !slices.Equal(reads, want) {
		t.Errorf("GitHub was read at %v, want %v", reads, want)
	}
	if want := []time.Duration{0, 60 * s, 99 * s}; !slices.Equal(pulls, want) {
		t.Errorf("the ledger was pulled at %v, want %v", pulls, want)
	}
	for second, went := range lastSync {
		if failed := second >= 30; went != failed {
			t.Errorf("at %ds, 013-risky's merge check went by the last sync: %v, want %v (GitHub's latest read failed: %v)", second, went, failed, failed)
		}
	}
	if after := homeState(t, c); after != before {
		t.Errorf("the view changed the home:\nbefore %s\nafter  %s", before, after)
	}
}

// homeState is what git says of the home: what's changed in it, and its HEAD.
func homeState(t *testing.T, c *Crew) string {
	t.Helper()
	return gitOut(t, c.Home, "status", "--porcelain") + " " + gitOut(t, c.Home, "rev-parse", "HEAD")
}

// liveCrew is statusLedger with p's clone a real repo: a stack in flight whose
// parent tips are pushed, an open PR stacked on it, a PR that targets a branch
// other than its task's base, and a PR that can merge now. Stubs for claude, gh
// and git log every call to calls. claude lists the workers in workers, and
// fails while claudeFails exists.
type liveCrew struct {
	*Crew
	repo                        string
	calls, workers, claudeFails string
}

func newLiveCrew(t *testing.T) liveCrew {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	c := statusLedger(t)
	outside := t.TempDir() // the stubs' files, kept out of the home
	l := liveCrew{Crew: c, repo: stackRepo(t, "agent/lower", "agent/upper", "agent/top", "agent/moved", "agent/old", "agent/ready"),
		calls: outside + "/calls.log", workers: outside + "/workers.json", claudeFails: outside + "/claude-fails"}
	writeFile(t, c, "REPO="+l.repo+"\n", "projects", "p", "project.env")
	l.listWorkers(t, `{"id":"w1","state":"working","pid":1},{"id":"w20","state":"working","pid":2},{"id":"w24","state":"idle","pid":3}`)
	writeFile(t, c, `#!/bin/sh
echo "claude $*" >>`+l.calls+`
[ -e `+l.claudeFails+` ] && exit 1
cat `+l.workers+`
`, "bin", "claude")
	writeFile(t, c, `#!/bin/sh
echo "gh $*" >>`+l.calls+`
echo '{"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}'
`, "bin", "gh")
	writeFile(t, c, `#!/bin/sh
echo "git $*" >>`+l.calls+`
exec `+realGit+` "$@"
`, "bin", "git")
	reportTask(t, c, "p", "020-lower", false, "status: working", "base: main", "branch: agent/lower", "worker_job: w20")
	reportTask(t, c, "p", "021-upper", false, "status: working", "base: agent/lower", "branch: agent/upper")
	reportTask(t, c, "p", "022-top", false, "status: pr-open", "base: agent/upper", "branch: agent/top",
		"pr: https://github.com/o/r/pull/22", "pr_state: open", "pr_base: agent/upper")
	reportTask(t, c, "p", "023-moved", false, "status: working", "base: main", "branch: agent/moved",
		"pr: https://github.com/o/r/pull/23", "pr_state: open", "pr_base: agent/old")
	reportTask(t, c, "p", "024-ready", false, "status: pr-open", "risk: low", "base: main", "branch: agent/ready",
		"pr: https://github.com/o/r/pull/24", "pr_state: open", "pr_base: main", "worker_job: w24")
	return l
}

// listWorkers has claude agents list the workers given, as JSON objects.
func (l liveCrew) listWorkers(t *testing.T, workers string) {
	t.Helper()
	if err := os.WriteFile(l.workers, []byte("["+workers+"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// moveOn puts a new commit on a branch's tip on origin, as its worker would by
// pushing, and returns the tip it had.
func (l liveCrew) moveOn(t *testing.T, branch string) string {
	t.Helper()
	g := gitRepo{t, l.repo}
	was := g.git("rev-parse", "refs/remotes/origin/"+branch)
	g.git("update-ref", "refs/remotes/origin/"+branch, g.git("commit-tree", was+"^{tree}", "-p", was, "-m", "moved on"))
	return was
}

// callsSince is the calls the stubs logged after the first n.
func (l liveCrew) callsSince(n int) []string {
	b, _ := os.ReadFile(l.calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	return lines[min(n, len(lines)):]
}

// The calls a second of the live view made, counted by what they read.
type liveCalls struct{ builds, listings, branchCalls, ghCalls, pulls int }

// countCalls sorts calls by what they read: the workers, p's branches, GitHub,
// or the ledger.
func (l liveCrew) countCalls(calls []string) liveCalls {
	var n liveCalls
	for _, call := range calls {
		switch {
		case strings.HasPrefix(call, "claude agents "):
			n.listings++
		case strings.HasPrefix(call, "git -C "+l.repo+" "):
			n.branchCalls++
		case strings.HasPrefix(call, "gh "):
			n.ghCalls++
		case strings.HasPrefix(call, "git ") && strings.Contains(call, " pull "):
			n.pulls++
		}
	}
	return n
}

// secondsWhen is the seconds, from the start, in which a holds.
func secondsWhen(seconds []liveCalls, holds func(liveCalls) bool) []time.Duration {
	var at []time.Duration
	for i, n := range seconds {
		if holds(n) {
			at = append(at, time.Duration(i)*time.Second)
		}
	}
	return at
}

// For a minute, with a task file changing every few seconds, the live view
// lists the workers and reads the tasks' branches every 15 seconds, however
// often it builds. GitHub is read every 30 seconds and the ledger pulled every
// minute.
func TestLiveStatusListsWorkersAndReadsBranchesWhenDue(t *testing.T) {
	l := newLiveCrew(t)
	writeFile(t, l.Crew, "laptop\n", "host") // another machine runs the crew, so the ledger is pulled
	g := gitRepo{t, l.Home}
	g.git("init", "-q")
	g.git("add", "-A")
	g.git("commit", "-qm", "home")
	logged := len(l.callsSince(0))
	l.readBranches([]string{"p"})
	oneRead := l.countCalls(l.callsSince(logged)).branchCalls

	feed := feedOf(l.Crew, "p", false)
	var builds atomic.Int32
	build := feed.build
	feed.build = func(r statusReads) (statusReport, error) { builds.Add(1); return build(r) }
	logged = len(l.callsSince(0))
	var seconds []liveCalls
	run := newLiveRun(feed)
	for s := 0; ; s++ {
		calls := l.callsSince(logged)
		logged += len(calls)
		n := l.countCalls(calls)
		n.builds = int(builds.Swap(0))
		seconds = append(seconds, n)
		if s == 60 {
			break
		}
		if s%4 == 3 { // the ledger changes under the view
			reportTask(t, l.Crew, "p", "012-idea", s%8 == 3, "status: draft", "base: main")
		}
		run.second()
	}

	built := secondsWhen(seconds, func(n liveCalls) bool { return n.builds > 0 })
	listed := secondsWhen(seconds, func(n liveCalls) bool { return n.listings > 0 })
	branchesRead := secondsWhen(seconds, func(n liveCalls) bool { return n.branchCalls > 0 })
	gitHubRead := secondsWhen(seconds, func(n liveCalls) bool { return n.ghCalls > 0 })
	pulled := secondsWhen(seconds, func(n liveCalls) bool { return n.pulls > 0 })
	t.Logf("built in %d seconds; listed the workers at %v; read the branches at %v, %d git calls a read; read GitHub at %v; pulled at %v",
		len(built), listed, branchesRead, oneRead, gitHubRead, pulled)

	s := time.Second
	every15s := []time.Duration{0, 15 * s, 30 * s, 45 * s, 60 * s}
	if len(built) < 20 {
		t.Errorf("the view built its report in %d seconds of 61, want every 3s at least", len(built))
	}
	for i, n := range seconds {
		if n.listings > 1 || n.branchCalls > oneRead {
			t.Errorf("at %ds, the view listed the workers %d times and ran git %d times in the clone, want once and one read's %d at most", i, n.listings, n.branchCalls, oneRead)
		}
	}
	if !slices.Equal(listed, every15s) {
		t.Errorf("the workers were listed at %v, want every 15s", listed)
	}
	if !slices.Equal(branchesRead, every15s) {
		t.Errorf("the branches were read at %v, want every 15s", branchesRead)
	}
	if !slices.Equal(gitHubRead, []time.Duration{0, 30 * s, 60 * s}) {
		t.Errorf("GitHub was read at %v, want every 30s", gitHubRead)
	}
	if !slices.Equal(pulled, []time.Duration{0, 60 * s}) {
		t.Errorf("the ledger was pulled at %v, want every minute", pulled)
	}
}

// liveRun is a live view run second by second, as its beat runs it, with each
// command it gives run to the end before the next second.
type liveRun struct {
	m     *liveStatus
	clock time.Time
}

// newLiveRun opens a view on feed at tenOClock.
func newLiveRun(feed statusFeed) *liveRun {
	r := &liveRun{m: newLiveStatus(feed), clock: tenOClock}
	r.m.now = func() time.Time { return r.clock }
	settle(r.m, r.m.Init())
	return r
}

// second moves the clock on a second and lets the view do what's due.
func (r *liveRun) second() {
	r.clock = r.clock.Add(time.Second)
	_, cmd := r.m.Update(beat{})
	settle(r.m, cmd)
}

// open is how long the view has been open.
func (r *liveRun) open() time.Duration { return r.clock.Sub(tenOClock) }

// fields are what the view has of a task after its ID, as the plain text
// words them: its status, worker, PR, model, effort and notes; none when it
// isn't on show.
func (r *liveRun) fields(id string) []string {
	if i := lineOf(r.m, id); i >= 0 {
		return texts(r.m.shown[i].fields)
	}
	return make([]string, len(columns))
}

// says is what the view shows of a task after its ID, its fields joined.
func (r *liveRun) says(id string) string { return strings.Join(r.fields(id), "  ") }

// A change to a task file shows with the next build, within 3 seconds, and
// the view lists no workers and reads no branches to show it.
func TestLiveStatusShowsALedgerChangeWithoutReadingAgain(t *testing.T) {
	l := newLiveCrew(t)
	run := newLiveRun(feedOf(l.Crew, "p", false))
	for range 5 {
		run.second()
	}
	logged := len(l.callsSince(0))
	reportTask(t, l.Crew, "p", "020-lower", false, "status: needs-input", "base: main", "branch: agent/lower", "worker_job: w20")
	reportTask(t, l.Crew, "p", "024-ready", true, "status: pr-open", "risk: low", "base: main", "branch: agent/ready",
		"pr: https://github.com/o/r/pull/24", "pr_state: open", "pr_base: main", "worker_job: w24")
	changed := run.open()
	for run.fields("020-lower")[statusField] != "needs-input" || !strings.Contains(run.says("024-ready"), "1 open question(s)") {
		if run.open()-changed == 3*time.Second {
			t.Fatalf("3s after the ledger changed, the view shows 020-lower %q and 024-ready %q", run.says("020-lower"), run.says("024-ready"))
		}
		run.second()
	}
	if calls := l.callsSince(logged); len(calls) > 0 {
		t.Errorf("to show the change, the view ran:\n%s", strings.Join(calls, "\n"))
	}
}

// A worker's change of state, and a restack becoming needed, show within 15
// seconds. After r, they show as soon as the reads it starts come back.
func TestLiveStatusShowsWorkersAndRestacksWithin15Seconds(t *testing.T) {
	l := newLiveCrew(t)
	run := newLiveRun(feedOf(l.Crew, "p", false))
	for range 5 {
		run.second()
	}
	// 024-ready's worker starts on a round, and 020-lower's branch moves on
	// under 021-upper.
	l.listWorkers(t, `{"id":"w1","state":"working","pid":1},{"id":"w20","state":"working","pid":2},{"id":"w24","state":"working","pid":3}`)
	lowerWas := l.moveOn(t, "agent/lower")
	for range 15 {
		run.second()
	}
	if got := run.fields("024-ready"); got[workerField] != "working" || !strings.HasPrefix(got[notesField], "not yet: worker busy") {
		t.Errorf("15s after its worker started, 024-ready shows %q", got)
	}
	if got := run.says("021-upper"); !strings.Contains(got, "needs restack: 020-lower moved") {
		t.Errorf("15s after 020-lower moved on, 021-upper shows %q", got)
	}

	// Both go back; r shows it at once.
	l.listWorkers(t, `{"id":"w1","state":"working","pid":1},{"id":"w20","state":"working","pid":2},{"id":"w24","state":"idle","pid":3}`)
	gitRepo{t, l.repo}.git("update-ref", "refs/remotes/origin/agent/lower", lowerWas)
	settle(run.m, press(run.m, "r"))
	if got := run.fields("024-ready"); got[workerField] != "idle" || !strings.HasPrefix(got[notesField], "merge now") {
		t.Errorf("after r, with its worker idle, 024-ready shows %q", got)
	}
	if got := run.says("021-upper"); strings.Contains(got, "needs restack") {
		t.Errorf("after r, with 020-lower back where it was, 021-upper shows %q", got)
	}
}

// Until the workers are first listed and the branches first read, the view
// claims nothing they could contradict.
func TestLiveStatusClaimsNothingBeforeTheFirstReads(t *testing.T) {
	l := newLiveCrew(t)
	hold := make(chan struct{})
	feed := feedOf(l.Crew, "p", false)
	listWorkers, readBranches := feed.listWorkers, feed.readBranches
	feed.listWorkers = func() *workerList { <-hold; return listWorkers() }
	feed.readBranches = func() branchReads { <-hold; return readBranches() }
	m := newLiveStatus(feed)
	m.now = func() time.Time { return tenOClock }
	msgs := make(chan tea.Msg, 8)
	inBackground(m.Init(), msgs)
	handle := func() {
		_, cmd := m.Update(next(t, msgs))
		inBackground(cmd, msgs)
	}
	// Until a report built from GitHub's answer is on show.
	for m.reads.prs == nil || m.building.running || m.building.asked || len(m.shown) == 0 {
		handle()
	}

	openAll(m)
	for _, line := range m.shown {
		if strings.Contains(says(line.statusLine), "merge now") {
			t.Errorf("before the first listing, %s says %q", line.head, says(line.statusLine))
		}
	}
	if got := m.shown[0].head; got != "workers running: not listed yet" {
		t.Errorf("before the first listing, the workers line is %q", got)
	}
	for _, id := range []string{"001-in-flight", "020-lower", "024-ready"} {
		if got := lineOf(m, id); got < 0 || m.shown[got].fields[workerField].text != "not listed yet" {
			t.Errorf("before the first listing, %s's worker isn't said to be not listed yet", id)
		}
	}
	for _, id := range []string{"021-upper", "023-moved"} {
		if got := lineOf(m, id); got < 0 || !strings.Contains(says(m.shown[got].statusLine), "branches not read yet") {
			t.Errorf("before the first branch read, %s doesn't say its branches aren't read yet", id)
		}
	}
	// git is never asked about 020-lower: it isn't stacked, and has no PR.
	if got := lineOf(m, "020-lower"); got < 0 || strings.Contains(says(m.shown[got].statusLine), "branches not read yet") {
		t.Errorf("before the first branch read, 020-lower is shown as %q", says(m.shown[got].statusLine))
	}

	close(hold) // the reads come back, and with them what they say
	shows := func(id string) string { return says(m.shown[lineOf(m, id)].statusLine) }
	for !strings.Contains(shows("024-ready"), "merge now") || !strings.Contains(shows("023-moved"), "PR targets agent/old, not main") {
		handle()
	}
}

// When listing the workers starts failing, the build after says so, and no
// row says merge now until a listing succeeds again.
func TestLiveStatusSaysWhenTheWorkersCouldntBeListed(t *testing.T) {
	l := newLiveCrew(t)
	run := newLiveRun(feedOf(l.Crew, "p", false))
	for run.open() < 60*time.Second {
		run.second()
		switch run.open() {
		case 20 * time.Second: // claude agents fails from here, until 40s
			if err := os.WriteFile(l.claudeFails, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		case 40 * time.Second:
			if err := os.Remove(l.claudeFails); err != nil {
				t.Fatal(err)
			}
		}
		// The listings at 30s fails; the one at 45s succeeds.
		failed := run.open() >= 30*time.Second && run.open() < 45*time.Second
		got := run.says("024-ready")
		if failed && (!strings.Contains(got, "workers couldn't be listed") || strings.Contains(got, "merge now")) {
			t.Errorf("at %v, after a listing failed, 024-ready shows %q", run.open(), got)
		}
		if !failed && !strings.Contains(got, "merge now") {
			t.Errorf("at %v, after a listing succeeded, 024-ready shows %q", run.open(), got)
		}
	}
}
